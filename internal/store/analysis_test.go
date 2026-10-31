package store_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/kiry163/llmmeter/internal/store"
	"github.com/kiry163/llmmeter/internal/usage"
)

// analysisRates is the per-MTok rate snapshot stored on priced requests.
const analysisRates = `{"model":"m","window":"base","input_per_mtok":0.28,"output_per_mtok":0.42,"cache_read_per_mtok":0.028,"cache_write_per_mtok":0,"reasoning_per_mtok":0.42}`

// seedAnalysisStore writes three 2026-10-29 requests and their minute
// aggregates: a streamed priced call at 10:15, a non-streamed priced call at
// 09:30 and a non-streamed 500 / too_large / disconnected call at 09:35.
func seedAnalysisStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "analysis.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	base := time.Date(2026, 10, 29, 9, 30, 0, 0, time.UTC)

	// 10:15, streamed, priced, 1000 cached tokens.
	st.EnqueueRecord(store.Record{
		StartedAt: base.Add(45 * time.Minute), CompletedAt: base.Add(45*time.Minute + 120*time.Millisecond),
		LocalDay: "2026-10-29", MinuteOfDay: 615,
		Path: "/v1/chat/completions", PathClass: "chat",
		Model: "deepseek-chat", Stream: true, Status: 200, LatencyMS: 120, TTFTMS: new(int64(50)),
		ReqBodyBytes: 10, RespBodyBytes: 20,
		Usage: usage.Usage{
			PromptTokens: 200, CompletionTokens: 100, CacheReadTokens: 1000, TotalTokens: 300,
		},
		WindowLabel: "base", RatesJSON: analysisRates,
		CostNanoUSD: 1_200_000, Priced: true, UsageStatus: usage.StatusOK,
	})
	st.EnqueueMinute(store.MinuteDelta{
		LocalDay: "2026-10-29", MinuteOfDay: 615, PathClass: "chat", Model: "deepseek-chat",
		Requests: 1, ReqBytes: 10, RespBytes: 20,
		PromptTokens: 200, CompletionTokens: 100, CacheReadTokens: 1000,
		CostNanoUSD: 1_200_000, LatencySumMS: 120, LatencyMaxMS: 120,
	})

	// 09:30, not streamed, priced, no cached tokens.
	st.EnqueueRecord(store.Record{
		StartedAt: base, CompletedAt: base.Add(220 * time.Millisecond),
		LocalDay: "2026-10-29", MinuteOfDay: 570,
		Path: "/v1/chat/completions", PathClass: "chat",
		Model: "deepseek-chat", Status: 200, LatencyMS: 220,
		ReqBodyBytes: 15, RespBodyBytes: 30,
		Usage: usage.Usage{
			PromptTokens: 150, CompletionTokens: 50, TotalTokens: 200,
		},
		WindowLabel: "base", RatesJSON: analysisRates,
		CostNanoUSD: 500_000, Priced: true, UsageStatus: usage.StatusOK,
	})
	st.EnqueueMinute(store.MinuteDelta{
		LocalDay: "2026-10-29", MinuteOfDay: 570, PathClass: "chat", Model: "deepseek-chat",
		Requests: 1, ReqBytes: 15, RespBytes: 30,
		PromptTokens: 150, CompletionTokens: 50,
		CostNanoUSD: 500_000, LatencySumMS: 220, LatencyMaxMS: 220,
	})

	// 09:35, not streamed, failed and unmetered.
	st.EnqueueRecord(store.Record{
		StartedAt: base.Add(5 * time.Minute), CompletedAt: base.Add(5*time.Minute + 900*time.Millisecond),
		LocalDay: "2026-10-29", MinuteOfDay: 575,
		Path: "/v1/chat/completions", PathClass: "chat",
		Model: "other-model", Status: 500, LatencyMS: 900,
		ReqBodyBytes: 25, RespBodyBytes: 40,
		UsageStatus: usage.StatusTooLarge, ClientDisconnected: true,
	})
	st.EnqueueMinute(store.MinuteDelta{
		LocalDay: "2026-10-29", MinuteOfDay: 575, PathClass: "chat", Model: "other-model",
		Requests: 1, Errors: 1, ReqBytes: 25, RespBytes: 40,
		LatencySumMS: 900, LatencyMaxMS: 900,
	})

	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	return st
}

// trendSeriesFor returns the single trend series of one model in a bucket.
func trendSeriesFor(t *testing.T, b store.TrendBucket, model string) store.TrendSeries {
	t.Helper()
	for _, s := range b.Series {
		if s.Model == model {
			return s
		}
	}
	t.Fatalf("bucket %+v has no series for %q", b, model)
	return store.TrendSeries{}
}

func TestTrendByModelAndTop(t *testing.T) {
	st := seedAnalysisStore(t)
	ctx := context.Background()
	trend, err := st.TrendByModel(ctx, "2026-10-29", "2026-10-29", store.IntervalHour, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(trend) != 2 {
		t.Fatalf("buckets = %+v", trend)
	}
	if trend[1].MinuteOfDay != 10 || trendSeriesFor(t, trend[1], "deepseek-chat").CostNanoUSD != 1_200_000 {
		t.Fatalf("bucket = %+v", trend[1])
	}

	top, err := st.TopRequests(ctx, "2026-10-29", "2026-10-29", "cost", 10, "")
	if err != nil || len(top) != 2 || top[0].CostNanoUSD < top[1].CostNanoUSD {
		t.Fatalf("top cost = %+v err=%v", top, err)
	}
	byTokens, err := st.TopRequests(ctx, "2026-10-29", "2026-10-29", "tokens", 1, "")
	if err != nil || len(byTokens) != 1 || byTokens[0].TotalTokens != 300 {
		t.Fatalf("top tokens=%+v", byTokens)
	}
	if _, err := st.TopRequests(ctx, "2026-10-29", "2026-10-29", "bogus", 10, ""); err == nil {
		t.Fatal("invalid by must error")
	}
}

func TestTrendByModelIntervals(t *testing.T) {
	st := seedAnalysisStore(t)
	ctx := context.Background()

	minute, err := st.TrendByModel(ctx, "2026-10-29", "2026-10-29", store.IntervalMinute, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(minute) != 3 || minute[0].MinuteOfDay != 570 ||
		trendSeriesFor(t, minute[0], "deepseek-chat").Requests != 1 {
		t.Fatalf("minute buckets = %+v", minute)
	}

	day, err := st.TrendByModel(ctx, "2026-10-29", "2026-10-29", store.IntervalDay, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(day) != 1 || day[0].MinuteOfDay != 0 ||
		trendSeriesFor(t, day[0], "deepseek-chat").CostNanoUSD != 1_700_000 {
		t.Fatalf("day buckets = %+v", day)
	}
}

func TestAnalysisHealthAndCache(t *testing.T) {
	st := seedAnalysisStore(t)
	ctx := context.Background()
	h, err := st.AnalysisHealth(ctx, "2026-10-29", "2026-10-29", "")
	if err != nil {
		t.Fatal(err)
	}
	if h.Requests != 3 || h.Errors != 1 || h.Unmetered != 1 || h.TooLarge != 1 ||
		h.ClientDisconnected != 1 || h.P95MS == nil || h.CountOver != 0 || h.ThresholdMS != 2000 {
		t.Fatalf("health = %+v", h)
	}
	c, err := st.CacheStats(ctx, "2026-10-29", "2026-10-29", "")
	if err != nil {
		t.Fatal(err)
	}
	// 1000 cached tokens, rates input 0.28 / cache 0.028 => 252000 nano
	if c.SavedNanoUSD == nil || *c.SavedNanoUSD != 252_000 || c.CacheReadTokens != 1000 {
		t.Fatalf("cache = %+v", c)
	}
	obs, err := st.ObservedModels(ctx, "2026-10-29")
	if err != nil || len(obs) != 2 || obs[0].Model == "" {
		t.Fatalf("observed=%+v err=%v", obs, err)
	}
	if obs[0].Model != "deepseek-chat" || obs[0].Requests != 2 {
		t.Fatalf("observed[0] = %+v", obs[0])
	}
}

func TestRequestsStreamFilter(t *testing.T) {
	st := seedAnalysisStore(t)
	yes, err := st.Requests(store.RequestFilter{From: "2026-10-29", To: "2026-10-29", Stream: new(true), Limit: 10})
	if err != nil || len(yes) != 1 || !yes[0].Stream {
		t.Fatalf("stream=true = %+v", yes)
	}
	no, err := st.Requests(store.RequestFilter{From: "2026-10-29", To: "2026-10-29", Stream: new(false), Limit: 10})
	if err != nil || len(no) != 2 {
		t.Fatalf("stream=false = %+v", no)
	}
}

// TestAnalysisKeepsUpstreamDimension pins that analysis aggregates and top
// requests keep the upstream (and, for detail rows, key) dimension instead of
// merging same-named models across upstreams.
func TestAnalysisKeepsUpstreamDimension(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	seed := []struct {
		upstream, upstreamName, key, keyName string
		cost, cached                         int64
	}{
		{"a", "Upstream A", "ka", "Key A", 2000, 10},
		{"b", "Upstream B", "kb", "Key B", 1000, 5},
	}
	for _, s := range seed {
		r := testRecord()
		r.UpstreamID, r.UpstreamName = s.upstream, s.upstreamName
		r.KeyID, r.KeyName = s.key, s.keyName
		r.Usage = usage.Usage{PromptTokens: 10, CompletionTokens: 5, CacheReadTokens: s.cached, TotalTokens: 15}
		r.CostNanoUSD, r.Priced = s.cost, true
		st.EnqueueRecord(r)
		st.EnqueueMinute(store.MinuteDelta{
			LocalDay: "2026-10-31", MinuteOfDay: 780, PathClass: "chat",
			UpstreamID: s.upstream, Model: "m", Requests: 1,
			PromptTokens: 10, CompletionTokens: 5, CacheReadTokens: s.cached,
			CostNanoUSD: s.cost, LatencySumMS: 1000, LatencyMaxMS: 1000,
		})
	}
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}

	trend, err := st.TrendByModel(ctx, "2026-10-31", "2026-10-31", store.IntervalDay, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(trend) != 1 || len(trend[0].Series) != 2 {
		t.Fatalf("trend = %+v", trend)
	}
	if trend[0].Series[0].UpstreamID != "a" || trend[0].Series[1].UpstreamID != "b" {
		t.Fatalf("series upstreams = %+v", trend[0].Series)
	}
	if trend[0].Series[0].CacheReadTokens != 10 || trend[0].Series[0].CostNanoUSD != 2000 {
		t.Fatalf("series[0] = %+v", trend[0].Series[0])
	}

	// The upstream filter narrows every aggregate to that upstream only.
	filtered, err := st.TrendByModel(ctx, "2026-10-31", "2026-10-31", store.IntervalDay, "a")
	if err != nil || len(filtered) != 1 || len(filtered[0].Series) != 1 ||
		filtered[0].Series[0].UpstreamID != "a" {
		t.Fatalf("filtered trend = %+v err=%v", filtered, err)
	}

	rows, err := st.Breakdown("2026-10-31", "2026-10-31", "model", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].UpstreamID != "a" || rows[1].UpstreamID != "b" {
		t.Fatalf("breakdown = %+v", rows)
	}
	if rows[0].Key != "m" || rows[0].Requests != 1 || rows[0].UpBytes != 0 {
		t.Fatalf("breakdown row0 = %+v", rows[0])
	}
	if rows, err = st.Breakdown("2026-10-31", "2026-10-31", "model", "b"); err != nil ||
		len(rows) != 1 || rows[0].UpstreamID != "b" {
		t.Fatalf("filtered breakdown = %+v err=%v", rows, err)
	}

	top, err := st.TopRequests(ctx, "2026-10-31", "2026-10-31", "cost", 10, "")
	if err != nil || len(top) != 2 {
		t.Fatalf("top = %+v err=%v", top, err)
	}
	if top[0].UpstreamID != "a" || top[0].UpstreamName != "Upstream A" ||
		top[0].KeyID != "ka" || top[0].KeyName != "Key A" {
		t.Fatalf("top[0] = %+v", top[0])
	}
	if top, err = st.TopRequests(ctx, "2026-10-31", "2026-10-31", "cost", 10, "b"); err != nil ||
		len(top) != 1 || top[0].UpstreamID != "b" {
		t.Fatalf("filtered top = %+v err=%v", top, err)
	}

	health, err := st.AnalysisHealth(ctx, "2026-10-31", "2026-10-31", "a")
	if err != nil || health.Requests != 1 || health.Errors != 0 {
		t.Fatalf("filtered health = %+v err=%v", health, err)
	}
	allHealth, err := st.AnalysisHealth(ctx, "2026-10-31", "2026-10-31", "")
	if err != nil || allHealth.Requests != 2 {
		t.Fatalf("health = %+v err=%v", allHealth, err)
	}
	cache, err := st.CacheStats(ctx, "2026-10-31", "2026-10-31", "a")
	if err != nil || cache.PromptTokens != 10 || cache.CacheReadTokens != 10 {
		t.Fatalf("filtered cache = %+v err=%v", cache, err)
	}
	allCache, err := st.CacheStats(ctx, "2026-10-31", "2026-10-31", "")
	if err != nil || allCache.PromptTokens != 20 {
		t.Fatalf("cache = %+v err=%v", allCache, err)
	}
}
