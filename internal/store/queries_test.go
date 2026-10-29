package store_test

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/kiry163/llmmeter/internal/store"
	"github.com/kiry163/llmmeter/internal/usage"
)

// seedStore writes two request rows and their minute aggregates for
// 2026-10-29: a priced deepseek-chat call and an unpriced unknown-model call.
func seedStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "seed.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	base := time.Date(2026, 10, 29, 4, 0, 0, 0, time.UTC)
	st.EnqueueRecord(store.Record{
		StartedAt: base, CompletedAt: base.Add(900 * time.Millisecond),
		LocalDay: "2026-10-29", MinuteOfDay: 720,
		Path: "/v1/chat/completions", PathClass: "chat",
		Model: "deepseek-chat", Status: 200, LatencyMS: 120,
		ReqBodyBytes: 10, RespBodyBytes: 20,
		Usage:       usage.Usage{PromptTokens: 100, CompletionTokens: 40, CacheReadTokens: 10, TotalTokens: 140},
		WindowLabel: "base", CostNanoUSD: 1000, Priced: true,
		UsageStatus: usage.StatusOK,
	})
	st.EnqueueMinute(store.MinuteDelta{
		LocalDay: "2026-10-29", MinuteOfDay: 720, PathClass: "chat", Model: "deepseek-chat",
		Requests: 1, ReqBytes: 10, RespBytes: 20,
		PromptTokens: 100, CompletionTokens: 40, CacheReadTokens: 10,
		CostNanoUSD: 1000, LatencySumMS: 120, LatencyMaxMS: 120,
	})

	st.EnqueueRecord(store.Record{
		StartedAt: base.Add(30 * time.Second), CompletedAt: base.Add(31 * time.Second),
		LocalDay: "2026-10-29", MinuteOfDay: 720,
		Path: "/v1/chat/completions", PathClass: "chat",
		Model: "unknown", Status: 200, LatencyMS: 220,
		ReqBodyBytes: 20, RespBodyBytes: 40,
		Usage:       usage.Usage{PromptTokens: 20, TotalTokens: 20},
		WindowLabel: "base", Priced: false,
		UsageStatus: usage.StatusOK,
	})
	st.EnqueueMinute(store.MinuteDelta{
		LocalDay: "2026-10-29", MinuteOfDay: 720, PathClass: "chat", Model: "unknown",
		Requests: 1, ReqBytes: 20, RespBytes: 40,
		PromptTokens: 20, LatencySumMS: 220, LatencyMaxMS: 220,
	})

	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestOverviewAndSeries(t *testing.T) {
	st := seedStore(t)
	ov, err := st.Overview("2026-10-29", "2026-10-29")
	if err != nil {
		t.Fatal(err)
	}
	if ov.Requests != 2 || ov.UpBytes != 30 || ov.DownBytes != 60 || ov.Priced != 1 || ov.Unpriced != 1 {
		t.Fatalf("overview = %+v", ov)
	}
	if ov.PromptTokens != 120 || ov.CompletionTokens != 40 || ov.TotalTokens != 160 {
		t.Fatalf("tokens = %+v", ov)
	}
	if ov.CacheReadTokens != 10 || ov.CostNanoUSD != 1000 {
		t.Fatalf("tokens/cost = %+v", ov)
	}
	if ov.LatencyP50MS == nil || ov.LatencyP95MS == nil {
		t.Fatalf("latency = %+v", ov)
	}
	if ov.LatencyAvgMS == nil || *ov.LatencyAvgMS != 170 {
		t.Fatalf("avg latency = %+v", ov.LatencyAvgMS)
	}
	if *ov.LatencyP50MS != 200 || *ov.LatencyP95MS != 220 {
		t.Fatalf("percentiles = %d %d", *ov.LatencyP50MS, *ov.LatencyP95MS)
	}

	pts, err := st.Series("2026-10-29", "2026-10-29", store.IntervalDay)
	if err != nil || len(pts) != 1 || pts[0].Requests != 2 {
		t.Fatalf("series = %+v err=%v", pts, err)
	}
	if pts[0].Day != "2026-10-29" || pts[0].MinuteOfDay != 0 {
		t.Fatalf("day point = %+v", pts[0])
	}
	if pts[0].UpBytes != 30 || pts[0].DownBytes != 60 || pts[0].CostNanoUSD != 1000 {
		t.Fatalf("day sums = %+v", pts[0])
	}
}

func TestSeriesIntervals(t *testing.T) {
	st := seedStore(t)
	pts, err := st.Series("2026-10-29", "2026-10-29", store.IntervalMinute)
	if err != nil || len(pts) != 1 || pts[0].MinuteOfDay != 720 {
		t.Fatalf("minute series = %+v err=%v", pts, err)
	}
	pts, err = st.Series("2026-10-29", "2026-10-29", store.IntervalHour)
	if err != nil || len(pts) != 1 || pts[0].MinuteOfDay != 12 {
		t.Fatalf("hour series = %+v err=%v", pts, err)
	}
	// Out-of-range days yield no series points.
	pts, err = st.Series("2026-10-30", "2026-10-30", store.IntervalDay)
	if err != nil || len(pts) != 0 {
		t.Fatalf("empty series = %+v err=%v", pts, err)
	}
}

func TestOverviewEmptyRange(t *testing.T) {
	st := seedStore(t)
	ov, err := st.Overview("2026-10-30", "2026-10-30")
	if err != nil {
		t.Fatal(err)
	}
	if ov.LatencyAvgMS != nil || ov.LatencyP50MS != nil || ov.LatencyP95MS != nil {
		t.Fatalf("latency = %+v", ov)
	}
	if ov.Requests != 0 || ov.Priced != 0 || ov.Unpriced != 0 || ov.Unmetered != 0 {
		t.Fatalf("overview = %+v", ov)
	}
}

func TestBreakdown(t *testing.T) {
	st := seedStore(t)
	rows, err := st.Breakdown("2026-10-29", "2026-10-29", "model")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("model rows = %+v", rows)
	}
	// Ordered by cost descending: deepseek-chat first.
	if rows[0].Key != "deepseek-chat" || rows[0].Requests != 1 || rows[0].CostNanoUSD != 1000 || rows[0].Unpriced != 0 {
		t.Fatalf("row0 = %+v", rows[0])
	}
	if rows[1].Key != "unknown" || rows[1].Requests != 1 || rows[1].Unpriced != 1 {
		t.Fatalf("row1 = %+v", rows[1])
	}

	rows, err = st.Breakdown("2026-10-29", "2026-10-29", "pathClass")
	if err != nil || len(rows) != 1 {
		t.Fatalf("pathClass rows = %+v err=%v", rows, err)
	}
	if rows[0].Key != "chat" || rows[0].Requests != 2 || rows[0].Unpriced != 1 {
		t.Fatalf("pathClass row = %+v", rows[0])
	}
	if rows[0].UpBytes != 30 || rows[0].DownBytes != 60 {
		t.Fatalf("pathClass bytes = %+v", rows[0])
	}
}

func TestRequestsCursorAndFilters(t *testing.T) {
	st := seedStore(t)
	page1, err := st.Requests(store.RequestFilter{From: "2026-10-29", To: "2026-10-29", Limit: 1})
	if err != nil || len(page1) != 1 {
		t.Fatalf("page1=%v err=%v", page1, err)
	}
	page2, err := st.Requests(store.RequestFilter{
		From: "2026-10-29", To: "2026-10-29", Limit: 1, BeforeID: page1[0].ID,
	})
	if err != nil || len(page2) != 1 || page2[0].ID >= page1[0].ID {
		t.Fatalf("page2=%+v err=%v", page2, err)
	}
	model := "deepseek-chat"
	filtered, err := st.Requests(store.RequestFilter{
		From: "2026-10-29", To: "2026-10-29", Model: model, Limit: 10,
	})
	if err != nil || len(filtered) != 1 {
		t.Fatalf("filtered=%v err=%v", filtered, err)
	}
	if filtered[0].Model != model || filtered[0].Path != "/v1/chat/completions" || !filtered[0].Priced {
		t.Fatalf("filtered[0] = %+v", filtered[0])
	}
}

func TestRequestsFiltersAndLimit(t *testing.T) {
	st := seedStore(t)

	all, err := st.Requests(store.RequestFilter{From: "2026-10-29", To: "2026-10-29"})
	if err != nil || len(all) != 2 {
		t.Fatalf("all = %+v err=%v", all, err)
	}
	if all[0].ID <= all[1].ID {
		t.Fatalf("not descending: %d %d", all[0].ID, all[1].ID)
	}

	priced := false
	unpriced, err := st.Requests(store.RequestFilter{
		From: "2026-10-29", To: "2026-10-29", Priced: &priced,
	})
	if err != nil || len(unpriced) != 1 || unpriced[0].Model != "unknown" {
		t.Fatalf("unpriced = %+v err=%v", unpriced, err)
	}

	status := 200
	byStatus, err := st.Requests(store.RequestFilter{
		From: "2026-10-29", To: "2026-10-29", Status: &status,
	})
	if err != nil || len(byStatus) != 2 {
		t.Fatalf("byStatus = %+v err=%v", byStatus, err)
	}
	missing := 500
	byStatus, err = st.Requests(store.RequestFilter{
		From: "2026-10-29", To: "2026-10-29", Status: &missing,
	})
	if err != nil || len(byStatus) != 0 {
		t.Fatalf("status 500 = %+v err=%v", byStatus, err)
	}

	ok := usage.StatusOK
	byUsage, err := st.Requests(store.RequestFilter{
		From: "2026-10-29", To: "2026-10-29", UsageStatus: &ok, Limit: 200,
	})
	if err != nil || len(byUsage) != 2 {
		t.Fatalf("byUsage = %+v err=%v", byUsage, err)
	}

	byClass, err := st.Requests(store.RequestFilter{
		From: "2026-10-29", To: "2026-10-29", PathClass: "models",
	})
	if err != nil || len(byClass) != 0 {
		t.Fatalf("byClass = %+v err=%v", byClass, err)
	}
}

func TestRequestsLimitClamp(t *testing.T) {
	st := seedStore(t)
	// Negative and oversized limits fall back to the default and cap.
	rows, err := st.Requests(store.RequestFilter{From: "2026-10-29", To: "2026-10-29", Limit: -1})
	if err != nil || len(rows) != 2 {
		t.Fatalf("limit -1 = %d err=%v", len(rows), err)
	}
	rows, err = st.Requests(store.RequestFilter{From: "2026-10-29", To: "2026-10-29", Limit: 10000})
	if err != nil || len(rows) != 2 {
		t.Fatalf("limit 10000 = %d err=%v", len(rows), err)
	}
}

func TestRequestDetail(t *testing.T) {
	st := seedStore(t)
	rows, err := st.Requests(store.RequestFilter{From: "2026-10-29", To: "2026-10-29", Limit: 1})
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %+v err=%v", rows, err)
	}
	got, err := st.Request(rows[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != rows[0].ID || got.Model != "unknown" || got.LatencyMS != 220 {
		t.Fatalf("detail = %+v", got)
	}
	if got.LocalDay != "2026-10-29" || got.MinuteOfDay != 720 || got.Priced {
		t.Fatalf("detail = %+v", got)
	}
	if got.Usage.PromptTokens != 20 || got.StartedAt.IsZero() || got.CompletedAt.IsZero() {
		t.Fatalf("detail usage/time = %+v", got)
	}

	if _, err := st.Request(999999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing request err = %v", err)
	}
}
