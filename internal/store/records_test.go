package store_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kiry163/llmmeter/internal/store"
	"github.com/kiry163/llmmeter/internal/usage"
)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func testRecord() store.Record {
	base := time.Date(2026, 10, 31, 5, 0, 0, 0, time.UTC)
	return store.Record{
		StartedAt: base, CompletedAt: base.Add(time.Second),
		LocalDay: "2026-10-31", MinuteOfDay: 780,
		Path: "/v1/chat/completions", PathClass: "chat",
		Model: "m", Status: 200, LatencyMS: 1000,
		UsageStatus: usage.StatusOK,
	}
}

// seedTwoUpstreams writes one request per upstream, each with its own key, so
// filters can distinguish the two upstreams and their keys.
func seedTwoUpstreams(t *testing.T, st *store.Store) {
	t.Helper()
	for _, pair := range [][2]string{{"a", "ka"}, {"b", "kb"}} {
		r := testRecord()
		r.UpstreamID, r.UpstreamName = pair[0], strings.ToUpper(pair[0])
		r.KeyID, r.KeyName = pair[1], pair[1]
		st.EnqueueRecord(r)
	}
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
}

func TestRecordRoundTripCarriesUpstreamAndKey(t *testing.T) {
	st := openTestStore(t)
	rec := testRecord()
	rec.UpstreamID, rec.UpstreamName = "deepseek", "DeepSeek"
	rec.KeyID, rec.KeyName = "macbook", "MacBook"
	st.EnqueueRecord(rec)
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	rows, err := st.Requests(store.RequestFilter{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].UpstreamID != "deepseek" || rows[0].KeyName != "MacBook" {
		t.Fatalf("rows = %+v", rows)
	}
	detail, err := st.Request(rows[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.UpstreamName != "DeepSeek" || detail.KeyID != "macbook" {
		t.Fatalf("detail = %+v", detail)
	}
}

func TestRequestsFilterByUpstreamAndKey(t *testing.T) {
	st := openTestStore(t)
	seedTwoUpstreams(t, st)
	rows, err := st.Requests(store.RequestFilter{UpstreamID: "a", KeyID: "ka", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	sum, err := st.RequestsSummary(store.RequestFilter{UpstreamID: "a", KeyID: "ka"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].UpstreamID != "a" || rows[0].KeyID != "ka" {
		t.Fatalf("rows = %+v", rows)
	}
	if sum.Requests != 1 {
		t.Fatalf("sum = %+v", sum)
	}

	byUpstream, err := st.Requests(store.RequestFilter{UpstreamID: "b", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(byUpstream) != 1 || byUpstream[0].UpstreamID != "b" {
		t.Fatalf("by upstream = %+v", byUpstream)
	}
}

// TestMinuteDeltaSplitsUpstreams pins the aggregate key: the same minute,
// path class and model under two upstreams is two rows, and there is no key
// dimension in traffic_minutes.
func TestMinuteDeltaSplitsUpstreams(t *testing.T) {
	st := openTestStore(t)
	for _, upstream := range []string{"a", "b"} {
		st.EnqueueMinute(store.MinuteDelta{
			LocalDay: "2026-10-31", MinuteOfDay: 780, PathClass: "chat",
			UpstreamID: upstream, Model: "m", Requests: 1, LatencySumMS: 100,
		})
	}
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM traffic_minutes
		WHERE local_day = '2026-10-31' AND minute_of_day = 780`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("rows = %d, want one per upstream", n)
	}

	// Repeating one upstream accumulates into its own row only.
	st.EnqueueMinute(store.MinuteDelta{
		LocalDay: "2026-10-31", MinuteOfDay: 780, PathClass: "chat",
		UpstreamID: "a", Model: "m", Requests: 2,
	})
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := st.DB().QueryRow(`SELECT requests FROM traffic_minutes
		WHERE upstream_id = 'a' AND local_day = '2026-10-31' AND minute_of_day = 780`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("upstream a requests = %d, want 3", n)
	}
}
