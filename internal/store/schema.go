package store

// UserVersion is the schema version written to PRAGMA user_version.
const UserVersion = 2

// Schema is the complete schema v2 for the llmmeter database. Configuration
// and pricing live in the YAML config file, so there are no settings, models
// or price_windows tables.
const Schema = `
CREATE TABLE requests (
  id INTEGER PRIMARY KEY,
  started_at TEXT NOT NULL,
  completed_at TEXT NOT NULL,
  local_day TEXT NOT NULL,
  minute_of_day INTEGER NOT NULL,
  path TEXT NOT NULL,
  path_class TEXT NOT NULL,
  upstream_id TEXT NOT NULL,
  upstream_name TEXT NOT NULL,
  key_id TEXT NOT NULL,
  key_name TEXT NOT NULL,
  model TEXT NOT NULL DEFAULT '',
  upstream_model TEXT NOT NULL DEFAULT '',
  stream INTEGER NOT NULL,
  status INTEGER NOT NULL,
  latency_ms INTEGER NOT NULL,
  ttft_ms INTEGER,
  req_body_bytes INTEGER NOT NULL,
  req_header_bytes INTEGER NOT NULL,
  resp_body_bytes INTEGER NOT NULL,
  resp_header_bytes INTEGER NOT NULL,
  prompt_tokens INTEGER NOT NULL DEFAULT 0,
  completion_tokens INTEGER NOT NULL DEFAULT 0,
  cache_read_tokens INTEGER NOT NULL DEFAULT 0,
  cache_write_tokens INTEGER NOT NULL DEFAULT 0,
  reasoning_tokens INTEGER NOT NULL DEFAULT 0,
  total_tokens INTEGER NOT NULL DEFAULT 0,
  window_label TEXT NOT NULL DEFAULT 'base',
  rates_json TEXT NOT NULL DEFAULT '',
  cost_nano_usd INTEGER NOT NULL DEFAULT 0,
  priced INTEGER NOT NULL DEFAULT 0,
  usage_status TEXT NOT NULL,
  client_disconnected INTEGER NOT NULL DEFAULT 0,
  error TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_requests_started ON requests(started_at);
CREATE INDEX idx_requests_upstream_started ON requests(upstream_id, started_at);
CREATE INDEX idx_requests_upstream_key_started ON requests(upstream_id, key_id, started_at);
CREATE INDEX idx_requests_model_started ON requests(model, started_at);
CREATE INDEX idx_requests_unpriced ON requests(priced) WHERE priced = 0;

CREATE TABLE traffic_minutes (
  local_day TEXT NOT NULL,
  minute_of_day INTEGER NOT NULL,
  path_class TEXT NOT NULL,
  upstream_id TEXT NOT NULL,
  model TEXT NOT NULL DEFAULT '',
  requests INTEGER NOT NULL,
  errors INTEGER NOT NULL,
  req_bytes INTEGER NOT NULL,
  resp_bytes INTEGER NOT NULL,
  prompt_tokens INTEGER NOT NULL,
  completion_tokens INTEGER NOT NULL,
  cache_read_tokens INTEGER NOT NULL,
  reasoning_tokens INTEGER NOT NULL,
  cost_nano_usd INTEGER NOT NULL,
  latency_sum_ms INTEGER NOT NULL,
  latency_max_ms INTEGER NOT NULL,
  PRIMARY KEY (local_day, minute_of_day, path_class, upstream_id, model)
) WITHOUT ROWID;
`
