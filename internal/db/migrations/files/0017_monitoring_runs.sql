CREATE TABLE IF NOT EXISTS monitor_runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  run_type TEXT NOT NULL,
  created_at TEXT NOT NULL,
  status TEXT NOT NULL,
  item_count INTEGER NOT NULL DEFAULT 0,
  summary_json TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS monitor_run_items (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  monitor_run_id INTEGER NOT NULL,
  artifact_type TEXT NOT NULL,
  artifact_id INTEGER NOT NULL,
  monitor_status TEXT NOT NULL,
  reasons_json TEXT NOT NULL DEFAULT '[]',
  recommended_action TEXT NOT NULL,
  details_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL,
  FOREIGN KEY(monitor_run_id) REFERENCES monitor_runs(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_monitor_runs_type_created ON monitor_runs(run_type, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_monitor_items_run_id ON monitor_run_items(monitor_run_id);
CREATE INDEX IF NOT EXISTS idx_monitor_items_artifact ON monitor_run_items(artifact_type, artifact_id);
CREATE INDEX IF NOT EXISTS idx_monitor_items_status ON monitor_run_items(monitor_status, created_at DESC);
