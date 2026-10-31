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

	"github.com/kiry163/llmmeter/internal/config"
	"github.com/kiry163/llmmeter/internal/pricecatalog"
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

// catalogBody is the per-upstream model catalog response. FetchedAt is nil
// until an upstream listing succeeds; UpstreamError explains a failed attempt.
// Presets come from the embedded price snapshot and never hit the network.
type catalogBody struct {
	FetchedAt      *time.Time            `json:"fetchedAt"`
	Provider       string                `json:"provider"`
	PresetSnapshot string                `json:"presetSnapshot"`
	Configured     []string              `json:"configured"`
	Upstream       []catalogModel        `json:"upstream"`
	Observed       []catalogObserved     `json:"observed"`
	Presets        []pricecatalog.Preset `json:"presets"`
	UpstreamError  string                `json:"upstreamError"`
}

// catalogCacheEntry is one cached upstream model listing. The endpoint and key
// are part of the entry so a configuration change is observed even before an
// explicit invalidation.
type catalogCacheEntry struct {
	base string
	key  string
	body catalogBody
	at   time.Time
}

// catalog combines the cached upstream model listing of each upstream with the
// models observed in local traffic and the embedded price presets.
type catalog struct {
	configStore *config.Store
	store       *store.Store
	now         func() time.Time
	client      *http.Client

	mu     sync.Mutex
	cached map[string]catalogCacheEntry
}

// newCatalog returns a catalog reading the configuration from cs and traffic
// from st.
func newCatalog(cs *config.Store, st *store.Store, now func() time.Time) *catalog {
	return &catalog{
		configStore: cs,
		store:       st,
		now:         now,
		client:      &http.Client{},
		cached:      make(map[string]catalogCacheEntry),
	}
}

// invalidate drops every cached upstream listing. Upstream and
// configuration-reload handlers call it after the configuration changes.
func (c *catalog) invalidate() {
	c.mu.Lock()
	c.cached = make(map[string]catalogCacheEntry)
	c.mu.Unlock()
}

// get returns the catalog of one upstream, refetching its model listing when
// the cache is empty, stale, belongs to a different endpoint or key, or refresh
// is set. Observed models are always read from the store.
func (c *catalog) get(ctx context.Context, u *config.Upstream, refresh bool) (catalogBody, error) {
	observed, err := c.observed(ctx, u.ID)
	if err != nil {
		return catalogBody{}, err
	}

	now := c.now()
	c.mu.Lock()
	entry, ok := c.cached[u.ID]
	c.mu.Unlock()

	fresh := ok && entry.base == u.BaseURL && entry.key == u.APIKey &&
		now.Sub(entry.at) < catalogCacheTTL
	if refresh || !fresh {
		entry = catalogCacheEntry{
			base: u.BaseURL,
			key:  u.APIKey,
			body: c.fetch(ctx, u.BaseURL, u.APIKey),
			at:   now,
		}
		c.mu.Lock()
		c.cached[u.ID] = entry
		c.mu.Unlock()
	}

	return c.compose(u, entry.body, observed), nil
}

// compose merges a cached listing with the configured and observed model sets
// and the embedded presets.
func (c *catalog) compose(u *config.Upstream, cached catalogBody, observed []catalogObserved) catalogBody {
	body := cached
	body.Provider = pricecatalog.Provider(u.BaseURL)
	body.PresetSnapshot = pricecatalog.SnapshotDate
	body.Configured = make([]string, 0, len(u.Models))
	candidates := make([]string, 0, len(u.Models)+len(body.Upstream)+len(observed))
	for _, m := range u.Models {
		body.Configured = append(body.Configured, m.Name)
		candidates = append(candidates, m.Name)
	}
	for _, m := range body.Upstream {
		candidates = append(candidates, m.ID)
	}
	for _, m := range observed {
		candidates = append(candidates, m.Model)
	}
	body.Presets = pricecatalog.Match(u.BaseURL, candidates)
	body.Observed = observed
	if body.Upstream == nil {
		body.Upstream = []catalogModel{}
	}
	return body
}

// observed returns the models this upstream served inside the retention
// window, most frequent first.
func (c *catalog) observed(ctx context.Context, upstreamID string) ([]catalogObserved, error) {
	out := make([]catalogObserved, 0)
	if c.store == nil {
		return out, nil
	}
	rows, err := c.store.DB().QueryContext(ctx, `
SELECT model, COUNT(*), MAX(started_at) FROM requests
WHERE model <> '' AND upstream_id = ? AND local_day >= ?
GROUP BY model ORDER BY COUNT(*) DESC, model ASC`, upstreamID, c.observedSince())
	if err != nil {
		return nil, fmt.Errorf("observed models: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			m       catalogObserved
			started string
		)
		if err := rows.Scan(&m.Model, &m.Requests, &started); err != nil {
			return nil, fmt.Errorf("scan observed model: %w", err)
		}
		lastSeen, err := time.Parse(time.RFC3339, started)
		if err != nil {
			return nil, fmt.Errorf("parse started_at %q: %w", started, err)
		}
		m.LastSeen = lastSeen
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate observed models: %w", err)
	}
	return out, nil
}

// observedSince returns the first local day inside the retention window.
func (c *catalog) observedSince() string {
	loc := time.UTC
	retention := config.DefaultRetentionDays
	if c.configStore != nil {
		if snap := c.configStore.Snapshot(); snap != nil {
			if snap.Location != nil {
				loc = snap.Location
			}
			retention = snap.Config.General.RetentionDays
		}
	}
	now := c.now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	return today.AddDate(0, 0, -retention).Format(dateLayout)
}

// fetch queries the upstream models endpoint. A missing endpoint yields an
// empty listing without an error; every failure leaves FetchedAt nil and
// records a message that never contains the API key.
func (c *catalog) fetch(ctx context.Context, base, key string) catalogBody {
	body := catalogBody{Upstream: []catalogModel{}}
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		body.UpstreamError = "upstream not configured"
		return body
	}

	ctx, cancel := context.WithTimeout(ctx, upstreamProbeTimeout)
	defer cancel()

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
			body.UpstreamError = redactUpstreamMessage(reqErr.Error(), key)
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

// redactUpstreamMessage removes the API key from a message and drops any query
// string.
func redactUpstreamMessage(msg, key string) string {
	if key != "" {
		msg = strings.ReplaceAll(msg, key, "[redacted]")
	}
	if i := strings.IndexByte(msg, '?'); i >= 0 {
		msg = msg[:i]
	}
	return msg
}

// upstreamCatalog answers GET /api/upstreams/{id}/catalog, combining the
// upstream-visible models with the configured and observed ones.
func (s *Server) upstreamCatalog(w http.ResponseWriter, r *http.Request) {
	u, ok := s.upstreamFromPath(w, r)
	if !ok {
		return
	}
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
	body, err := s.catalog.get(r.Context(), u, refresh)
	if err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, body)
}
