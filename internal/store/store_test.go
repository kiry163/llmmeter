package store_test

import (
	"path/filepath"
	"testing"

	"github.com/kiry163/llmmeter/internal/store"
)

func TestOpenDBCreatesSchemaV1(t *testing.T) {
	db, err := store.OpenDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var v int
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != 1 {
		t.Fatalf("user_version = %d, want 1", v)
	}
	for _, table := range []string{"settings", "models", "price_windows", "requests", "traffic_minutes"} {
		var name string
		err := db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", table,
		).Scan(&name)
		if err != nil {
			t.Fatalf("table %s missing: %v", table, err)
		}
	}
}
