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
	UpstreamID                       string
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
	id         int64
	started    time.Time
	upstreamID string
	pathClass  string
	model      string
}

// Snapshot is the current live view of gateway traffic. Totals cover every
// upstream; ByUpstream splits the same window per upstream and Spark stays
// global.
type Snapshot struct {
	TS               string                      `json:"ts"`
	WindowS          int                         `json:"windowS"`
	Inflight         int                         `json:"inflight"`
	QPS              float64                     `json:"qps"`
	Errors           int64                       `json:"errors"`
	UpBps            float64                     `json:"upBps"`
	DownBps          float64                     `json:"downBps"`
	LatencyP50MS     *int64                      `json:"latencyP50Ms"`
	LatencyP95MS     *int64                      `json:"latencyP95Ms"`
	PromptTokens     int64                       `json:"promptTokens"`
	CacheReadTokens  int64                       `json:"cacheReadTokens"`
	CacheHitRate     *float64                    `json:"cacheHitRate"`
	ByModel          []ModelRate                 `json:"byModel"`
	ByUpstream       map[string]UpstreamSnapshot `json:"byUpstream"`
	Spark            []SparkPoint                `json:"spark"`
	InflightRequests []InflightInfo              `json:"inflightRequests"`
}

// UpstreamSnapshot is one upstream's share of the live snapshot. Spark uses
// the same second offsets as Snapshot.Spark so readers can switch between the
// global and per-upstream view without reconnecting or recomputing positions.
type UpstreamSnapshot struct {
	Requests        int64        `json:"requests"`
	Inflight        int          `json:"inflight"`
	QPS             float64      `json:"qps"`
	Errors          int64        `json:"errors"`
	UpBps           float64      `json:"upBps"`
	DownBps         float64      `json:"downBps"`
	LatencyP50MS    *int64       `json:"latencyP50Ms"`
	LatencyP95MS    *int64       `json:"latencyP95Ms"`
	PromptTokens    int64        `json:"promptTokens"`
	CacheReadTokens int64        `json:"cacheReadTokens"`
	CacheHitRate    *float64     `json:"cacheHitRate"`
	Spark           []SparkPoint `json:"spark"`
}

// ModelRate is the per-model share of a snapshot window. UpstreamID keeps
// same-named models of different upstreams apart.
type ModelRate struct {
	UpstreamID string  `json:"upstreamId"`
	Model      string  `json:"model"`
	QPS        float64 `json:"qps"`
	Errors     int64   `json:"errors"`
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
	UpstreamID string `json:"upstreamId"`
	PathClass  string `json:"pathClass"`
	Model      string `json:"model"`
	ElapsedMS  int64  `json:"elapsedMs"`
}

// latencyBoundsMS are the histogram bucket upper bounds in milliseconds; a
// sample lands in the first bucket whose bound it does not exceed.
var latencyBoundsMS = [...]int64{50, 100, 200, 400, 800, 1600, 3200, 6400, 12800}

const latencyBuckets = len(latencyBoundsMS)

// Snapshot returns the live metrics for the trailing window. Seconds outside
// [now-window, now] and stale ring slots are ignored. Global totals are the
// sum of the per-upstream rings, so both views always share one window.
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
	seconds := float64(windowS)

	type modelKey struct {
		upstreamID string
		model      string
	}
	models := make(map[modelKey]*modelCount)
	byUpstream := make(map[string]UpstreamSnapshot, len(h.rings))
	var requests, errors, upBytes, downBytes, promptTokens, cacheReadTokens int64
	var latency [latencyBuckets]int64
	spark := emptySpark(windowS)

	inflightByUpstream := make(map[string]int, len(h.rings))
	for _, f := range h.inflight {
		inflightByUpstream[f.upstreamID]++
	}

	for upstreamID, ring := range h.rings {
		us := UpstreamSnapshot{Spark: emptySpark(windowS)}
		var up, down, prompt, cacheRead int64
		var hist [latencyBuckets]int64
		for i := int64(0); i < windowS; i++ {
			sec := end - (windowS - 1 - i)
			s := &ring[slotIndex(sec)]
			if s.second != sec {
				continue
			}
			point := &us.Spark[i]
			point.Requests = s.requests
			point.UpBytes = s.upBytes
			point.DownBytes = s.downBytes
			spark[i].Requests += s.requests
			spark[i].UpBytes += s.upBytes
			spark[i].DownBytes += s.downBytes
			us.Requests += s.requests
			us.Errors += s.errors
			up += s.upBytes
			down += s.downBytes
			prompt += s.promptTokens
			cacheRead += s.cacheReadTokens
			for b := range s.latency {
				hist[b] += s.latency[b]
			}
			for model, mc := range s.models {
				key := modelKey{upstreamID: upstreamID, model: model}
				acc := models[key]
				if acc == nil {
					acc = &modelCount{}
					models[key] = acc
				}
				acc.requests += mc.requests
				acc.errors += mc.errors
			}
		}

		requests += us.Requests
		errors += us.Errors
		upBytes += up
		downBytes += down
		promptTokens += prompt
		cacheReadTokens += cacheRead
		for b := range hist {
			latency[b] += hist[b]
		}

		n := inflightByUpstream[upstreamID]
		if us.Requests == 0 && n == 0 {
			// A removed upstream drops out once its slots leave the window.
			continue
		}
		us.Inflight = n
		us.QPS = float64(us.Requests) / seconds
		us.UpBps = float64(up) / seconds
		us.DownBps = float64(down) / seconds
		us.LatencyP50MS = percentile(hist, 50)
		us.LatencyP95MS = percentile(hist, 95)
		us.PromptTokens = prompt
		us.CacheReadTokens = cacheRead
		us.CacheHitRate = cacheHitRate(prompt, cacheRead)
		byUpstream[upstreamID] = us
	}
	// An upstream with only in-flight requests is visible before its first
	// response lands; its traffic totals remain zero.
	for upstreamID, n := range inflightByUpstream {
		if _, ok := byUpstream[upstreamID]; ok {
			continue
		}
		byUpstream[upstreamID] = UpstreamSnapshot{Inflight: n, Spark: emptySpark(windowS)}
	}

	byModel := make([]ModelRate, 0, len(models))
	for key, mc := range models {
		byModel = append(byModel, ModelRate{
			UpstreamID: key.upstreamID,
			Model:      key.model,
			QPS:        float64(mc.requests) / seconds,
			Errors:     mc.errors,
		})
	}
	sort.Slice(byModel, func(i, j int) bool {
		if byModel[i].QPS != byModel[j].QPS {
			return byModel[i].QPS > byModel[j].QPS
		}
		if byModel[i].Model != byModel[j].Model {
			return byModel[i].Model < byModel[j].Model
		}
		return byModel[i].UpstreamID < byModel[j].UpstreamID
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
			UpstreamID: f.upstreamID,
			PathClass:  f.pathClass,
			Model:      f.model,
			ElapsedMS:  elapsed,
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
		PromptTokens:     promptTokens,
		CacheReadTokens:  cacheReadTokens,
		CacheHitRate:     cacheHitRate(promptTokens, cacheReadTokens),
		ByModel:          byModel,
		ByUpstream:       byUpstream,
		Spark:            spark,
		InflightRequests: inflightRequests,
	}
}

// emptySpark returns one zero point per second of the window, oldest first.
func emptySpark(windowS int64) []SparkPoint {
	spark := make([]SparkPoint, windowS)
	for i := range spark {
		spark[i].OffsetS = i - int(windowS-1)
	}
	return spark
}

// cacheHitRate returns cacheReadTokens/promptTokens, or nil when the window
// observed no prompt tokens, so the JSON carries null instead of a divide by
// zero.
func cacheHitRate(promptTokens, cacheReadTokens int64) *float64 {
	if promptTokens <= 0 {
		return nil
	}
	rate := float64(cacheReadTokens) / float64(promptTokens)
	return &rate
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
