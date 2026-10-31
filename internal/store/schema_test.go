package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// TestOpenDBCreatesV2 checks the freshly created database matches schema v2:
// version marker, required tables, upstream/key snapshot columns, indexes and
// the traffic_minutes primary key.
func TestOpenDBCreatesV2(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.db")
	db, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != UserVersion {
		t.Fatalf("user_version = %d, want %d", version, UserVersion)
	}

	for _, name := range []string{"requests", "traffic_minutes"} {
		ok, err := tableExists(db, name)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatalf("table %s must exist", name)
		}
	}
	for _, name := range []string{"settings", "models", "price_windows"} {
		ok, err := tableExists(db, name)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			t.Fatalf("table %s must not exist in schema v2", name)
		}
	}

	columns := tableColumns(t, db, "requests")
	for _, want := range []string{"upstream_id", "upstream_name", "key_id", "key_name"} {
		if !columns[want] {
			t.Fatalf("requests is missing column %s", want)
		}
	}

	wantIndexes := []string{
		"idx_requests_started",
		"idx_requests_upstream_started",
		"idx_requests_upstream_key_started",
		"idx_requests_model_started",
		"idx_requests_unpriced",
	}
	for _, want := range wantIndexes {
		if !indexExists(t, db, want) {
			t.Fatalf("index %s is missing", want)
		}
	}

	wantPK := []string{"local_day", "minute_of_day", "path_class", "upstream_id", "model"}
	gotPK := primaryKey(t, db, "traffic_minutes")
	if len(gotPK) != len(wantPK) {
		t.Fatalf("traffic_minutes primary key = %v, want %v", gotPK, wantPK)
	}
	for i := range wantPK {
		if gotPK[i] != wantPK[i] {
			t.Fatalf("traffic_minutes primary key = %v, want %v", gotPK, wantPK)
		}
	}

	// Reopening an existing v2 database must succeed without touching it.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := OpenDB(path)
	if err != nil {
		t.Fatalf("reopen v2 database: %v", err)
	}
	again.Close()
}

func TestOpenDBCreatesV2WithoutSettingsTables(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "new.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, name := range []string{"settings", "models", "price_windows"} {
		if ok, _ := tableExists(st.DB(), name); ok {
			t.Fatalf("table %s must not exist", name)
		}
	}
}

func TestOpenDBRejectsV1(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "v1.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE requests (id INTEGER); PRAGMA user_version = 1;`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := OpenDB(path); err == nil || !strings.Contains(err.Error(), "incompatible database schema") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenDBRejectsNewerVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "future.db")
	db, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version = 3"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	_, err = OpenDB(path)
	if err == nil || !strings.Contains(err.Error(), "incompatible database schema (found 3, need 2)") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenDBRejectsUnversionedDatabaseWithTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE requests (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	_, err = OpenDB(path)
	if err == nil || !strings.Contains(err.Error(), "incompatible database schema (found 0, need 2)") {
		t.Fatalf("err = %v", err)
	}
}

// tableColumns returns the column names of table.
func tableColumns(t *testing.T, db *sql.DB, table string) map[string]bool {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns := make(map[string]bool)
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return columns
}

// primaryKey returns the primary-key columns of table in key order.
func primaryKey(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	keyed := make(map[int]string)
	max := 0
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		if pk > 0 {
			keyed[pk] = name
			if pk > max {
				max = pk
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	key := make([]string, 0, max)
	for i := 1; i <= max; i++ {
		key = append(key, keyed[i])
	}
	return key
}

func indexExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}
