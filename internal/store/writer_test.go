package store_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kiry163/llmmeter/internal/store"
	"github.com/kiry163/llmmeter/internal/usage"
)

// waitCount polls until query yields want.
func waitCount(t *testing.T, st *store.Store, query string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var got int
	for time.Now().Before(deadline) {
		if err := st.DB().QueryRow(query).Scan(&got); err == nil && got == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s = %d, want %d", query, got, want)
}

func TestWriterFlushesBatchOnTick(t *testing.T) {
	st := newStore(t)
	base := time.Date(2026, 10, 29, 4, 0, 0, 0, time.UTC)
	for range 5 {
		st.EnqueueRecord(store.Record{
			StartedAt: base, CompletedAt: base, LocalDay: "2026-10-29",
			MinuteOfDay: 720, Path: "/v1/chat/completions", PathClass: "chat",
			UsageStatus: usage.StatusOK,
		})
	}
	// No Flush: the 10ms ticker must drain the pending batch.
	waitCount(t, st, "SELECT COUNT(*) FROM requests", 5)
}

func TestRecordColumnsPersist(t *testing.T) {
	st := newStore(t)
	base := time.Date(2026, 10, 29, 4, 5, 6, 789*int(time.Millisecond), time.UTC)
	ttft := int64(250)
	st.EnqueueRecord(store.Record{
		StartedAt: base, CompletedAt: base.Add(2 * time.Second),
		LocalDay: "2026-10-29", MinuteOfDay: 725,
		Path: "/v1/responses", PathClass: "chat",
		Model: "gpt-x", UpstreamModel: "up-x", Stream: true, Status: 200,
		LatencyMS: 2000, TTFTMS: &ttft,
		ReqBodyBytes: 1, ReqHeaderBytes: 2, RespBodyBytes: 3, RespHeaderBytes: 4,
		Usage: usage.Usage{
			PromptTokens: 10, CompletionTokens: 20, CacheReadTokens: 5,
			CacheWriteTokens: 6, ReasoningTokens: 7, TotalTokens: 48,
		},
		WindowLabel: "peak", RatesJSON: `{"input":1}`, CostNanoUSD: 1234,
		Priced: true, UsageStatus: usage.StatusOK, ClientDisconnected: true,
		Error: "boom",
	})
	st.EnqueueRecord(store.Record{
		StartedAt: base, CompletedAt: base, LocalDay: "2026-10-29",
		MinuteOfDay: 725, Path: "/v1/chat/completions", PathClass: "chat",
		Status: 500, LatencyMS: 3, UsageStatus: usage.StatusError,
	})
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}

	var (
		started, completed, day, path, pathClass, model, upstream string
		minute, status                                            int
		latency, ttftVal                                          int64
		ttftNull                                                  bool
		reqBody, reqHdr, respBody, respHdr                        int64
		prompt, completion, cacheRead, cacheWrite, reasoning      int64
		total                                                     int64
		window, rates, usageStatus, errText                       string
		cost                                                      int64
		stream, priced, disconnected                              int
	)
	err := st.DB().QueryRow(`SELECT started_at, completed_at, local_day, path, path_class,
		model, upstream_model, minute_of_day, status, latency_ms, ttft_ms,
		req_body_bytes, req_header_bytes, resp_body_bytes, resp_header_bytes,
		prompt_tokens, completion_tokens, cache_read_tokens, cache_write_tokens,
		reasoning_tokens, total_tokens, window_label, rates_json, cost_nano_usd,
		stream, priced, usage_status, client_disconnected, error
		FROM requests WHERE upstream_model = 'up-x'`).Scan(
		&started, &completed, &day, &path, &pathClass,
		&model, &upstream, &minute, &status, &latency, &ttftVal,
		&reqBody, &reqHdr, &respBody, &respHdr,
		&prompt, &completion, &cacheRead, &cacheWrite,
		&reasoning, &total, &window, &rates, &cost,
		&stream, &priced, &usageStatus, &disconnected, &errText,
	)
	if err != nil {
		t.Fatal(err)
	}
	if started != "2026-10-29T04:05:06.789Z" || completed != "2026-10-29T04:05:08.789Z" {
		t.Fatalf("timestamps = %q / %q", started, completed)
	}
	if day != "2026-10-29" || minute != 725 || path != "/v1/responses" || pathClass != "chat" ||
		model != "gpt-x" || upstream != "up-x" || status != 200 || latency != 2000 || ttftVal != 250 {
		t.Fatalf("scalar columns wrong: %q %d %q %q %q %q %d %d %d",
			day, minute, path, pathClass, model, upstream, status, latency, ttftVal)
	}
	if reqBody != 1 || reqHdr != 2 || respBody != 3 || respHdr != 4 {
		t.Fatalf("byte columns = %d %d %d %d", reqBody, reqHdr, respBody, respHdr)
	}
	if prompt != 10 || completion != 20 || cacheRead != 5 || cacheWrite != 6 ||
		reasoning != 7 || total != 48 {
		t.Fatalf("usage columns = %d %d %d %d %d %d", prompt, completion, cacheRead, cacheWrite, reasoning, total)
	}
	if window != "peak" || rates != `{"input":1}` || cost != 1234 {
		t.Fatalf("pricing columns = %q %q %d", window, rates, cost)
	}
	if stream != 1 || priced != 1 || disconnected != 1 {
		t.Fatalf("bools = %d %d %d", stream, priced, disconnected)
	}
	if usageStatus != usage.StatusOK || errText != "boom" {
		t.Fatalf("status columns = %q %q", usageStatus, errText)
	}

	// The second record has a nil TTFT, which must be stored as NULL.
	if err := st.DB().QueryRow(`SELECT ttft_ms IS NULL, stream, priced,
		client_disconnected, error FROM requests
		WHERE path = '/v1/chat/completions'`).
		Scan(&ttftNull, &stream, &priced, &disconnected, &errText); err != nil {
		t.Fatal(err)
	}
	if !ttftNull || stream != 0 || priced != 0 || disconnected != 0 || errText != "" {
		t.Fatalf("nil/false defaults = null:%v stream:%d priced:%d disc:%d err:%q",
			ttftNull, stream, priced, disconnected, errText)
	}
}

func TestMinuteUpsertMergesAllColumns(t *testing.T) {
	st := newStore(t)
	st.EnqueueMinute(store.MinuteDelta{
		LocalDay: "2026-10-29", MinuteOfDay: 60, PathClass: "chat", Model: "m",
		Requests: 1, Errors: 1, ReqBytes: 10, RespBytes: 20,
		PromptTokens: 30, CompletionTokens: 40, CacheReadTokens: 5, ReasoningTokens: 6,
		CostNanoUSD: 100, LatencySumMS: 200, LatencyMaxMS: 300,
	})
	st.EnqueueMinute(store.MinuteDelta{
		LocalDay: "2026-10-29", MinuteOfDay: 60, PathClass: "chat", Model: "m",
		Requests: 2, ReqBytes: 1, RespBytes: 2, PromptTokens: 3, CompletionTokens: 4,
		CacheReadTokens: 1, ReasoningTokens: 2, CostNanoUSD: 7,
		LatencySumMS: 10, LatencyMaxMS: 50,
	})
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	var requests, errors, reqBytes, respBytes, prompt, completion, cacheRead, reasoning, cost, sum, max int64
	err := st.DB().QueryRow(`SELECT requests, errors, req_bytes, resp_bytes,
		prompt_tokens, completion_tokens, cache_read_tokens, reasoning_tokens,
		cost_nano_usd, latency_sum_ms, latency_max_ms FROM traffic_minutes
		WHERE local_day = '2026-10-29' AND minute_of_day = 60 AND path_class = 'chat' AND model = 'm'`).
		Scan(&requests, &errors, &reqBytes, &respBytes, &prompt, &completion,
			&cacheRead, &reasoning, &cost, &sum, &max)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 3 || errors != 1 || reqBytes != 11 || respBytes != 22 ||
		prompt != 33 || completion != 44 || cacheRead != 6 || reasoning != 8 ||
		cost != 107 || sum != 210 || max != 300 {
		t.Fatalf("merged row = %d %d %d %d %d %d %d %d %d %d %d",
			requests, errors, reqBytes, respBytes, prompt, completion,
			cacheRead, reasoning, cost, sum, max)
	}
}

func TestPruneKeepsNewerRequests(t *testing.T) {
	st := newStore(t)
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fresh := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	st.EnqueueRecord(store.Record{StartedAt: old, CompletedAt: old, LocalDay: "2026-01-01", UsageStatus: usage.StatusOK})
	st.EnqueueRecord(store.Record{StartedAt: fresh, CompletedAt: fresh, LocalDay: "2026-07-01", UsageStatus: usage.StatusOK})
	st.Flush()
	st.EnqueuePrune(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := st.DB().QueryRow("SELECT COUNT(*) FROM requests").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("rows = %d, want 1", n)
	}
}

func TestConcurrentEnqueue(t *testing.T) {
	st := newStore(t)
	const workers, perWorker = 8, 50
	base := time.Date(2026, 10, 29, 4, 0, 0, 0, time.UTC)
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for range perWorker {
				st.EnqueueRecord(store.Record{
					StartedAt: base, CompletedAt: base, LocalDay: "2026-10-29",
					MinuteOfDay: 720, Path: fmt.Sprintf("/p/%d", w), PathClass: "chat",
					UsageStatus: usage.StatusOK,
				})
				st.EnqueueMinute(store.MinuteDelta{
					LocalDay: "2026-10-29", MinuteOfDay: 720, PathClass: "chat", Model: "m",
					Requests: 1,
				})
			}
		}(w)
	}
	wg.Wait()
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	var recs, minutes int
	if err := st.DB().QueryRow("SELECT COUNT(*) FROM requests").Scan(&recs); err != nil {
		t.Fatal(err)
	}
	if err := st.DB().QueryRow("SELECT requests FROM traffic_minutes WHERE local_day = '2026-10-29' AND minute_of_day = 720 AND path_class = 'chat' AND model = 'm'").Scan(&minutes); err != nil {
		t.Fatal(err)
	}
	if recs != workers*perWorker || minutes != workers*perWorker {
		t.Fatalf("records=%d minutes=%d, want %d", recs, minutes, workers*perWorker)
	}
}

func TestEnqueueAfterCloseIsDropped(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/s.db")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st.EnqueueRecord(store.Record{StartedAt: time.Now()})
	st.EnqueueMinute(store.MinuteDelta{LocalDay: "2026-10-29"})
	st.EnqueuePrune(time.Now())
	if d := st.QueueDepth(); d != 0 {
		t.Fatalf("queue depth after close = %d", d)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

// failPathTrigger makes inserts for a specific path fail, standing in for a
// single corrupt record among valid ones.
func failPathTrigger(t *testing.T, st *store.Store, path string) {
	t.Helper()
	// Trigger bodies cannot use bind parameters, so the path is inlined with
	// SQL string quoting.
	literal := "'" + strings.ReplaceAll(path, "'", "''") + "'"
	_, err := st.DB().Exec(`CREATE TRIGGER fail_bad_row BEFORE INSERT ON requests
		WHEN NEW.path = ` + literal + ` BEGIN SELECT RAISE(ABORT, 'bad row'); END`)
	if err != nil {
		t.Fatal(err)
	}
}

func TestWriterIsolatesFailedRow(t *testing.T) {
	st := newStore(t)
	failPathTrigger(t, st, "/bad")
	base := time.Date(2026, 10, 29, 4, 0, 0, 0, time.UTC)
	for _, path := range []string{"/a", "/bad", "/c"} {
		st.EnqueueRecord(store.Record{
			StartedAt: base, CompletedAt: base, LocalDay: "2026-10-29",
			MinuteOfDay: 720, Path: path, PathClass: "chat",
			UsageStatus: usage.StatusOK,
		})
	}
	if err := st.Flush(); err == nil {
		t.Fatal("Flush = nil, want the failed row reported")
	}
	var total, survived int
	if err := st.DB().QueryRow("SELECT COUNT(*) FROM requests").Scan(&total); err != nil {
		t.Fatal(err)
	}
	if err := st.DB().QueryRow("SELECT COUNT(*) FROM requests WHERE path IN ('/a', '/c')").Scan(&survived); err != nil {
		t.Fatal(err)
	}
	if total != 2 || survived != 2 {
		t.Fatalf("requests=%d surviving=%d, want 2/2", total, survived)
	}
	var bad int
	if err := st.DB().QueryRow("SELECT COUNT(*) FROM requests WHERE path = '/bad'").Scan(&bad); err != nil {
		t.Fatal(err)
	}
	if bad != 0 {
		t.Fatalf("bad row persisted %d times", bad)
	}
}

func TestFlushClearsReportedError(t *testing.T) {
	st := newStore(t)
	failPathTrigger(t, st, "/bad")
	base := time.Date(2026, 10, 29, 4, 0, 0, 0, time.UTC)
	st.EnqueueRecord(store.Record{
		StartedAt: base, CompletedAt: base, LocalDay: "2026-10-29",
		Path: "/bad", PathClass: "chat", UsageStatus: usage.StatusOK,
	})
	if err := st.Flush(); err == nil {
		t.Fatal("first Flush = nil, want the failed row reported")
	}
	if err := st.Flush(); err != nil {
		t.Fatalf("second Flush = %v, want nil after the error was reported", err)
	}
	if _, err := st.DB().Exec("DROP TRIGGER fail_bad_row"); err != nil {
		t.Fatal(err)
	}
	st.EnqueueRecord(store.Record{
		StartedAt: base, CompletedAt: base, LocalDay: "2026-10-29",
		Path: "/ok", PathClass: "chat", UsageStatus: usage.StatusOK,
	})
	if err := st.Flush(); err != nil {
		t.Fatalf("Flush after recovery = %v", err)
	}
	var n int
	if err := st.DB().QueryRow("SELECT COUNT(*) FROM requests").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("requests=%d, want 1", n)
	}
}
