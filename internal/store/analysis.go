package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/kiry163/llmmeter/internal/usage"
)

// TrendModel is one model's contribution to a trend bucket.
type TrendModel struct {
	Requests, PromptTokens, CompletionTokens, CacheReadTokens int64
	CostNanoUSD, UpBytes, DownBytes                           int64
}

// TrendSeries is one (upstream, model) row of a trend bucket, which is what
// the analysis series[] entries are built from. Upstreams are never merged:
// the same model served by two upstreams yields two series entries.
type TrendSeries struct {
	UpstreamID, Model string
	TrendModel
}

// TrendBucket is one time bucket of the per-model trend query. MinuteOfDay is
// 0 for day buckets. Series keeps the upstream dimension; ByModel is the
// legacy per-model view that sums every upstream together and is retained
// until the API serves series[].
type TrendBucket struct {
	Day         string
	MinuteOfDay int
	Series      []TrendSeries
	ByModel     map[string]TrendModel
}

// TopRequest is one entry of a top-requests ranking. The upstream and key
// names are the snapshots taken when the request started. TTFTMS is nil when
// the request had no time to first token.
type TopRequest struct {
	ID                                              int64
	StartedAt                                       time.Time
	UpstreamID, UpstreamName                        string
	KeyID, KeyName                                  string
	Model, PathClass                                string
	Stream                                          bool
	PromptTokens, CompletionTokens, CacheReadTokens int64
	TotalTokens, CostNanoUSD, LatencyMS             int64
	TTFTMS                                          *int64
	Status                                          int
	UsageStatus                                     string
}

// AnalysisHealth counts the health signals of one day range. Requests and
// Errors come from the permanent aggregate; the other counts come from the
// requests retention window.
type AnalysisHealth struct {
	Requests, Errors, Unmetered, TooLarge, ClientDisconnected int64
	ErrorRate                                                 float64
	P95MS                                                     *int64
	CountOver, ThresholdMS                                    int64
}

// CacheStats summarises prompt-cache usage. SavedNanoUSD is the saving
// estimated from the priced detail rows; it points to 0 when no such rows
// exist. Callers decide whether the detail rows are inside retention and null
// the value for partial ranges.
type CacheStats struct {
	PromptTokens, CacheReadTokens int64
	HitRate                       float64
	SavedNanoUSD                  *int64
}

// ObservedModel is one model name seen in the requests retention window.
type ObservedModel struct {
	Model    string
	Requests int64
	LastSeen time.Time
}

const (
	// topRequestDefaultLimit is used when the requested limit is not positive.
	topRequestDefaultLimit = 10
	// topRequestMaxLimit caps a top-requests ranking.
	topRequestMaxLimit = 50
	// slowRequestMS is the latency threshold the health counters use.
	slowRequestMS = 2000
)

const (
	trendModelMinuteSQL = `
SELECT local_day, minute_of_day, upstream_id, model,
       SUM(requests), SUM(prompt_tokens), SUM(completion_tokens), SUM(cache_read_tokens),
       SUM(cost_nano_usd), SUM(req_bytes), SUM(resp_bytes)
FROM traffic_minutes WHERE local_day BETWEEN ? AND ?
GROUP BY local_day, minute_of_day, upstream_id, model HAVING SUM(requests) > 0
ORDER BY local_day, minute_of_day, upstream_id, model`

	trendModelHourSQL = `
SELECT local_day, minute_of_day / 60, upstream_id, model,
       SUM(requests), SUM(prompt_tokens), SUM(completion_tokens), SUM(cache_read_tokens),
       SUM(cost_nano_usd), SUM(req_bytes), SUM(resp_bytes)
FROM traffic_minutes WHERE local_day BETWEEN ? AND ?
GROUP BY local_day, minute_of_day / 60, upstream_id, model HAVING SUM(requests) > 0
ORDER BY local_day, minute_of_day / 60, upstream_id, model`

	trendModelDaySQL = `
SELECT local_day, 0, upstream_id, model,
       SUM(requests), SUM(prompt_tokens), SUM(completion_tokens), SUM(cache_read_tokens),
       SUM(cost_nano_usd), SUM(req_bytes), SUM(resp_bytes)
FROM traffic_minutes WHERE local_day BETWEEN ? AND ?
GROUP BY local_day, upstream_id, model HAVING SUM(requests) > 0
ORDER BY local_day, upstream_id, model`
)

// TrendByModel returns per-model traffic buckets for the inclusive local_day
// range, ordered ascending by (day, bucket). Day buckets carry MinuteOfDay 0.
func (s *Store) TrendByModel(ctx context.Context, from, to string, iv Interval) ([]TrendBucket, error) {
	q := trendModelDaySQL
	switch iv {
	case IntervalMinute:
		q = trendModelMinuteSQL
	case IntervalHour:
		q = trendModelHourSQL
	}
	rows, err := s.db.QueryContext(ctx, q, from, to)
	if err != nil {
		return nil, fmt.Errorf("trend by model: %w", err)
	}
	defer rows.Close()

	buckets := make([]TrendBucket, 0, 64)
	for rows.Next() {
		var (
			day    string
			minute int
			series TrendSeries
		)
		if err := rows.Scan(
			&day, &minute, &series.UpstreamID, &series.Model,
			&series.Requests, &series.PromptTokens, &series.CompletionTokens,
			&series.CacheReadTokens, &series.CostNanoUSD,
			&series.UpBytes, &series.DownBytes,
		); err != nil {
			return nil, fmt.Errorf("scan trend bucket: %w", err)
		}
		n := len(buckets)
		if n == 0 || buckets[n-1].Day != day || buckets[n-1].MinuteOfDay != minute {
			buckets = append(buckets, TrendBucket{
				Day: day, MinuteOfDay: minute, ByModel: make(map[string]TrendModel),
			})
			n++
		}
		b := &buckets[n-1]
		b.Series = append(b.Series, series)
		// The legacy map folds every upstream into the model total; the API
		// switches to Series, which keeps them apart.
		total := b.ByModel[series.Model]
		total.Requests += series.Requests
		total.PromptTokens += series.PromptTokens
		total.CompletionTokens += series.CompletionTokens
		total.CacheReadTokens += series.CacheReadTokens
		total.CostNanoUSD += series.CostNanoUSD
		total.UpBytes += series.UpBytes
		total.DownBytes += series.DownBytes
		b.ByModel[series.Model] = total
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate trend: %w", err)
	}
	return buckets, nil
}

// topRequestColumns maps the public "by" values to their ranking column.
var topRequestColumns = map[string]string{
	"cost":    "cost_nano_usd",
	"tokens":  "total_tokens",
	"latency": "latency_ms",
}

const topRequestsSQL = `
SELECT id, started_at, upstream_id, upstream_name, key_id, key_name, model, path_class, stream,
       prompt_tokens, completion_tokens, cache_read_tokens,
       total_tokens, cost_nano_usd, latency_ms, ttft_ms, status, usage_status
FROM requests WHERE local_day BETWEEN ? AND ? AND status < 400
ORDER BY %s DESC, id DESC LIMIT ?`

// TopRequests returns the highest-ranked requests for the inclusive local_day
// range. by must be "cost", "tokens" or "latency"; limit is clamped to
// 1..topRequestMaxLimit. Failed requests (status >= 400) are excluded because
// their usage is unreliable and would otherwise top the latency ranking.
func (s *Store) TopRequests(ctx context.Context, from, to, by string, limit int) ([]TopRequest, error) {
	col, ok := topRequestColumns[by]
	if !ok {
		return nil, fmt.Errorf("top requests: unknown ranking %q", by)
	}
	if limit <= 0 {
		limit = topRequestDefaultLimit
	}
	if limit > topRequestMaxLimit {
		limit = topRequestMaxLimit
	}
	q := fmt.Sprintf(topRequestsSQL, col)
	rows, err := s.db.QueryContext(ctx, q, from, to, limit)
	if err != nil {
		return nil, fmt.Errorf("top requests: %w", err)
	}
	defer rows.Close()

	out := make([]TopRequest, 0, limit)
	for rows.Next() {
		var (
			r       TopRequest
			started string
			stream  int
			ttft    sql.NullInt64
		)
		if err := rows.Scan(
			&r.ID, &started, &r.UpstreamID, &r.UpstreamName, &r.KeyID, &r.KeyName,
			&r.Model, &r.PathClass, &stream,
			&r.PromptTokens, &r.CompletionTokens, &r.CacheReadTokens,
			&r.TotalTokens, &r.CostNanoUSD, &r.LatencyMS, &ttft,
			&r.Status, &r.UsageStatus,
		); err != nil {
			return nil, fmt.Errorf("scan top request: %w", err)
		}
		if r.StartedAt, err = time.Parse(time.RFC3339, started); err != nil {
			return nil, fmt.Errorf("parse started_at %q: %w", started, err)
		}
		if ttft.Valid {
			v := ttft.Int64
			r.TTFTMS = &v
		}
		r.Stream = stream != 0
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate top requests: %w", err)
	}
	return out, nil
}

// AnalysisHealth aggregates the health counters for the inclusive local_day
// range and approximates the 95th latency percentile from the histogram.
func (s *Store) AnalysisHealth(ctx context.Context, from, to string) (AnalysisHealth, error) {
	h := AnalysisHealth{ThresholdMS: slowRequestMS}
	err := s.db.QueryRowContext(ctx, `
SELECT COALESCE(SUM(requests), 0), COALESCE(SUM(errors), 0)
FROM traffic_minutes WHERE local_day BETWEEN ? AND ?`, from, to).Scan(
		&h.Requests, &h.Errors,
	)
	if err != nil {
		return AnalysisHealth{}, fmt.Errorf("analysis health totals: %w", err)
	}

	err = s.db.QueryRowContext(ctx, `
SELECT COALESCE(SUM(CASE WHEN usage_status NOT IN (?, ?) THEN 1 ELSE 0 END), 0),
       COALESCE(SUM(CASE WHEN usage_status = ? THEN 1 ELSE 0 END), 0),
       COALESCE(SUM(CASE WHEN client_disconnected = 1 THEN 1 ELSE 0 END), 0),
       COALESCE(SUM(CASE WHEN latency_ms > ? THEN 1 ELSE 0 END), 0)
FROM requests WHERE local_day BETWEEN ? AND ?`,
		usage.StatusOK, usage.StatusNotApplicable, usage.StatusTooLarge,
		h.ThresholdMS, from, to).Scan(
		&h.Unmetered, &h.TooLarge, &h.ClientDisconnected, &h.CountOver,
	)
	if err != nil {
		return AnalysisHealth{}, fmt.Errorf("analysis health counts: %w", err)
	}

	if h.Requests > 0 {
		h.ErrorRate = float64(h.Errors) / float64(h.Requests)
	}
	buckets, err := s.latencyBuckets(from, to)
	if err != nil {
		return AnalysisHealth{}, err
	}
	h.P95MS = percentile(buckets, 95)
	return h, nil
}

// CacheStats aggregates prompt-cache usage for the inclusive local_day range.
// The saving is estimated per priced detail row from its rates snapshot.
func (s *Store) CacheStats(ctx context.Context, from, to string) (CacheStats, error) {
	var c CacheStats
	err := s.db.QueryRowContext(ctx, `
SELECT COALESCE(SUM(prompt_tokens), 0), COALESCE(SUM(cache_read_tokens), 0)
FROM traffic_minutes WHERE local_day BETWEEN ? AND ?`, from, to).Scan(
		&c.PromptTokens, &c.CacheReadTokens,
	)
	if err != nil {
		return CacheStats{}, fmt.Errorf("cache totals: %w", err)
	}
	if c.PromptTokens > 0 {
		c.HitRate = float64(c.CacheReadTokens) / float64(c.PromptTokens)
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT cache_read_tokens, rates_json FROM requests
WHERE local_day BETWEEN ? AND ? AND cache_read_tokens > 0 AND rates_json <> ''`,
		from, to)
	if err != nil {
		return CacheStats{}, fmt.Errorf("cache rows: %w", err)
	}
	defer rows.Close()

	var saved float64
	for rows.Next() {
		var (
			cached int64
			raw    string
			snap   struct {
				InputPerMTok     *float64 `json:"input_per_mtok"`
				CacheReadPerMTok *float64 `json:"cache_read_per_mtok"`
			}
		)
		if err := rows.Scan(&cached, &raw); err != nil {
			return CacheStats{}, fmt.Errorf("scan cache row: %w", err)
		}
		if err := json.Unmarshal([]byte(raw), &snap); err != nil {
			return CacheStats{}, fmt.Errorf("parse rates_json: %w", err)
		}
		if snap.InputPerMTok == nil || snap.CacheReadPerMTok == nil {
			continue
		}
		delta := *snap.InputPerMTok - *snap.CacheReadPerMTok
		if delta <= 0 {
			continue
		}
		saved += float64(cached) * delta
	}
	if err := rows.Err(); err != nil {
		return CacheStats{}, fmt.Errorf("iterate cache rows: %w", err)
	}
	v := int64(math.Round(saved * 1000))
	c.SavedNanoUSD = &v
	return c, nil
}

// ObservedModels returns the models seen since sinceDay (inclusive), most
// frequent first. Empty model names are excluded.
func (s *Store) ObservedModels(ctx context.Context, sinceDay string) ([]ObservedModel, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT model, COUNT(*), MAX(started_at) FROM requests
WHERE model <> '' AND local_day >= ?
GROUP BY model ORDER BY COUNT(*) DESC, model ASC`, sinceDay)
	if err != nil {
		return nil, fmt.Errorf("observed models: %w", err)
	}
	defer rows.Close()

	out := make([]ObservedModel, 0, 16)
	for rows.Next() {
		var (
			m       ObservedModel
			started string
		)
		if err := rows.Scan(&m.Model, &m.Requests, &started); err != nil {
			return nil, fmt.Errorf("scan observed model: %w", err)
		}
		if m.LastSeen, err = time.Parse(time.RFC3339, started); err != nil {
			return nil, fmt.Errorf("parse started_at %q: %w", started, err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate observed models: %w", err)
	}
	return out, nil
}
