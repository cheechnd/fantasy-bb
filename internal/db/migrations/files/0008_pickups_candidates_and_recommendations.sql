CREATE TABLE IF NOT EXISTS espn_candidate_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    sync_run_id INTEGER,
    query_type TEXT NOT NULL,
    query_text TEXT,
    filters_json TEXT,
    started_at TEXT NOT NULL,
    completed_at TEXT,
    status TEXT NOT NULL,
    candidate_count INTEGER NOT NULL DEFAULT 0,
    warning_count INTEGER NOT NULL DEFAULT 0,
    summary_json TEXT,
    FOREIGN KEY(sync_run_id) REFERENCES espn_sync_runs(id)
);

CREATE TABLE IF NOT EXISTS espn_free_agent_candidates (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    candidate_run_id INTEGER NOT NULL,
    espn_player_id INTEGER,
    player_name TEXT NOT NULL,
    normalized_name TEXT NOT NULL,
    mlb_team TEXT,
    is_pitcher INTEGER NOT NULL DEFAULT 1,
    role TEXT,
    status_tag TEXT,
    raw_player_json TEXT,
    created_at TEXT NOT NULL,
    FOREIGN KEY(candidate_run_id) REFERENCES espn_candidate_runs(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS pickup_recommendation_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    sync_run_id INTEGER,
    import_run_id INTEGER,
    candidate_run_id INTEGER,
    window_start TEXT NOT NULL,
    window_end TEXT NOT NULL,
    created_at TEXT NOT NULL,
    status TEXT NOT NULL,
    summary_json TEXT,
    FOREIGN KEY(sync_run_id) REFERENCES espn_sync_runs(id),
    FOREIGN KEY(import_run_id) REFERENCES forecaster_import_runs(id),
    FOREIGN KEY(candidate_run_id) REFERENCES espn_candidate_runs(id)
);

CREATE TABLE IF NOT EXISTS pickup_recommendation_items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    recommendation_run_id INTEGER NOT NULL,
    item_type TEXT NOT NULL,
    player_name TEXT NOT NULL,
    mlb_team TEXT,
    espn_player_id INTEGER,
    matched_pitcher_name TEXT,
    projected_start_count INTEGER NOT NULL DEFAULT 0,
    total_projected_fpts REAL,
    comparison_target_name TEXT,
    comparison_delta_fpts REAL,
    result_rank INTEGER,
    flags_json TEXT,
    notes_json TEXT,
    details_json TEXT,
    created_at TEXT NOT NULL,
    FOREIGN KEY(recommendation_run_id) REFERENCES pickup_recommendation_runs(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_espn_candidate_runs_started_at ON espn_candidate_runs(started_at DESC);
CREATE INDEX IF NOT EXISTS idx_espn_candidate_runs_sync_run ON espn_candidate_runs(sync_run_id);
CREATE INDEX IF NOT EXISTS idx_espn_free_agent_candidates_run ON espn_free_agent_candidates(candidate_run_id);
CREATE INDEX IF NOT EXISTS idx_espn_free_agent_candidates_name ON espn_free_agent_candidates(normalized_name);
CREATE INDEX IF NOT EXISTS idx_pickup_recommendation_runs_created_at ON pickup_recommendation_runs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_pickup_recommendation_runs_candidate_run ON pickup_recommendation_runs(candidate_run_id);
CREATE INDEX IF NOT EXISTS idx_pickup_recommendation_items_run_type ON pickup_recommendation_items(recommendation_run_id, item_type);
CREATE INDEX IF NOT EXISTS idx_pickup_recommendation_items_rank ON pickup_recommendation_items(recommendation_run_id, result_rank);
