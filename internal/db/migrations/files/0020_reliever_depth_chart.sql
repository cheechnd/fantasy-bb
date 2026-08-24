CREATE TABLE IF NOT EXISTS reliever_depth_chart_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source TEXT NOT NULL,
    source_url TEXT NOT NULL,
    source_date TEXT,
    fetched_at TEXT NOT NULL,
    status TEXT NOT NULL,
    team_count INTEGER NOT NULL DEFAULT 0,
    row_count INTEGER NOT NULL DEFAULT 0,
    matched_count INTEGER NOT NULL DEFAULT 0,
    unmatched_count INTEGER NOT NULL DEFAULT 0,
    ambiguous_count INTEGER NOT NULL DEFAULT 0,
    conflict_count INTEGER NOT NULL DEFAULT 0,
    warnings_json TEXT NOT NULL DEFAULT '[]',
    summary_json TEXT NOT NULL DEFAULT '{}',
    raw_html TEXT
);

CREATE TABLE IF NOT EXISTS reliever_depth_chart_entries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id INTEGER NOT NULL,
    espn_player_id INTEGER,
    player_name TEXT NOT NULL,
    normalized_name TEXT NOT NULL,
    mlb_team TEXT NOT NULL,
    relief_role TEXT NOT NULL,
    source_role_label TEXT NOT NULL,
    roster_percent REAL,
    match_status TEXT NOT NULL,
    match_reason TEXT,
    conflict_flag INTEGER NOT NULL DEFAULT 0,
    conflict_reason TEXT,
    created_at TEXT NOT NULL,
    FOREIGN KEY(run_id) REFERENCES reliever_depth_chart_runs(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_reliever_runs_fetched_at ON reliever_depth_chart_runs(fetched_at DESC);
CREATE INDEX IF NOT EXISTS idx_reliever_runs_status ON reliever_depth_chart_runs(status);
CREATE INDEX IF NOT EXISTS idx_reliever_entries_run_id ON reliever_depth_chart_entries(run_id);
CREATE INDEX IF NOT EXISTS idx_reliever_entries_player_id ON reliever_depth_chart_entries(espn_player_id);
CREATE INDEX IF NOT EXISTS idx_reliever_entries_name_team ON reliever_depth_chart_entries(normalized_name, mlb_team);
CREATE INDEX IF NOT EXISTS idx_reliever_entries_role ON reliever_depth_chart_entries(relief_role);
