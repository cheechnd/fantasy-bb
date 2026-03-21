CREATE TABLE IF NOT EXISTS ad_hoc_transaction_requests (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  requested_add_player_name TEXT NOT NULL,
  requested_drop_player_name TEXT NOT NULL,
  normalized_add_lookup TEXT NOT NULL DEFAULT '',
  normalized_drop_lookup TEXT NOT NULL DEFAULT '',
  resolved_add_player_name TEXT NOT NULL DEFAULT '',
  resolved_add_espn_player_id INTEGER NULL,
  resolved_drop_player_name TEXT NOT NULL DEFAULT '',
  resolved_drop_espn_player_id INTEGER NULL,
  request_state TEXT NOT NULL,
  resolution_status TEXT NOT NULL,
  resolution_notes_json TEXT NOT NULL DEFAULT '{}',
  linked_plan_id INTEGER NULL REFERENCES transaction_plans(id) ON DELETE SET NULL,
  linked_plan_item_id INTEGER NULL REFERENCES transaction_plan_items(id) ON DELETE SET NULL,
  linked_execution_attempt_id INTEGER NULL REFERENCES execution_attempts(id) ON DELETE SET NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS ad_hoc_transaction_request_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  request_id INTEGER NOT NULL REFERENCES ad_hoc_transaction_requests(id) ON DELETE CASCADE,
  event_type TEXT NOT NULL,
  event_data_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_ad_hoc_requests_state
  ON ad_hoc_transaction_requests(request_state, updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_ad_hoc_requests_resolution_status
  ON ad_hoc_transaction_requests(resolution_status, updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_ad_hoc_request_events_request
  ON ad_hoc_transaction_request_events(request_id, id);
