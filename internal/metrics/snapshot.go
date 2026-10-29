// Package metrics aggregates live gateway traffic in memory and broadcasts
// per-second snapshots to SSE subscribers. It is independent of the HTTP layer.
package metrics

import (
	"sort"
	"time"
)

// Observation is the accounting record of one completed gateway request.
type Observation struct {
	CompletedAt                      time.Time
	LocalDay                         string
	MinuteOfDay                      int
	PathClass, Model                 string
	Status                           int
	UsageStatus                      string
	ReqBytes, RespBytes              int64
	PromptTokens, CompletionTokens   int64
	CacheReadTokens, ReasoningTokens int64
	CostNanoUSD                      int64
	LatencyMS                        int64
}

// Inflight tracks a request from Started until Finished.
type Inflight struct {
	id        int64
	started   time.Time
	pathClass string
	model     string
}

// Snapshot is the current live view of gateway traffic.
type Snapshot struct {
	TS               string         `json:"ts"`
	WindowS          int            `json:"windowS"`
	Inflight         int            `json:"inflight"`
	QPS              float64        `json:"qps"`
	Errors           int64          `json:"errors"`
	UpBps            float64        `json:"upBps"`
	DownBps          float64        `json:"downBps"`
	LatencyP50MS     *int64         `json:"latencyP50Ms"`
	LatencyP95MS     *int64         `json:"latencyP95Ms"`
	ByModel          []ModelRate    `json:"byModel"`
	Spark            []SparkPoint   `json:"spark"`
	InflightRequests []InflightInfo `json:"inflightRequests"`
}

// ModelRate is the per-model share of a snapshot window.
type ModelRate struct {
	Model  string  `json:"model"`
	QPS    float64 `json:"qps"`
	Errors int64   `json:"errors"`
}

// SparkPoint is one non-empty second of the snapshot window.
type SparkPoint struct {
	OffsetS   int   `json:"offsetS"`
	Requests  int64 `json:"requests"`
	UpBytes   int64 `json:"upBytes"`
	DownBytes int64 `json:"downBytes"`
}

// InflightInfo describes one request that has not completed yet.
type InflightInfo struct {
	PathClass string `json:"pathClass"`
	Model     string `json:"model"`
	ElapsedMS int64  `json:"elapsedMs"`
}

// latencyBoundsMS are the histogram bucket upper bounds in milliseconds; a
// sample lands in the first bucket whose bound it does not exceed.
var latencyBoundsMS = [...]int64{50, 100, 200, 400, 800, 1600, 3200, 6400, 12800}

const latencyBuckets = len(latencyBoundsMS)

// Snapshot returns the live metrics for the trailing window. Seconds outside
// [now-window, now] and stale ring slots are ignored.
func (h *Hub) Snapshot(window time.Duration) Snapshot {
	h.mu.Lock()
	defer h.mu.Unlock()

	now := h.now()
	windowS := int64(window / time.Second)
	if windowS <= 0 {
		windowS = 60
	}
	if windowS > ringSeconds {
		windowS = ringSeconds
	}
	end := now.Unix()

	models := make(map[string]*modelCount)
	var requests, errors, upBytes, downBytes int64
	var latency [latencyBuckets]int64
	spark := make([]SparkPoint, 0, windowS)
	for i := windowS - 1; i >= 0; i-- {
		sec := end - i
		s := &h.ring[slotIndex(sec)]
		if s.second != sec {
			continue
		}
		requests += s.requests
		errors += s.errors
		upBytes += s.upBytes
		downBytes += s.downBytes
		for b := range s.latency {
			latency[b] += s.latency[b]
		}
		for model, mc := range s.models {
			acc := models[model]
			if acc == nil {
				acc = &modelCount{}
				models[model] = acc
			}
			acc.requests += mc.requests
			acc.errors += mc.errors
		}
		if s.requests > 0 || s.upBytes > 0 || s.downBytes > 0 {
			spark = append(spark, SparkPoint{
				OffsetS:   int(sec - end),
				Requests:  s.requests,
				UpBytes:   s.upBytes,
				DownBytes: s.downBytes,
			})
		}
	}

	seconds := float64(windowS)
	byModel := make([]ModelRate, 0, len(models))
	for model, mc := range models {
		byModel = append(byModel, ModelRate{
			Model:  model,
			QPS:    float64(mc.requests) / seconds,
			Errors: mc.errors,
		})
	}
	sort.Slice(byModel, func(i, j int) bool {
		if byModel[i].QPS != byModel[j].QPS {
			return byModel[i].QPS > byModel[j].QPS
		}
		return byModel[i].Model < byModel[j].Model
	})

	type startedInfo struct {
		id   int64
		info InflightInfo
	}
	started := make([]startedInfo, 0, len(h.inflight))
	for _, f := range h.inflight {
		elapsed := now.Sub(f.started).Milliseconds()
		if elapsed < 0 {
			elapsed = 0
		}
		started = append(started, startedInfo{id: f.id, info: InflightInfo{
			PathClass: f.pathClass,
			Model:     f.model,
			ElapsedMS: elapsed,
		}})
	}
	// Oldest first, capped at the earliest maxInflightListed requests.
	sort.Slice(started, func(i, j int) bool { return started[i].id < started[j].id })
	if len(started) > maxInflightListed {
		started = started[:maxInflightListed]
	}
	inflightRequests := make([]InflightInfo, len(started))
	for i, s := range started {
		inflightRequests[i] = s.info
	}

	return Snapshot{
		TS:               now.UTC().Format(time.RFC3339),
		WindowS:          int(windowS),
		Inflight:         len(h.inflight),
		QPS:              float64(requests) / seconds,
		Errors:           errors,
		UpBps:            float64(upBytes) / seconds,
		DownBps:          float64(downBytes) / seconds,
		LatencyP50MS:     percentile(latency, 50),
		LatencyP95MS:     percentile(latency, 95),
		ByModel:          byModel,
		Spark:            spark,
		InflightRequests: inflightRequests,
	}
}

// percentile returns the upper bound of the bucket holding the pct-th
// percentile, or nil when the histogram is empty.
func percentile(hist [latencyBuckets]int64, pct int64) *int64 {
	var total int64
	for _, n := range hist {
		total += n
	}
	if total == 0 {
		return nil
	}
	// Ceiling rank keeps small samples at the slow end of the bucket.
	rank := (pct*total + 99) / 100
	var cum int64
	for i, n := range hist {
		cum += n
		if cum >= rank {
			upper := latencyBoundsMS[i]
			return &upper
		}
	}
	upper := latencyBoundsMS[latencyBuckets-1]
	return &upper
}

// bucketIndex maps a latency to its histogram bucket.
func bucketIndex(latencyMS int64) int {
	for i, bound := range latencyBoundsMS {
		if latencyMS <= bound {
			return i
		}
	}
	return latencyBuckets - 1
}
