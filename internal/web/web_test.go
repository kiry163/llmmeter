package web

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kiry163/llmmeter/internal/store"
	"github.com/kiry163/llmmeter/internal/usage"
)

func TestSummaryAndExport(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().UTC()
	st.Enqueue(store.Record{
		StartedAt: now, CompletedAt: now, LocalDay: "2026-10-28",
		Path: "/v1/chat/completions", Model: "deepseek-chat", Status: 200,
		Usage: usage.Usage{Prompt: 10, Completion: 5}, CostNanoUSD: 42,
		Priced: true, UsageStatus: "ok",
	})
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}

	loc, _ := time.LoadLocation("Asia/Shanghai")
	h := New(st, loc)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/api/summary?from=2026-10-28&to=2026-10-28", nil))
	if rr.Code != 200 {
		t.Fatalf("status = %d", rr.Code)
	}
	var sum store.Summary
	if err := json.Unmarshal(rr.Body.Bytes(), &sum); err != nil {
		t.Fatal(err)
	}
	if sum.Requests != 1 || sum.PromptTokens != 10 {
		t.Fatalf("summary = %+v", sum)
	}

	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/api/export.csv?from=2026-10-28&to=2026-10-28", nil))
	if rr.Code != 200 || rr.Header().Get("Content-Type") != "text/csv; charset=utf-8" {
		t.Fatalf("csv status=%d type=%q", rr.Code, rr.Header().Get("Content-Type"))
	}
	if got := rr.Body.String(); !strings.HasPrefix(got, "started,") {
		t.Fatalf("csv head = %q", got)
	}
}
