package api_test

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kiry163/llmmeter/internal/store"
	"github.com/kiry163/llmmeter/internal/usage"
)

// analysisRates is the rate snapshot of the extra cached request.
const analysisRates = `{"model":"deepseek-chat","window":"base","input_per_mtok":0.28,"output_per_mtok":0.42,"cache_read_per_mtok":0.028,"cache_write_per_mtok":0,"reasoning_per_mtok":0.42}`

// newAnalysisServer starts the seeded API server and appends one priced,
// cached request so the health and cache aggregates have three requests. The
// shared seedAPIData stays untouched for the other API tests.
func newAnalysisServer(t *testing.T) *httptest.Server {
	t.Helper()
	ts, st := newAPIServerStore(t, true, "127.0.0.1:8100")

	base := time.Date(2026, 10, 29, 4, 1, 0, 0, time.UTC)
	st.EnqueueRecord(store.Record{
		StartedAt: base, CompletedAt: base.Add(150 * time.Millisecond),
		LocalDay: "2026-10-29", MinuteOfDay: 721,
		Path: "/v1/chat/completions", PathClass: "chat",
		Model: "deepseek-chat", Status: 200, LatencyMS: 150,
		ReqBodyBytes: 30, RespBodyBytes: 60,
		Usage:       usage.Usage{PromptTokens: 200, CompletionTokens: 80, CacheReadTokens: 1000, TotalTokens: 280},
		WindowLabel: "base", RatesJSON: analysisRates,
		CostNanoUSD: 2000, Priced: true,
		UsageStatus: usage.StatusOK,
	})
	st.EnqueueMinute(store.MinuteDelta{
		LocalDay: "2026-10-29", MinuteOfDay: 721, PathClass: "chat", Model: "deepseek-chat",
		Requests: 1, ReqBytes: 30, RespBytes: 60,
		PromptTokens: 200, CompletionTokens: 80, CacheReadTokens: 1000,
		CostNanoUSD: 2000, LatencySumMS: 150, LatencyMaxMS: 150,
	})
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	return ts
}

func TestAnalysisEndpoint(t *testing.T) {
	srv := newAnalysisServer(t)
	rec := doGET(t, srv, "/api/analysis?from=2026-10-29&to=2026-10-29")
	if rec.Code != 200 {
		t.Fatalf("analysis = %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Range struct {
			From, To, Interval string
			Partial            bool
		} `json:"range"`
		Trend []map[string]any `json:"trend"`
		Top   struct {
			Cost []map[string]any `json:"cost"`
		} `json:"topRequests"`
		Health struct {
			Requests int64
			TooLarge int64
		} `json:"health"`
		Cache struct {
			Available    bool
			SavedNanoUSD *int64 `json:"savedNanoUsd"`
		} `json:"cache"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Range.Interval != "minute" || len(body.Trend) == 0 || len(body.Top.Cost) == 0 {
		t.Fatalf("body = %+v", body)
	}
	if body.Health.Requests != 3 || body.Cache.SavedNanoUSD == nil || *body.Cache.SavedNanoUSD != 252_000 {
		t.Fatalf("health/cache = %+v", body)
	}
	if body.Range.From != "2026-10-29" || body.Range.To != "2026-10-29" || body.Range.Partial {
		t.Fatalf("range = %+v", body.Range)
	}
	if !body.Cache.Available {
		t.Fatalf("cache = %+v", body.Cache)
	}
}

func TestAnalysisPartialDisablesDetailViews(t *testing.T) {
	srv := newAnalysisServer(t)
	rec := doGET(t, srv, "/api/analysis?from=2020-01-01&to=2020-01-02&interval=day")
	if rec.Code != 200 {
		t.Fatalf("code = %d", rec.Code)
	}
	var body struct {
		Range struct{ Partial bool } `json:"range"`
		Trend []any                  `json:"trend"`
		Top   struct {
			Cost    []any `json:"cost"`
			Tokens  []any `json:"tokens"`
			Latency []any `json:"latency"`
		} `json:"topRequests"`
		Health struct {
			Slow struct {
				P95MS *int64 `json:"p95Ms"`
			} `json:"slow"`
		} `json:"health"`
		Cache struct {
			Available    bool
			SavedNanoUSD *int64 `json:"savedNanoUsd"`
		} `json:"cache"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Range.Partial || len(body.Top.Cost) != 0 || body.Cache.Available || body.Cache.SavedNanoUSD != nil {
		t.Fatalf("partial = %+v", body)
	}
	if body.Top.Tokens == nil || body.Top.Latency == nil {
		t.Fatalf("top arrays must be empty not null: %s", rec.Body.String())
	}
	if body.Health.Slow.P95MS != nil {
		t.Fatalf("partial p95 = %+v", body.Health.Slow.P95MS)
	}
	if body.Trend == nil {
		t.Fatalf("trend must be an array: %s", rec.Body.String())
	}
}

func TestAnalysisIntervalValidation(t *testing.T) {
	srv := newAnalysisServer(t)
	cases := []struct {
		name, query string
	}{
		{"minute too wide", "/api/analysis?from=2026-01-01&to=2026-10-29&interval=minute"},
		{"hour too wide", "/api/analysis?from=2025-01-01&to=2026-10-29&interval=hour"},
		{"unknown interval", "/api/analysis?from=2026-10-29&to=2026-10-29&interval=week"},
	}
	for _, tc := range cases {
		rec := doGET(t, srv, tc.query)
		if rec.Code != 400 {
			t.Fatalf("%s: code = %d %s", tc.name, rec.Code, rec.Body.String())
		}
	}
}

func TestAnalysisAutoIntervalBoundaries(t *testing.T) {
	srv := newAnalysisServer(t)
	cases := []struct {
		name, from, to, want string
	}{
		{"single day", "2026-10-29", "2026-10-29", "minute"},
		{"two days", "2026-10-28", "2026-10-29", "minute"},
		{"three days", "2026-10-27", "2026-10-29", "hour"},
		{"seven days", "2026-10-23", "2026-10-29", "hour"},
		{"eight days", "2026-10-22", "2026-10-29", "day"},
		{"nine days", "2026-10-21", "2026-10-29", "day"},
	}
	for _, tc := range cases {
		rec := doGET(t, srv, "/api/analysis?from="+tc.from+"&to="+tc.to)
		if rec.Code != 200 {
			t.Fatalf("%s: code = %d %s", tc.name, rec.Code, rec.Body.String())
		}
		var body struct {
			Range struct {
				Interval string `json:"interval"`
			} `json:"range"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Range.Interval != tc.want {
			t.Fatalf("%s: interval = %q want %q", tc.name, body.Range.Interval, tc.want)
		}
	}
}

func TestAnalysisDTOShape(t *testing.T) {
	srv := newAnalysisServer(t)
	rec := doGET(t, srv, "/api/analysis?from=2026-10-29&to=2026-10-29&interval=day")
	if rec.Code != 200 {
		t.Fatalf("analysis = %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Range struct {
			From     string `json:"from"`
			To       string `json:"to"`
			Interval string `json:"interval"`
			Partial  bool   `json:"partial"`
		} `json:"range"`
		Trend  []map[string]any `json:"trend"`
		Models []map[string]any `json:"models"`
		Paths  []map[string]any `json:"paths"`
		Top    struct {
			Cost []map[string]any `json:"cost"`
		} `json:"topRequests"`
		Health map[string]any `json:"health"`
		Cache  map[string]any `json:"cache"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Range.Interval != "day" {
		t.Fatalf("interval = %q", body.Range.Interval)
	}
	if len(body.Models) == 0 || len(body.Paths) == 0 {
		t.Fatalf("models/paths = %s", rec.Body.String())
	}
	model := body.Models[0]
	for _, k := range []string{"key", "requests", "errors", "upBytes", "downBytes",
		"promptTokens", "completionTokens", "cacheReadTokens", "costNanoUsd", "unpriced"} {
		if _, ok := model[k]; !ok {
			t.Fatalf("models[0] missing %q: %s", k, rec.Body.String())
		}
	}
	trend := body.Trend[0]
	if _, ok := trend["day"]; !ok {
		t.Fatalf("trend missing day: %s", rec.Body.String())
	}
	if _, ok := trend["minuteOfDay"]; !ok {
		t.Fatalf("trend missing minuteOfDay: %s", rec.Body.String())
	}
	if _, ok := trend["byModel"]; !ok {
		t.Fatalf("trend missing byModel: %s", rec.Body.String())
	}
	top := body.Top.Cost[0]
	for _, k := range []string{"id", "startedAt", "model", "pathClass", "stream",
		"promptTokens", "completionTokens", "cacheReadTokens", "totalTokens",
		"costNanoUsd", "latencyMs", "ttftMs", "status", "usageStatus"} {
		if _, ok := top[k]; !ok {
			t.Fatalf("top missing %q: %s", k, rec.Body.String())
		}
	}
	for _, k := range []string{"requests", "errors", "errorRate", "unmetered", "tooLarge",
		"clientDisconnected", "slow"} {
		if _, ok := body.Health[k]; !ok {
			t.Fatalf("health missing %q: %s", k, rec.Body.String())
		}
	}
	slow := body.Health["slow"].(map[string]any)
	for _, k := range []string{"thresholdMs", "p95Ms", "countOver"} {
		if _, ok := slow[k]; !ok {
			t.Fatalf("health.slow missing %q: %s", k, rec.Body.String())
		}
	}
	for _, k := range []string{"promptTokens", "cacheReadTokens", "hitRate", "savedNanoUsd", "available"} {
		if _, ok := body.Cache[k]; !ok {
			t.Fatalf("cache missing %q: %s", k, rec.Body.String())
		}
	}
}
