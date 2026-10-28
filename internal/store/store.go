package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/kiry163/llmmeter/internal/usage"
)

const schema = `
CREATE TABLE IF NOT EXISTS requests (
  id INTEGER PRIMARY KEY,
  started_at TEXT NOT NULL,
  completed_at TEXT NOT NULL,
  local_day TEXT NOT NULL,
  path TEXT NOT NULL,
  model TEXT NOT NULL DEFAULT '',
  upstream_model TEXT NOT NULL DEFAULT '',
  stream INTEGER NOT NULL,
  status INTEGER NOT NULL,
  latency_ms INTEGER NOT NULL,
  prompt_tokens INTEGER NOT NULL DEFAULT 0,
  completion_tokens INTEGER NOT NULL DEFAULT 0,
  cache_read_tokens INTEGER NOT NULL DEFAULT 0,
  cache_write_tokens INTEGER NOT NULL DEFAULT 0,
  reasoning_tokens INTEGER NOT NULL DEFAULT 0,
  total_tokens INTEGER NOT NULL DEFAULT 0,
  window_label TEXT NOT NULL DEFAULT '',
  rates_json TEXT NOT NULL DEFAULT '',
  cost_nano_usd INTEGER NOT NULL DEFAULT 0,
  priced INTEGER NOT NULL DEFAULT 1,
  usage_status TEXT NOT NULL,
  error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_requests_day ON requests(local_day);
CREATE INDEX IF NOT EXISTS idx_requests_model ON requests(model);
`

type Record struct {
	StartedAt     time.Time   `json:"started_at"`
	CompletedAt   time.Time   `json:"completed_at"`
	LocalDay      string      `json:"local_day"`
	Path          string      `json:"path"`
	Model         string      `json:"model"`
	UpstreamModel string      `json:"upstream_model"`
	Stream        bool        `json:"stream"`
	Status        int         `json:"status"`
	LatencyMS     int64       `json:"latency_ms"`
	Usage         usage.Usage `json:"usage"`
	WindowLabel   string      `json:"window_label"`
	RatesJSON     string      `json:"-"`
	CostNanoUSD   int64       `json:"cost_nano_usd"`
	Priced        bool        `json:"priced"`
	UsageStatus   string      `json:"usage_status"`
	Error         string      `json:"error"`
}

type Summary struct {
	Requests         int64 `json:"requests"`
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	CacheReadTokens  int64 `json:"cache_read_tokens"`
	CostNanoUSD      int64 `json:"cost_nano_usd"`
	Unmetered        int64 `json:"unmetered"`
}

type Daily struct {
	Day          string `json:"day"`
	Requests     int64  `json:"requests"`
	PromptTokens int64  `json:"prompt_tokens"`
	Completion   int64  `json:"completion_tokens"`
	CostNanoUSD  int64  `json:"cost_nano_usd"`
}

type ModelRow struct {
	Model            string `json:"model"`
	Requests         int64  `json:"requests"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens"`
	ReasoningTokens  int64  `json:"reasoning_tokens"`
	CostNanoUSD      int64  `json:"cost_nano_usd"`
	Unpriced         int64  `json:"unpriced"`
}

type Store struct {
	db    *sql.DB
	ch    chan Record
	flush chan chan struct{}
	wg    sync.WaitGroup
}

func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db, ch: make(chan Record, 1024), flush: make(chan chan struct{})}
	s.wg.Add(1)
	go s.writer()
	return s, nil
}

// Enqueue blocks when the queue is full; blocking is preferable to dropping a
// record or writing concurrently with the batch writer.
func (s *Store) Enqueue(r Record) { s.ch <- r }

// Flush blocks until every record enqueued so far is committed.
func (s *Store) Flush() error {
	ack := make(chan struct{})
	s.flush <- ack
	<-ack
	return nil
}

func (s *Store) writer() {
	defer s.wg.Done()
	batch := make([]Record, 0, 200)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case r, ok := <-s.ch:
			if !ok {
				s.writeBatch(batch)
				return
			}
			batch = append(batch, r)
			if len(batch) >= 200 {
				s.writeBatch(batch)
				batch = batch[:0]
			}
		case <-ticker.C:
			if len(batch) == 0 {
				continue
			}
			s.writeBatch(batch)
			batch = batch[:0]
		case ack := <-s.flush:
			var open bool
			batch, open = s.drain(batch)
			s.writeBatch(batch)
			batch = batch[:0]
			close(ack)
			if !open {
				return
			}
		}
	}
}

// drain moves everything already queued into batch. It reports false when the
// channel was closed while draining.
func (s *Store) drain(batch []Record) ([]Record, bool) {
	for {
		select {
		case r, ok := <-s.ch:
			if !ok {
				return batch, false
			}
			batch = append(batch, r)
		default:
			return batch, true
		}
	}
}

func (s *Store) writeBatch(batch []Record) {
	if len(batch) == 0 {
		return
	}
	tx, err := s.db.Begin()
	if err != nil {
		return
	}
	for _, r := range batch {
		if err := insertTx(tx, r); err != nil {
			tx.Rollback()
			return
		}
	}
	_ = tx.Commit()
}

const insertSQL = `INSERT INTO requests
(started_at, completed_at, local_day, path, model, upstream_model, stream, status, latency_ms,
 prompt_tokens, completion_tokens, cache_read_tokens, cache_write_tokens, reasoning_tokens,
 total_tokens, window_label, rates_json, cost_nano_usd, priced, usage_status, error)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`

func args(r Record) []any {
	return []any{
		r.StartedAt.UTC().Format(time.RFC3339Nano),
		r.CompletedAt.UTC().Format(time.RFC3339Nano),
		r.LocalDay, r.Path, r.Model, r.UpstreamModel, boolInt(r.Stream), r.Status, r.LatencyMS,
		r.Usage.Prompt, r.Usage.Completion, r.Usage.CacheRead, r.Usage.CacheWrite,
		r.Usage.Reasoning, r.Usage.Total, r.WindowLabel, r.RatesJSON, r.CostNanoUSD,
		boolInt(r.Priced), r.UsageStatus, r.Error,
	}
}

func insertTx(tx *sql.Tx, r Record) error {
	_, err := tx.Exec(insertSQL, args(r)...)
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Store) Close() error {
	close(s.ch)
	s.wg.Wait()
	if _, err := s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		s.db.Close()
		return err
	}
	return s.db.Close()
}

func (s *Store) Summary(from, to string) (Summary, error) {
	var out Summary
	err := s.db.QueryRow(`SELECT COUNT(*),
	  COALESCE(SUM(prompt_tokens),0), COALESCE(SUM(completion_tokens),0),
	  COALESCE(SUM(cache_read_tokens),0), COALESCE(SUM(cost_nano_usd),0),
	  COALESCE(SUM(CASE WHEN usage_status != 'ok' THEN 1 ELSE 0 END),0)
	  FROM requests WHERE local_day BETWEEN ? AND ?`, from, to).
		Scan(&out.Requests, &out.PromptTokens, &out.CompletionTokens,
			&out.CacheReadTokens, &out.CostNanoUSD, &out.Unmetered)
	return out, err
}

func (s *Store) Daily(from, to string) ([]Daily, error) {
	rows, err := s.db.Query(`SELECT local_day, COUNT(*),
	  COALESCE(SUM(prompt_tokens),0), COALESCE(SUM(completion_tokens),0),
	  COALESCE(SUM(cost_nano_usd),0)
	  FROM requests WHERE local_day BETWEEN ? AND ?
	  GROUP BY local_day ORDER BY local_day`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Daily
	for rows.Next() {
		var d Daily
		if err := rows.Scan(&d.Day, &d.Requests, &d.PromptTokens, &d.Completion, &d.CostNanoUSD); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) ByModel(from, to string) ([]ModelRow, error) {
	rows, err := s.db.Query(`SELECT model, COUNT(*),
	  COALESCE(SUM(prompt_tokens),0), COALESCE(SUM(completion_tokens),0),
	  COALESCE(SUM(cache_read_tokens),0), COALESCE(SUM(reasoning_tokens),0),
	  COALESCE(SUM(cost_nano_usd),0), COALESCE(SUM(CASE WHEN priced = 0 THEN 1 ELSE 0 END),0)
	  FROM requests WHERE local_day BETWEEN ? AND ?
	  GROUP BY model ORDER BY SUM(cost_nano_usd) DESC`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ModelRow
	for rows.Next() {
		var m ModelRow
		if err := rows.Scan(&m.Model, &m.Requests, &m.PromptTokens, &m.CompletionTokens,
			&m.CacheReadTokens, &m.ReasoningTokens, &m.CostNanoUSD, &m.Unpriced); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

const selectColumns = `SELECT started_at, completed_at, local_day, path, model, upstream_model,
  stream, status, latency_ms, prompt_tokens, completion_tokens, cache_read_tokens,
  cache_write_tokens, reasoning_tokens, total_tokens, window_label, rates_json,
  cost_nano_usd, priced, usage_status, error FROM requests`

func (s *Store) Requests(from, to, model string, limit, offset int) ([]Record, error) {
	q := selectColumns + " WHERE local_day BETWEEN ? AND ?"
	args := []any{from, to}
	if model != "" {
		q += " AND model = ?"
		args = append(args, model)
	}
	q += " ORDER BY completed_at DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRecords(rows)
}

func (s *Store) AllForExport(from, to, model string) ([]Record, error) {
	q := selectColumns + " WHERE local_day BETWEEN ? AND ?"
	args := []any{from, to}
	if model != "" {
		q += " AND model = ?"
		args = append(args, model)
	}
	q += " ORDER BY completed_at DESC"
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRecords(rows)
}

func scanRecords(rows *sql.Rows) ([]Record, error) {
	var out []Record
	for rows.Next() {
		var r Record
		var started, completed string
		var stream, priced int
		if err := rows.Scan(&started, &completed, &r.LocalDay, &r.Path, &r.Model, &r.UpstreamModel,
			&stream, &r.Status, &r.LatencyMS, &r.Usage.Prompt, &r.Usage.Completion,
			&r.Usage.CacheRead, &r.Usage.CacheWrite, &r.Usage.Reasoning, &r.Usage.Total,
			&r.WindowLabel, &r.RatesJSON, &r.CostNanoUSD, &priced, &r.UsageStatus, &r.Error); err != nil {
			return nil, err
		}
		r.StartedAt, _ = time.Parse(time.RFC3339Nano, started)
		r.CompletedAt, _ = time.Parse(time.RFC3339Nano, completed)
		r.Stream = stream == 1
		r.Priced = priced == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarshalRates stores the rate snapshot for later inspection.
func MarshalRates(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
