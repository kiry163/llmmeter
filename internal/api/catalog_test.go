package api_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kiry163/llmmeter/internal/api"
	"github.com/kiry163/llmmeter/internal/settings"
	"github.com/kiry163/llmmeter/internal/store"
	"github.com/kiry163/llmmeter/internal/usage"
)

// newAPIServerWithUpstream starts the full handler against a fresh database
// configured with the given upstream base URL and one observed request.
func newAPIServerWithUpstream(t *testing.T, upstreamURL string) *httptest.Server {
	t.Helper()

	st, err := store.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	repo, err := settings.Open(st.DB())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Setup(context.Background(), settings.SetupInput{
		Upstream: settings.Upstream{
			BaseURL: upstreamURL,
			APIKey:  "sk-up",
		},
		Timezone: "UTC",
	}); err != nil {
		t.Fatal(err)
	}

	started := time.Date(2026, 10, 29, 4, 0, 0, 0, time.UTC)
	st.EnqueueRecord(store.Record{
		StartedAt: started, CompletedAt: started.Add(time.Second),
		LocalDay: "2026-10-29", MinuteOfDay: 240,
		Path: "/v1/chat/completions", PathClass: "chat",
		Model: "local-model", Status: 200, LatencyMS: 10,
		Usage:       usage.Usage{PromptTokens: 1, TotalTokens: 1},
		UsageStatus: usage.StatusOK,
	})
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}

	srv := api.New(api.Config{
		Version: "test",
		Listen:  "127.0.0.1:8100",
		DBPath:  "/tmp/llmmeter-test.db",
		Repo:    repo,
		Store:   st,
		Now:     func() time.Time { return apiNow },
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func TestModelCatalogFallsBackAndCaches(t *testing.T) {
	var hits int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if r.URL.Path == "/models" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer sk-up" {
			t.Errorf("key")
		}
		io.WriteString(w, `{"data":[{"id":"deepseek-chat","owned_by":"deepseek"},{"id":"m2"}]}`)
	}))
	defer upstream.Close()

	srv := newAPIServerWithUpstream(t, upstream.URL)
	rec := doGET(t, srv, "/api/model-catalog")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"deepseek-chat"`) {
		t.Fatalf("catalog = %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"local-model"`) {
		t.Fatalf("observed missing: %s", rec.Body.String())
	}
	doGET(t, srv, "/api/model-catalog")
	if atomic.LoadInt32(&hits) != 2 {
		t.Fatalf("cache miss: hits=%d", hits)
	}
	doGET(t, srv, "/api/model-catalog?refresh=1")
	if atomic.LoadInt32(&hits) != 4 {
		t.Fatalf("refresh hits=%d", hits)
	}
}

func TestModelCatalogFailureStillReturnsObserved(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom: sk-up", 500)
	}))
	defer upstream.Close()
	srv := newAPIServerWithUpstream(t, upstream.URL)
	rec := doGET(t, srv, "/api/model-catalog")
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "sk-up") ||
		!strings.Contains(rec.Body.String(), `"upstreamError"`) {
		t.Fatalf("failure = %d %s", rec.Code, rec.Body.String())
	}
}

func TestModelCatalogRejectsInvalidRefresh(t *testing.T) {
	srv := newAPIServerWithUpstream(t, "http://127.0.0.1:1")
	rec := doGET(t, srv, "/api/model-catalog?refresh=maybe")
	assertAPIError(t, rec, http.StatusBadRequest, "validation")
}

func TestModelCatalogInvalidatedBySettingsSave(t *testing.T) {
	var firstHits, secondHits int32
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&firstHits, 1)
		io.WriteString(w, `{"data":[{"id":"before"}]}`)
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&secondHits, 1)
		io.WriteString(w, `{"data":[{"id":"after"}]}`)
	}))
	defer second.Close()

	srv := newAPIServerWithUpstream(t, first.URL)
	if rec := doGET(t, srv, "/api/model-catalog"); rec.Code != 200 ||
		!strings.Contains(rec.Body.String(), `"before"`) {
		t.Fatalf("first catalog = %d %s", rec.Code, rec.Body.String())
	}

	rec := doPATCH(t, srv, "/api/settings", `{"upstream":{"baseUrl":"`+second.URL+`"}}`)
	if rec.Code != 200 {
		t.Fatalf("patch = %d %s", rec.Code, rec.Body.String())
	}

	rec = doGET(t, srv, "/api/model-catalog")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"after"`) {
		t.Fatalf("second catalog = %d %s", rec.Code, rec.Body.String())
	}
	if atomic.LoadInt32(&secondHits) == 0 {
		t.Fatal("cache not invalidated after settings save")
	}
}
