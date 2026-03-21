CREATE TABLE IF NOT EXISTS execution_attempts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  approved_item_id INTEGER NOT NULL REFERENCES transaction_plan_items(id) ON DELETE CASCADE,
  source_plan_id INTEGER NOT NULL REFERENCES transaction_plans(id) ON DELETE CASCADE,
  preflight_run_id INTEGER NULL REFERENCES execution_runs(id) ON DELETE SET NULL,
  started_at TEXT NOT NULL,
  completed_at TEXT NULL,
  execution_status TEXT NOT NULL,
  verification_status TEXT NOT NULL,
  add_player_name TEXT NOT NULL,
  drop_player_name TEXT NOT NULL,
  request_summary_json TEXT NOT NULL DEFAULT '{}',
  response_summary_json TEXT NOT NULL DEFAULT '{}',
  error_message TEXT NOT NULL DEFAULT '',
  details_json TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS execution_attempt_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  execution_attempt_id INTEGER NOT NULL REFERENCES execution_attempts(id) ON DELETE CASCADE,
  event_type TEXT NOT NULL,
  event_data_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_execution_attempts_approved_item
  ON execution_attempts(approved_item_id);

CREATE INDEX IF NOT EXISTS idx_execution_attempts_started_at
  ON execution_attempts(started_at DESC);

CREATE INDEX IF NOT EXISTS idx_execution_attempts_status
  ON execution_attempts(execution_status);

CREATE INDEX IF NOT EXISTS idx_execution_attempt_events_attempt_id
  ON execution_attempt_events(execution_attempt_id);
