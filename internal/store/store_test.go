package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/kiry163/llmmeter/internal/usage"
)

func rec(day string, tokens int64, cost int64, status string) Record {
	return Record{
		StartedAt: time.Now().UTC(), CompletedAt: time.Now().UTC(),
		LocalDay: day, Path: "/v1/chat/completions", Model: "deepseek-chat",
		Status: 200, Usage: usage.Usage{Prompt: tokens}, CostNanoUSD: cost,
		Priced: true, UsageStatus: status,
	}
}

func TestStoreWritesAndAggregates(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	s.Enqueue(rec("2026-10-28", 100, 1000, "ok"))
	s.Enqueue(rec("2026-10-28", 200, 2000, "no_usage"))
	s.Enqueue(rec("2026-10-29", 300, 3000, "ok"))
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}

	sum, err := s.Summary("2026-10-28", "2026-10-29")
	if err != nil {
		t.Fatal(err)
	}
	if sum.Requests != 3 || sum.PromptTokens != 600 || sum.CostNanoUSD != 6000 || sum.Unmetered != 1 {
		t.Fatalf("summary = %+v", sum)
	}

	daily, err := s.Daily("2026-10-28", "2026-10-29")
	if err != nil || len(daily) != 2 {
		t.Fatalf("daily = %+v err=%v", daily, err)
	}
	if daily[0].Day != "2026-10-28" || daily[0].Requests != 2 {
		t.Fatalf("first day = %+v", daily[0])
	}

	models, err := s.ByModel("2026-10-28", "2026-10-29")
	if err != nil || len(models) != 1 || models[0].Requests != 3 {
		t.Fatalf("models = %+v err=%v", models, err)
	}

	rows, err := s.Requests("2026-10-28", "2026-10-29", "", 2, 0)
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows = %+v err=%v", rows, err)
	}
}

func TestCloseFlushesQueue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for range 50 {
		s.Enqueue(rec("2026-10-28", 1, 1, "ok"))
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	sum, err := reopened.Summary("2026-10-28", "2026-10-28")
	if err != nil || sum.Requests != 50 {
		t.Fatalf("summary=%+v err=%v", sum, err)
	}
}

func TestUnmeteredRowsAreNotUnpriced(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "u.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	r1 := rec("2026-10-28", 10, 5, "ok")
	r1.Priced = false
	r2 := rec("2026-10-28", 0, 0, "no_usage")
	r2.Priced = false
	s.Enqueue(r1)
	s.Enqueue(r2)
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	models, err := s.ByModel("2026-10-28", "2026-10-28")
	if err != nil || len(models) != 1 {
		t.Fatalf("models=%+v err=%v", models, err)
	}
	if models[0].Unpriced != 1 {
		t.Fatalf("unpriced = %d, want 1 (only the metered row)", models[0].Unpriced)
	}
}

func TestEnqueueAfterCloseIsIgnored(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s.Enqueue(rec("2026-10-28", 1, 1, "ok"))
}

func TestWriteBatchCountsDroppedRows(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	s.db.Close()
	s.writeBatch([]Record{rec("2026-10-28", 1, 1, "ok")})
	if got := s.dropped.Load(); got != 1 {
		t.Fatalf("dropped = %d, want 1", got)
	}
}
