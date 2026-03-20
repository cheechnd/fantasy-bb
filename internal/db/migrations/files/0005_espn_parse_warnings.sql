CREATE TABLE IF NOT EXISTS espn_parse_warnings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    sync_run_id INTEGER NOT NULL,
    warning_type TEXT NOT NULL,
    message TEXT NOT NULL,
    row_context_json TEXT,
    created_at TEXT NOT NULL,
    FOREIGN KEY(sync_run_id) REFERENCES espn_sync_runs(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_espn_parse_warnings_sync_run ON espn_parse_warnings(sync_run_id);
CREATE INDEX IF NOT EXISTS idx_espn_parse_warnings_type ON espn_parse_warnings(warning_type);
