package store

import (
	"time"

	"github.com/kiry163/llmmeter/internal/usage"
)

// Record is one completed request as persisted into the requests table.
type Record struct {
	StartedAt, CompletedAt                                       time.Time
	LocalDay                                                     string
	MinuteOfDay                                                  int
	Path, PathClass                                              string
	Model, UpstreamModel                                         string
	Stream                                                       bool
	Status                                                       int
	LatencyMS                                                    int64
	TTFTMS                                                       *int64
	ReqBodyBytes, ReqHeaderBytes, RespBodyBytes, RespHeaderBytes int64
	Usage                                                        usage.Usage
	WindowLabel                                                  string
	RatesJSON                                                    string
	CostNanoUSD                                                  int64
	Priced                                                       bool
	UsageStatus                                                  string
	ClientDisconnected                                           bool
	Error                                                        string
}

// MinuteDelta is an incremental contribution to one traffic_minutes row.
type MinuteDelta struct {
	LocalDay                         string
	MinuteOfDay                      int
	PathClass, Model                 string
	Requests, Errors                 int64
	ReqBytes, RespBytes              int64
	PromptTokens, CompletionTokens   int64
	CacheReadTokens, ReasoningTokens int64
	CostNanoUSD                      int64
	LatencySumMS, LatencyMaxMS       int64
}

// timestampLayout is RFC 3339 with millisecond precision in UTC, which also
// sorts lexicographically.
const timestampLayout = "2006-01-02T15:04:05.000Z"

// timestampText formats t as UTC; zero values become the zero instant.
func timestampText(t time.Time) string {
	return t.UTC().Format(timestampLayout)
}

// boolInt maps a bool to the 0/1 integer SQLite stores.
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

const insertRequestSQL = `INSERT INTO requests (
  started_at, completed_at, local_day, minute_of_day,
  path, path_class, model, upstream_model, stream, status,
  latency_ms, ttft_ms,
  req_body_bytes, req_header_bytes, resp_body_bytes, resp_header_bytes,
  prompt_tokens, completion_tokens, cache_read_tokens, cache_write_tokens,
  reasoning_tokens, total_tokens,
  window_label, rates_json, cost_nano_usd, priced,
  usage_status, client_disconnected, error
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`

// insertArgs flattens r into the bind parameters of insertRequestSQL.
func insertArgs(r *Record) []any {
	var ttft any // nil binds SQL NULL
	if r.TTFTMS != nil {
		ttft = *r.TTFTMS
	}
	return []any{
		timestampText(r.StartedAt), timestampText(r.CompletedAt),
		r.LocalDay, r.MinuteOfDay,
		r.Path, r.PathClass, r.Model, r.UpstreamModel,
		boolInt(r.Stream), r.Status,
		r.LatencyMS, ttft,
		r.ReqBodyBytes, r.ReqHeaderBytes, r.RespBodyBytes, r.RespHeaderBytes,
		r.Usage.PromptTokens, r.Usage.CompletionTokens,
		r.Usage.CacheReadTokens, r.Usage.CacheWriteTokens,
		r.Usage.ReasoningTokens, r.Usage.TotalTokens,
		r.WindowLabel, r.RatesJSON, r.CostNanoUSD, boolInt(r.Priced),
		r.UsageStatus, boolInt(r.ClientDisconnected), r.Error,
	}
}

const upsertMinuteSQL = `INSERT INTO traffic_minutes (
  local_day, minute_of_day, path_class, model,
  requests, errors, req_bytes, resp_bytes,
  prompt_tokens, completion_tokens, cache_read_tokens, reasoning_tokens,
  cost_nano_usd, latency_sum_ms, latency_max_ms
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(local_day, minute_of_day, path_class, model) DO UPDATE SET
  requests = requests + excluded.requests,
  errors = errors + excluded.errors,
  req_bytes = req_bytes + excluded.req_bytes,
  resp_bytes = resp_bytes + excluded.resp_bytes,
  prompt_tokens = prompt_tokens + excluded.prompt_tokens,
  completion_tokens = completion_tokens + excluded.completion_tokens,
  cache_read_tokens = cache_read_tokens + excluded.cache_read_tokens,
  reasoning_tokens = reasoning_tokens + excluded.reasoning_tokens,
  cost_nano_usd = cost_nano_usd + excluded.cost_nano_usd,
  latency_sum_ms = latency_sum_ms + excluded.latency_sum_ms,
  latency_max_ms = MAX(latency_max_ms, excluded.latency_max_ms)`

// minuteArgs flattens d into the bind parameters of upsertMinuteSQL.
func minuteArgs(d *MinuteDelta) []any {
	return []any{
		d.LocalDay, d.MinuteOfDay, d.PathClass, d.Model,
		d.Requests, d.Errors, d.ReqBytes, d.RespBytes,
		d.PromptTokens, d.CompletionTokens, d.CacheReadTokens, d.ReasoningTokens,
		d.CostNanoUSD, d.LatencySumMS, d.LatencyMaxMS,
	}
}

// pruneSQL deletes at most one chunk of requests older than the bound time.
// Chunking keeps each statement short so the writer stays responsive.
const pruneSQL = `DELETE FROM requests WHERE id IN (
  SELECT id FROM requests WHERE started_at < ? LIMIT 10000
)`
