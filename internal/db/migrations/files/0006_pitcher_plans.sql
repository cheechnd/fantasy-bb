CREATE TABLE IF NOT EXISTS pitcher_plans (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    sync_run_id INTEGER,
    import_run_id INTEGER,
    analysis_run_id INTEGER,
    window_start TEXT NOT NULL,
    window_end TEXT NOT NULL,
    created_at TEXT NOT NULL,
    status TEXT NOT NULL,
    plan_summary_json TEXT,
    FOREIGN KEY(sync_run_id) REFERENCES espn_sync_runs(id),
    FOREIGN KEY(import_run_id) REFERENCES forecaster_import_runs(id),
    FOREIGN KEY(analysis_run_id) REFERENCES analysis_runs(id)
);

CREATE TABLE IF NOT EXISTS pitcher_plan_items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    plan_id INTEGER NOT NULL,
    bucket TEXT NOT NULL,
    player_name TEXT NOT NULL,
    mlb_team TEXT,
    espn_player_id INTEGER,
    matched_pitcher_name TEXT,
    projected_start_count INTEGER NOT NULL DEFAULT 0,
    total_projected_fpts REAL,
    result_rank INTEGER,
    flags_json TEXT,
    notes_json TEXT,
    details_json TEXT,
    created_at TEXT NOT NULL,
    FOREIGN KEY(plan_id) REFERENCES pitcher_plans(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_pitcher_plans_created_at ON pitcher_plans(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_pitcher_plans_sync_run ON pitcher_plans(sync_run_id);
CREATE INDEX IF NOT EXISTS idx_pitcher_plan_items_plan ON pitcher_plan_items(plan_id);
CREATE INDEX IF NOT EXISTS idx_pitcher_plan_items_bucket ON pitcher_plan_items(plan_id, bucket);
CREATE INDEX IF NOT EXISTS idx_pitcher_plan_items_rank ON pitcher_plan_items(plan_id, result_rank);
