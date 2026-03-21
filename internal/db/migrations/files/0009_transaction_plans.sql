CREATE TABLE IF NOT EXISTS transaction_plans (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  sync_run_id INTEGER NULL REFERENCES espn_sync_runs(id) ON DELETE SET NULL,
  import_run_id INTEGER NULL REFERENCES forecaster_import_runs(id) ON DELETE SET NULL,
  pitcher_plan_id INTEGER NULL REFERENCES pitcher_plans(id) ON DELETE SET NULL,
  pickup_recommendation_run_id INTEGER NULL REFERENCES pickup_recommendation_runs(id) ON DELETE SET NULL,
  window_start TEXT NOT NULL,
  window_end TEXT NOT NULL,
  created_at TEXT NOT NULL,
  status TEXT NOT NULL,
  plan_summary_json TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS transaction_plan_items (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  transaction_plan_id INTEGER NOT NULL REFERENCES transaction_plans(id) ON DELETE CASCADE,
  bucket TEXT NOT NULL,
  add_player_name TEXT NOT NULL,
  add_player_team TEXT NULL,
  add_espn_player_id INTEGER NULL,
  drop_player_name TEXT NOT NULL,
  drop_player_team TEXT NULL,
  drop_espn_player_id INTEGER NULL,
  add_projected_start_count INTEGER NOT NULL DEFAULT 0,
  add_total_projected_fpts REAL NULL,
  drop_projected_start_count INTEGER NOT NULL DEFAULT 0,
  drop_total_projected_fpts REAL NULL,
  delta_fpts REAL NULL,
  result_rank INTEGER NULL,
  flags_json TEXT NOT NULL DEFAULT '[]',
  notes_json TEXT NOT NULL DEFAULT '[]',
  details_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_transaction_plans_created_at
  ON transaction_plans(created_at DESC);

CREATE INDEX IF NOT EXISTS idx_transaction_plan_items_plan_id
  ON transaction_plan_items(transaction_plan_id);

CREATE INDEX IF NOT EXISTS idx_transaction_plan_items_bucket
  ON transaction_plan_items(bucket);

CREATE INDEX IF NOT EXISTS idx_transaction_plan_items_rank
  ON transaction_plan_items(result_rank);
