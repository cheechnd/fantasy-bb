CREATE TABLE IF NOT EXISTS execution_runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  run_type TEXT NOT NULL,
  created_at TEXT NOT NULL,
  status TEXT NOT NULL,
  item_count INTEGER NOT NULL DEFAULT 0,
  summary_json TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS execution_run_items (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  execution_run_id INTEGER NOT NULL REFERENCES execution_runs(id) ON DELETE CASCADE,
  approved_item_id INTEGER NOT NULL REFERENCES transaction_plan_items(id) ON DELETE CASCADE,
  source_plan_id INTEGER NOT NULL REFERENCES transaction_plans(id) ON DELETE CASCADE,
  add_player_name TEXT NOT NULL,
  drop_player_name TEXT NOT NULL,
  validation_status TEXT NOT NULL,
  readiness_rank INTEGER NULL,
  validation_reasons_json TEXT NOT NULL DEFAULT '[]',
  action_preview_json TEXT NOT NULL DEFAULT '{}',
  details_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_execution_runs_created_at
  ON execution_runs(created_at DESC);

CREATE INDEX IF NOT EXISTS idx_execution_run_items_run_id
  ON execution_run_items(execution_run_id);

CREATE INDEX IF NOT EXISTS idx_execution_run_items_approved_item
  ON execution_run_items(approved_item_id);

CREATE INDEX IF NOT EXISTS idx_execution_run_items_status
  ON execution_run_items(validation_status);
