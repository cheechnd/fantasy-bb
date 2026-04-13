ALTER TABLE espn_free_agent_candidates ADD COLUMN acquisition_status TEXT;

CREATE INDEX IF NOT EXISTS idx_espn_free_agent_candidates_acquisition_status
ON espn_free_agent_candidates(acquisition_status);

