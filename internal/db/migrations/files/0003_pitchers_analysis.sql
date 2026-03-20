CREATE TABLE IF NOT EXISTS analysis_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    analysis_type TEXT NOT NULL,
    import_run_id INTEGER,
    roster_source_path TEXT,
    pool_source_path TEXT,
    window_start TEXT NOT NULL,
    window_end TEXT NOT NULL,
    created_at TEXT NOT NULL,
    status TEXT NOT NULL,
    summary_json TEXT,
    FOREIGN KEY(import_run_id) REFERENCES forecaster_import_runs(id)
);

CREATE TABLE IF NOT EXISTS analysis_results (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    analysis_run_id INTEGER NOT NULL,
    result_type TEXT NOT NULL,
    player_name TEXT NOT NULL,
    mlb_team TEXT,
    matched_pitcher_name TEXT,
    projected_start_count INTEGER NOT NULL DEFAULT 0,
    total_projected_fpts REAL,
    result_rank INTEGER,
    flags_json TEXT,
    details_json TEXT,
    created_at TEXT NOT NULL,
    FOREIGN KEY(analysis_run_id) REFERENCES analysis_runs(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS player_match_results (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    analysis_run_id INTEGER NOT NULL,
    input_player_name TEXT NOT NULL,
    input_mlb_team TEXT,
    normalized_lookup_key TEXT NOT NULL,
    match_status TEXT NOT NULL,
    matched_entity_json TEXT,
    explanation TEXT,
    created_at TEXT NOT NULL,
    FOREIGN KEY(analysis_run_id) REFERENCES analysis_runs(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_analysis_runs_created_at ON analysis_runs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_analysis_runs_type ON analysis_runs(analysis_type);
CREATE INDEX IF NOT EXISTS idx_analysis_results_run_type ON analysis_results(analysis_run_id, result_type);
CREATE INDEX IF NOT EXISTS idx_analysis_results_rank ON analysis_results(analysis_run_id, result_rank);
CREATE INDEX IF NOT EXISTS idx_player_match_results_run ON player_match_results(analysis_run_id);
