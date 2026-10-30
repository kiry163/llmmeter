// Package e2e exercises the whole llmmeter stack: store, settings, metrics,
// gateway and API wired together exactly as main does.
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kiry163/llmmeter/internal/api"
	"github.com/kiry163/llmmeter/internal/gateway"
	"github.com/kiry163/llmmeter/internal/metrics"
	"github.com/kiry163/llmmeter/internal/settings"
	"github.com/kiry163/llmmeter/internal/store"
)

// localRequest returns a request that looks like it came from the browser on
// the same machine, which is how the panel is used by default.
func localRequest(method, path string, body io.Reader) *http.Request {
	r := httptest.NewRequest(method, path, body)
	r.RemoteAddr = "127.0.0.1:41234"
	r.Host = "127.0.0.1:8100"
	if method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions {
		r.Header.Set("X-Requested-With", "llmmeter")
	}
	return r
}

func TestEndToEndAccountingAndAPI(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"model":"m","usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
	}))
	defer upstream.Close()

	dbPath := filepath.Join(t.TempDir(), "e2e.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	repo, err := settings.Open(st.DB())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Setup(context.Background(), settings.SetupInput{
		Upstream: settings.Upstream{BaseURL: upstream.URL, APIKey: "sk-up"},
		Timezone: "UTC",
	}); err != nil {
		t.Fatal(err)
	}

	hub := metrics.New(st, time.Now)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)
	gw := gateway.New(repo, st, hub, time.Now)
	apiSrv := api.New(api.Config{
		Version: "test", Listen: "127.0.0.1:0", DBPath: dbPath,
		Repo: repo, Store: st, Hub: hub, Now: time.Now,
	})
	root := api.RootHandler(apiSrv.Handler(), http.NotFoundHandler(), gw)

	// One non-streaming chat request goes through the real proxy and is
	// recorded, unpriced because no model exists yet.
	req := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"m","messages":[]}`))
	req.Header.Set("Authorization", "Bearer "+repo.Snapshot().GatewayKey)
	rec := httptest.NewRecorder()
	root.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}

	rec = httptest.NewRecorder()
	root.ServeHTTP(rec, localRequest("GET", "/api/requests?limit=10", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"promptTokens":10`) {
		t.Fatalf("api = %d %s", rec.Code, rec.Body.String())
	}

	// Hot price apply: creating the model must affect the next request without
	// a restart, and leave the earlier record alone.
	rec = httptest.NewRecorder()
	root.ServeHTTP(rec, localRequest("POST", "/api/models",
		strings.NewReader(`{"name":"m","inputPerMTok":1,"outputPerMTok":2}`)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("model create = %d %s", rec.Code, rec.Body.String())
	}

	// Hot upstream switch: PATCH settings, then the next request hits the
	// second upstream.
	var hits2 atomic.Int64
	upstream2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits2.Add(1)
		// The model catalog probes /models; fail it so the catalog has to
		// report an upstream error while still serving observed traffic.
		if r.URL.Path == "/models" || r.URL.Path == "/v1/models" {
			http.Error(w, "boom: sk-up", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"model":"m","usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer upstream2.Close()
	rec = httptest.NewRecorder()
	root.ServeHTTP(rec, localRequest("PATCH", "/api/settings",
		strings.NewReader(fmt.Sprintf(`{"upstream":{"baseUrl":%q}}`, upstream2.URL))))
	if rec.Code != http.StatusOK {
		t.Fatalf("patch settings = %d %s", rec.Code, rec.Body.String())
	}

	req2 := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m","messages":[]}`))
	req2.Header.Set("Authorization", "Bearer "+repo.Snapshot().GatewayKey)
	root.ServeHTTP(httptest.NewRecorder(), req2)
	if hits2.Load() != 1 {
		t.Fatalf("new upstream hits = %d", hits2.Load())
	}
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}

	rec = httptest.NewRecorder()
	root.ServeHTTP(rec, localRequest("GET", "/api/requests?limit=1", nil))
	var list struct {
		Requests []struct {
			Priced      bool   `json:"priced"`
			CostNanoUSD int64  `json:"costNanoUsd"`
			Model       string `json:"model"`
		} `json:"requests"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Requests) != 1 || !list.Requests[0].Priced || list.Requests[0].CostNanoUSD <= 0 {
		t.Fatalf("second request not priced: %s", rec.Body.String())
	}
	if list.Requests[0].Model != "m" {
		t.Fatalf("second request model = %q", list.Requests[0].Model)
	}

	// The first request keeps its unpriced state: prices are snapshotted per
	// record, never recomputed.
	rec = httptest.NewRecorder()
	root.ServeHTTP(rec, localRequest("GET", "/api/requests?limit=10", nil))
	var all struct {
		Requests []struct {
			Priced bool `json:"priced"`
		} `json:"requests"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &all); err != nil {
		t.Fatal(err)
	}
	if len(all.Requests) != 2 {
		t.Fatalf("records = %d, want 2: %s", len(all.Requests), rec.Body.String())
	}
	if all.Requests[1].Priced {
		t.Fatalf("first request was retroactively priced")
	}

	// Live accounting sees both requests through the hub without a DB read.
	snap := hub.Snapshot(time.Minute)
	var live int64
	for _, p := range snap.Spark {
		live += p.Requests
	}
	if live < 2 {
		t.Fatalf("live snapshot requests = %d", live)
	}

	// Model catalog: the upstream listing fails, but the endpoint must still
	// answer 200 with the observed model and a redacted upstream error.
	rec = httptest.NewRecorder()
	root.ServeHTTP(rec, localRequest("GET", "/api/model-catalog", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("catalog = %d %s", rec.Code, rec.Body.String())
	}
	catalogJSON := rec.Body.String()
	if strings.Contains(catalogJSON, "sk-up") {
		t.Fatalf("catalog leaked the upstream API key: %s", catalogJSON)
	}
	var catalog struct {
		Upstream []json.RawMessage `json:"upstream"`
		Observed []struct {
			Model string `json:"model"`
		} `json:"observed"`
		UpstreamError string `json:"upstreamError"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	observed := false
	for _, o := range catalog.Observed {
		if o.Model == "m" {
			observed = true
		}
	}
	if catalog.Upstream == nil || catalog.Observed == nil {
		t.Fatalf("catalog arrays missing: %s", catalogJSON)
	}
	if !observed || catalog.UpstreamError == "" {
		t.Fatalf("catalog observed=%v error=%q: %s", observed, catalog.UpstreamError, catalogJSON)
	}

	// Analysis: the recorded traffic shows up in the aggregated sections.
	// The minute aggregates live in the hub until its next flush.
	hub.FlushForTest()
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	today := time.Now().UTC().Format("2006-01-02")
	rec = httptest.NewRecorder()
	root.ServeHTTP(rec, localRequest("GET",
		"/api/analysis?from="+today+"&to="+today+"&interval=auto", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("analysis = %d %s", rec.Code, rec.Body.String())
	}
	var analysis struct {
		Range struct {
			Interval string `json:"interval"`
		} `json:"range"`
		Trend  []json.RawMessage `json:"trend"`
		Models []struct {
			Key string `json:"key"`
		} `json:"models"`
		Paths []struct {
			Key string `json:"key"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &analysis); err != nil {
		t.Fatal(err)
	}
	if analysis.Range.Interval == "" || analysis.Trend == nil ||
		analysis.Models == nil || analysis.Paths == nil {
		t.Fatalf("analysis shape = %s", rec.Body.String())
	}
	hasModel, hasPath := false, false
	for _, m := range analysis.Models {
		if m.Key == "m" {
			hasModel = true
		}
	}
	for _, p := range analysis.Paths {
		if p.Key == "chat" {
			hasPath = true
		}
	}
	if !hasModel || !hasPath {
		t.Fatalf("analysis missing recorded traffic: %s", rec.Body.String())
	}
}
