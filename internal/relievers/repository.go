package relievers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

type Repository struct{ db *sql.DB }

func NewRepository(db *sql.DB) *Repository { return &Repository{db: db} }

type SaveRunInput struct {
	Source     string
	SourceURL  string
	SourceDate string
	FetchedAt  time.Time
	Status     string
	Warnings   []string
	Summary    map[string]any
	RawHTML    string
	Entries    []DepthChartEntry
}

func (r *Repository) SaveRun(ctx context.Context, input SaveRunInput) (int64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin reliever depth chart tx: %w", err)
	}
	defer tx.Rollback()
	fetchedAt := input.FetchedAt
	if fetchedAt.IsZero() {
		fetchedAt = time.Now().UTC()
	}
	matched, unmatched, ambiguous, conflicts := countStatuses(input.Entries)
	warningsJSON := toJSON(input.Warnings, "[]")
	summaryJSON := toJSON(input.Summary, "{}")
	res, err := tx.ExecContext(ctx, `
		INSERT INTO reliever_depth_chart_runs (
			source, source_url, source_date, fetched_at, status, team_count, row_count,
			matched_count, unmatched_count, ambiguous_count, conflict_count,
			warnings_json, summary_json, raw_html
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, input.Source, input.SourceURL, input.SourceDate, fetchedAt.UTC().Format(time.RFC3339), input.Status, intFromSummary(input.Summary, "team_count"), len(input.Entries), matched, unmatched, ambiguous, conflicts, warningsJSON, summaryJSON, input.RawHTML)
	if err != nil {
		return 0, fmt.Errorf("insert reliever depth chart run: %w", err)
	}
	runID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("reliever depth chart run id: %w", err)
	}
	now := fetchedAt.UTC().Format(time.RFC3339)
	for _, e := range input.Entries {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO reliever_depth_chart_entries (
				run_id, espn_player_id, player_name, normalized_name, mlb_team,
				relief_role, source_role_label, roster_percent, match_status,
				match_reason, conflict_flag, conflict_reason, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, runID, nullInt64(e.ESPNPlayerID), e.PlayerName, e.NormalizedName, e.MLBTeam, e.ReliefRole, e.SourceRoleLabel, nullFloat(e.RosterPercent), e.MatchStatus, e.MatchReason, boolInt(e.ConflictFlag), e.ConflictReason, now)
		if err != nil {
			return 0, fmt.Errorf("insert reliever depth chart entry: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit reliever depth chart tx: %w", err)
	}
	return runID, nil
}

func (r *Repository) LatestRun(ctx context.Context) (*DepthChartRun, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, source, source_url, COALESCE(source_date, ''), fetched_at, status,
		       team_count, row_count, matched_count, unmatched_count, ambiguous_count,
		       conflict_count, COALESCE(warnings_json, '[]'), COALESCE(summary_json, '{}')
		FROM reliever_depth_chart_runs
		ORDER BY id DESC LIMIT 1
	`)
	return scanRun(row)
}

func (r *Repository) ListRuns(ctx context.Context, limit int) ([]DepthChartRun, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, source, source_url, COALESCE(source_date, ''), fetched_at, status,
		       team_count, row_count, matched_count, unmatched_count, ambiguous_count,
		       conflict_count, COALESCE(warnings_json, '[]'), COALESCE(summary_json, '{}')
		FROM reliever_depth_chart_runs
		ORDER BY id DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("query reliever depth chart runs: %w", err)
	}
	defer rows.Close()
	out := []DepthChartRun{}
	for rows.Next() {
		run, err := scanRunRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

func (r *Repository) Entries(ctx context.Context, runID *int64, limit int) ([]DepthChartEntry, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, run_id, espn_player_id, player_name, normalized_name, mlb_team,
		       relief_role, source_role_label, roster_percent, match_status,
		       COALESCE(match_reason, ''), conflict_flag, COALESCE(conflict_reason, ''), created_at
		FROM reliever_depth_chart_entries
		WHERE run_id = COALESCE(?, (SELECT id FROM reliever_depth_chart_runs ORDER BY id DESC LIMIT 1))
		ORDER BY mlb_team ASC, relief_role ASC, player_name ASC
		LIMIT ?
	`, nullInt64(runID), limit)
	if err != nil {
		return nil, fmt.Errorf("query reliever depth chart entries: %w", err)
	}
	defer rows.Close()
	out := []DepthChartEntry{}
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *Repository) LatestEntryByPlayer(ctx context.Context, playerID *int64, normalizedName, mlbTeam string) (*DepthChartEntry, *DepthChartRun, error) {
	latest, err := r.LatestRun(ctx)
	if err != nil || latest == nil {
		return nil, latest, err
	}
	where := "run_id = ? AND normalized_name = ? AND mlb_team = ?"
	args := []any{latest.ID, normalizedName, mlbTeam}
	if playerID != nil {
		where = "run_id = ? AND espn_player_id = ?"
		args = []any{latest.ID, *playerID}
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, run_id, espn_player_id, player_name, normalized_name, mlb_team,
		       relief_role, source_role_label, roster_percent, match_status,
		       COALESCE(match_reason, ''), conflict_flag, COALESCE(conflict_reason, ''), created_at
		FROM reliever_depth_chart_entries
		WHERE `+where+`
		ORDER BY id ASC
	`, args...)
	if err != nil {
		return nil, latest, fmt.Errorf("query latest reliever entry: %w", err)
	}
	defer rows.Close()
	entries := []DepthChartEntry{}
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, latest, err
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, latest, err
	}
	if len(entries) == 0 {
		return nil, latest, nil
	}
	if len(entries) > 1 {
		e := entries[0]
		e.MatchStatus = "ambiguous"
		e.MatchReason = "multiple latest reliever depth chart rows matched this player"
		e.ConflictFlag = true
		e.ConflictReason = "multiple_relief_roles"
		return &e, latest, nil
	}
	return &entries[0], latest, nil
}

func scanRun(row *sql.Row) (*DepthChartRun, error) {
	var out DepthChartRun
	var fetchedRaw string
	if err := row.Scan(&out.ID, &out.Source, &out.SourceURL, &out.SourceDate, &fetchedRaw, &out.Status, &out.TeamCount, &out.RowCount, &out.MatchedCount, &out.UnmatchedCount, &out.AmbiguousCount, &out.ConflictCount, &out.WarningsJSON, &out.SummaryJSON); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scan reliever depth chart run: %w", err)
	}
	fetchedAt, err := time.Parse(time.RFC3339, fetchedRaw)
	if err != nil {
		return nil, fmt.Errorf("parse reliever depth chart fetched_at: %w", err)
	}
	out.FetchedAt = fetchedAt
	return &out, nil
}

type runScanner interface{ Scan(dest ...any) error }

func scanRunRows(row runScanner) (DepthChartRun, error) {
	var out DepthChartRun
	var fetchedRaw string
	if err := row.Scan(&out.ID, &out.Source, &out.SourceURL, &out.SourceDate, &fetchedRaw, &out.Status, &out.TeamCount, &out.RowCount, &out.MatchedCount, &out.UnmatchedCount, &out.AmbiguousCount, &out.ConflictCount, &out.WarningsJSON, &out.SummaryJSON); err != nil {
		return out, fmt.Errorf("scan reliever depth chart run: %w", err)
	}
	fetchedAt, err := time.Parse(time.RFC3339, fetchedRaw)
	if err != nil {
		return out, fmt.Errorf("parse reliever depth chart fetched_at: %w", err)
	}
	out.FetchedAt = fetchedAt
	return out, nil
}

func scanEntry(row runScanner) (DepthChartEntry, error) {
	var e DepthChartEntry
	var playerID sql.NullInt64
	var pct sql.NullFloat64
	var conflict int
	var createdRaw string
	if err := row.Scan(&e.ID, &e.RunID, &playerID, &e.PlayerName, &e.NormalizedName, &e.MLBTeam, &e.ReliefRole, &e.SourceRoleLabel, &pct, &e.MatchStatus, &e.MatchReason, &conflict, &e.ConflictReason, &createdRaw); err != nil {
		return e, fmt.Errorf("scan reliever depth chart entry: %w", err)
	}
	if playerID.Valid {
		v := playerID.Int64
		e.ESPNPlayerID = &v
	}
	if pct.Valid {
		v := pct.Float64
		e.RosterPercent = &v
	}
	e.ConflictFlag = conflict == 1
	createdAt, err := time.Parse(time.RFC3339, createdRaw)
	if err != nil {
		return e, fmt.Errorf("parse reliever depth chart entry created_at: %w", err)
	}
	e.CreatedAt = createdAt
	return e, nil
}

func countStatuses(entries []DepthChartEntry) (matched, unmatched, ambiguous, conflicts int) {
	for _, e := range entries {
		switch e.MatchStatus {
		case "matched":
			matched++
		case "ambiguous":
			ambiguous++
		default:
			unmatched++
		}
		if e.ConflictFlag {
			conflicts++
		}
	}
	return
}

func toJSON(v any, fallback string) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fallback
	}
	return string(b)
}

func intFromSummary(summary map[string]any, key string) int {
	v, _ := summary[key].(int)
	return v
}

func nullInt64(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

func nullFloat(v *float64) any {
	if v == nil {
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
