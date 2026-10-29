package metrics

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/kiry163/llmmeter/internal/store"
	"github.com/kiry163/llmmeter/internal/usage"
)

type fakeSink struct{ deltas []store.MinuteDelta }

func (f *fakeSink) EnqueueMinute(d store.MinuteDelta) { f.deltas = append(f.deltas, d) }

func TestSnapshotQPSAndPercentiles(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 30, 0, time.UTC)
	clock := func() time.Time { return now }
	h := New(&fakeSink{}, clock)
	for i := range 4 {
		f := h.Started("chat", "m")
		h.Finished(f, Observation{
			CompletedAt: now.Add(-time.Duration(i) * time.Second),
			LocalDay:    "2026-10-29", MinuteOfDay: 720, PathClass: "chat", Model: "m",
			Status: 200, UsageStatus: usage.StatusOK, ReqBytes: 10, RespBytes: 20,
			LatencyMS: int64(100 * (i + 1)),
		})
	}
	s := h.Snapshot(60 * time.Second)
	if s.QPS <= 0 || math.Abs(s.UpBps-40.0/60) > 0.01 || math.Abs(s.DownBps-80.0/60) > 0.01 || s.Inflight != 0 {
		t.Fatalf("snapshot = %+v", s)
	}
	if s.LatencyP50MS == nil || *s.LatencyP50MS < 200 {
		t.Fatalf("p50 = %v", s.LatencyP50MS)
	}
	if len(s.ByModel) != 1 || s.ByModel[0].Model != "m" {
		t.Fatalf("byModel = %+v", s.ByModel)
	}
}

func TestMinuteFlushSendsOnlyDelta(t *testing.T) {
	sink := &fakeSink{}
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	h := New(sink, func() time.Time { return now })
	f := h.Started("chat", "m")
	h.Finished(f, Observation{CompletedAt: now, LocalDay: "2026-10-29", MinuteOfDay: 720, PathClass: "chat", Model: "m", Status: 200})
	h.FlushForTest()
	h.FlushForTest()
	if len(sink.deltas) != 1 || sink.deltas[0].Requests != 1 {
		t.Fatalf("deltas = %+v", sink.deltas)
	}
}

func TestSlowSubscriberDropped(t *testing.T) {
	h := New(&fakeSink{}, time.Now)
	ch, cancel := h.Subscribe(60 * time.Second)
	defer cancel()
	for range 9 {
		h.broadcastOnce()
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("slow subscriber was not closed")
		}
	}
}

func TestSnapshotErrorsAndInflight(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 30, 0, time.UTC)
	h := New(&fakeSink{}, func() time.Time { return now })
	base := Observation{
		CompletedAt: now, LocalDay: "2026-10-29", MinuteOfDay: 720,
		PathClass: "chat", Model: "m", Status: 200, ReqBytes: 5, RespBytes: 6,
	}
	for _, o := range []Observation{
		{Status: 500},
		{Status: 200, UsageStatus: usage.StatusError},
		{Status: 200, UsageStatus: "client_disconnected"},
	} {
		obs := base
		obs.Status, obs.UsageStatus = o.Status, o.UsageStatus
		h.Finished(h.Started("chat", "m"), obs)
	}
	s := h.Snapshot(60 * time.Second)
	if s.Errors != 2 {
		t.Fatalf("errors = %d", s.Errors)
	}
	if len(s.ByModel) != 1 || s.ByModel[0].Errors != 2 || s.ByModel[0].QPS != 3.0/60 {
		t.Fatalf("byModel = %+v", s.ByModel)
	}
	if s.LatencyP50MS == nil || *s.LatencyP50MS != 50 || s.LatencyP95MS == nil || *s.LatencyP95MS != 50 {
		t.Fatalf("percentiles for zero-latency samples = %v/%v", s.LatencyP50MS, s.LatencyP95MS)
	}
	empty := New(&fakeSink{}, func() time.Time { return now }).Snapshot(60 * time.Second)
	if empty.LatencyP50MS != nil || empty.LatencyP95MS != nil {
		t.Fatalf("percentiles without samples = %v/%v", empty.LatencyP50MS, empty.LatencyP95MS)
	}

	var inflight []*Inflight
	for range 25 {
		inflight = append(inflight, h.Started("chat", "m"))
	}
	s = h.Snapshot(60 * time.Second)
	if s.Inflight != 25 || len(s.InflightRequests) != 20 {
		t.Fatalf("inflight = %d, listed = %d", s.Inflight, len(s.InflightRequests))
	}
	for _, f := range inflight {
		h.Finished(f, base)
	}
	if s := h.Snapshot(60 * time.Second); s.Inflight != 0 {
		t.Fatalf("inflight after finish = %d", s.Inflight)
	}
}

func TestSnapshotWindowExcludesOldTraffic(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 1, 0, 0, time.UTC)
	h := New(&fakeSink{}, func() time.Time { return now })
	obs := Observation{LocalDay: "2026-10-29", MinuteOfDay: 720, PathClass: "chat", Model: "m", Status: 200}

	old := obs
	old.CompletedAt = now.Add(-61 * time.Second)
	h.Finished(h.Started("chat", "m"), old)
	fresh := obs
	fresh.CompletedAt = now.Add(-2 * time.Second)
	h.Finished(h.Started("chat", "m"), fresh)

	if s := h.Snapshot(60 * time.Second); s.QPS != 1.0/60 || len(s.Spark) != 1 || s.Spark[0].OffsetS != -2 || s.Spark[0].Requests != 1 {
		t.Fatalf("snapshot = %+v", s)
	}
	if s := h.Snapshot(300 * time.Second); s.QPS != 2.0/300 || len(s.Spark) != 2 || s.Spark[0].OffsetS != -61 {
		t.Fatalf("snapshot 300 = %+v", s)
	}
}

func TestSnapshotJSONFieldNames(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 30, 0, time.UTC)
	h := New(&fakeSink{}, func() time.Time { return now })
	h.Finished(h.Started("chat", "m"), Observation{
		CompletedAt: now, LocalDay: "2026-10-29", MinuteOfDay: 720,
		PathClass: "chat", Model: "m", Status: 200, LatencyMS: 120,
	})
	raw, err := json.Marshal(h.Snapshot(60 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"ts", "windowS", "inflight", "qps", "errors", "upBps", "downBps",
		"latencyP50Ms", "latencyP95Ms", "byModel", "spark", "inflightRequests",
	} {
		if _, ok := got[key]; !ok {
			t.Fatalf("missing JSON key %q in %s", key, raw)
		}
	}
	var spark []map[string]json.RawMessage
	if err := json.Unmarshal(got["spark"], &spark); err != nil {
		t.Fatal(err)
	}
	if len(spark) != 1 {
		t.Fatalf("spark = %s", got["spark"])
	}
	for _, key := range []string{"offsetS", "requests", "upBytes", "downBytes"} {
		if _, ok := spark[0][key]; !ok {
			t.Fatalf("missing spark key %q in %s", key, got["spark"])
		}
	}
	var models []map[string]json.RawMessage
	if err := json.Unmarshal(got["byModel"], &models); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"model", "qps", "errors"} {
		if _, ok := models[0][key]; !ok {
			t.Fatalf("missing byModel key %q in %s", key, got["byModel"])
		}
	}
	if string(got["ts"]) != `"2026-10-29T12:00:30Z"` {
		t.Fatalf("ts = %s", got["ts"])
	}
}

func TestMinuteAccumulatorSumsAndPrunes(t *testing.T) {
	sink := &fakeSink{}
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	h := New(sink, func() time.Time { return now })
	obs := Observation{
		CompletedAt: now, LocalDay: "2026-10-29", MinuteOfDay: 720,
		PathClass: "chat", Model: "m", Status: 200,
		ReqBytes: 10, RespBytes: 20, PromptTokens: 3, CompletionTokens: 4,
		CacheReadTokens: 5, ReasoningTokens: 6, CostNanoUSD: 7, LatencyMS: 120,
	}
	h.Finished(h.Started("chat", "m"), obs)
	obs.CompletedAt = now.Add(30 * time.Second)
	obs.LatencyMS = 300
	obs.Status = 502
	obs.Model = "m2"
	h.Finished(h.Started("chat", "m2"), obs)

	h.FlushForTest()
	if len(sink.deltas) != 2 {
		t.Fatalf("deltas = %+v", sink.deltas)
	}
	byModel := map[string]store.MinuteDelta{}
	for _, d := range sink.deltas {
		byModel[d.Model] = d
	}
	d := byModel["m"]
	if d.Requests != 1 || d.Errors != 0 || d.ReqBytes != 10 || d.RespBytes != 20 ||
		d.PromptTokens != 3 || d.CompletionTokens != 4 || d.CacheReadTokens != 5 ||
		d.ReasoningTokens != 6 || d.CostNanoUSD != 7 || d.LatencySumMS != 120 || d.LatencyMaxMS != 120 {
		t.Fatalf("delta m = %+v", d)
	}
	if d := byModel["m2"]; d.Requests != 1 || d.Errors != 1 || d.LatencyMaxMS != 300 {
		t.Fatalf("delta m2 = %+v", d)
	}

	now = now.Add(3 * time.Minute)
	h.Finished(h.Started("chat", "m"), Observation{
		CompletedAt: now, LocalDay: "2026-10-29", MinuteOfDay: 723, PathClass: "chat", Model: "m", Status: 200,
	})
	h.FlushForTest()
	h.mu.Lock()
	keys := len(h.minutes)
	h.mu.Unlock()
	if keys != 1 {
		t.Fatalf("stale minute keys not pruned: %d", keys)
	}
}

func TestStopFlushesRemainingDeltas(t *testing.T) {
	sink := &fakeSink{}
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	h := New(sink, func() time.Time { return now })
	h.Finished(h.Started("chat", "m"), Observation{
		CompletedAt: now, LocalDay: "2026-10-29", MinuteOfDay: 720,
		PathClass: "chat", Model: "m", Status: 200,
	})
	h.Stop()
	h.Stop()
	if len(sink.deltas) != 1 || sink.deltas[0].Requests != 1 {
		t.Fatalf("deltas = %+v", sink.deltas)
	}
}

func TestSubscribeCancelClosesChannel(t *testing.T) {
	h := New(&fakeSink{}, time.Now)
	ch, cancel := h.Subscribe(60 * time.Second)
	cancel()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("channel delivered a frame after cancel")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("channel not closed by cancel")
	}
	cancel() // idempotent
}
