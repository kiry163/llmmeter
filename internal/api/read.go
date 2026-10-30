package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kiry163/llmmeter/internal/store"
)

const (
	// dateLayout is the local-date format of from/to query parameters.
	dateLayout = "2006-01-02"

	// healthProbeTimeout bounds the write probe so /api/health answers
	// promptly even while the database is busy.
	healthProbeTimeout = 500 * time.Millisecond

	// defaultRangeDays is the length of the default range ending today.
	defaultRangeDays = 7

	// requestsDefaultLimit and requestsMaxLimit bound the cursor page size.
	requestsDefaultLimit = 50
	requestsMaxLimit     = 200

	// maxMinuteIntervalDays and maxHourIntervalDays bound those series
	// intervals; longer ranges must use day buckets.
	maxMinuteIntervalDays = 2
	maxHourIntervalDays   = 90
)

// bootstrap is the unauthenticated first-load payload.
type bootstrap struct {
	Version      string `json:"version"`
	NeedsSetup   bool   `json:"needsSetup"`
	AuthRequired bool   `json:"authRequired"`
	Listen       string `json:"listen"`
	Timezone     string `json:"timezone"`
	EarliestDay  string `json:"earliestDay"`
}

func (s *Server) bootstrap(w http.ResponseWriter, _ *http.Request) {
	snap := s.repo.Snapshot()
	earliest, err := s.store.EarliestDay()
	if err != nil {
		slog.Error("api: earliest day", "error", sanitizeLogError(err))
	}
	writeJSON(w, http.StatusOK, bootstrap{
		Version:      s.version,
		NeedsSetup:   snap == nil || !snap.SetupDone,
		AuthRequired: snap != nil && snap.AdminPasswordHash != "",
		Listen:       s.listen,
		Timezone:     s.location().String(),
		EarliestDay:  earliest,
	})
}

// me reports the viewer's authentication state without requiring a session.
type me struct {
	AuthRequired  bool `json:"authRequired"`
	Authenticated bool `json:"authenticated"`
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	snap := s.repo.Snapshot()
	writeJSON(w, http.StatusOK, me{
		AuthRequired:  snap != nil && snap.AdminPasswordHash != "",
		Authenticated: s.auth.Authenticated(r),
	})
}

// health reports process and database liveness. dbWritable comes from a real
// write-transaction probe with a short deadline, so a read-only or locked
// database is reported instead of a plain SELECT answering.
type health struct {
	OK         bool   `json:"ok"`
	DBWritable bool   `json:"dbWritable"`
	QueueDepth int    `json:"queueDepth"`
	Version    string `json:"version"`
	LastError  string `json:"lastError,omitempty"`
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), healthProbeTimeout)
	defer cancel()
	err := s.store.ProbeWritable(ctx)
	ok := err == nil
	h := health{
		OK:         ok,
		DBWritable: ok,
		QueueDepth: s.store.QueueDepth(),
		Version:    s.version,
	}
	if werr := s.store.LastError(); werr != nil {
		h.LastError = werr.Error()
	}
	status := http.StatusOK
	if !ok {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, h)
}

// overviewStats is one period of the overview cards.
type overviewStats struct {
	Requests         int64 `json:"requests"`
	Errors           int64 `json:"errors"`
	PromptTokens     int64 `json:"promptTokens"`
	CompletionTokens int64 `json:"completionTokens"`
	CacheReadTokens  int64 `json:"cacheReadTokens"`
	ReasoningTokens  int64 `json:"reasoningTokens"`
	TotalTokens      int64 `json:"totalTokens"`
	CostNanoUSD      int64 `json:"costNanoUsd"`
	UpBytes          int64 `json:"upBytes"`
	DownBytes        int64 `json:"downBytes"`
	Priced           int64 `json:"priced"`
	Unpriced         int64 `json:"unpriced"`
	Unmetered        int64 `json:"unmetered"`

	LatencyP50MS *int64 `json:"latencyP50Ms"`
	LatencyP95MS *int64 `json:"latencyP95Ms"`
	LatencyAvgMS *int64 `json:"latencyAvgMs"`
	LatencyMaxMS *int64 `json:"latencyMaxMs"`
}

// overviewBody is the overview response, including the equal-length period
// immediately before the requested one.
type overviewBody struct {
	From     string        `json:"from"`
	To       string        `json:"to"`
	Partial  bool          `json:"partial"`
	Current  overviewStats `json:"current"`
	Previous overviewStats `json:"previous"`
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	from, to, ok := s.rangeQuery(w, r)
	if !ok {
		return
	}
	current, err := s.store.Overview(from, to)
	if err != nil {
		writeInternal(w, err)
		return
	}

	days := daySpan(from, to)
	fromDay, _ := time.Parse(dateLayout, from)
	prevTo := fromDay.AddDate(0, 0, -1)
	prevFrom := prevTo.AddDate(0, 0, -(days - 1))

	previous, err := s.store.Overview(prevFrom.Format(dateLayout), prevTo.Format(dateLayout))
	if err != nil {
		writeInternal(w, err)
		return
	}

	writeJSON(w, http.StatusOK, overviewBody{
		From:     from,
		To:       to,
		Partial:  s.partialRange(from),
		Current:  statsDTO(current),
		Previous: statsDTO(previous),
	})
}

// statsDTO flattens a store.Overview.
func statsDTO(o store.Overview) overviewStats {
	return overviewStats{
		Requests:         o.Requests,
		Errors:           o.Errors,
		PromptTokens:     o.PromptTokens,
		CompletionTokens: o.CompletionTokens,
		CacheReadTokens:  o.CacheReadTokens,
		ReasoningTokens:  o.ReasoningTokens,
		TotalTokens:      o.TotalTokens,
		CostNanoUSD:      o.CostNanoUSD,
		UpBytes:          o.UpBytes,
		DownBytes:        o.DownBytes,
		Priced:           o.Priced,
		Unpriced:         o.Unpriced,
		Unmetered:        o.Unmetered,
		LatencyP50MS:     o.LatencyP50MS,
		LatencyP95MS:     o.LatencyP95MS,
		LatencyAvgMS:     o.LatencyAvgMS,
		LatencyMaxMS:     o.LatencyMaxMS,
	}
}

// seriesPoint is one bucket of the series response.
type seriesPoint struct {
	Day              string `json:"day"`
	MinuteOfDay      int    `json:"minuteOfDay"`
	Requests         int64  `json:"requests"`
	Errors           int64  `json:"errors"`
	UpBytes          int64  `json:"upBytes"`
	DownBytes        int64  `json:"downBytes"`
	PromptTokens     int64  `json:"promptTokens"`
	CompletionTokens int64  `json:"completionTokens"`
	CacheReadTokens  int64  `json:"cacheReadTokens"`
	CostNanoUSD      int64  `json:"costNanoUsd"`
}

func (s *Server) series(w http.ResponseWriter, r *http.Request) {
	from, to, ok := s.rangeQuery(w, r)
	if !ok {
		return
	}

	var iv store.Interval
	switch v := r.URL.Query().Get("interval"); v {
	case "", "day":
		iv = store.IntervalDay
	case "hour":
		iv = store.IntervalHour
	case "minute":
		iv = store.IntervalMinute
	default:
		writeAPIError(w, http.StatusBadRequest, codeValidation, "interval must be minute, hour or day")
		return
	}

	days := daySpan(from, to)
	if iv == store.IntervalMinute && days > maxMinuteIntervalDays {
		writeAPIError(w, http.StatusBadRequest, codeValidation,
			fmt.Sprintf("interval minute is limited to %d days", maxMinuteIntervalDays))
		return
	}
	if iv == store.IntervalHour && days > maxHourIntervalDays {
		writeAPIError(w, http.StatusBadRequest, codeValidation,
			fmt.Sprintf("interval hour is limited to %d days", maxHourIntervalDays))
		return
	}

	points, err := s.store.Series(from, to, iv)
	if err != nil {
		writeInternal(w, err)
		return
	}
	out := make([]seriesPoint, 0, len(points))
	for _, p := range points {
		out = append(out, seriesPoint{
			Day:              p.Day,
			MinuteOfDay:      p.MinuteOfDay,
			Requests:         p.Requests,
			Errors:           p.Errors,
			UpBytes:          p.UpBytes,
			DownBytes:        p.DownBytes,
			PromptTokens:     p.PromptTokens,
			CompletionTokens: p.CompletionTokens,
			CacheReadTokens:  p.CacheReadTokens,
			CostNanoUSD:      p.CostNanoUSD,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"points": out})
}

// breakdownRow is one key of the breakdown response.
type breakdownRow struct {
	Key              string `json:"key"`
	Requests         int64  `json:"requests"`
	Errors           int64  `json:"errors"`
	UpBytes          int64  `json:"upBytes"`
	DownBytes        int64  `json:"downBytes"`
	PromptTokens     int64  `json:"promptTokens"`
	CompletionTokens int64  `json:"completionTokens"`
	CostNanoUSD      int64  `json:"costNanoUsd"`
	Unpriced         int64  `json:"unpriced"`
}

func (s *Server) breakdown(w http.ResponseWriter, r *http.Request) {
	from, to, ok := s.rangeQuery(w, r)
	if !ok {
		return
	}
	by := r.URL.Query().Get("by")
	if by != "model" && by != "pathClass" {
		writeAPIError(w, http.StatusBadRequest, codeValidation, "by must be model or pathClass")
		return
	}

	rows, err := s.store.Breakdown(from, to, by)
	if err != nil {
		writeInternal(w, err)
		return
	}
	out := make([]breakdownRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, breakdownRow{
			Key:              row.Key,
			Requests:         row.Requests,
			Errors:           row.Errors,
			UpBytes:          row.UpBytes,
			DownBytes:        row.DownBytes,
			PromptTokens:     row.PromptTokens,
			CompletionTokens: row.CompletionTokens,
			CostNanoUSD:      row.CostNanoUSD,
			Unpriced:         row.Unpriced,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"by": by, "rows": out})
}

// recordDTO is the flat JSON form of a store.Record. Token and byte counters
// are inlined and the rates snapshot is decoded into an object.
type recordDTO struct {
	ID            int64  `json:"id"`
	StartedAt     string `json:"startedAt"`
	CompletedAt   string `json:"completedAt"`
	LocalDay      string `json:"localDay"`
	MinuteOfDay   int    `json:"minuteOfDay"`
	Path          string `json:"path"`
	PathClass     string `json:"pathClass"`
	Model         string `json:"model"`
	UpstreamModel string `json:"upstreamModel"`
	Stream        bool   `json:"stream"`
	Status        int    `json:"status"`
	LatencyMS     int64  `json:"latencyMs"`
	TTFTMS        *int64 `json:"ttftMs"`

	PromptTokens     int64 `json:"promptTokens"`
	CompletionTokens int64 `json:"completionTokens"`
	CacheReadTokens  int64 `json:"cacheReadTokens"`
	CacheWriteTokens int64 `json:"cacheWriteTokens"`
	ReasoningTokens  int64 `json:"reasoningTokens"`
	TotalTokens      int64 `json:"totalTokens"`

	ReqBodyBytes    int64 `json:"reqBodyBytes"`
	ReqHeaderBytes  int64 `json:"reqHeaderBytes"`
	RespBodyBytes   int64 `json:"respBodyBytes"`
	RespHeaderBytes int64 `json:"respHeaderBytes"`

	WindowLabel string          `json:"windowLabel"`
	Rates       json.RawMessage `json:"rates"`
	CostNanoUSD int64           `json:"costNanoUsd"`
	Priced      bool            `json:"priced"`

	UsageStatus        string `json:"usageStatus"`
	ClientDisconnected bool   `json:"clientDisconnected"`
	Error              string `json:"error"`
}

// recordToDTO flattens r for the wire.
func recordToDTO(r store.Record) recordDTO {
	return recordDTO{
		ID:            r.ID,
		StartedAt:     r.StartedAt.UTC().Format(time.RFC3339),
		CompletedAt:   r.CompletedAt.UTC().Format(time.RFC3339),
		LocalDay:      r.LocalDay,
		MinuteOfDay:   r.MinuteOfDay,
		Path:          r.Path,
		PathClass:     r.PathClass,
		Model:         r.Model,
		UpstreamModel: r.UpstreamModel,
		Stream:        r.Stream,
		Status:        r.Status,
		LatencyMS:     r.LatencyMS,
		TTFTMS:        r.TTFTMS,

		PromptTokens:     r.Usage.PromptTokens,
		CompletionTokens: r.Usage.CompletionTokens,
		CacheReadTokens:  r.Usage.CacheReadTokens,
		CacheWriteTokens: r.Usage.CacheWriteTokens,
		ReasoningTokens:  r.Usage.ReasoningTokens,
		TotalTokens:      r.Usage.TotalTokens,

		ReqBodyBytes:    r.ReqBodyBytes,
		ReqHeaderBytes:  r.ReqHeaderBytes,
		RespBodyBytes:   r.RespBodyBytes,
		RespHeaderBytes: r.RespHeaderBytes,

		WindowLabel: r.WindowLabel,
		Rates:       decodeRates(r.RatesJSON),
		CostNanoUSD: r.CostNanoUSD,
		Priced:      r.Priced,

		UsageStatus:        r.UsageStatus,
		ClientDisconnected: r.ClientDisconnected,
		Error:              r.Error,
	}
}

// decodeRates returns the stored rates snapshot as raw JSON, or nil when the
// request carried no pricing snapshot.
func decodeRates(raw string) json.RawMessage {
	raw = strings.TrimSpace(raw)
	if raw == "" || !json.Valid([]byte(raw)) {
		return nil
	}
	return json.RawMessage(raw)
}

// requestsSummary is the aggregate of every request matching the filters,
// independent of the cursor page.
type requestsSummary struct {
	Requests    int64 `json:"requests"`
	CostNanoUSD int64 `json:"costNanoUsd"`
	TotalTokens int64 `json:"totalTokens"`
	Errors      int64 `json:"errors"`
}

// requestsBody is the cursor page response.
type requestsBody struct {
	Requests     []recordDTO     `json:"requests"`
	NextBeforeID int64           `json:"nextBeforeId"`
	Summary      requestsSummary `json:"summary"`
}

func (s *Server) requests(w http.ResponseWriter, r *http.Request) {
	from, to, ok := s.rangeQuery(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()

	f := store.RequestFilter{From: from, To: to}
	f.Model = q.Get("model")
	f.PathClass = q.Get("pathClass")

	if v := q.Get("status"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, codeValidation, "status must be an integer")
			return
		}
		f.Status = &n
	}
	if v := q.Get("statusClass"); v != "" {
		if v != "2xx" && v != "4xx" && v != "5xx" {
			writeAPIError(w, http.StatusBadRequest, codeValidation, "statusClass must be 2xx, 4xx or 5xx")
			return
		}
		f.StatusClass = v
	}
	if v := q.Get("usageStatus"); v != "" {
		f.UsageStatus = &v
	}
	if v := q.Get("priced"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, codeValidation, "priced must be a boolean")
			return
		}
		f.Priced = &b
	}
	if v := q.Get("stream"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, codeValidation, "stream must be a boolean")
			return
		}
		f.Stream = &b
	}
	if v := q.Get("before_id"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			writeAPIError(w, http.StatusBadRequest, codeValidation, "before_id must be a non-negative integer")
			return
		}
		f.BeforeID = n
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > requestsMaxLimit {
			writeAPIError(w, http.StatusBadRequest, codeValidation,
				fmt.Sprintf("limit must be between 1 and %d", requestsMaxLimit))
			return
		}
		f.Limit = n
	} else {
		f.Limit = requestsDefaultLimit
	}

	rows, err := s.store.Requests(f)
	if err != nil {
		writeInternal(w, err)
		return
	}
	summary, err := s.store.RequestsSummary(f)
	if err != nil {
		writeInternal(w, err)
		return
	}
	out := make([]recordDTO, 0, len(rows))
	for _, row := range rows {
		out = append(out, recordToDTO(row))
	}
	var next int64
	if len(out) > 0 {
		next = out[len(out)-1].ID
	}
	writeJSON(w, http.StatusOK, requestsBody{
		Requests:     out,
		NextBeforeID: next,
		Summary: requestsSummary{
			Requests:    summary.Requests,
			CostNanoUSD: summary.CostNanoUSD,
			TotalTokens: summary.TotalTokens,
			Errors:      summary.Errors,
		},
	})
}

func (s *Server) request(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeAPIError(w, http.StatusBadRequest, codeValidation, "invalid request id")
		return
	}
	row, err := s.store.Request(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeAPIError(w, http.StatusNotFound, codeNotFound, "request not found")
			return
		}
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, recordToDTO(row))
}

func (s *Server) liveSnapshot(w http.ResponseWriter, r *http.Request) {
	window, ok := windowQuery(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.hub.Snapshot(window))
}

func (s *Server) liveStream(w http.ResponseWriter, r *http.Request) {
	window, ok := windowQuery(w, r)
	if !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAPIError(w, http.StatusInternalServerError, codeInternal, "streaming unsupported")
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch, cancel := s.hub.Subscribe(window)
	defer cancel()

	// Seed the client with the current snapshot instead of waiting a second.
	if frame, err := json.Marshal(s.hub.Snapshot(window)); err == nil {
		if !writeTick(w, frame) {
			return
		}
		flusher.Flush()
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case frame, ok := <-ch:
			if !ok {
				return
			}
			if !writeTick(w, frame) {
				return
			}
			flusher.Flush()
		}
	}
}

// writeTick writes one SSE frame and reports whether it was accepted.
func writeTick(w http.ResponseWriter, frame []byte) bool {
	if _, err := fmt.Fprintf(w, "event: tick\ndata: %s\n\n", frame); err != nil {
		return false
	}
	return true
}

// windowQuery parses the live window parameter: 60 or 300 seconds, default 60.
func windowQuery(w http.ResponseWriter, r *http.Request) (time.Duration, bool) {
	switch r.URL.Query().Get("window") {
	case "", "60":
		return 60 * time.Second, true
	case "300":
		return 300 * time.Second, true
	default:
		writeAPIError(w, http.StatusBadRequest, codeValidation, "window must be 60 or 300")
		return 0, false
	}
}

// rangeQuery parses and validates the from/to local-day parameters. Defaults
// are today and the six days before it.
func (s *Server) rangeQuery(w http.ResponseWriter, r *http.Request) (from, to string, ok bool) {
	q := r.URL.Query()
	to = s.localToday().Format(dateLayout)
	if v := q.Get("to"); v != "" {
		if _, err := time.Parse(dateLayout, v); err != nil {
			writeAPIError(w, http.StatusBadRequest, codeValidation, "to must be a YYYY-MM-DD date")
			return "", "", false
		}
		to = v
	}
	toDay, _ := time.Parse(dateLayout, to)
	from = toDay.AddDate(0, 0, -(defaultRangeDays - 1)).Format(dateLayout)
	if v := q.Get("from"); v != "" {
		if _, err := time.Parse(dateLayout, v); err != nil {
			writeAPIError(w, http.StatusBadRequest, codeValidation, "from must be a YYYY-MM-DD date")
			return "", "", false
		}
		from = v
	}
	if from > to {
		writeAPIError(w, http.StatusBadRequest, codeValidation, "to must not be before from")
		return "", "", false
	}
	return from, to, true
}

// daySpan counts the inclusive days of a validated from/to pair.
func daySpan(from, to string) int {
	fromDay, err := time.Parse(dateLayout, from)
	if err != nil {
		return 0
	}
	toDay, err := time.Parse(dateLayout, to)
	if err != nil {
		return 0
	}
	return int(math.Round(toDay.Sub(fromDay).Hours()/24)) + 1
}

// partialRange reports whether from reaches before the retention window, i.e.
// some aggregates have no matching request detail rows.
func (s *Server) partialRange(from string) bool {
	cutoff := s.localToday().AddDate(0, 0, -s.retentionDays())
	return from < cutoff.Format(dateLayout)
}

// localToday returns the start of the current day in the configured zone.
func (s *Server) localToday() time.Time {
	loc := s.location()
	now := s.now().In(loc)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
}

// location returns the configured timezone, defaulting to UTC.
func (s *Server) location() *time.Location {
	if snap := s.repo.Snapshot(); snap != nil && snap.Location != nil {
		return snap.Location
	}
	return time.UTC
}

// retentionDays returns the configured detail retention, defaulting to zero
// when settings are unavailable.
func (s *Server) retentionDays() int {
	if snap := s.repo.Snapshot(); snap != nil {
		return snap.RetentionDays
	}
	return 0
}

// writeInternal answers an unexpected failure without leaking its details to
// the client, while logging the sanitized error for operators.
func writeInternal(w http.ResponseWriter, err error) {
	slog.Error("api: internal error", "error", sanitizeLogError(err))
	writeAPIError(w, http.StatusInternalServerError, codeInternal, "internal server error")
}

// sanitizeLogError strips query strings from an error message so a URL logged
// at error level cannot carry credentials into the log.
func sanitizeLogError(err error) string {
	if err == nil {
		return "unknown"
	}
	msg := err.Error()
	if i := strings.IndexByte(msg, '?'); i >= 0 {
		msg = msg[:i]
	}
	return msg
}
