CREATE TABLE IF NOT EXISTS espn_sync_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    sync_type TEXT NOT NULL,
    league_id TEXT NOT NULL,
    team_id TEXT NOT NULL,
    season INTEGER NOT NULL,
    started_at TEXT NOT NULL,
    completed_at TEXT,
    status TEXT NOT NULL,
    warning_count INTEGER NOT NULL DEFAULT 0,
    summary_json TEXT
);

CREATE TABLE IF NOT EXISTS espn_raw_payloads (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    sync_run_id INTEGER NOT NULL,
    payload_type TEXT NOT NULL,
    source_endpoint TEXT NOT NULL,
    response_status INTEGER NOT NULL,
    payload_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    FOREIGN KEY(sync_run_id) REFERENCES espn_sync_runs(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS espn_league_snapshots (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    sync_run_id INTEGER NOT NULL,
    league_id TEXT NOT NULL,
    season INTEGER NOT NULL,
    league_name TEXT,
    team_id TEXT NOT NULL,
    team_name TEXT,
    scoring_type TEXT,
    settings_json TEXT,
    created_at TEXT NOT NULL,
    FOREIGN KEY(sync_run_id) REFERENCES espn_sync_runs(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS espn_roster_snapshots (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    sync_run_id INTEGER NOT NULL,
    espn_player_id INTEGER,
    player_name TEXT NOT NULL,
    normalized_name TEXT NOT NULL,
    mlb_team TEXT,
    roster_slot TEXT,
    is_pitcher INTEGER NOT NULL DEFAULT 0,
    role TEXT,
    status_tag TEXT,
    raw_player_json TEXT,
    created_at TEXT NOT NULL,
    FOREIGN KEY(sync_run_id) REFERENCES espn_sync_runs(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_espn_sync_runs_started_at ON espn_sync_runs(started_at DESC);
CREATE INDEX IF NOT EXISTS idx_espn_sync_runs_status ON espn_sync_runs(status);
CREATE INDEX IF NOT EXISTS idx_espn_raw_payloads_sync_run ON espn_raw_payloads(sync_run_id);
CREATE INDEX IF NOT EXISTS idx_espn_league_snapshots_sync_run ON espn_league_snapshots(sync_run_id);
CREATE INDEX IF NOT EXISTS idx_espn_roster_snapshots_sync_run ON espn_roster_snapshots(sync_run_id);
CREATE INDEX IF NOT EXISTS idx_espn_roster_snapshots_name ON espn_roster_snapshots(player_name);
CREATE INDEX IF NOT EXISTS idx_espn_roster_snapshots_normalized_name ON espn_roster_snapshots(normalized_name);
CREATE INDEX IF NOT EXISTS idx_espn_roster_snapshots_is_pitcher ON espn_roster_snapshots(is_pitcher);
