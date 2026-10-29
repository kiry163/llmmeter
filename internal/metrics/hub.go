package metrics

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/kiry163/llmmeter/internal/store"
)

const (
	// ringSeconds is the number of one-second slots retained in memory.
	ringSeconds = 300
	// maxInflightListed caps the in-flight requests exposed by a snapshot.
	maxInflightListed = 20
	// subscriberBuffer is how many snapshots a subscriber may lag behind.
	subscriberBuffer = 8
	// flushInterval is how often minute deltas reach the sink.
	flushInterval = 5 * time.Second
	// minuteRetention drops minute accumulators idle for longer than this.
	minuteRetention = 2 * time.Minute
	// usageStatusError is the usage parse status that counts as an error.
	usageStatusError = "error"
)

// Sink receives incremental minute aggregates.
type Sink interface {
	EnqueueMinute(store.MinuteDelta)
}

// modelCount is the per-model traffic inside one second or one snapshot.
type modelCount struct {
	requests int64
	errors   int64
}

// slot is one second of the ring buffer.
type slot struct {
	second    int64
	requests  int64
	errors    int64
	upBytes   int64
	downBytes int64
	latency   [latencyBuckets]int64
	models    map[string]*modelCount
}

// minuteKey identifies one traffic_minutes row.
type minuteKey struct {
	localDay  string
	minute    int
	pathClass string
	model     string
}

// accum holds the running total for a minute row and the portion already
// handed to the sink.
type accum struct {
	total        store.MinuteDelta
	flushed      store.MinuteDelta
	lastActivity time.Time
}

// subscriber is one SSE consumer.
type subscriber struct {
	ch     chan []byte
	window time.Duration
}

// Hub aggregates gateway traffic in memory and broadcasts snapshots.
type Hub struct {
	sink Sink
	now  func() time.Time

	mu       sync.Mutex
	ring     [ringSeconds]slot
	inflight map[int64]*Inflight
	nextID   int64
	minutes  map[minuteKey]*accum

	subMu   sync.Mutex
	subs    map[int64]*subscriber
	nextSub int64

	ticks    int
	stop     chan struct{}
	stopOnce sync.Once
}

// New returns a hub writing minute deltas to sink. now may be nil.
func New(sink Sink, now func() time.Time) *Hub {
	if now == nil {
		now = time.Now
	}
	return &Hub{
		sink:     sink,
		now:      now,
		inflight: make(map[int64]*Inflight),
		minutes:  make(map[minuteKey]*accum),
		subs:     make(map[int64]*subscriber),
		stop:     make(chan struct{}),
	}
}

// Started registers a request in flight.
func (h *Hub) Started(pathClass, model string) *Inflight {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextID++
	f := &Inflight{id: h.nextID, started: h.now(), pathClass: pathClass, model: model}
	h.inflight[f.id] = f
	return f
}

// Finished records the observation of a completed request and clears it from
// the in-flight set.
func (h *Hub) Finished(f *Inflight, o Observation) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if f != nil {
		delete(h.inflight, f.id)
	}
	h.recordLocked(o)
}

// recordLocked adds o to its second slot and minute accumulator. h.mu must be
// held.
func (h *Hub) recordLocked(o Observation) {
	var failed int64
	if o.Status >= 400 || o.UsageStatus == usageStatusError {
		failed = 1
	}

	sec := o.CompletedAt.Unix()
	s := &h.ring[slotIndex(sec)]
	if s.second != sec {
		*s = slot{second: sec, models: make(map[string]*modelCount)}
	}
	s.requests++
	s.errors += failed
	s.upBytes += o.ReqBytes
	s.downBytes += o.RespBytes
	s.latency[bucketIndex(o.LatencyMS)]++
	mc := s.models[o.Model]
	if mc == nil {
		mc = &modelCount{}
		s.models[o.Model] = mc
	}
	mc.requests++
	mc.errors += failed

	key := minuteKey{localDay: o.LocalDay, minute: o.MinuteOfDay, pathClass: o.PathClass, model: o.Model}
	a := h.minutes[key]
	if a == nil {
		a = &accum{}
		h.minutes[key] = a
	}
	a.total.Requests++
	a.total.Errors += failed
	a.total.ReqBytes += o.ReqBytes
	a.total.RespBytes += o.RespBytes
	a.total.PromptTokens += o.PromptTokens
	a.total.CompletionTokens += o.CompletionTokens
	a.total.CacheReadTokens += o.CacheReadTokens
	a.total.ReasoningTokens += o.ReasoningTokens
	a.total.CostNanoUSD += o.CostNanoUSD
	a.total.LatencySumMS += o.LatencyMS
	if o.LatencyMS > a.total.LatencyMaxMS {
		a.total.LatencyMaxMS = o.LatencyMS
	}
	a.lastActivity = h.now()
}

// slotIndex maps a Unix second to its ring position.
func slotIndex(sec int64) int64 {
	i := sec % ringSeconds
	if i < 0 {
		i += ringSeconds
	}
	return i
}

// FlushForTest publishes pending minute deltas. Run also does this every
// flushInterval.
func (h *Hub) FlushForTest() { h.flush() }

// flush sends total-minus-flushed deltas to the sink, skipping zero deltas and
// dropping minute keys that have been idle for more than minuteRetention.
func (h *Hub) flush() {
	h.mu.Lock()
	cutoff := h.now().Add(-minuteRetention)
	var deltas []store.MinuteDelta
	for key, a := range h.minutes {
		// An idle key may still hold a delta that no flush has published yet;
		// keep it until it is fully flushed instead of dropping traffic.
		if a.total == a.flushed && a.lastActivity.Before(cutoff) {
			delete(h.minutes, key)
			continue
		}
		d := store.MinuteDelta{
			LocalDay:         key.localDay,
			MinuteOfDay:      key.minute,
			PathClass:        key.pathClass,
			Model:            key.model,
			Requests:         a.total.Requests - a.flushed.Requests,
			Errors:           a.total.Errors - a.flushed.Errors,
			ReqBytes:         a.total.ReqBytes - a.flushed.ReqBytes,
			RespBytes:        a.total.RespBytes - a.flushed.RespBytes,
			PromptTokens:     a.total.PromptTokens - a.flushed.PromptTokens,
			CompletionTokens: a.total.CompletionTokens - a.flushed.CompletionTokens,
			CacheReadTokens:  a.total.CacheReadTokens - a.flushed.CacheReadTokens,
			ReasoningTokens:  a.total.ReasoningTokens - a.flushed.ReasoningTokens,
			CostNanoUSD:      a.total.CostNanoUSD - a.flushed.CostNanoUSD,
			LatencySumMS:     a.total.LatencySumMS - a.flushed.LatencySumMS,
		}
		// The store keeps the maximum, so a new sample contributes its own
		// maximum rather than the difference between maxima.
		if a.total.Requests != a.flushed.Requests {
			d.LatencyMaxMS = a.total.LatencyMaxMS
		}
		if d.Requests == 0 && d.Errors == 0 && d.ReqBytes == 0 && d.RespBytes == 0 &&
			d.PromptTokens == 0 && d.CompletionTokens == 0 && d.CacheReadTokens == 0 &&
			d.ReasoningTokens == 0 && d.CostNanoUSD == 0 &&
			d.LatencySumMS == 0 && d.LatencyMaxMS == 0 {
			continue
		}
		a.flushed = a.total
		deltas = append(deltas, d)
	}
	h.mu.Unlock()

	for _, d := range deltas {
		h.sink.EnqueueMinute(d)
	}
}

// Subscribe registers an SSE consumer for the given window. The returned cancel
// function closes the channel and is safe to call more than once.
func (h *Hub) Subscribe(window time.Duration) (<-chan []byte, func()) {
	if window <= 0 {
		window = 60 * time.Second
	}
	ch := make(chan []byte, subscriberBuffer)
	h.subMu.Lock()
	h.nextSub++
	id := h.nextSub
	h.subs[id] = &subscriber{ch: ch, window: window}
	h.subMu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			h.subMu.Lock()
			if s, ok := h.subs[id]; ok {
				delete(h.subs, id)
				close(s.ch)
			}
			h.subMu.Unlock()
		})
	}
	return ch, cancel
}

// broadcastOnce sends one snapshot to every subscriber. A subscriber that
// cannot keep up is closed and removed.
func (h *Hub) broadcastOnce() {
	h.subMu.Lock()
	defer h.subMu.Unlock()
	for id, s := range h.subs {
		data, err := json.Marshal(h.Snapshot(s.window))
		if err != nil {
			continue
		}
		select {
		case s.ch <- data:
		default:
			close(s.ch)
			delete(h.subs, id)
		}
	}
}

// Run broadcasts one snapshot per second and flushes minute deltas every
// flushInterval until ctx is cancelled or Stop is called.
func (h *Hub) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			h.flush()
			return
		case <-h.stop:
			h.flush()
			return
		case <-ticker.C:
			h.broadcastOnce()
			h.ticks++
			if h.ticks%int(flushInterval/time.Second) == 0 {
				h.flush()
			}
		}
	}
}

// Stop ends Run and publishes remaining deltas. It is safe to call alone.
func (h *Hub) Stop() {
	h.stopOnce.Do(func() { close(h.stop) })
	h.flush()
}
