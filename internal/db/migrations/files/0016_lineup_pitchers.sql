CREATE TABLE IF NOT EXISTS lineup_plans (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  pitcher_plan_id INTEGER,
  sync_run_id INTEGER,
  created_at TEXT NOT NULL,
  status TEXT NOT NULL,
  summary_json TEXT NOT NULL DEFAULT '{}',
  FOREIGN KEY(pitcher_plan_id) REFERENCES pitcher_plans(id) ON DELETE SET NULL,
  FOREIGN KEY(sync_run_id) REFERENCES espn_sync_runs(id) ON DELETE SET NULL
);

CREATE TABLE IF NOT EXISTS lineup_plan_items (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  lineup_plan_id INTEGER NOT NULL,
  action_type TEXT NOT NULL,
  player_name TEXT NOT NULL,
  espn_player_id INTEGER,
  current_slot TEXT,
  target_slot TEXT,
  rationale_json TEXT NOT NULL DEFAULT '{}',
  flags_json TEXT NOT NULL DEFAULT '[]',
  created_at TEXT NOT NULL,
  FOREIGN KEY(lineup_plan_id) REFERENCES lineup_plans(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS lineup_review_states (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  lineup_plan_item_id INTEGER NOT NULL UNIQUE,
  current_state TEXT NOT NULL,
  note TEXT,
  updated_at TEXT NOT NULL,
  FOREIGN KEY(lineup_plan_item_id) REFERENCES lineup_plan_items(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS lineup_review_history (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  lineup_plan_item_id INTEGER NOT NULL,
  previous_state TEXT NOT NULL,
  new_state TEXT NOT NULL,
  note TEXT,
  changed_at TEXT NOT NULL,
  FOREIGN KEY(lineup_plan_item_id) REFERENCES lineup_plan_items(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS lineup_execution_attempts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  approved_lineup_item_id INTEGER NOT NULL,
  lineup_plan_id INTEGER NOT NULL,
  started_at TEXT NOT NULL,
  completed_at TEXT,
  execution_status TEXT NOT NULL,
  verification_status TEXT NOT NULL,
  request_summary_json TEXT NOT NULL DEFAULT '{}',
  response_summary_json TEXT NOT NULL DEFAULT '{}',
  error_message TEXT,
  details_json TEXT NOT NULL DEFAULT '{}',
  FOREIGN KEY(approved_lineup_item_id) REFERENCES lineup_plan_items(id) ON DELETE CASCADE,
  FOREIGN KEY(lineup_plan_id) REFERENCES lineup_plans(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS lineup_execution_attempt_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  lineup_execution_attempt_id INTEGER NOT NULL,
  event_type TEXT NOT NULL,
  event_data_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL,
  FOREIGN KEY(lineup_execution_attempt_id) REFERENCES lineup_execution_attempts(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_lineup_plan_items_plan_id ON lineup_plan_items(lineup_plan_id);
CREATE INDEX IF NOT EXISTS idx_lineup_review_states_state ON lineup_review_states(current_state, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_lineup_review_history_item_id ON lineup_review_history(lineup_plan_item_id, changed_at DESC);
CREATE INDEX IF NOT EXISTS idx_lineup_exec_attempts_item_id ON lineup_execution_attempts(approved_lineup_item_id);
CREATE INDEX IF NOT EXISTS idx_lineup_exec_attempts_status ON lineup_execution_attempts(execution_status, verification_status);
CREATE INDEX IF NOT EXISTS idx_lineup_exec_attempt_events_attempt_id ON lineup_execution_attempt_events(lineup_execution_attempt_id, id ASC);
