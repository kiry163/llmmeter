package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// OpenDB opens (creating if needed) the SQLite database at path, checks the
// schema version, applies schema v1 for a fresh database and records
// PRAGMA user_version = UserVersion.
func OpenDB(path string) (*sql.DB, error) {
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)" +
		"&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)
	if err := ensureSchema(db, path); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// ensureSchema inspects PRAGMA user_version before touching any table. A
// database written by a newer llmmeter, or a legacy database with llmmeter
// tables but no version marker, is refused instead of overwritten.
func ensureSchema(db *sql.DB, path string) error {
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}
	if version > UserVersion {
		return fmt.Errorf(
			"database %s uses schema version %d, newer than the supported version %d; refusing to start",
			path, version, UserVersion)
	}
	if version == 0 {
		legacy, err := tableExists(db, "requests")
		if err != nil {
			return fmt.Errorf("inspect schema: %w", err)
		}
		if legacy {
			return fmt.Errorf(
				"database %s contains a requests table without a schema version; refusing to overwrite a legacy database",
				path)
		}
	}
	if _, err := db.Exec(Schema); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	if version < UserVersion {
		if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", UserVersion)); err != nil {
			return fmt.Errorf("set user_version: %w", err)
		}
	}
	return nil
}

// tableExists reports whether name is a table in the main schema.
func tableExists(db *sql.DB, name string) (bool, error) {
	var n int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n)
	return n > 0, err
}
