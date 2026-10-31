package api

import (
	"context"
	"net/http"
	"time"

	"github.com/kiry163/llmmeter/internal/store"
)

// analysisTimeout bounds the analysis aggregate queries so a slow database
// answers with an internal error instead of hanging the request.
const analysisTimeout = 5 * time.Second

// analysisRange is the resolved range of an analysis response.
type analysisRange struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Interval string `json:"interval"`
	Partial  bool   `json:"partial"`
}

// analysisTrendSeries is one (upstream, model) contribution to a trend bucket.
// Upstreams are never merged: the same model served by two upstreams yields
// two entries.
type analysisTrendSeries struct {
	UpstreamID       string `json:"upstreamId"`
	UpstreamName     string `json:"upstreamName"`
	Model            string `json:"model"`
	Requests         int64  `json:"requests"`
	PromptTokens     int64  `json:"promptTokens"`
	CompletionTokens int64  `json:"completionTokens"`
	CacheReadTokens  int64  `json:"cacheReadTokens"`
	CostNanoUSD      int64  `json:"costNanoUsd"`
	UpBytes          int64  `json:"upBytes"`
	DownBytes        int64  `json:"downBytes"`
}

// analysisTrendBucket is one bucket of the trend with its per-upstream model
// series.
type analysisTrendBucket struct {
	Day         string                `json:"day"`
	MinuteOfDay int                   `json:"minuteOfDay"`
	Series      []analysisTrendSeries `json:"series"`
}

// analysisRow is one model or path-class aggregate, scoped to an upstream.
type analysisRow struct {
	UpstreamID       string `json:"upstreamId"`
	UpstreamName     string `json:"upstreamName"`
	Key              string `json:"key"`
	Requests         int64  `json:"requests"`
	Errors           int64  `json:"errors"`
	UpBytes          int64  `json:"upBytes"`
	DownBytes        int64  `json:"downBytes"`
	PromptTokens     int64  `json:"promptTokens"`
	CompletionTokens int64  `json:"completionTokens"`
	CacheReadTokens  int64  `json:"cacheReadTokens"`
	CostNanoUSD      int64  `json:"costNanoUsd"`
	Unpriced         int64  `json:"unpriced"`
}

// analysisTopRequest is one entry of a top-requests ranking.
type analysisTopRequest struct {
	ID               int64  `json:"id"`
	StartedAt        string `json:"startedAt"`
	UpstreamID       string `json:"upstreamId"`
	UpstreamName     string `json:"upstreamName"`
	KeyID            string `json:"keyId"`
	KeyName          string `json:"keyName"`
	Model            string `json:"model"`
	PathClass        string `json:"pathClass"`
	Stream           bool   `json:"stream"`
	PromptTokens     int64  `json:"promptTokens"`
	CompletionTokens int64  `json:"completionTokens"`
	CacheReadTokens  int64  `json:"cacheReadTokens"`
	TotalTokens      int64  `json:"totalTokens"`
	CostNanoUSD      int64  `json:"costNanoUsd"`
	LatencyMS        int64  `json:"latencyMs"`
	TTFTMS           *int64 `json:"ttftMs"`
	Status           int    `json:"status"`
	UsageStatus      string `json:"usageStatus"`
}

// analysisTop is the three top-requests rankings. The arrays are always
// present, empty when the detail rows are outside retention.
type analysisTop struct {
	Cost    []analysisTopRequest `json:"cost"`
	Tokens  []analysisTopRequest `json:"tokens"`
	Latency []analysisTopRequest `json:"latency"`
}

// analysisSlow is the slow-request signal. P95MS is null when the detail rows
// are outside retention.
type analysisSlow struct {
	ThresholdMS int64  `json:"thresholdMs"`
	P95MS       *int64 `json:"p95Ms"`
	CountOver   int64  `json:"countOver"`
}

// analysisHealth is the health section of the analysis response.
type analysisHealth struct {
	Requests           int64        `json:"requests"`
	Errors             int64        `json:"errors"`
	ErrorRate          float64      `json:"errorRate"`
	Unmetered          int64        `json:"unmetered"`
	TooLarge           int64        `json:"tooLarge"`
	ClientDisconnected int64        `json:"clientDisconnected"`
	Slow               analysisSlow `json:"slow"`
}

// analysisCache is the prompt-cache section of the analysis response.
// Available is false and SavedNanoUSD null when the priced detail rows are
// outside retention; the token counts still come from the permanent
// aggregate.
type analysisCache struct {
	PromptTokens    int64   `json:"promptTokens"`
	CacheReadTokens int64   `json:"cacheReadTokens"`
	HitRate         float64 `json:"hitRate"`
	SavedNanoUSD    *int64  `json:"savedNanoUsd"`
	Available       bool    `json:"available"`
}

// analysisBody is the /api/analysis response.
type analysisBody struct {
	Range       analysisRange         `json:"range"`
	Trend       []analysisTrendBucket `json:"trend"`
	Models      []analysisRow         `json:"models"`
	Paths       []analysisRow         `json:"paths"`
	TopRequests analysisTop           `json:"topRequests"`
	Health      analysisHealth        `json:"health"`
	Cache       analysisCache         `json:"cache"`
}

// analysis aggregates the whole analysis page in one response. Views backed by
// the requests retention window are flagged through range.partial instead of
// being omitted; every array is present so the client can render partial data
// without null checks.
func (s *Server) analysis(w http.ResponseWriter, r *http.Request) {
	from, to, ok := s.rangeQuery(w, r)
	if !ok {
		return
	}
	iv, ok := analysisInterval(w, r.URL.Query().Get("interval"), daySpan(from, to))
	if !ok {
		return
	}
	partial := s.partialRange(from)
	upstream := r.URL.Query().Get("upstream")

	ctx, cancel := context.WithTimeout(r.Context(), analysisTimeout)
	defer cancel()

	trend, err := s.store.TrendByModel(ctx, from, to, iv, upstream)
	if err != nil {
		writeInternal(w, err)
		return
	}
	models, err := s.store.Breakdown(from, to, "model", upstream)
	if err != nil {
		writeInternal(w, err)
		return
	}
	paths, err := s.store.Breakdown(from, to, "pathClass", upstream)
	if err != nil {
		writeInternal(w, err)
		return
	}
	health, err := s.store.AnalysisHealth(ctx, from, to, upstream)
	if err != nil {
		writeInternal(w, err)
		return
	}
	cache, err := s.store.CacheStats(ctx, from, to, upstream)
	if err != nil {
		writeInternal(w, err)
		return
	}

	body := analysisBody{
		Range: analysisRange{
			From:     from,
			To:       to,
			Interval: intervalName(iv),
			Partial:  partial,
		},
		Trend:  make([]analysisTrendBucket, 0, len(trend)),
		Models: s.analysisRows(models),
		Paths:  s.analysisRows(paths),
		TopRequests: analysisTop{
			Cost:    make([]analysisTopRequest, 0),
			Tokens:  make([]analysisTopRequest, 0),
			Latency: make([]analysisTopRequest, 0),
		},
		Health: analysisHealth{
			Requests:           health.Requests,
			Errors:             health.Errors,
			ErrorRate:          health.ErrorRate,
			Unmetered:          health.Unmetered,
			TooLarge:           health.TooLarge,
			ClientDisconnected: health.ClientDisconnected,
			Slow: analysisSlow{
				ThresholdMS: health.ThresholdMS,
				P95MS:       health.P95MS,
				CountOver:   health.CountOver,
			},
		},
		Cache: analysisCache{
			PromptTokens:    cache.PromptTokens,
			CacheReadTokens: cache.CacheReadTokens,
			HitRate:         cache.HitRate,
			SavedNanoUSD:    cache.SavedNanoUSD,
			Available:       true,
		},
	}
	for _, b := range trend {
		dto := analysisTrendBucket{
			Day:         b.Day,
			MinuteOfDay: b.MinuteOfDay,
			Series:      make([]analysisTrendSeries, 0, len(b.Series)),
		}
		for _, series := range b.Series {
			dto.Series = append(dto.Series, analysisTrendSeries{
				UpstreamID:       series.UpstreamID,
				UpstreamName:     s.upstreamDisplayName(series.UpstreamID, ""),
				Model:            series.Model,
				Requests:         series.Requests,
				PromptTokens:     series.PromptTokens,
				CompletionTokens: series.CompletionTokens,
				CacheReadTokens:  series.CacheReadTokens,
				CostNanoUSD:      series.CostNanoUSD,
				UpBytes:          series.UpBytes,
				DownBytes:        series.DownBytes,
			})
		}
		body.Trend = append(body.Trend, dto)
	}

	if partial {
		// The latency percentile and cache saving need detail rows that are
		// already pruned; keep the permanent counters but drop the estimates.
		body.Health.Slow.P95MS = nil
		body.Cache.Available = false
		body.Cache.SavedNanoUSD = nil
		writeJSON(w, http.StatusOK, body)
		return
	}

	cost, err := s.store.TopRequests(ctx, from, to, "cost", 10, upstream)
	if err != nil {
		writeInternal(w, err)
		return
	}
	tokens, err := s.store.TopRequests(ctx, from, to, "tokens", 10, upstream)
	if err != nil {
		writeInternal(w, err)
		return
	}
	latency, err := s.store.TopRequests(ctx, from, to, "latency", 10, upstream)
	if err != nil {
		writeInternal(w, err)
		return
	}
	body.TopRequests = analysisTop{
		Cost:    s.topRequestsDTO(cost),
		Tokens:  s.topRequestsDTO(tokens),
		Latency: s.topRequestsDTO(latency),
	}
	writeJSON(w, http.StatusOK, body)
}

// analysisRows converts store breakdown rows to wire rows.
func (s *Server) analysisRows(rows []store.BreakdownRow) []analysisRow {
	out := make([]analysisRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, analysisRow{
			UpstreamID:       row.UpstreamID,
			UpstreamName:     s.upstreamDisplayName(row.UpstreamID, ""),
			Key:              row.Key,
			Requests:         row.Requests,
			Errors:           row.Errors,
			UpBytes:          row.UpBytes,
			DownBytes:        row.DownBytes,
			PromptTokens:     row.PromptTokens,
			CompletionTokens: row.CompletionTokens,
			CacheReadTokens:  row.CacheReadTokens,
			CostNanoUSD:      row.CostNanoUSD,
			Unpriced:         row.Unpriced,
		})
	}
	return out
}

// topRequestsDTO converts a store ranking to wire entries. The upstream name
// comes from the configuration snapshot, falling back to the snapshot stored
// on the request row when the upstream has since been deleted.
func (s *Server) topRequestsDTO(rows []store.TopRequest) []analysisTopRequest {
	out := make([]analysisTopRequest, 0, len(rows))
	for _, row := range rows {
		out = append(out, analysisTopRequest{
			ID:               row.ID,
			StartedAt:        row.StartedAt.UTC().Format(time.RFC3339),
			UpstreamID:       row.UpstreamID,
			UpstreamName:     s.upstreamDisplayName(row.UpstreamID, row.UpstreamName),
			KeyID:            row.KeyID,
			KeyName:          row.KeyName,
			Model:            row.Model,
			PathClass:        row.PathClass,
			Stream:           row.Stream,
			PromptTokens:     row.PromptTokens,
			CompletionTokens: row.CompletionTokens,
			CacheReadTokens:  row.CacheReadTokens,
			TotalTokens:      row.TotalTokens,
			CostNanoUSD:      row.CostNanoUSD,
			LatencyMS:        row.LatencyMS,
			TTFTMS:           row.TTFTMS,
			Status:           row.Status,
			UsageStatus:      row.UsageStatus,
		})
	}
	return out
}

// analysisInterval resolves and validates the interval parameter. Explicit
// intervals are rejected when they would produce too many buckets; auto picks
// the finest interval the range allows.
func analysisInterval(w http.ResponseWriter, raw string, days int) (store.Interval, bool) {
	switch raw {
	case "auto", "":
		return autoInterval(days), true
	case "minute":
		if days > 2 {
			writeAPIError(w, http.StatusBadRequest, codeValidation, "interval minute supports at most 2 days")
			return 0, false
		}
		return store.IntervalMinute, true
	case "hour":
		if days > 90 {
			writeAPIError(w, http.StatusBadRequest, codeValidation, "interval hour supports at most 90 days")
			return 0, false
		}
		return store.IntervalHour, true
	case "day":
		return store.IntervalDay, true
	default:
		writeAPIError(w, http.StatusBadRequest, codeValidation, "interval must be auto, minute, hour or day")
		return 0, false
	}
}

// autoInterval chooses the interval for an inclusive day span: minute up to
// two days, hour up to a week, day beyond that.
func autoInterval(days int) store.Interval {
	switch {
	case days <= 2:
		return store.IntervalMinute
	case days <= 7:
		return store.IntervalHour
	default:
		return store.IntervalDay
	}
}

// intervalName returns the wire name of an interval.
func intervalName(iv store.Interval) string {
	switch iv {
	case store.IntervalMinute:
		return "minute"
	case store.IntervalHour:
		return "hour"
	default:
		return "day"
	}
}
