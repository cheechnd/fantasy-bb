CREATE TABLE IF NOT EXISTS forecaster_import_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_type TEXT NOT NULL,
    source_identifier TEXT NOT NULL,
    imported_at TEXT NOT NULL,
    raw_row_count INTEGER NOT NULL DEFAULT 0,
    probable_start_count INTEGER NOT NULL DEFAULT 0,
    warning_count INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL,
    notes_json TEXT
);

CREATE TABLE IF NOT EXISTS probable_starts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    import_run_id INTEGER NOT NULL,
    game_date TEXT,
    source_date_text TEXT NOT NULL,
    team TEXT NOT NULL,
    opponent TEXT,
    pitcher_name TEXT,
    throws_hand TEXT,
    projected_fpts REAL,
    status TEXT NOT NULL,
    raw_fields_json TEXT,
    created_at TEXT NOT NULL,
    FOREIGN KEY(import_run_id) REFERENCES forecaster_import_runs(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS parse_warnings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    import_run_id INTEGER NOT NULL,
    warning_type TEXT NOT NULL,
    message TEXT NOT NULL,
    row_context_json TEXT,
    created_at TEXT NOT NULL,
    FOREIGN KEY(import_run_id) REFERENCES forecaster_import_runs(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_probable_starts_import_run_id ON probable_starts(import_run_id);
CREATE INDEX IF NOT EXISTS idx_probable_starts_game_date ON probable_starts(game_date);
CREATE INDEX IF NOT EXISTS idx_probable_starts_team ON probable_starts(team);
CREATE INDEX IF NOT EXISTS idx_probable_starts_pitcher_name ON probable_starts(pitcher_name);
CREATE INDEX IF NOT EXISTS idx_parse_warnings_import_run_id ON parse_warnings(import_run_id);
CREATE INDEX IF NOT EXISTS idx_forecaster_import_runs_imported_at ON forecaster_import_runs(imported_at DESC);
