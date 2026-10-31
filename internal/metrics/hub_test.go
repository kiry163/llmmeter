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
		f := h.Started("chat", "u", "m")
		h.Finished(f, Observation{
			CompletedAt: now.Add(-time.Duration(i) * time.Second),
			LocalDay:    "2026-10-29", MinuteOfDay: 720, UpstreamID: "u", PathClass: "chat", Model: "m",
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
	f := h.Started("chat", "u", "m")
	h.Finished(f, Observation{CompletedAt: now, LocalDay: "2026-10-29", MinuteOfDay: 720, UpstreamID: "u", PathClass: "chat", Model: "m", Status: 200})
	h.FlushForTest()
	h.FlushForTest()
	if len(sink.deltas) != 1 || sink.deltas[0].Requests != 1 {
		t.Fatalf("deltas = %+v", sink.deltas)
	}
}

func TestMinuteFlushSplitsUpstreams(t *testing.T) {
	sink := &fakeSink{}
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	h := New(sink, func() time.Time { return now })
	obs := Observation{
		CompletedAt: now, LocalDay: "2026-10-29", MinuteOfDay: 720,
		PathClass: "chat", Model: "m", Status: 200,
		PromptTokens: 10, CacheReadTokens: 2,
	}
	for _, upstream := range []string{"a", "b"} {
		o := obs
		o.UpstreamID = upstream
		h.Finished(h.Started("chat", upstream, "m"), o)
	}
	h.FlushForTest()
	if len(sink.deltas) != 2 {
		t.Fatalf("deltas = %+v", sink.deltas)
	}
	got := map[string]store.MinuteDelta{}
	for _, d := range sink.deltas {
		got[d.UpstreamID] = d
	}
	for _, upstream := range []string{"a", "b"} {
		d, ok := got[upstream]
		if !ok || d.Requests != 1 || d.PromptTokens != 10 || d.CacheReadTokens != 2 || d.Model != "m" {
			t.Fatalf("delta %q = %+v (all = %+v)", upstream, d, sink.deltas)
		}
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
		UpstreamID: "u", PathClass: "chat", Model: "m", Status: 200, ReqBytes: 5, RespBytes: 6,
	}
	for _, o := range []Observation{
		{Status: 500},
		{Status: 200, UsageStatus: usage.StatusError},
		{Status: 200, UsageStatus: "client_disconnected"},
	} {
		obs := base
		obs.Status, obs.UsageStatus = o.Status, o.UsageStatus
		h.Finished(h.Started("chat", "u", "m"), obs)
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
		inflight = append(inflight, h.Started("chat", "u", "m"))
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

func TestSnapshotSeparatesUpstreams(t *testing.T) {
	h := New(nil, nil)
	start := h.Started("a", "chat", "m1")
	h.Finished(start, Observation{UpstreamID: "a", Model: "m1", Status: 200, UsageStatus: "ok"})
	start = h.Started("b", "chat", "m2")
	h.Finished(start, Observation{UpstreamID: "b", Model: "m2", Status: 200, UsageStatus: "ok"})
	s := h.Snapshot(60 * time.Second)
	if s.ByUpstream["a"].Requests != 1 || s.ByUpstream["b"].Requests != 1 {
		t.Fatalf("byUpstream = %+v", s.ByUpstream)
	}
}

func TestSnapshotUpstreamStatsAndCacheHit(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 30, 0, time.UTC)
	h := New(&fakeSink{}, func() time.Time { return now })
	finish := func(upstream, model string, status int, latency, prompt, cache int64) {
		h.Finished(h.Started("chat", upstream, model), Observation{
			CompletedAt: now, UpstreamID: upstream, PathClass: "chat", Model: model,
			Status: status, LatencyMS: latency,
			PromptTokens: prompt, CacheReadTokens: cache,
		})
	}
	finish("a", "m1", 200, 100, 1000, 250)
	finish("a", "m1", 200, 300, 1000, 750)
	finish("b", "m2", 500, 900, 0, 0)

	s := h.Snapshot(60 * time.Second)
	a := s.ByUpstream["a"]
	if a.Requests != 2 || a.Errors != 0 || a.QPS != 2.0/60 {
		t.Fatalf("upstream a = %+v", a)
	}
	if a.PromptTokens != 2000 || a.CacheReadTokens != 1000 ||
		a.CacheHitRate == nil || math.Abs(*a.CacheHitRate-0.5) > 1e-9 {
		t.Fatalf("upstream a cache = %+v", a)
	}
	if a.LatencyP50MS == nil || *a.LatencyP50MS != 100 || a.LatencyP95MS == nil || *a.LatencyP95MS != 400 {
		t.Fatalf("upstream a latency = %v/%v", a.LatencyP50MS, a.LatencyP95MS)
	}
	if len(a.Spark) != 60 || a.Spark[59].OffsetS != 0 || a.Spark[59].Requests != 2 {
		t.Fatalf("upstream a spark = %+v", a.Spark)
	}
	b := s.ByUpstream["b"]
	if b.Requests != 1 || b.Errors != 1 || b.PromptTokens != 0 || b.CacheHitRate != nil {
		t.Fatalf("upstream b = %+v", b)
	}
	if b.LatencyP95MS == nil || *b.LatencyP95MS != 1600 {
		t.Fatalf("upstream b latency = %v", b.LatencyP95MS)
	}

	if s.QPS != 3.0/60 || s.Errors != 1 || s.PromptTokens != 2000 || s.CacheReadTokens != 1000 {
		t.Fatalf("global snapshot = %+v", s)
	}
	if s.CacheHitRate == nil || math.Abs(*s.CacheHitRate-0.5) > 1e-9 {
		t.Fatalf("global cacheHitRate = %v", s.CacheHitRate)
	}
	if sparkRequests(s.Spark) != 3 {
		t.Fatalf("global spark = %+v", s.Spark)
	}
	if len(s.ByModel) != 2 || s.ByModel[0].UpstreamID != "a" || s.ByModel[0].Model != "m1" ||
		s.ByModel[1].UpstreamID != "b" || s.ByModel[1].Model != "m2" {
		t.Fatalf("byModel = %+v", s.ByModel)
	}

	h.Started("chat", "b", "m2")
	s = h.Snapshot(60 * time.Second)
	if s.ByUpstream["b"].Inflight != 1 || s.Inflight != 1 {
		t.Fatalf("inflight byUpstream = %+v", s.ByUpstream["b"])
	}
	if len(s.InflightRequests) != 1 || s.InflightRequests[0].UpstreamID != "b" {
		t.Fatalf("inflightRequests = %+v", s.InflightRequests)
	}
}

func TestUpstreamSparkExpiresWithWindow(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 1, 0, 0, time.UTC)
	h := New(&fakeSink{}, func() time.Time { return now })
	obs := func(upstream string, at time.Time) Observation {
		return Observation{
			CompletedAt: at, UpstreamID: upstream,
			PathClass: "chat", Model: "m", Status: 200,
		}
	}
	h.Finished(h.Started("chat", "a", "m"), obs("a", now.Add(-2*time.Second)))
	h.Finished(h.Started("chat", "a", "m"), obs("a", now.Add(-61*time.Second)))

	s := h.Snapshot(60 * time.Second)
	a, ok := s.ByUpstream["a"]
	if !ok || a.Requests != 1 || len(a.Spark) != 60 {
		t.Fatalf("upstream a = %+v", a)
	}
	if a.Spark[57].OffsetS != s.Spark[57].OffsetS || a.Spark[57].OffsetS != -2 || a.Spark[57].Requests != 1 {
		t.Fatalf("upstream spark = %+v, global = %+v", a.Spark[57], s.Spark[57])
	}
	if _, ok := h.Snapshot(time.Second).ByUpstream["a"]; ok {
		t.Fatalf("upstream with no traffic in window still listed: %+v", h.Snapshot(time.Second).ByUpstream)
	}
}

func TestInflightUpstreamAppearsWithoutTraffic(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 30, 0, time.UTC)
	h := New(&fakeSink{}, func() time.Time { return now })
	h.Started("chat", "fresh", "m")
	s := h.Snapshot(60 * time.Second)
	us, ok := s.ByUpstream["fresh"]
	if !ok || us.Inflight != 1 || us.Requests != 0 || us.CacheHitRate != nil || len(us.Spark) != 60 {
		t.Fatalf("byUpstream = %+v", s.ByUpstream)
	}
	if len(s.InflightRequests) != 1 || s.InflightRequests[0].UpstreamID != "fresh" ||
		s.InflightRequests[0].PathClass != "chat" || s.InflightRequests[0].Model != "m" {
		t.Fatalf("inflightRequests = %+v", s.InflightRequests)
	}
}

func TestSnapshotWindowExcludesOldTraffic(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 1, 0, 0, time.UTC)
	h := New(&fakeSink{}, func() time.Time { return now })
	obs := Observation{LocalDay: "2026-10-29", MinuteOfDay: 720, UpstreamID: "u", PathClass: "chat", Model: "m", Status: 200}

	old := obs
	old.CompletedAt = now.Add(-61 * time.Second)
	h.Finished(h.Started("chat", "u", "m"), old)
	fresh := obs
	fresh.CompletedAt = now.Add(-2 * time.Second)
	h.Finished(h.Started("chat", "u", "m"), fresh)

	s := h.Snapshot(60 * time.Second)
	if s.QPS != 1.0/60 || len(s.Spark) != 60 || sparkRequests(s.Spark) != 1 {
		t.Fatalf("snapshot = %+v", s)
	}
	if p := s.Spark[57]; p.OffsetS != -2 || p.Requests != 1 {
		t.Fatalf("spark[-2s] = %+v", p)
	}
	s = h.Snapshot(300 * time.Second)
	if s.QPS != 2.0/300 || len(s.Spark) != 300 || sparkRequests(s.Spark) != 2 {
		t.Fatalf("snapshot 300 = %+v", s)
	}
	if p := s.Spark[238]; p.OffsetS != -61 || p.Requests != 1 {
		t.Fatalf("spark[-61s] = %+v", p)
	}
}

func sparkRequests(points []SparkPoint) int64 {
	var n int64
	for _, p := range points {
		n += p.Requests
	}
	return n
}

func TestSparkOnePointPerSecond(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 30, 0, time.UTC)
	h := New(&fakeSink{}, func() time.Time { return now })
	s := h.Snapshot(60 * time.Second)
	if len(s.Spark) != 60 || sparkRequests(s.Spark) != 0 {
		t.Fatalf("idle spark = %+v", s.Spark)
	}
	for i, p := range s.Spark {
		if p.OffsetS != i-59 || p.Requests != 0 || p.UpBytes != 0 || p.DownBytes != 0 {
			t.Fatalf("idle spark[%d] = %+v", i, p)
		}
	}

	h.Finished(h.Started("chat", "u", "m"), Observation{
		CompletedAt: now, LocalDay: "2026-10-29", MinuteOfDay: 720,
		UpstreamID: "u", PathClass: "chat", Model: "m", Status: 200, ReqBytes: 3, RespBytes: 4,
	})
	s = h.Snapshot(60 * time.Second)
	if len(s.Spark) != 60 || sparkRequests(s.Spark) != 1 {
		t.Fatalf("spark = %+v", s.Spark)
	}
	if p := s.Spark[59]; p.OffsetS != 0 || p.Requests != 1 || p.UpBytes != 3 || p.DownBytes != 4 {
		t.Fatalf("spark[0s] = %+v", p)
	}
}

func TestSnapshotJSONFieldNames(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 30, 0, time.UTC)
	h := New(&fakeSink{}, func() time.Time { return now })
	h.Finished(h.Started("chat", "u", "m"), Observation{
		CompletedAt: now, LocalDay: "2026-10-29", MinuteOfDay: 720,
		UpstreamID: "u", PathClass: "chat", Model: "m", Status: 200, LatencyMS: 120,
	})
	h.Started("chat", "u", "m")
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
		"latencyP50Ms", "latencyP95Ms", "promptTokens", "cacheReadTokens",
		"cacheHitRate", "byModel", "byUpstream", "spark", "inflightRequests",
	} {
		if _, ok := got[key]; !ok {
			t.Fatalf("missing JSON key %q in %s", key, raw)
		}
	}
	if string(got["cacheHitRate"]) != "null" {
		t.Fatalf("cacheHitRate without prompt tokens = %s", got["cacheHitRate"])
	}
	var spark []map[string]json.RawMessage
	if err := json.Unmarshal(got["spark"], &spark); err != nil {
		t.Fatal(err)
	}
	if len(spark) != 60 {
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
	for _, key := range []string{"upstreamId", "model", "qps", "errors"} {
		if _, ok := models[0][key]; !ok {
			t.Fatalf("missing byModel key %q in %s", key, got["byModel"])
		}
	}
	var upstreams map[string]map[string]json.RawMessage
	if err := json.Unmarshal(got["byUpstream"], &upstreams); err != nil {
		t.Fatal(err)
	}
	up, ok := upstreams["u"]
	if !ok {
		t.Fatalf("missing upstream %q in %s", "u", got["byUpstream"])
	}
	for _, key := range []string{
		"requests", "inflight", "qps", "errors", "upBps", "downBps",
		"latencyP50Ms", "latencyP95Ms", "promptTokens", "cacheReadTokens",
		"cacheHitRate", "spark",
	} {
		if _, ok := up[key]; !ok {
			t.Fatalf("missing byUpstream key %q in %s", key, got["byUpstream"])
		}
	}
	var upstreamSpark []map[string]json.RawMessage
	if err := json.Unmarshal(up["spark"], &upstreamSpark); err != nil {
		t.Fatal(err)
	}
	if len(upstreamSpark) != 60 {
		t.Fatalf("byUpstream spark = %s", up["spark"])
	}
	for _, key := range []string{"offsetS", "requests", "upBytes", "downBytes"} {
		if _, ok := upstreamSpark[0][key]; !ok {
			t.Fatalf("missing byUpstream spark key %q in %s", key, up["spark"])
		}
	}
	var inflight []map[string]json.RawMessage
	if err := json.Unmarshal(got["inflightRequests"], &inflight); err != nil {
		t.Fatal(err)
	}
	if len(inflight) != 1 {
		t.Fatalf("inflightRequests = %s", got["inflightRequests"])
	}
	for _, key := range []string{"upstreamId", "pathClass", "model", "elapsedMs"} {
		if _, ok := inflight[0][key]; !ok {
			t.Fatalf("missing inflightRequests key %q in %s", key, got["inflightRequests"])
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
		UpstreamID: "u", PathClass: "chat", Model: "m", Status: 200,
		ReqBytes: 10, RespBytes: 20, PromptTokens: 3, CompletionTokens: 4,
		CacheReadTokens: 5, ReasoningTokens: 6, CostNanoUSD: 7, LatencyMS: 120,
	}
	h.Finished(h.Started("chat", "u", "m"), obs)
	obs.CompletedAt = now.Add(30 * time.Second)
	obs.LatencyMS = 300
	obs.Status = 502
	obs.Model = "m2"
	h.Finished(h.Started("chat", "u", "m2"), obs)

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
	h.Finished(h.Started("chat", "u", "m"), Observation{
		CompletedAt: now, LocalDay: "2026-10-29", MinuteOfDay: 723, UpstreamID: "u", PathClass: "chat", Model: "m", Status: 200,
	})
	h.FlushForTest()
	h.mu.Lock()
	keys := len(h.minutes)
	h.mu.Unlock()
	if keys != 1 {
		t.Fatalf("stale minute keys not pruned: %d", keys)
	}
}

func TestPendingDeltaSurvivesRetention(t *testing.T) {
	sink := &fakeSink{}
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	h := New(sink, func() time.Time { return now })
	h.Finished(h.Started("chat", "u", "m"), Observation{
		CompletedAt: now, LocalDay: "2026-10-29", MinuteOfDay: 720,
		UpstreamID: "u", PathClass: "chat", Model: "m", Status: 200, ReqBytes: 9,
	})
	now = now.Add(3 * time.Minute)
	h.FlushForTest()
	if len(sink.deltas) != 1 || sink.deltas[0].Requests != 1 || sink.deltas[0].ReqBytes != 9 {
		t.Fatalf("deltas = %+v", sink.deltas)
	}
	h.FlushForTest()
	if len(sink.deltas) != 1 {
		t.Fatalf("pending delta resent: %+v", sink.deltas)
	}
	h.mu.Lock()
	keys := len(h.minutes)
	h.mu.Unlock()
	if keys != 0 {
		t.Fatalf("flushed idle keys not pruned: %d", keys)
	}
}

func TestStopFlushesRemainingDeltas(t *testing.T) {
	sink := &fakeSink{}
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	h := New(sink, func() time.Time { return now })
	h.Finished(h.Started("chat", "u", "m"), Observation{
		CompletedAt: now, LocalDay: "2026-10-29", MinuteOfDay: 720,
		UpstreamID: "u", PathClass: "chat", Model: "m", Status: 200,
	})
	h.Stop()
	h.Stop()
	if len(sink.deltas) != 1 || sink.deltas[0].Requests != 1 {
		t.Fatalf("deltas = %+v", sink.deltas)
	}
}

func TestCloseSubscribersClosesChannel(t *testing.T) {
	h := New(&fakeSink{}, time.Now)
	ch, cancel := h.Subscribe(60 * time.Second)
	defer cancel()

	h.CloseSubscribers()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("channel delivered a frame after CloseSubscribers")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("channel not closed by CloseSubscribers")
	}
	cancel() // idempotent after the channel was closed globally

	// Subscribers registered after the sweep must still receive frames.
	next, nextCancel := h.Subscribe(60 * time.Second)
	defer nextCancel()
	h.broadcastOnce()
	select {
	case <-next:
	case <-time.After(2 * time.Second):
		t.Fatal("new subscriber did not receive a frame")
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
