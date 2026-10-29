package settings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"modernc.org/sqlite"

	"github.com/kiry163/llmmeter/internal/pricing"
)

const maxWindows = 32

// ModelInput is a new price model. Rates are USD per million tokens.
type ModelInput struct {
	Name       string
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
	Reasoning  *float64
}

// ModelPatch updates a model. nil fields are unchanged. For Reasoning the
// outer pointer controls whether the rate changes: a non-nil outer pointer
// with a nil inner pointer clears the rate so it falls back to output.
type ModelPatch struct {
	Name       *string
	Input      *float64
	Output     *float64
	CacheRead  *float64
	CacheWrite *float64
	Reasoning  **float64
}

// sqliteConstraint is SQLITE_CONSTRAINT; extended codes keep the same low byte.
const sqliteConstraint = 19

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// Models returns the price models with their windows ordered by sort_order.
func (r *Repo) Models(ctx context.Context) ([]pricing.Model, error) {
	return loadModels(ctx, r.db)
}

// loadModels reads all models and windows. The result is never nil.
func loadModels(ctx context.Context, q queryer) ([]pricing.Model, error) {
	models := make([]pricing.Model, 0)
	index := make(map[int64]int)

	rows, err := q.QueryContext(ctx,
		`SELECT id, name, input_per_mtok, output_per_mtok, cache_read_per_mtok,
		        cache_write_per_mtok, reasoning_per_mtok
		   FROM models ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("settings: read models: %w", err)
	}
	for rows.Next() {
		var m pricing.Model
		var reasoning sql.NullFloat64
		if err := rows.Scan(&m.ID, &m.Name, &m.Input, &m.Output, &m.CacheRead,
			&m.CacheWrite, &reasoning); err != nil {
			rows.Close()
			return nil, fmt.Errorf("settings: scan model: %w", err)
		}
		if reasoning.Valid {
			v := reasoning.Float64
			m.Reasoning = &v
		}
		index[m.ID] = len(models)
		models = append(models, m)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("settings: read models: %w", err)
	}
	rows.Close()

	wrows, err := q.QueryContext(ctx,
		`SELECT id, model_id, label, days_mask, start_minute, end_minute,
		        input_per_mtok, output_per_mtok, cache_read_per_mtok,
		        cache_write_per_mtok, reasoning_per_mtok
		   FROM price_windows ORDER BY model_id, sort_order, id`)
	if err != nil {
		return nil, fmt.Errorf("settings: read price windows: %w", err)
	}
	defer wrows.Close()
	for wrows.Next() {
		var modelID int64
		var w pricing.Window
		var in, out, cacheRead, cacheWrite, reasoning sql.NullFloat64
		if err := wrows.Scan(&w.ID, &modelID, &w.Label, &w.DaysMask, &w.StartMinute,
			&w.EndMinute, &in, &out, &cacheRead, &cacheWrite, &reasoning); err != nil {
			return nil, fmt.Errorf("settings: scan price window: %w", err)
		}
		w.Input = nullableFloat(in)
		w.Output = nullableFloat(out)
		w.CacheRead = nullableFloat(cacheRead)
		w.CacheWrite = nullableFloat(cacheWrite)
		w.Reasoning = nullableFloat(reasoning)
		if i, ok := index[modelID]; ok {
			models[i].Windows = append(models[i].Windows, w)
		}
	}
	if err := wrows.Err(); err != nil {
		return nil, fmt.Errorf("settings: read price windows: %w", err)
	}
	return models, nil
}

func nullableFloat(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	f := v.Float64
	return &f
}

func nullableValue(f *float64) any {
	if f == nil {
		return nil
	}
	return *f
}

// CreateModel validates and inserts a price model.
func (r *Repo) CreateModel(ctx context.Context, in ModelInput) (pricing.Model, error) {
	name := strings.TrimSpace(in.Name)
	fields := make(map[string]string)
	if name == "" {
		fields["name"] = "required"
	}
	validateRate(fields, "inputPerMTok", in.Input)
	validateRate(fields, "outputPerMTok", in.Output)
	validateRate(fields, "cacheReadPerMTok", in.CacheRead)
	validateRate(fields, "cacheWritePerMTok", in.CacheWrite)
	validateRatePtr(fields, "reasoningPerMTok", in.Reasoning)
	if len(fields) > 0 {
		return pricing.Model{}, &ValidationError{Fields: fields}
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return pricing.Model{}, err
	}
	defer tx.Rollback()

	var existing int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM models WHERE name = ?`, name).Scan(&existing); err != nil {
		return pricing.Model{}, err
	}
	if existing > 0 {
		return pricing.Model{}, ErrConflict
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := tx.ExecContext(ctx,
		`INSERT INTO models (name, input_per_mtok, output_per_mtok, cache_read_per_mtok,
		                     cache_write_per_mtok, reasoning_per_mtok, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		name, in.Input, in.Output, in.CacheRead, in.CacheWrite,
		nullableValue(in.Reasoning), now, now)
	if isUniqueViolation(err) {
		return pricing.Model{}, ErrConflict
	}
	if err != nil {
		return pricing.Model{}, fmt.Errorf("settings: create model: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return pricing.Model{}, err
	}
	if err := tx.Commit(); err != nil {
		return pricing.Model{}, err
	}
	if err := r.Refresh(); err != nil {
		return pricing.Model{}, err
	}
	return pricing.Model{
		ID: id, Name: name, Input: in.Input, Output: in.Output,
		CacheRead: in.CacheRead, CacheWrite: in.CacheWrite, Reasoning: in.Reasoning,
	}, nil
}

// UpdateModel applies the non-nil fields of p. A set outer Reasoning pointer
// with a nil inner pointer clears the reasoning rate back to the output rate.
func (r *Repo) UpdateModel(ctx context.Context, id int64, p ModelPatch) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var cur pricing.Model
	var reasoning sql.NullFloat64
	err = tx.QueryRowContext(ctx,
		`SELECT name, input_per_mtok, output_per_mtok, cache_read_per_mtok,
		        cache_write_per_mtok, reasoning_per_mtok
		   FROM models WHERE id = ?`, id).
		Scan(&cur.Name, &cur.Input, &cur.Output, &cur.CacheRead, &cur.CacheWrite, &reasoning)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("settings: read model: %w", err)
	}
	cur.Reasoning = nullableFloat(reasoning)

	fields := make(map[string]string)
	name := cur.Name
	if p.Name != nil {
		name = strings.TrimSpace(*p.Name)
		if name == "" {
			fields["name"] = "required"
		} else {
			var clash int
			if err := tx.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM models WHERE name = ? AND id <> ?`, name, id).Scan(&clash); err != nil {
				return err
			}
			if clash > 0 {
				return ErrConflict
			}
		}
	}
	if p.Input != nil {
		cur.Input = *p.Input
		validateRate(fields, "inputPerMTok", cur.Input)
	}
	if p.Output != nil {
		cur.Output = *p.Output
		validateRate(fields, "outputPerMTok", cur.Output)
	}
	if p.CacheRead != nil {
		cur.CacheRead = *p.CacheRead
		validateRate(fields, "cacheReadPerMTok", cur.CacheRead)
	}
	if p.CacheWrite != nil {
		cur.CacheWrite = *p.CacheWrite
		validateRate(fields, "cacheWritePerMTok", cur.CacheWrite)
	}
	if p.Reasoning != nil {
		if *p.Reasoning == nil {
			cur.Reasoning = nil
		} else {
			v := **p.Reasoning
			cur.Reasoning = &v
			validateRate(fields, "reasoningPerMTok", v)
		}
	}
	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx,
		`UPDATE models
		    SET name = ?, input_per_mtok = ?, output_per_mtok = ?, cache_read_per_mtok = ?,
		        cache_write_per_mtok = ?, reasoning_per_mtok = ?, updated_at = ?
		  WHERE id = ?`,
		name, cur.Input, cur.Output, cur.CacheRead, cur.CacheWrite,
		nullableValue(cur.Reasoning), now, id); err != nil {
		if isUniqueViolation(err) {
			return ErrConflict
		}
		return fmt.Errorf("settings: update model: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return r.Refresh()
}

// DeleteModel removes a model and its windows.
func (r *Repo) DeleteModel(ctx context.Context, id int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `DELETE FROM models WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("settings: delete model: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM price_windows WHERE model_id = ?`, id); err != nil {
		return fmt.Errorf("settings: delete price windows: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return r.Refresh()
}

// ReplaceWindows atomically replaces every window of modelID. The slice index
// becomes sort_order.
func (r *Repo) ReplaceWindows(ctx context.Context, modelID int64, wins []pricing.Window) error {
	if err := validateWindows(wins); err != nil {
		return err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM models WHERE id = ?`, modelID).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM price_windows WHERE model_id = ?`, modelID); err != nil {
		return fmt.Errorf("settings: delete price windows: %w", err)
	}
	for i, w := range wins {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO price_windows (model_id, label, days_mask, start_minute, end_minute,
			                            input_per_mtok, output_per_mtok, cache_read_per_mtok,
			                            cache_write_per_mtok, reasoning_per_mtok, sort_order)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			modelID, strings.TrimSpace(w.Label), w.DaysMask, w.StartMinute, w.EndMinute,
			nullableValue(w.Input), nullableValue(w.Output), nullableValue(w.CacheRead),
			nullableValue(w.CacheWrite), nullableValue(w.Reasoning), i); err != nil {
			return fmt.Errorf("settings: insert price window: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return r.Refresh()
}

func validateWindows(wins []pricing.Window) error {
	if len(wins) > maxWindows {
		return &ValidationError{Fields: map[string]string{
			"windows": fmt.Sprintf("at most %d windows are allowed", maxWindows),
		}}
	}
	for i, w := range wins {
		msg := ""
		switch {
		case strings.TrimSpace(w.Label) == "":
			msg = "label is required"
		case w.StartMinute < 0 || w.StartMinute > 1439 || w.EndMinute < 0 || w.EndMinute > 1439:
			msg = "startMinute and endMinute must be between 0 and 1439"
		case w.StartMinute == w.EndMinute:
			msg = "startMinute and endMinute must differ"
		case w.DaysMask < 1 || w.DaysMask > 127:
			msg = "daysMask must be between 1 and 127"
		default:
			if bad := firstBadRate(w); bad != "" {
				msg = bad + " must be a finite number >= 0"
			}
		}
		if msg != "" {
			return &ValidationError{Fields: map[string]string{
				"windows": fmt.Sprintf("window %d: %s", i+1, msg),
			}}
		}
	}
	return nil
}

func firstBadRate(w pricing.Window) string {
	for _, pair := range []struct {
		name string
		v    *float64
	}{
		{"inputPerMTok", w.Input},
		{"outputPerMTok", w.Output},
		{"cacheReadPerMTok", w.CacheRead},
		{"cacheWritePerMTok", w.CacheWrite},
		{"reasoningPerMTok", w.Reasoning},
	} {
		if pair.v != nil && !validRate(*pair.v) {
			return pair.name
		}
	}
	return ""
}

func validRate(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0
}

func validateRate(fields map[string]string, field string, v float64) {
	if !validRate(v) {
		fields[field] = "must be a finite number >= 0"
	}
}

func validateRatePtr(fields map[string]string, field string, v *float64) {
	if v != nil {
		validateRate(fields, field, *v)
	}
}

// isUniqueViolation reports whether err is a SQLite UNIQUE constraint failure.
func isUniqueViolation(err error) bool {
	var se *sqlite.Error
	return errors.As(err, &se) && se.Code()&0xff == sqliteConstraint
}
