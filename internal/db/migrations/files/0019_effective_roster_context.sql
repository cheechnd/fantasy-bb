ALTER TABLE espn_sync_runs ADD COLUMN scoring_period_id INTEGER NULL;
ALTER TABLE espn_sync_runs ADD COLUMN effective_next_day INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_espn_sync_runs_roster_context
ON espn_sync_runs(sync_type, effective_next_day, scoring_period_id, id DESC);
