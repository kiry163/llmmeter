package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/kiry163/llmmeter/internal/usage"
)

// ErrNotFound is returned by single-row lookups when no row matches.
var ErrNotFound = errors.New("store: not found")

// Interval selects the time bucket used by Series.
type Interval int

// Series intervals.
const (
	IntervalMinute Interval = iota
	IntervalHour
	IntervalDay
)

// Overview aggregates one day range. Sums come from traffic_minutes, which is
// kept permanently; the priced counts and latency percentiles come from the
// requests retention window and are therefore absent for older ranges.
type Overview struct {
	Requests, Errors                                int64
	PromptTokens, CompletionTokens, CacheReadTokens int64
	ReasoningTokens, TotalTokens                    int64 // TotalTokens = prompt + completion
	CostNanoUSD                                     int64
	UpBytes, DownBytes                              int64
	Priced, Unpriced, Unmetered                     int64
	LatencyP50MS, LatencyP95MS, LatencyAvgMS        *int64
	// LatencyMaxMS is the slowest latency kept by the permanent aggregate;
	// it is the best available bound once detail rows have been pruned.
	LatencyMaxMS *int64
}

// SeriesPoint is one time bucket of the series query.
type SeriesPoint struct {
	Day                            string
	MinuteOfDay                    int
	Requests, Errors               int64
	UpBytes, DownBytes             int64
	PromptTokens, CompletionTokens int64
	CacheReadTokens                int64
	CostNanoUSD                    int64
}

// BreakdownRow is one key of the breakdown query. The key is scoped to the
// upstream, so the same model or path class under two upstreams yields two
// rows rather than one merged row.
type BreakdownRow struct {
	UpstreamID, Key                             string
	Requests, Errors                            int64
	UpBytes, DownBytes                          int64
	PromptTokens, CompletionTokens, CostNanoUSD int64
	CacheReadTokens                             int64
	Unpriced                                    int64
}

// RequestFilter selects requests for the cursor list. Zero values mean "no
// filter"; From and To are inclusive local_day bounds. StatusClass is one of
// the 2xx, 4xx or 5xx classes and combines with an exact Status.
type RequestFilter struct {
	From, To string
	// UpstreamID and KeyID narrow to one upstream and one gateway key. A key
	// is only meaningful inside an upstream; the API layer rejects a key
	// filter without an upstream.
	UpstreamID  string
	KeyID       string
	Model       string
	PathClass   string
	Status      *int
	StatusClass string
	UsageStatus *string
	Priced      *bool
	Stream      *bool
	BeforeID    int64
	Limit       int
}

// RequestsSummary aggregates the requests matched by a RequestFilter,
// ignoring BeforeID and Limit. Errors counts statuses >= 400.
type RequestsSummary struct {
	Requests, CostNanoUSD, TotalTokens, Errors int64
}

// statusClassRange maps a status class filter to inclusive bounds.
func statusClassRange(class string) (lo, hi int, ok bool) {
	switch class {
	case "2xx":
		return 200, 299, true
	case "4xx":
		return 400, 499, true
	case "5xx":
		return 500, 599, true
	}
	return 0, 0, false
}

// upstreamCondition returns the WHERE fragment and argument that restrict a
// query over traffic_minutes or requests to one upstream. An empty ID means
// every upstream.
func upstreamCondition(upstreamID string) (string, []any) {
	if upstreamID == "" {
		return "", nil
	}
	return " AND upstream_id = ?", []any{upstreamID}
}

const (
	// defaultRequestLimit is used when RequestFilter.Limit is not positive.
	defaultRequestLimit = 50
	// maxRequestLimit caps a single page.
	maxRequestLimit = 200
	// latencyBucketMS is the width of the percentile histogram buckets.
	latencyBucketMS = 100
)

// latencyBucketsSQL buckets latency into latencyBucketMS-wide buckets. The
// literal 100 must track latencyBucketMS. The trailing %s is the optional
// upstream restriction.
const latencyBucketsSQL = `
SELECT latency_ms / 100 * 100 AS bucket, COUNT(*), MAX(latency_ms)
FROM requests WHERE local_day BETWEEN ? AND ?%s
GROUP BY bucket ORDER BY bucket`

// Overview aggregates the inclusive local_day range [from, to], restricted to
// one upstream when upstreamID is not empty.
func (s *Store) Overview(from, to, upstreamID string) (Overview, error) {
	cond, extra := upstreamCondition(upstreamID)
	var ov Overview
	var sumLatency, maxLatency int64
	sumArgs := append([]any{from, to}, extra...)
	err := s.db.QueryRow(`
SELECT COALESCE(SUM(requests), 0), COALESCE(SUM(errors), 0),
       COALESCE(SUM(req_bytes), 0), COALESCE(SUM(resp_bytes), 0),
       COALESCE(SUM(prompt_tokens), 0), COALESCE(SUM(completion_tokens), 0),
       COALESCE(SUM(cache_read_tokens), 0), COALESCE(SUM(reasoning_tokens), 0),
       COALESCE(SUM(cost_nano_usd), 0), COALESCE(SUM(latency_sum_ms), 0),
       COALESCE(MAX(latency_max_ms), 0)
FROM traffic_minutes WHERE local_day BETWEEN ? AND ?`+cond, sumArgs...).Scan(
		&ov.Requests, &ov.Errors, &ov.UpBytes, &ov.DownBytes,
		&ov.PromptTokens, &ov.CompletionTokens,
		&ov.CacheReadTokens, &ov.ReasoningTokens,
		&ov.CostNanoUSD, &sumLatency, &maxLatency,
	)
	if err != nil {
		return Overview{}, fmt.Errorf("overview sums: %w", err)
	}
	ov.TotalTokens = ov.PromptTokens + ov.CompletionTokens
	if ov.Requests > 0 {
		avg := sumLatency / ov.Requests
		ov.LatencyAvgMS = &avg
		ov.LatencyMaxMS = &maxLatency
	}

	countArgs := append([]any{usage.StatusOK, usage.StatusNotApplicable, from, to}, extra...)
	err = s.db.QueryRow(`
SELECT COALESCE(SUM(CASE WHEN priced = 1 THEN 1 ELSE 0 END), 0),
       COALESCE(SUM(CASE WHEN priced = 0 THEN 1 ELSE 0 END), 0),
       COALESCE(SUM(CASE WHEN usage_status NOT IN (?, ?) THEN 1 ELSE 0 END), 0)
FROM requests WHERE local_day BETWEEN ? AND ?`+cond, countArgs...).Scan(
		&ov.Priced, &ov.Unpriced, &ov.Unmetered,
	)
	if err != nil {
		return Overview{}, fmt.Errorf("overview counts: %w", err)
	}

	buckets, err := s.latencyBuckets(from, to, upstreamID)
	if err != nil {
		return Overview{}, err
	}
	if len(buckets) > 0 {
		ov.LatencyP50MS = percentile(buckets, 50)
		ov.LatencyP95MS = percentile(buckets, 95)
	} else if ov.LatencyAvgMS != nil {
		// The range predates detail retention: no histogram is available, so
		// approximate both percentiles with the permanent aggregate mean.
		ov.LatencyP50MS = ov.LatencyAvgMS
		ov.LatencyP95MS = ov.LatencyAvgMS
	}
	return ov, nil
}

// latencyBucket is one histogram bucket: requests in [Start, Start+width) and
// the slowest latency observed in it.
type latencyBucket struct {
	Start, Count, Max int64
}

// latencyBuckets returns the non-empty latency histogram buckets in ascending
// order. Percentiles come from requests because traffic_minutes only keeps the
// sum and the maximum.
func (s *Store) latencyBuckets(from, to, upstreamID string) ([]latencyBucket, error) {
	cond, extra := upstreamCondition(upstreamID)
	args := append([]any{from, to}, extra...)
	rows, err := s.db.Query(fmt.Sprintf(latencyBucketsSQL, cond), args...)
	if err != nil {
		return nil, fmt.Errorf("latency buckets: %w", err)
	}
	defer rows.Close()

	var buckets []latencyBucket
	for rows.Next() {
		var b latencyBucket
		if err := rows.Scan(&b.Start, &b.Count, &b.Max); err != nil {
			return nil, fmt.Errorf("scan latency bucket: %w", err)
		}
		buckets = append(buckets, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate latency buckets: %w", err)
	}
	return buckets, nil
}

// percentile returns the upper bound of the bucket that holds the pct-th
// percentile, or nil when there are no buckets. The topmost bucket reports the
// slowest observed latency instead of a bucket edge.
func percentile(buckets []latencyBucket, pct int64) *int64 {
	var total int64
	for _, b := range buckets {
		total += b.Count
	}
	if total == 0 {
		return nil
	}
	// Ceiling rank keeps small samples at the slow end of the bucket.
	rank := (pct*total + 99) / 100
	var cum int64
	for i, b := range buckets {
		cum += b.Count
		if cum < rank {
			continue
		}
		var v int64
		if i == len(buckets)-1 {
			v = b.Max
		} else {
			v = b.Start + latencyBucketMS
		}
		return &v
	}
	v := buckets[len(buckets)-1].Max
	return &v
}

// EarliestDay returns the first local_day with traffic, or "" when the
// traffic_minutes aggregate is empty.
func (s *Store) EarliestDay() (string, error) {
	var day sql.NullString
	if err := s.db.QueryRow(`SELECT MIN(local_day) FROM traffic_minutes`).Scan(&day); err != nil {
		return "", fmt.Errorf("earliest day: %w", err)
	}
	return day.String, nil
}

const (
	// The trailing %s of every series query is the optional upstream
	// restriction.
	seriesMinuteSQL = `
SELECT local_day, minute_of_day,
       SUM(requests), SUM(errors), SUM(req_bytes), SUM(resp_bytes),
       SUM(prompt_tokens), SUM(completion_tokens), SUM(cache_read_tokens), SUM(cost_nano_usd)
FROM traffic_minutes WHERE local_day BETWEEN ? AND ?%s
GROUP BY local_day, minute_of_day ORDER BY local_day, minute_of_day`

	seriesHourSQL = `
SELECT local_day, minute_of_day / 60,
       SUM(requests), SUM(errors), SUM(req_bytes), SUM(resp_bytes),
       SUM(prompt_tokens), SUM(completion_tokens), SUM(cache_read_tokens), SUM(cost_nano_usd)
FROM traffic_minutes WHERE local_day BETWEEN ? AND ?%s
GROUP BY local_day, minute_of_day / 60 ORDER BY local_day, minute_of_day / 60`

	seriesDaySQL = `
SELECT local_day, 0,
       SUM(requests), SUM(errors), SUM(req_bytes), SUM(resp_bytes),
       SUM(prompt_tokens), SUM(completion_tokens), SUM(cache_read_tokens), SUM(cost_nano_usd)
FROM traffic_minutes WHERE local_day BETWEEN ? AND ?%s
GROUP BY local_day ORDER BY local_day`
)

// Series returns traffic buckets for the inclusive local_day range, ordered
// ascending, restricted to one upstream when upstreamID is not empty. Day
// buckets carry MinuteOfDay = 0.
func (s *Store) Series(from, to string, iv Interval, upstreamID string) ([]SeriesPoint, error) {
	q := seriesDaySQL
	switch iv {
	case IntervalMinute:
		q = seriesMinuteSQL
	case IntervalHour:
		q = seriesHourSQL
	}
	cond, extra := upstreamCondition(upstreamID)
	args := append([]any{from, to}, extra...)
	rows, err := s.db.Query(fmt.Sprintf(q, cond), args...)
	if err != nil {
		return nil, fmt.Errorf("series: %w", err)
	}
	defer rows.Close()

	points := make([]SeriesPoint, 0, 64)
	for rows.Next() {
		var p SeriesPoint
		if err := rows.Scan(
			&p.Day, &p.MinuteOfDay,
			&p.Requests, &p.Errors, &p.UpBytes, &p.DownBytes,
			&p.PromptTokens, &p.CompletionTokens, &p.CacheReadTokens, &p.CostNanoUSD,
		); err != nil {
			return nil, fmt.Errorf("scan series point: %w", err)
		}
		points = append(points, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate series: %w", err)
	}
	return points, nil
}

const (
	// The trailing %s of every breakdown query is the optional upstream
	// restriction.
	breakdownModelSQL = `
SELECT upstream_id, model, SUM(requests), SUM(errors), SUM(req_bytes), SUM(resp_bytes),
       SUM(prompt_tokens), SUM(completion_tokens), SUM(cache_read_tokens), SUM(cost_nano_usd)
FROM traffic_minutes WHERE local_day BETWEEN ? AND ?%s
GROUP BY upstream_id, model ORDER BY SUM(cost_nano_usd) DESC, upstream_id ASC, model ASC`

	breakdownPathClassSQL = `
SELECT upstream_id, path_class, SUM(requests), SUM(errors), SUM(req_bytes), SUM(resp_bytes),
       SUM(prompt_tokens), SUM(completion_tokens), SUM(cache_read_tokens), SUM(cost_nano_usd)
FROM traffic_minutes WHERE local_day BETWEEN ? AND ?%s
GROUP BY upstream_id, path_class ORDER BY SUM(cost_nano_usd) DESC, upstream_id ASC, path_class ASC`

	unpricedByModelSQL = `
SELECT upstream_id, model, COUNT(*) FROM requests
WHERE local_day BETWEEN ? AND ?%s AND priced = 0
GROUP BY upstream_id, model`

	unpricedByPathClassSQL = `
SELECT upstream_id, path_class, COUNT(*) FROM requests
WHERE local_day BETWEEN ? AND ?%s AND priced = 0
GROUP BY upstream_id, path_class`
)

// Breakdown aggregates the inclusive local_day range by "model" (anything
// else groups by path_class), ordered by cost descending, restricted to one
// upstream when upstreamID is not empty. Unpriced counts come from the
// requests retention window.
func (s *Store) Breakdown(from, to, by, upstreamID string) ([]BreakdownRow, error) {
	q, unpricedSQL := breakdownPathClassSQL, unpricedByPathClassSQL
	if by == "model" {
		q, unpricedSQL = breakdownModelSQL, unpricedByModelSQL
	}
	cond, extra := upstreamCondition(upstreamID)
	args := append([]any{from, to}, extra...)
	rows, err := s.db.Query(fmt.Sprintf(q, cond), args...)
	if err != nil {
		return nil, fmt.Errorf("breakdown: %w", err)
	}
	defer rows.Close()

	out := make([]BreakdownRow, 0, 16)
	for rows.Next() {
		var r BreakdownRow
		if err := rows.Scan(
			&r.UpstreamID, &r.Key, &r.Requests, &r.Errors, &r.UpBytes, &r.DownBytes,
			&r.PromptTokens, &r.CompletionTokens, &r.CacheReadTokens, &r.CostNanoUSD,
		); err != nil {
			return nil, fmt.Errorf("scan breakdown row: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate breakdown: %w", err)
	}

	unpriced, err := s.unpricedCounts(unpricedSQL, from, to, upstreamID)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Unpriced = unpriced[unpricedKey{upstream: out[i].UpstreamID, key: out[i].Key}]
	}
	return out, nil
}

// unpricedKey identifies one breakdown row for the unpriced counter, which is
// scoped to an upstream just like the aggregate rows.
type unpricedKey struct {
	upstream, key string
}

// unpricedCounts maps each (upstream, key) pair to the number of unpriced
// requests in the range.
func (s *Store) unpricedCounts(q, from, to, upstreamID string) (map[unpricedKey]int64, error) {
	cond, extra := upstreamCondition(upstreamID)
	args := append([]any{from, to}, extra...)
	rows, err := s.db.Query(fmt.Sprintf(q, cond), args...)
	if err != nil {
		return nil, fmt.Errorf("unpriced counts: %w", err)
	}
	defer rows.Close()

	counts := make(map[unpricedKey]int64)
	for rows.Next() {
		var key unpricedKey
		var n int64
		if err := rows.Scan(&key.upstream, &key.key, &n); err != nil {
			return nil, fmt.Errorf("scan unpriced count: %w", err)
		}
		counts[key] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate unpriced counts: %w", err)
	}
	return counts, nil
}

// selectRecordColumns mirrors the column order of insertRequestSQL with the
// assigned id in front, so scanRecord can scan rows positionally.
const selectRecordColumns = `id, started_at, completed_at, local_day, minute_of_day,
  path, path_class, upstream_id, upstream_name, key_id, key_name,
  model, upstream_model, stream, status,
  latency_ms, ttft_ms,
  req_body_bytes, req_header_bytes, resp_body_bytes, resp_header_bytes,
  prompt_tokens, completion_tokens, cache_read_tokens, cache_write_tokens,
  reasoning_tokens, total_tokens,
  window_label, rates_json, cost_nano_usd, priced,
  usage_status, client_disconnected, error`

// requestWhere builds the WHERE clause shared by Requests and
// RequestsSummary from the filter fields, appending cursor when asked. Limit
// is never part of the filter.
func requestWhere(f RequestFilter, cursor bool) (string, []any) {
	// Empty bounds mean "no bound": the detail list is valid without a range.
	from, to := f.From, f.To
	if from == "" {
		from = "0000-01-01"
	}
	if to == "" {
		to = "9999-12-31"
	}
	q := " WHERE local_day BETWEEN ? AND ?"
	args := []any{from, to}
	if f.UpstreamID != "" {
		q += " AND upstream_id = ?"
		args = append(args, f.UpstreamID)
	}
	if f.KeyID != "" {
		q += " AND key_id = ?"
		args = append(args, f.KeyID)
	}
	if f.Model != "" {
		q += " AND model = ?"
		args = append(args, f.Model)
	}
	if f.PathClass != "" {
		q += " AND path_class = ?"
		args = append(args, f.PathClass)
	}
	if f.Status != nil {
		q += " AND status = ?"
		args = append(args, *f.Status)
	}
	if lo, hi, ok := statusClassRange(f.StatusClass); ok {
		q += " AND status BETWEEN ? AND ?"
		args = append(args, lo, hi)
	}
	if f.UsageStatus != nil {
		q += " AND usage_status = ?"
		args = append(args, *f.UsageStatus)
	}
	if f.Priced != nil {
		q += " AND priced = ?"
		args = append(args, boolInt(*f.Priced))
	}
	if f.Stream != nil {
		q += " AND stream = ?"
		args = append(args, boolInt(*f.Stream))
	}
	if cursor && f.BeforeID > 0 {
		q += " AND id < ?"
		args = append(args, f.BeforeID)
	}
	return q, args
}

// Requests returns at most Limit records for the filter, newest first.
func (s *Store) Requests(f RequestFilter) ([]Record, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = defaultRequestLimit
	}
	if limit > maxRequestLimit {
		limit = maxRequestLimit
	}

	where, args := requestWhere(f, true)
	q := "SELECT " + selectRecordColumns + " FROM requests" + where +
		" ORDER BY id DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("requests: %w", err)
	}
	defer rows.Close()

	out := make([]Record, 0, limit)
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate requests: %w", err)
	}
	return out, nil
}

// RequestsSummary aggregates every request matching the filter, ignoring
// BeforeID and Limit.
func (s *Store) RequestsSummary(f RequestFilter) (RequestsSummary, error) {
	where, args := requestWhere(f, false)
	var sum RequestsSummary
	err := s.db.QueryRow(`
SELECT COUNT(*), COALESCE(SUM(cost_nano_usd), 0),
       COALESCE(SUM(total_tokens), 0),
       COALESCE(SUM(CASE WHEN status >= 400 THEN 1 ELSE 0 END), 0)
FROM requests`+where, args...).Scan(
		&sum.Requests, &sum.CostNanoUSD, &sum.TotalTokens, &sum.Errors,
	)
	if err != nil {
		return RequestsSummary{}, fmt.Errorf("requests summary: %w", err)
	}
	return sum, nil
}

// Request loads one record by id.
func (s *Store) Request(id int64) (Record, error) {
	row := s.db.QueryRow("SELECT "+selectRecordColumns+" FROM requests WHERE id = ?", id)
	r, err := scanRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, fmt.Errorf("%w: request %d: %w", ErrNotFound, id, sql.ErrNoRows)
	}
	return r, err
}

// rowScanner is implemented by *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanRecord reads one row selected with selectRecordColumns.
func scanRecord(sc rowScanner) (Record, error) {
	var (
		r                          Record
		started, completed         string
		stream, priced, disconnect int
		ttft                       sql.NullInt64
	)
	err := sc.Scan(
		&r.ID, &started, &completed, &r.LocalDay, &r.MinuteOfDay,
		&r.Path, &r.PathClass, &r.UpstreamID, &r.UpstreamName,
		&r.KeyID, &r.KeyName, &r.Model, &r.UpstreamModel, &stream, &r.Status,
		&r.LatencyMS, &ttft,
		&r.ReqBodyBytes, &r.ReqHeaderBytes, &r.RespBodyBytes, &r.RespHeaderBytes,
		&r.Usage.PromptTokens, &r.Usage.CompletionTokens,
		&r.Usage.CacheReadTokens, &r.Usage.CacheWriteTokens,
		&r.Usage.ReasoningTokens, &r.Usage.TotalTokens,
		&r.WindowLabel, &r.RatesJSON, &r.CostNanoUSD, &priced,
		&r.UsageStatus, &disconnect, &r.Error,
	)
	if err != nil {
		return Record{}, err
	}
	if r.StartedAt, err = time.Parse(timestampLayout, started); err != nil {
		return Record{}, fmt.Errorf("parse started_at %q: %w", started, err)
	}
	if r.CompletedAt, err = time.Parse(timestampLayout, completed); err != nil {
		return Record{}, fmt.Errorf("parse completed_at %q: %w", completed, err)
	}
	if ttft.Valid {
		v := ttft.Int64
		r.TTFTMS = &v
	}
	r.Stream = stream != 0
	r.Priced = priced != 0
	r.ClientDisconnected = disconnect != 0
	return r, nil
}
