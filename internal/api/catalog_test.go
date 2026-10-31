package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kiry163/llmmeter/internal/config"
	"github.com/kiry163/llmmeter/internal/pricecatalog"
	"github.com/kiry163/llmmeter/internal/store"
	"github.com/kiry163/llmmeter/internal/usage"
)

// catalogBody is the decoded shape of GET /api/upstreams/{id}/catalog.
type catalogBody struct {
	FetchedAt      *time.Time `json:"fetchedAt"`
	Provider       string     `json:"provider"`
	PresetSnapshot string     `json:"presetSnapshot"`
	Configured     []string   `json:"configured"`
	Upstream       []struct {
		ID      string `json:"id"`
		OwnedBy string `json:"ownedBy"`
	} `json:"upstream"`
	Observed []struct {
		Model    string    `json:"model"`
		Requests int64     `json:"requests"`
		LastSeen time.Time `json:"lastSeen"`
	} `json:"observed"`
	Presets       []pricecatalog.Preset `json:"presets"`
	UpstreamError string                `json:"upstreamError"`
}

func decodeCatalog(t *testing.T, body []byte) catalogBody {
	t.Helper()
	var out catalogBody
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode catalog: %v: %s", err, body)
	}
	return out
}

// addPricedUpstream appends one upstream with the given model names.
func addPricedUpstream(t *testing.T, cs *config.Store, id, baseURL string, modelNames ...string) {
	t.Helper()
	models := make([]config.Model, 0, len(modelNames))
	for _, name := range modelNames {
		models = append(models, config.Model{Name: name})
	}
	if err := cs.Update(func(c *config.Config) error {
		c.Upstreams = append(c.Upstreams, config.Upstream{
			ID:      id,
			Name:    id,
			BaseURL: baseURL,
			APIKey:  "sk-" + id,
			Models:  models,
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// seedCatalogRequest records one completed request attributed to an upstream.
func seedCatalogRequest(t *testing.T, env *configTestEnv, upstreamID, model string, at time.Time) {
	t.Helper()
	env.data.EnqueueRecord(store.Record{
		StartedAt: at, CompletedAt: at.Add(time.Second),
		LocalDay: at.Format("2006-01-02"), MinuteOfDay: at.Hour()*60 + at.Minute(),
		Path: "/v1/chat/completions", PathClass: "chat",
		UpstreamID: upstreamID, UpstreamName: upstreamID,
		Model: model, Status: 200, LatencyMS: 10,
		Usage:       usage.Usage{TotalTokens: 1},
		WindowLabel: "base", UsageStatus: usage.StatusOK,
	})
	if err := env.data.Flush(); err != nil {
		t.Fatal(err)
	}
}

// catalogUpstream serves an OpenAI-compatible models listing, optionally only
// at the versioned path.
func catalogUpstream(t *testing.T, versionedOnly bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/models", func(w http.ResponseWriter, r *http.Request) {
		if versionedOnly {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, `{"data":[{"id":"upstream-model","owned_by":"x"},{"id":"shared"}]}`)
	})
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-u1" {
			t.Errorf("probe key = %q", r.Header.Get("Authorization"))
		}
		io.WriteString(w, `{"data":[{"id":"upstream-model","owned_by":"x"},{"id":"shared"}]}`)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func TestUpstreamCatalogCombinesAndCaches(t *testing.T) {
	var hits int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		io.WriteString(w, `{"data":[{"id":"upstream-model","owned_by":"x"}]}`)
	}))
	defer upstream.Close()

	env := newConfigEnv(t, "127.0.0.1:8100")
	addPricedUpstream(t, env.config, "u1", upstream.URL, "configured-model")
	addPricedUpstream(t, env.config, "other", "https://other.example.com", "configured-model")
	seedCatalogRequest(t, env, "u1", "observed-model", time.Date(2026, 10, 29, 4, 0, 0, 0, time.UTC))
	// A request of another upstream must not leak into u1's observed list.
	seedCatalogRequest(t, env, "other", "foreign-model", time.Date(2026, 10, 29, 4, 0, 0, 0, time.UTC))

	rec := doGET(t, env.srv, "/api/upstreams/u1/catalog")
	if rec.Code != http.StatusOK {
		t.Fatalf("catalog = %d %s", rec.Code, rec.Body.String())
	}
	got := decodeCatalog(t, rec.Body.Bytes())
	if got.FetchedAt == nil {
		t.Fatalf("fetchedAt is nil: %s", rec.Body.String())
	}
	if got.PresetSnapshot != pricecatalog.SnapshotDate {
		t.Fatalf("presetSnapshot = %q", got.PresetSnapshot)
	}
	if got.Provider != "" {
		t.Fatalf("provider = %q, want empty for a loopback host", got.Provider)
	}
	if len(got.Configured) != 1 || got.Configured[0] != "configured-model" {
		t.Fatalf("configured = %+v", got.Configured)
	}
	if len(got.Upstream) != 1 || got.Upstream[0].ID != "upstream-model" {
		t.Fatalf("upstream = %+v", got.Upstream)
	}
	if len(got.Observed) != 1 || got.Observed[0].Model != "observed-model" || got.Observed[0].Requests != 1 {
		t.Fatalf("observed = %+v", got.Observed)
	}
	if got.Presets == nil {
		t.Fatal("presets must be an empty array, not null")
	}
	if got.UpstreamError != "" {
		t.Fatalf("upstreamError = %q", got.UpstreamError)
	}

	// The listing is cached for catalogCacheTTL.
	doGET(t, env.srv, "/api/upstreams/u1/catalog")
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Fatalf("cache miss: hits=%d", n)
	}
	// refresh=1 bypasses the cache.
	doGET(t, env.srv, "/api/upstreams/u1/catalog?refresh=1")
	if n := atomic.LoadInt32(&hits); n != 2 {
		t.Fatalf("refresh hits=%d", n)
	}
}

func TestUpstreamCatalogFallsBackToVersionedPath(t *testing.T) {
	upstream := catalogUpstream(t, true)
	env := newConfigEnv(t, "127.0.0.1:8100")
	addPricedUpstream(t, env.config, "u1", upstream.URL)

	rec := doGET(t, env.srv, "/api/upstreams/u1/catalog")
	if rec.Code != http.StatusOK {
		t.Fatalf("catalog = %d %s", rec.Code, rec.Body.String())
	}
	got := decodeCatalog(t, rec.Body.Bytes())
	if len(got.Upstream) != 2 || got.Upstream[0].ID != "upstream-model" {
		t.Fatalf("upstream = %+v", got.Upstream)
	}
}

func TestUpstreamCatalogFailureStillReturnsConfigured(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom: sk-u1", http.StatusInternalServerError)
	}))
	defer upstream.Close()

	env := newConfigEnv(t, "127.0.0.1:8100")
	addPricedUpstream(t, env.config, "u1", upstream.URL, "configured-model")

	rec := doGET(t, env.srv, "/api/upstreams/u1/catalog")
	if rec.Code != http.StatusOK {
		t.Fatalf("catalog = %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "sk-u1") {
		t.Fatalf("catalog leaked the key: %s", rec.Body.String())
	}
	got := decodeCatalog(t, rec.Body.Bytes())
	if got.FetchedAt != nil || got.UpstreamError == "" {
		t.Fatalf("failure body = %+v", got)
	}
	if got.Upstream == nil {
		t.Fatal("upstream must be an empty array, not null")
	}
	if len(got.Configured) != 1 || got.Configured[0] != "configured-model" {
		t.Fatalf("configured = %+v", got.Configured)
	}
}

func TestUpstreamCatalogUnknownAndInvalidRefresh(t *testing.T) {
	env := newConfigEnv(t, "127.0.0.1:8100")
	addPricedUpstream(t, env.config, "u1", "http://127.0.0.1:1")

	rec := doGET(t, env.srv, "/api/upstreams/nope/catalog")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown = %d %s", rec.Code, rec.Body.String())
	}

	rec = doGET(t, env.srv, "/api/upstreams/u1/catalog?refresh=maybe")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"refresh"`) {
		t.Fatalf("invalid refresh = %d %s", rec.Code, rec.Body.String())
	}
}

func TestUpstreamCatalogInvalidatedByUpstreamPatch(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":[{"id":"before"}]}`)
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":[{"id":"after"}]}`)
	}))
	defer second.Close()

	env := newConfigEnv(t, "127.0.0.1:8100")
	addPricedUpstream(t, env.config, "u1", first.URL)

	rec := doGET(t, env.srv, "/api/upstreams/u1/catalog")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"before"`) {
		t.Fatalf("first catalog = %d %s", rec.Code, rec.Body.String())
	}

	rec = doPATCH(t, env.srv, "/api/upstreams/u1", `{"baseUrl":"`+second.URL+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch = %d %s", rec.Code, rec.Body.String())
	}

	rec = doGET(t, env.srv, "/api/upstreams/u1/catalog")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"after"`) {
		t.Fatalf("second catalog = %d %s", rec.Code, rec.Body.String())
	}
}
