package store_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/kiry163/llmmeter/internal/store"
	"github.com/kiry163/llmmeter/internal/usage"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestRecordsAndMinutesPersist(t *testing.T) {
	st := newStore(t)
	base := time.Date(2026, 10, 29, 4, 0, 0, 0, time.UTC)
	st.EnqueueRecord(store.Record{
		StartedAt: base, CompletedAt: base.Add(time.Second), LocalDay: "2026-10-29",
		MinuteOfDay: 720, Path: "/v1/chat/completions", PathClass: "chat",
		Model: "m", Status: 200, LatencyMS: 1000,
		ReqBodyBytes: 10, RespBodyBytes: 20, UsageStatus: usage.StatusOK, Priced: true,
	})
	st.EnqueueMinute(store.MinuteDelta{
		LocalDay: "2026-10-29", MinuteOfDay: 720, PathClass: "chat", Model: "m",
		Requests: 1, ReqBytes: 10, RespBytes: 20, LatencySumMS: 1000, LatencyMaxMS: 1000,
	})
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := st.DB().QueryRow("SELECT COUNT(*) FROM requests").Scan(&n); err != nil || n != 1 {
		t.Fatalf("requests=%d err=%v", n, err)
	}
	if err := st.DB().QueryRow("SELECT requests FROM traffic_minutes WHERE local_day='2026-10-29' AND minute_of_day=720").Scan(&n); err != nil || n != 1 {
		t.Fatalf("minutes=%d err=%v", n, err)
	}
	// 同一聚合增量重复写入必须累加而不是覆盖
	st.EnqueueMinute(store.MinuteDelta{LocalDay: "2026-10-29", MinuteOfDay: 720, PathClass: "chat", Model: "m", Requests: 2})
	st.Flush()
	if err := st.DB().QueryRow("SELECT requests FROM traffic_minutes WHERE local_day='2026-10-29' AND minute_of_day=720").Scan(&n); err != nil || n != 3 {
		t.Fatalf("after upsert requests=%d err=%v", n, err)
	}
}

func TestPruneRemovesOldRequests(t *testing.T) {
	st := newStore(t)
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	st.EnqueueRecord(store.Record{StartedAt: old, CompletedAt: old, LocalDay: "2026-01-01", UsageStatus: usage.StatusOK, Priced: true})
	st.Flush()
	st.EnqueuePrune(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	st.Flush()
	var n int
	st.DB().QueryRow("SELECT COUNT(*) FROM requests").Scan(&n)
	if n != 0 {
		t.Fatalf("left %d rows", n)
	}
}

func TestProbeWritable(t *testing.T) {
	st := newStore(t)
	if err := st.ProbeWritable(context.Background()); err != nil {
		t.Fatalf("probe = %v", err)
	}
	if err := st.DB().Close(); err != nil {
		t.Fatal(err)
	}
	if err := st.ProbeWritable(context.Background()); err == nil {
		t.Fatal("probe on a closed database succeeded")
	}
}

func TestDatabaseFilesAre0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod permissions are not meaningful on Windows")
	}
	path := filepath.Join(t.TempDir(), "perm.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	check := func(name string, optional bool) {
		t.Helper()
		info, err := os.Stat(name)
		if err != nil {
			if optional && os.IsNotExist(err) {
				return
			}
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("%s mode = %o, want 0600", name, got)
		}
	}
	check(path, false)

	// The first write creates the WAL, which must not inherit umask defaults.
	base := time.Date(2026, 10, 29, 4, 0, 0, 0, time.UTC)
	st.EnqueueRecord(store.Record{
		StartedAt: base, CompletedAt: base.Add(time.Second), LocalDay: "2026-10-29",
		MinuteOfDay: 720, PathClass: "chat", Model: "m", Status: 200,
		UsageStatus: usage.StatusOK,
	})
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	check(path, false)
	check(path+"-wal", true)

	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	// Closing checkpoints and may remove the sidecars; the database file must
	// still be 0600.
	check(path, false)
}
