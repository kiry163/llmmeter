// Package settings persists the llmmeter runtime configuration and price
// models in SQLite and exposes them as an atomically swapped, immutable
// Snapshot. Gateway, auth and API code read Snapshot once per request rather
// than querying the database.
package settings

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/kiry163/llmmeter/internal/pricing"
)

// Settings keys in the settings table.
const (
	keyUpstream          = "upstream"
	keyTimezone          = "timezone"
	keyRetentionDays     = "retentionDays"
	keyGatewayKey        = "gatewayKey"
	keyAdminPasswordHash = "adminPasswordHash"
	keySessionSecret     = "sessionSecret"
)

const (
	defaultTimezone      = "Asia/Shanghai"
	defaultRetentionDays = 90

	minRetentionDays = 7
	maxRetentionDays = 3650
	minPasswordLen   = 8
)

// Errors returned by Repo methods. Validation failures return
// *ValidationError instead.
var (
	ErrNotFound     = errors.New("settings: not found")
	ErrConflict     = errors.New("settings: name already exists")
	ErrAlreadySetup = errors.New("settings: setup already completed")
)

// Upstream is the configured upstream API endpoint.
type Upstream struct {
	BaseURL string
	APIKey  string
}

// Snapshot is an immutable view of the settings and price table.
type Snapshot struct {
	SetupDone         bool
	Upstream          Upstream
	Location          *time.Location
	RetentionDays     int
	GatewayKey        string
	AdminPasswordHash string
	SessionSecret     []byte
	Models            []pricing.Model
	Table             *pricing.Table
}

// SetupInput is the first-run configuration.
type SetupInput struct {
	Upstream      Upstream
	Timezone      string
	AdminPassword string
}

// Patch is a partial update of the non-password settings. Password changes
// go through SetPassword or ApplyPatch.
type Patch struct {
	BaseURL       *string
	APIKey        *string
	Timezone      *string
	RetentionDays *int
}

// PasswordChange is one admin password modification: set New, or Clear the
// stored hash. Current must match when a password is already configured.
type PasswordChange struct {
	Current string
	New     string
	Clear   bool
}

// ValidationError reports per-field validation failures with camelCase keys.
type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string {
	keys := make([]string, 0, len(e.Fields))
	for k := range e.Fields {
		keys = append(keys, k)
	}
	// Deterministic message regardless of map iteration order.
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+": "+e.Fields[k])
	}
	return "invalid settings: " + strings.Join(parts, "; ")
}

// Repo owns the settings rows and the current Snapshot.
type Repo struct {
	db   *sql.DB
	snap atomic.Pointer[Snapshot]
}

// Open binds the repository to db and loads the initial snapshot.
func Open(db *sql.DB) (*Repo, error) {
	r := &Repo{db: db}
	if err := r.Refresh(); err != nil {
		return nil, err
	}
	return r, nil
}

// Snapshot returns the current immutable snapshot. Never nil after Open.
func (r *Repo) Snapshot() *Snapshot {
	return r.snap.Load()
}

type upstreamJSON struct {
	BaseURL string `json:"baseUrl"`
	APIKey  string `json:"apiKey"`
}

// Refresh rebuilds the snapshot from the database and swaps it in. A failed
// rebuild leaves the previous snapshot untouched.
func (r *Repo) Refresh() error {
	ctx := context.Background()

	up := Upstream{}
	tz := defaultTimezone
	retention := defaultRetentionDays
	gatewayKey := ""
	passwordHash := ""
	var secret []byte

	rows, err := r.db.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return fmt.Errorf("settings: read settings: %w", err)
	}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			rows.Close()
			return fmt.Errorf("settings: scan setting: %w", err)
		}
		switch key {
		case keyUpstream:
			var u upstreamJSON
			if err := json.Unmarshal([]byte(value), &u); err != nil {
				rows.Close()
				return fmt.Errorf("settings: decode upstream: %w", err)
			}
			up.BaseURL = u.BaseURL
			up.APIKey = u.APIKey
		case keyTimezone:
			tz = value
		case keyRetentionDays:
			n, err := strconv.Atoi(value)
			if err != nil {
				rows.Close()
				return fmt.Errorf("settings: decode retentionDays: %w", err)
			}
			retention = n
		case keyGatewayKey:
			gatewayKey = value
		case keyAdminPasswordHash:
			passwordHash = value
		case keySessionSecret:
			b, err := base64.StdEncoding.DecodeString(value)
			if err != nil {
				rows.Close()
				return fmt.Errorf("settings: decode sessionSecret: %w", err)
			}
			secret = b
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("settings: read settings: %w", err)
	}
	rows.Close()

	loc, err := time.LoadLocation(tz)
	if err != nil {
		return fmt.Errorf("settings: load timezone %q: %w", tz, err)
	}
	models, err := loadModels(ctx, r.db)
	if err != nil {
		return err
	}

	snap := &Snapshot{
		SetupDone:         gatewayKey != "",
		Upstream:          up,
		Location:          loc,
		RetentionDays:     retention,
		GatewayKey:        gatewayKey,
		AdminPasswordHash: passwordHash,
		SessionSecret:     secret,
		Models:            models,
		Table:             pricing.NewTable(models),
	}
	r.snap.Store(snap)
	return nil
}

// Setup performs first-run initialization. It may be called only once.
func (r *Repo) Setup(ctx context.Context, in SetupInput) (string, error) {
	if snap := r.Snapshot(); snap != nil && snap.SetupDone {
		return "", ErrAlreadySetup
	}

	fields := make(map[string]string)
	baseURL, ok := checkBaseURL(in.Upstream.BaseURL)
	if !ok {
		fields["baseUrl"] = "must be an http or https URL with a host"
	}
	if in.Upstream.APIKey == "" {
		fields["apiKey"] = "required"
	}
	tz := strings.TrimSpace(in.Timezone)
	if tz == "" {
		tz = defaultTimezone
	}
	if _, err := time.LoadLocation(tz); err != nil {
		fields["timezone"] = "unknown timezone"
	}
	if in.AdminPassword != "" && len(in.AdminPassword) < minPasswordLen {
		fields["adminPassword"] = fmt.Sprintf("must be at least %d characters", minPasswordLen)
	}
	if len(fields) > 0 {
		return "", &ValidationError{Fields: fields}
	}

	passwordHash := ""
	if in.AdminPassword != "" {
		h, err := HashPassword(in.AdminPassword)
		if err != nil {
			return "", err
		}
		passwordHash = h
	}
	gatewayKey := NewGatewayKey()
	secret, err := RandomSecret()
	if err != nil {
		return "", err
	}
	upJSON, err := json.Marshal(upstreamJSON{
		BaseURL: baseURL,
		APIKey:  in.Upstream.APIKey,
	})
	if err != nil {
		return "", fmt.Errorf("settings: encode upstream: %w", err)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var existing int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM settings WHERE key = ?`, keyGatewayKey).Scan(&existing); err != nil {
		return "", err
	}
	if existing > 0 {
		return "", ErrAlreadySetup
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	set := func(key, value string) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)`, key, value, now)
		return err
	}
	steps := []struct{ key, value string }{
		{keyUpstream, string(upJSON)},
		{keyTimezone, tz},
		{keyRetentionDays, strconv.Itoa(defaultRetentionDays)},
		{keyGatewayKey, gatewayKey},
		{keySessionSecret, base64.StdEncoding.EncodeToString(secret)},
	}
	for _, s := range steps {
		if err := set(s.key, s.value); err != nil {
			return "", fmt.Errorf("settings: write %s: %w", s.key, err)
		}
	}
	if passwordHash != "" {
		if err := set(keyAdminPasswordHash, passwordHash); err != nil {
			return "", fmt.Errorf("settings: write password hash: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	if err := r.Refresh(); err != nil {
		return "", err
	}
	return gatewayKey, nil
}

// Update applies the non-nil fields of p in one transaction, then refreshes
// the snapshot.
func (r *Repo) Update(ctx context.Context, p Patch) error {
	return r.ApplyPatch(ctx, p, nil)
}

// SetPassword verifies current (when a password is set) and stores a hash of
// next.
func (r *Repo) SetPassword(ctx context.Context, current, next string) error {
	return r.ApplyPatch(ctx, Patch{}, &PasswordChange{Current: current, New: next})
}

// ApplyPatch validates the whole patch, including the password change, before
// writing anything, then commits the settings and the password hash in one
// transaction. On any validation or database error nothing changes.
func (r *Repo) ApplyPatch(ctx context.Context, p Patch, pw *PasswordChange) error {
	snap := r.Snapshot()
	fields := make(map[string]string)
	updates := make(map[string]string)

	up := Upstream{}
	if snap != nil {
		up = snap.Upstream
	}
	upstreamChanged := false

	if p.BaseURL != nil {
		v, ok := checkBaseURL(*p.BaseURL)
		if !ok {
			fields["baseUrl"] = "must be an http or https URL with a host"
		} else {
			up.BaseURL = v
			upstreamChanged = true
		}
	}
	if p.APIKey != nil && *p.APIKey != "" {
		// An empty API key means "leave unchanged".
		up.APIKey = *p.APIKey
		upstreamChanged = true
	}
	if p.Timezone != nil {
		tz := strings.TrimSpace(*p.Timezone)
		if _, err := time.LoadLocation(tz); err != nil {
			fields["timezone"] = "unknown timezone"
		} else {
			updates[keyTimezone] = tz
		}
	}
	if p.RetentionDays != nil {
		if *p.RetentionDays < minRetentionDays || *p.RetentionDays > maxRetentionDays {
			fields["retentionDays"] = fmt.Sprintf("must be between %d and %d", minRetentionDays, maxRetentionDays)
		} else {
			updates[keyRetentionDays] = strconv.Itoa(*p.RetentionDays)
		}
	}
	if pw != nil {
		switch {
		case pw.Clear && pw.New != "":
			fields["newPassword"] = "cannot be set together with clearPassword"
		case !pw.Clear && len(pw.New) < minPasswordLen:
			fields["newPassword"] = fmt.Sprintf("must be at least %d characters", minPasswordLen)
		}
		if snap != nil && snap.AdminPasswordHash != "" && !VerifyPassword(snap.AdminPasswordHash, pw.Current) {
			fields["currentPassword"] = "incorrect password"
		}
	}

	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}

	if upstreamChanged {
		b, err := json.Marshal(upstreamJSON{
			BaseURL: up.BaseURL,
			APIKey:  up.APIKey,
		})
		if err != nil {
			return fmt.Errorf("settings: encode upstream: %w", err)
		}
		updates[keyUpstream] = string(b)
	}
	if len(updates) == 0 && pw == nil {
		return nil
	}

	var passwordHash string
	if pw != nil && !pw.Clear {
		hash, err := HashPassword(pw.New)
		if err != nil {
			return err
		}
		passwordHash = hash
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := writeSettingsTx(ctx, tx, updates); err != nil {
		return err
	}
	if pw != nil {
		if pw.Clear {
			if _, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, keyAdminPasswordHash); err != nil {
				return fmt.Errorf("settings: clear password: %w", err)
			}
		} else if err := writeSettingTx(ctx, tx, keyAdminPasswordHash, passwordHash); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return r.Refresh()
}

// RotateGatewayKey generates, stores and returns a fresh gateway key.
func (r *Repo) RotateGatewayKey(ctx context.Context) (string, error) {
	key := NewGatewayKey()
	if err := r.putSettings(ctx, map[string]string{keyGatewayKey: key}); err != nil {
		return "", err
	}
	if err := r.Refresh(); err != nil {
		return "", err
	}
	return key, nil
}

// putSettings upserts all key/value pairs in one transaction.
func (r *Repo) putSettings(ctx context.Context, values map[string]string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := writeSettingsTx(ctx, tx, values); err != nil {
		return err
	}
	return tx.Commit()
}

// writeSettingsTx upserts every key/value pair inside tx.
func writeSettingsTx(ctx context.Context, tx *sql.Tx, values map[string]string) error {
	for k, v := range values {
		if err := writeSettingTx(ctx, tx, k, v); err != nil {
			return err
		}
	}
	return nil
}

// writeSettingTx upserts one settings row inside tx.
func writeSettingTx(ctx context.Context, tx *sql.Tx, key, value string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, now); err != nil {
		return fmt.Errorf("settings: write %s: %w", key, err)
	}
	return nil
}

// checkBaseURL validates and normalizes the upstream base URL.
func checkBaseURL(raw string) (string, bool) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", false
	}
	u, err := url.Parse(v)
	if err != nil || u.Host == "" {
		return "", false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", false
	}
	return v, true
}
