ALTER TABLE execution_attempts ADD COLUMN submitted_at TEXT NULL;
ALTER TABLE execution_attempts ADD COLUMN last_verified_at TEXT NULL;
ALTER TABLE execution_attempts ADD COLUMN ambiguous_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE execution_attempts ADD COLUMN final_outcome_summary_json TEXT NOT NULL DEFAULT '{}';

CREATE INDEX IF NOT EXISTS idx_execution_attempts_verification_status
  ON execution_attempts(verification_status);

CREATE INDEX IF NOT EXISTS idx_execution_attempts_submitted_at
  ON execution_attempts(submitted_at DESC);

CREATE INDEX IF NOT EXISTS idx_execution_attempts_last_verified_at
  ON execution_attempts(last_verified_at DESC);
