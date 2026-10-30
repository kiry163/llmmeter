package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kiry163/llmmeter/internal/settings"
	"github.com/kiry163/llmmeter/internal/store"
)

const (
	// catalogCacheTTL bounds how long an upstream model listing is reused.
	catalogCacheTTL = 5 * time.Minute

	// catalogBodyLimit bounds the upstream models response body.
	catalogBodyLimit = 1 << 20
)

// catalogModel is one upstream model entry.
type catalogModel struct {
	ID      string `json:"id"`
	OwnedBy string `json:"ownedBy,omitempty"`
}

// catalogObserved is one model seen in local traffic.
type catalogObserved struct {
	Model    string    `json:"model"`
	Requests int64     `json:"requests"`
	LastSeen time.Time `json:"lastSeen"`
}

// catalogBody is the model catalog response. FetchedAt is nil until an
// upstream listing succeeds; UpstreamError explains a failed attempt.
type catalogBody struct {
	FetchedAt     *time.Time        `json:"fetchedAt"`
	Upstream      []catalogModel    `json:"upstream"`
	Observed      []catalogObserved `json:"observed"`
	UpstreamError string            `json:"upstreamError"`
}

// catalog combines the upstream model listing, cached for catalogCacheTTL,
// with the models observed in local traffic.
type catalog struct {
	repo   *settings.Repo
	store  *store.Store
	now    func() time.Time
	client *http.Client

	mu       sync.Mutex
	cached   catalogBody
	cachedAt time.Time
}

// newCatalog returns a catalog reading settings and traffic from repo and st.
func newCatalog(repo *settings.Repo, st *store.Store, now func() time.Time) *catalog {
	return &catalog{
		repo:   repo,
		store:  st,
		now:    now,
		client: &http.Client{Timeout: upstreamProbeTimeout},
	}
}

// invalidate drops the cached upstream listing. Settings handlers call it
// after saving a new upstream endpoint or key.
func (c *catalog) invalidate() {
	c.mu.Lock()
	c.cached = catalogBody{}
	c.cachedAt = time.Time{}
	c.mu.Unlock()
}

// get returns the catalog, refetching upstream when the cache is empty, older
// than catalogCacheTTL, or refresh is set. Observed models are always read
// from the store.
func (c *catalog) get(ctx context.Context, refresh bool) (catalogBody, error) {
	observed, err := c.store.ObservedModels(ctx, c.observedSince())
	if err != nil {
		return catalogBody{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	if refresh || c.cachedAt.IsZero() || now.Sub(c.cachedAt) >= catalogCacheTTL {
		c.cached = c.fetch(ctx)
		c.cachedAt = now
	}

	body := c.cached
	body.Observed = make([]catalogObserved, 0, len(observed))
	for _, m := range observed {
		body.Observed = append(body.Observed, catalogObserved{
			Model:    m.Model,
			Requests: m.Requests,
			LastSeen: m.LastSeen,
		})
	}
	if body.Upstream == nil {
		body.Upstream = []catalogModel{}
	}
	return body, nil
}

// observedSince returns the first local day inside the retention window.
func (c *catalog) observedSince() string {
	loc := time.UTC
	retention := 0
	if snap := c.repo.Snapshot(); snap != nil {
		if snap.Location != nil {
			loc = snap.Location
		}
		retention = snap.RetentionDays
	}
	now := c.now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	return today.AddDate(0, 0, -retention).Format(dateLayout)
}

// fetch queries the upstream models endpoint. A missing endpoint yields an
// empty listing without an error; every failure leaves FetchedAt nil and
// records a message that never contains the API key.
func (c *catalog) fetch(ctx context.Context) catalogBody {
	body := catalogBody{Upstream: []catalogModel{}}

	var base, key string
	if snap := c.repo.Snapshot(); snap != nil {
		base = strings.TrimRight(strings.TrimSpace(snap.Upstream.BaseURL), "/")
		key = snap.Upstream.APIKey
	}
	if base == "" {
		body.UpstreamError = "upstream not configured"
		return body
	}

	endpoints := []string{"/models"}
	if !strings.HasSuffix(base, "/v1") {
		endpoints = append(endpoints, "/v1/models")
	}

	var (
		resp   *http.Response
		reqErr error
	)
	for i, path := range endpoints {
		var req *http.Request
		req, reqErr = http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
		if reqErr != nil {
			body.UpstreamError = "invalid upstream URL"
			return body
		}
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		resp, reqErr = c.client.Do(req)
		if reqErr != nil {
			body.UpstreamError = c.redact(reqErr.Error(), key)
			return body
		}
		// An OpenAI-compatible server may expose only the versioned path.
		if (resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed) &&
			i < len(endpoints)-1 {
			resp.Body.Close()
			continue
		}
		break
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		models, err := decodeCatalogModels(resp)
		if err != nil {
			body.UpstreamError = "invalid upstream response"
			return body
		}
		body.Upstream = models
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		// No models endpoint: an empty listing is not an error.
	default:
		body.UpstreamError = fmt.Sprintf("upstream returned HTTP %d", resp.StatusCode)
		return body
	}

	at := c.now()
	body.FetchedAt = &at
	return body
}

// upstreamModel is the wire shape of one entry in the upstream data array.
type upstreamModel struct {
	ID      string `json:"id"`
	OwnedBy string `json:"owned_by"`
}

// decodeCatalogModels parses {"data":[{"id","owned_by"}]}, skipping entries
// without an id and dropping duplicate ids while preserving order.
func decodeCatalogModels(resp *http.Response) ([]catalogModel, error) {
	var payload struct {
		Data []upstreamModel `json:"data"`
	}
	dec := json.NewDecoder(io.LimitReader(resp.Body, catalogBodyLimit))
	if err := dec.Decode(&payload); err != nil {
		return nil, err
	}
	models := make([]catalogModel, 0, len(payload.Data))
	seen := make(map[string]struct{}, len(payload.Data))
	for _, m := range payload.Data {
		id := strings.TrimSpace(m.ID)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		models = append(models, catalogModel{ID: id, OwnedBy: m.OwnedBy})
	}
	return models, nil
}

// redact removes the API key from a message and drops any query string.
func (c *catalog) redact(msg, key string) string {
	if key != "" {
		msg = strings.ReplaceAll(msg, key, "[redacted]")
	}
	if i := strings.IndexByte(msg, '?'); i >= 0 {
		msg = msg[:i]
	}
	return msg
}

// modelCatalog answers the model catalog, combining upstream models with the
// models seen in retained traffic.
func (s *Server) modelCatalog(w http.ResponseWriter, r *http.Request) {
	refresh := false
	if v := r.URL.Query().Get("refresh"); v != "" {
		parsed, err := strconv.ParseBool(v)
		if err != nil {
			writeAPIErrorFields(w, http.StatusBadRequest, codeValidation, "invalid catalog query",
				map[string]string{"refresh": "must be a boolean"})
			return
		}
		refresh = parsed
	}
	body, err := s.catalog.get(r.Context(), refresh)
	if err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, body)
}
