package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// OpenDB opens (creating if needed) the SQLite database at path, checks the
// schema version and applies schema v2 to an empty database, recording
// PRAGMA user_version = UserVersion. There is no migration path: any existing
// database whose user_version is not UserVersion is refused.
func OpenDB(path string) (*sql.DB, error) {
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)" +
		"&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)
	if err := ensureSchema(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// ensureSchema inspects PRAGMA user_version before touching any table. Only
// two states are accepted: an empty database (version 0, no tables), which is
// created as schema v2, and a database already at UserVersion. Everything
// else — legacy v1, unknown versions, or an unversioned database that already
// holds tables — is refused instead of migrated or overwritten.
func ensureSchema(db *sql.DB) error {
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}
	if version == UserVersion {
		return nil
	}
	empty, err := schemaEmpty(db)
	if err != nil {
		return fmt.Errorf("inspect schema: %w", err)
	}
	if version != 0 || !empty {
		return incompatibleSchema(version)
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin schema transaction: %w", err)
	}
	if _, err := tx.Exec(Schema); err != nil {
		tx.Rollback()
		return fmt.Errorf("apply schema: %w", err)
	}
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", UserVersion)); err != nil {
		tx.Rollback()
		return fmt.Errorf("set user_version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit schema: %w", err)
	}
	return nil
}

// incompatibleSchema is the single failure shape for any database that cannot
// be used as-is. It matches the message documented in spec 6.2.
func incompatibleSchema(version int) error {
	return fmt.Errorf(
		"incompatible database schema (found %d, need %d): start with a new database path or remove this file",
		version, UserVersion)
}

// schemaEmpty reports whether the main schema contains no user tables.
func schemaEmpty(db *sql.DB) (bool, error) {
	var n int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`).Scan(&n)
	return n == 0, err
}

// tableExists reports whether name is a table in the main schema.
func tableExists(db *sql.DB, name string) (bool, error) {
	var n int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n)
	return n > 0, err
}
