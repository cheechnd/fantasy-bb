package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"fantasy-baseball/internal/espn"
)

type Repository struct {
	db *sql.DB
}

func New(db *sql.DB) *Repository {
	return &Repository{db: db}
}

type PersistSyncInput struct {
	SyncType         string
	LeagueID         string
	TeamID           string
	Season           int
	Status           string
	WarningCount     int
	ScoringPeriodID  *int
	EffectiveNextDay bool
	Summary          map[string]any
	Payloads         []espn.RawPayload
	League           espn.LeagueSnapshot
	Roster           []espn.RosterSnapshot
	Warnings         []espn.ParseWarningInput
}

type PersistCandidateInput struct {
	SyncRunID    *int64
	QueryType    string
	QueryText    string
	Filters      map[string]any
	Status       string
	WarningCount int
	Summary      map[string]any
	Payload      espn.RawPayload
	Candidates   []espn.FreeAgentCandidate
}

func (r *Repository) PersistSync(ctx context.Context, input PersistSyncInput) (int64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin espn sync tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	startedAt := now.Format(time.RFC3339)
	completedAt := startedAt
	summaryJSON := "{}"
	if len(input.Summary) > 0 {
		b, _ := json.Marshal(input.Summary)
		summaryJSON = string(b)
	}
	res, err := tx.ExecContext(ctx, `
		INSERT INTO espn_sync_runs (
			sync_type, league_id, team_id, season, started_at, completed_at,
			status, warning_count, scoring_period_id, effective_next_day, summary_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, input.SyncType, input.LeagueID, input.TeamID, input.Season, startedAt, completedAt, input.Status, input.WarningCount, nullInt(input.ScoringPeriodID), boolInt(input.EffectiveNextDay), summaryJSON)
	if err != nil {
		return 0, fmt.Errorf("insert espn_sync_run: %w", err)
	}
	syncRunID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("espn sync run id: %w", err)
	}

	for _, p := range input.Payloads {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO espn_raw_payloads (
				sync_run_id, payload_type, source_endpoint,
				response_status, payload_json, created_at
			) VALUES (?, ?, ?, ?, ?, ?)
		`, syncRunID, p.PayloadType, p.SourceEndpoint, p.ResponseStatus, p.PayloadJSON, startedAt)
		if err != nil {
			return 0, fmt.Errorf("insert espn_raw_payload: %w", err)
		}
	}

	league := input.League
	if league.CreatedAt.IsZero() {
		league.CreatedAt = now
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO espn_league_snapshots (
			sync_run_id, league_id, season, league_name, team_id,
			team_name, scoring_type, settings_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, syncRunID, league.LeagueID, league.Season, league.LeagueName, league.TeamID, league.TeamName, league.ScoringType, league.SettingsJSON, league.CreatedAt.UTC().Format(time.RFC3339))
	if err != nil {
		return 0, fmt.Errorf("insert espn_league_snapshot: %w", err)
	}

	for _, row := range input.Roster {
		createdAt := row.CreatedAt
		if createdAt.IsZero() {
			createdAt = now
		}
		isPitcher := 0
		if row.IsPitcher {
			isPitcher = 1
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO espn_roster_snapshots (
				sync_run_id, espn_player_id, player_name, normalized_name,
				mlb_team, roster_slot, is_pitcher, role, status_tag,
				raw_player_json, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, syncRunID, nullInt64(row.ESPNPlayerID), row.PlayerName, row.NormalizedName, row.MLBTeam, row.RosterSlot, isPitcher, row.Role, row.StatusTag, row.RawPlayerJSON, createdAt.UTC().Format(time.RFC3339))
		if err != nil {
			return 0, fmt.Errorf("insert espn_roster_snapshot: %w", err)
		}
	}

	for _, w := range input.Warnings {
		rowContextJSON := "{}"
		if len(w.RowContext) > 0 {
			b, _ := json.Marshal(w.RowContext)
			rowContextJSON = string(b)
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO espn_parse_warnings (
				sync_run_id, warning_type, message, row_context_json, created_at
			) VALUES (?, ?, ?, ?, ?)
		`, syncRunID, w.WarningType, w.Message, rowContextJSON, startedAt)
		if err != nil {
			return 0, fmt.Errorf("insert espn_parse_warning: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit espn sync tx: %w", err)
	}
	return syncRunID, nil
}

func (r *Repository) PersistCandidates(ctx context.Context, input PersistCandidateInput) (int64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin espn candidate tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)
	filtersJSON := "{}"
	if len(input.Filters) > 0 {
		b, _ := json.Marshal(input.Filters)
		filtersJSON = string(b)
	}
	summaryJSON := "{}"
	if len(input.Summary) > 0 {
		b, _ := json.Marshal(input.Summary)
		summaryJSON = string(b)
	}
	res, err := tx.ExecContext(ctx, `
		INSERT INTO espn_candidate_runs (
			sync_run_id, query_type, query_text, filters_json,
			started_at, completed_at, status, candidate_count, warning_count, summary_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, nullInt64(input.SyncRunID), input.QueryType, input.QueryText, filtersJSON, now, now, input.Status, len(input.Candidates), input.WarningCount, summaryJSON)
	if err != nil {
		return 0, fmt.Errorf("insert espn candidate run: %w", err)
	}
	candidateRunID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("espn candidate run id: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO espn_raw_payloads (
			sync_run_id, payload_type, source_endpoint, response_status, payload_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?)
	`, nullInt64(input.SyncRunID), input.Payload.PayloadType, input.Payload.SourceEndpoint, input.Payload.ResponseStatus, input.Payload.PayloadJSON, now)
	if err != nil {
		return 0, fmt.Errorf("insert espn candidate raw payload: %w", err)
	}

	for _, c := range input.Candidates {
		createdAt := c.CreatedAt
		if createdAt.IsZero() {
			createdAt = time.Now().UTC()
		}
		isPitcher := 0
		if c.IsPitcher {
			isPitcher = 1
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO espn_free_agent_candidates (
				candidate_run_id, espn_player_id, player_name, normalized_name,
				mlb_team, is_pitcher, role, acquisition_status, waiver_process_datetime, status_tag, raw_player_json, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, candidateRunID, nullInt64(c.ESPNPlayerID), c.PlayerName, c.NormalizedName, c.MLBTeam, isPitcher, c.Role, c.AcquisitionStatus, nullStringPtr(c.WaiverProcessDatetime), c.StatusTag, c.RawPlayerJSON, createdAt.UTC().Format(time.RFC3339))
		if err != nil {
			return 0, fmt.Errorf("insert espn free agent candidate: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit espn candidate tx: %w", err)
	}
	return candidateRunID, nil
}

func (r *Repository) LatestSyncRun(ctx context.Context) (*espn.SyncRun, error) {
	return r.latestSyncRun(ctx, nil, false)
}

func (r *Repository) LatestSyncRunForContext(ctx context.Context, scoringPeriodID *int, effectiveNextDay bool) (*espn.SyncRun, error) {
	return r.latestSyncRun(ctx, scoringPeriodID, effectiveNextDay)
}

func (r *Repository) latestSyncRun(ctx context.Context, scoringPeriodID *int, effectiveNextDay bool) (*espn.SyncRun, error) {
	where, args := syncContextWhere(scoringPeriodID, effectiveNextDay)
	row := r.db.QueryRowContext(ctx, `
		SELECT id, sync_type, league_id, team_id, season, started_at,
		       COALESCE(completed_at, started_at), status, warning_count,
		       scoring_period_id, effective_next_day,
		       COALESCE(summary_json, '{}')
		FROM espn_sync_runs
		WHERE `+where+`
		ORDER BY id DESC
		LIMIT 1
	`, args...)
	return scanSyncRunRow(row, "latest espn sync run")
}

func scanSyncRunRow(row *sql.Row, label string) (*espn.SyncRun, error) {
	var out espn.SyncRun
	var scoringPeriodID sql.NullInt64
	var effectiveNextDay int
	var startedAtRaw, completedAtRaw string
	if err := row.Scan(&out.ID, &out.SyncType, &out.LeagueID, &out.TeamID, &out.Season, &startedAtRaw, &completedAtRaw, &out.Status, &out.WarningCount, &scoringPeriodID, &effectiveNextDay, &out.SummaryJSON); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("query %s: %w", label, err)
	}
	startedAt, err := time.Parse(time.RFC3339, startedAtRaw)
	if err != nil {
		return nil, fmt.Errorf("parse espn sync started_at: %w", err)
	}
	completedAt, err := time.Parse(time.RFC3339, completedAtRaw)
	if err != nil {
		return nil, fmt.Errorf("parse espn sync completed_at: %w", err)
	}
	out.StartedAt = startedAt
	out.CompletedAt = completedAt
	if scoringPeriodID.Valid {
		v := int(scoringPeriodID.Int64)
		out.ScoringPeriodID = &v
	}
	out.EffectiveNextDay = effectiveNextDay == 1
	return &out, nil
}

func (r *Repository) SyncRunByID(ctx context.Context, syncRunID int64) (*espn.SyncRun, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, sync_type, league_id, team_id, season, started_at,
		       COALESCE(completed_at, started_at), status, warning_count,
		       scoring_period_id, effective_next_day,
		       COALESCE(summary_json, '{}')
		FROM espn_sync_runs
		WHERE id = ?
	`, syncRunID)
	return scanSyncRunRow(row, "espn sync run by id")
}

func (r *Repository) ListSyncRuns(ctx context.Context, limit int) ([]espn.SyncRun, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, sync_type, league_id, team_id, season, started_at,
		       COALESCE(completed_at, started_at), status, warning_count,
		       scoring_period_id, effective_next_day,
		       COALESCE(summary_json, '{}')
		FROM espn_sync_runs
		ORDER BY id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("query espn sync runs: %w", err)
	}
	defer rows.Close()

	out := []espn.SyncRun{}
	for rows.Next() {
		var row espn.SyncRun
		var scoringPeriodID sql.NullInt64
		var effectiveNextDay int
		var startedAtRaw, completedAtRaw string
		if err := rows.Scan(&row.ID, &row.SyncType, &row.LeagueID, &row.TeamID, &row.Season, &startedAtRaw, &completedAtRaw, &row.Status, &row.WarningCount, &scoringPeriodID, &effectiveNextDay, &row.SummaryJSON); err != nil {
			return nil, fmt.Errorf("scan espn sync run row: %w", err)
		}
		startedAt, err := time.Parse(time.RFC3339, startedAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse espn sync run started_at: %w", err)
		}
		completedAt, err := time.Parse(time.RFC3339, completedAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse espn sync run completed_at: %w", err)
		}
		row.StartedAt = startedAt
		row.CompletedAt = completedAt
		if scoringPeriodID.Valid {
			v := int(scoringPeriodID.Int64)
			row.ScoringPeriodID = &v
		}
		row.EffectiveNextDay = effectiveNextDay == 1
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate espn sync runs: %w", err)
	}
	return out, nil
}

func (r *Repository) LatestRoster(ctx context.Context, syncRunID *int64, pitchersOnly bool) ([]espn.RosterSnapshot, error) {
	return r.LatestRosterForContext(ctx, syncRunID, nil, false, pitchersOnly)
}

func (r *Repository) LatestRosterForContext(ctx context.Context, syncRunID *int64, scoringPeriodID *int, effectiveNextDay bool, pitchersOnly bool) ([]espn.RosterSnapshot, error) {
	where := "sync_run_id = COALESCE(?, (SELECT id FROM espn_sync_runs WHERE " + syncContextSubqueryWhere(scoringPeriodID, effectiveNextDay) + " ORDER BY id DESC LIMIT 1))"
	args := []any{nullInt64(syncRunID)}
	if scoringPeriodID != nil {
		args = append(args, *scoringPeriodID)
	}
	if pitchersOnly {
		where += " AND is_pitcher = 1"
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, sync_run_id, espn_player_id, player_name, normalized_name,
		       COALESCE(mlb_team, ''), COALESCE(roster_slot, ''), is_pitcher,
		       COALESCE(role, ''), COALESCE(status_tag, ''),
		       COALESCE(raw_player_json, '{}'), created_at
		FROM espn_roster_snapshots
		WHERE `+where+`
		ORDER BY player_name ASC
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("query espn roster snapshot: %w", err)
	}
	defer rows.Close()

	out := []espn.RosterSnapshot{}
	for rows.Next() {
		var row espn.RosterSnapshot
		var playerID sql.NullInt64
		var isPitcher int
		var createdAtRaw string
		if err := rows.Scan(&row.ID, &row.SyncRunID, &playerID, &row.PlayerName, &row.NormalizedName, &row.MLBTeam, &row.RosterSlot, &isPitcher, &row.Role, &row.StatusTag, &row.RawPlayerJSON, &createdAtRaw); err != nil {
			return nil, fmt.Errorf("scan espn roster row: %w", err)
		}
		if playerID.Valid {
			v := playerID.Int64
			row.ESPNPlayerID = &v
		}
		row.IsPitcher = isPitcher == 1
		tm, err := time.Parse(time.RFC3339, createdAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse espn roster created_at: %w", err)
		}
		row.CreatedAt = tm
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate espn roster rows: %w", err)
	}
	return out, nil
}

func (r *Repository) LatestLeague(ctx context.Context, syncRunID *int64) (*espn.LeagueSnapshot, error) {
	return r.LatestLeagueForContext(ctx, syncRunID, nil, false)
}

func (r *Repository) LatestLeagueForContext(ctx context.Context, syncRunID *int64, scoringPeriodID *int, effectiveNextDay bool) (*espn.LeagueSnapshot, error) {
	where := "sync_run_id = COALESCE(?, (SELECT id FROM espn_sync_runs WHERE " + syncContextSubqueryWhere(scoringPeriodID, effectiveNextDay) + " ORDER BY id DESC LIMIT 1))"
	args := []any{nullInt64(syncRunID)}
	if scoringPeriodID != nil {
		args = append(args, *scoringPeriodID)
	}
	row := r.db.QueryRowContext(ctx, `
		SELECT sync_run_id, league_id, season, COALESCE(league_name, ''),
		       team_id, COALESCE(team_name, ''), COALESCE(scoring_type, ''),
		       COALESCE(settings_json, '{}'), created_at
		FROM espn_league_snapshots
		WHERE `+where+`
		ORDER BY id DESC
		LIMIT 1
	`, args...)
	var out espn.LeagueSnapshot
	var createdAtRaw string
	if err := row.Scan(&out.SyncRunID, &out.LeagueID, &out.Season, &out.LeagueName, &out.TeamID, &out.TeamName, &out.ScoringType, &out.SettingsJSON, &createdAtRaw); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("query espn league snapshot: %w", err)
	}
	tm, err := time.Parse(time.RFC3339, createdAtRaw)
	if err != nil {
		return nil, fmt.Errorf("parse espn league created_at: %w", err)
	}
	out.CreatedAt = tm
	return &out, nil
}

func (r *Repository) ListWarnings(ctx context.Context, syncRunID *int64, limit int) ([]espn.ParseWarning, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, sync_run_id, warning_type, message, COALESCE(row_context_json, '{}'), created_at
		FROM espn_parse_warnings
		WHERE sync_run_id = COALESCE(?, (SELECT id FROM espn_sync_runs ORDER BY id DESC LIMIT 1))
		ORDER BY id DESC
		LIMIT ?
	`, nullInt64(syncRunID), limit)
	if err != nil {
		return nil, fmt.Errorf("query espn parse warnings: %w", err)
	}
	defer rows.Close()

	out := []espn.ParseWarning{}
	for rows.Next() {
		var row espn.ParseWarning
		var createdAtRaw string
		if err := rows.Scan(&row.ID, &row.SyncRunID, &row.WarningType, &row.Message, &row.RowContextJSON, &createdAtRaw); err != nil {
			return nil, fmt.Errorf("scan espn parse warning row: %w", err)
		}
		tm, err := time.Parse(time.RFC3339, createdAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse espn parse warning created_at: %w", err)
		}
		row.CreatedAt = tm
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate espn parse warnings: %w", err)
	}
	return out, nil
}

func (r *Repository) LatestCandidateRun(ctx context.Context) (*espn.CandidateRun, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, sync_run_id, query_type, COALESCE(query_text, ''), COALESCE(filters_json, '{}'),
		       started_at, COALESCE(completed_at, started_at), status, candidate_count,
		       warning_count, COALESCE(summary_json, '{}')
		FROM espn_candidate_runs
		ORDER BY id DESC
		LIMIT 1
	`)
	return scanCandidateRunRow(row)
}

func (r *Repository) CandidateRunByID(ctx context.Context, candidateRunID int64) (*espn.CandidateRun, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, sync_run_id, query_type, COALESCE(query_text, ''), COALESCE(filters_json, '{}'),
		       started_at, COALESCE(completed_at, started_at), status, candidate_count,
		       warning_count, COALESCE(summary_json, '{}')
		FROM espn_candidate_runs
		WHERE id = ?
	`, candidateRunID)
	return scanCandidateRunRow(row)
}

func (r *Repository) ListCandidates(ctx context.Context, candidateRunID *int64, limit int) ([]espn.FreeAgentCandidate, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, candidate_run_id, espn_player_id, player_name, normalized_name,
		       COALESCE(mlb_team, ''), is_pitcher, COALESCE(role, ''), COALESCE(acquisition_status, ''), waiver_process_datetime, COALESCE(status_tag, ''),
		       COALESCE(raw_player_json, '{}'), created_at
		FROM espn_free_agent_candidates
		WHERE candidate_run_id = COALESCE(?, (SELECT id FROM espn_candidate_runs ORDER BY id DESC LIMIT 1))
		ORDER BY player_name ASC
		LIMIT ?
	`, nullInt64(candidateRunID), limit)
	if err != nil {
		return nil, fmt.Errorf("query espn free agent candidates: %w", err)
	}
	defer rows.Close()

	out := []espn.FreeAgentCandidate{}
	for rows.Next() {
		var c espn.FreeAgentCandidate
		var playerID sql.NullInt64
		var isPitcher int
		var waiverProcess sql.NullString
		var createdAtRaw string
		if err := rows.Scan(&c.ID, &c.CandidateRunID, &playerID, &c.PlayerName, &c.NormalizedName, &c.MLBTeam, &isPitcher, &c.Role, &c.AcquisitionStatus, &waiverProcess, &c.StatusTag, &c.RawPlayerJSON, &createdAtRaw); err != nil {
			return nil, fmt.Errorf("scan espn free agent candidate: %w", err)
		}
		if playerID.Valid {
			v := playerID.Int64
			c.ESPNPlayerID = &v
		}
		c.IsPitcher = isPitcher == 1
		if waiverProcess.Valid {
			v := waiverProcess.String
			c.WaiverProcessDatetime = &v
		}
		tm, err := time.Parse(time.RFC3339, createdAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse espn free agent candidate created_at: %w", err)
		}
		c.CreatedAt = tm
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate espn free agent candidates: %w", err)
	}
	return out, nil
}

func scanCandidateRunRow(row *sql.Row) (*espn.CandidateRun, error) {
	var out espn.CandidateRun
	var syncRunID sql.NullInt64
	var startedAtRaw, completedAtRaw string
	if err := row.Scan(&out.ID, &syncRunID, &out.QueryType, &out.QueryText, &out.FiltersJSON, &startedAtRaw, &completedAtRaw, &out.Status, &out.CandidateCount, &out.WarningCount, &out.SummaryJSON); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("query espn candidate run: %w", err)
	}
	if syncRunID.Valid {
		v := syncRunID.Int64
		out.SyncRunID = &v
	}
	startedAt, err := time.Parse(time.RFC3339, startedAtRaw)
	if err != nil {
		return nil, fmt.Errorf("parse espn candidate run started_at: %w", err)
	}
	completedAt, err := time.Parse(time.RFC3339, completedAtRaw)
	if err != nil {
		return nil, fmt.Errorf("parse espn candidate run completed_at: %w", err)
	}
	out.StartedAt = startedAt
	out.CompletedAt = completedAt
	return &out, nil
}

func nullInt64(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

func nullInt(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}

func nullStringPtr(v *string) any {
	if v == nil || *v == "" {
		return nil
	}
	return *v
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func syncContextWhere(scoringPeriodID *int, effectiveNextDay bool) (string, []any) {
	if scoringPeriodID != nil {
		return "sync_type = 'roster' AND scoring_period_id = ? AND effective_next_day = ?", []any{*scoringPeriodID, boolInt(effectiveNextDay)}
	}
	if effectiveNextDay {
		return "sync_type = 'roster' AND effective_next_day = 1", nil
	}
	return "sync_type = 'roster' AND scoring_period_id IS NULL AND effective_next_day = ?", []any{boolInt(effectiveNextDay)}
}

func syncContextSubqueryWhere(scoringPeriodID *int, effectiveNextDay bool) string {
	if scoringPeriodID != nil {
		return "sync_type = 'roster' AND scoring_period_id = ? AND effective_next_day = " + fmt.Sprintf("%d", boolInt(effectiveNextDay))
	}
	if effectiveNextDay {
		return "sync_type = 'roster' AND effective_next_day = 1"
	}
	return "sync_type = 'roster' AND scoring_period_id IS NULL AND effective_next_day = " + fmt.Sprintf("%d", boolInt(effectiveNextDay))
}
