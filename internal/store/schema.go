package store

// UserVersion is the schema version written to PRAGMA user_version.
const UserVersion = 1

// Schema is the complete schema v1 for the llmmeter database.
const Schema = `
CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS models (
  id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE,
  input_per_mtok REAL NOT NULL DEFAULT 0,
  output_per_mtok REAL NOT NULL DEFAULT 0,
  cache_read_per_mtok REAL NOT NULL DEFAULT 0,
  cache_write_per_mtok REAL NOT NULL DEFAULT 0,
  reasoning_per_mtok REAL,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS price_windows (
  id INTEGER PRIMARY KEY,
  model_id INTEGER NOT NULL REFERENCES models(id) ON DELETE CASCADE,
  label TEXT NOT NULL,
  days_mask INTEGER NOT NULL DEFAULT 127,
  start_minute INTEGER NOT NULL,
  end_minute INTEGER NOT NULL,
  input_per_mtok REAL, output_per_mtok REAL,
  cache_read_per_mtok REAL, cache_write_per_mtok REAL, reasoning_per_mtok REAL,
  sort_order INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS requests (
  id INTEGER PRIMARY KEY,
  started_at TEXT NOT NULL, completed_at TEXT NOT NULL,
  local_day TEXT NOT NULL, minute_of_day INTEGER NOT NULL,
  path TEXT NOT NULL, path_class TEXT NOT NULL,
  model TEXT NOT NULL DEFAULT '', upstream_model TEXT NOT NULL DEFAULT '',
  stream INTEGER NOT NULL, status INTEGER NOT NULL,
  latency_ms INTEGER NOT NULL, ttft_ms INTEGER,
  req_body_bytes INTEGER NOT NULL, req_header_bytes INTEGER NOT NULL,
  resp_body_bytes INTEGER NOT NULL, resp_header_bytes INTEGER NOT NULL,
  prompt_tokens INTEGER NOT NULL DEFAULT 0, completion_tokens INTEGER NOT NULL DEFAULT 0,
  cache_read_tokens INTEGER NOT NULL DEFAULT 0, cache_write_tokens INTEGER NOT NULL DEFAULT 0,
  reasoning_tokens INTEGER NOT NULL DEFAULT 0, total_tokens INTEGER NOT NULL DEFAULT 0,
  window_label TEXT NOT NULL DEFAULT 'base', rates_json TEXT NOT NULL DEFAULT '',
  cost_nano_usd INTEGER NOT NULL DEFAULT 0, priced INTEGER NOT NULL DEFAULT 0,
  usage_status TEXT NOT NULL,
  client_disconnected INTEGER NOT NULL DEFAULT 0,
  error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_requests_started ON requests(started_at);
CREATE INDEX IF NOT EXISTS idx_requests_day_class ON requests(local_day, path_class);
CREATE INDEX IF NOT EXISTS idx_requests_model_started ON requests(model, started_at);
CREATE INDEX IF NOT EXISTS idx_requests_unpriced ON requests(priced) WHERE priced = 0;

CREATE TABLE IF NOT EXISTS traffic_minutes (
  local_day TEXT NOT NULL, minute_of_day INTEGER NOT NULL,
  path_class TEXT NOT NULL, model TEXT NOT NULL DEFAULT '',
  requests INTEGER NOT NULL, errors INTEGER NOT NULL,
  req_bytes INTEGER NOT NULL, resp_bytes INTEGER NOT NULL,
  prompt_tokens INTEGER NOT NULL, completion_tokens INTEGER NOT NULL,
  cache_read_tokens INTEGER NOT NULL, reasoning_tokens INTEGER NOT NULL,
  cost_nano_usd INTEGER NOT NULL,
  latency_sum_ms INTEGER NOT NULL, latency_max_ms INTEGER NOT NULL,
  PRIMARY KEY (local_day, minute_of_day, path_class, model)
) WITHOUT ROWID;
`
