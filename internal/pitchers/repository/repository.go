package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"fantasy-baseball/internal/pitchers"
)

type Repository struct {
	db *sql.DB
}

func New(db *sql.DB) *Repository {
	return &Repository{db: db}
}

type AnalysisRunRow struct {
	ID           int64
	AnalysisType string
	ImportRunID  *int64
	RosterPath   string
	WindowStart  string
	WindowEnd    string
	CreatedAt    time.Time
	Status       string
	SummaryJSON  string
}

type CreateRunInput struct {
	AnalysisType string
	ImportRunID  *int64
	RosterPath   string
	WindowStart  string
	WindowEnd    string
	Status       string
	Summary      map[string]any
}

type ResultInput struct {
	ResultType          string
	PlayerName          string
	MLBTeam             string
	MatchedPitcherName  string
	ProjectedStartCount int
	TotalProjectedFPTS  *float64
	ResultRank          *int
	Flags               []string
	Details             map[string]any
}

func (r *Repository) SaveRun(ctx context.Context, run CreateRunInput, matchResults []pitchers.MatchResult, results []ResultInput) (int64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin analysis tx: %w", err)
	}
	defer tx.Rollback()

	summaryJSON := "{}"
	if len(run.Summary) > 0 {
		b, _ := json.Marshal(run.Summary)
		summaryJSON = string(b)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := tx.ExecContext(ctx, `
		INSERT INTO analysis_runs (
			analysis_type, import_run_id, roster_source_path,
			window_start, window_end, created_at, status, summary_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, run.AnalysisType, run.ImportRunID, run.RosterPath, run.WindowStart, run.WindowEnd, now, run.Status, summaryJSON)
	if err != nil {
		return 0, fmt.Errorf("insert analysis_run: %w", err)
	}
	runID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("analysis run id: %w", err)
	}

	for _, m := range matchResults {
		matchedJSON, _ := json.Marshal(map[string]any{
			"matched_pitcher_name": m.MatchedPitcherName,
			"matched_pitcher_team": m.MatchedPitcherTeam,
			"candidates":           m.CandidateDisplayList,
		})
		_, err := tx.ExecContext(ctx, `
			INSERT INTO player_match_results (
				analysis_run_id, input_player_name, input_mlb_team,
				normalized_lookup_key, match_status, matched_entity_json,
				explanation, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, runID, m.InputPlayerName, m.InputMLBTeam, m.NormalizedLookupKey, m.MatchStatus, string(matchedJSON), m.Explanation, now)
		if err != nil {
			return 0, fmt.Errorf("insert player_match_result: %w", err)
		}
	}

	for _, result := range results {
		flagsJSON, _ := json.Marshal(result.Flags)
		detailsJSON, _ := json.Marshal(result.Details)
		_, err := tx.ExecContext(ctx, `
			INSERT INTO analysis_results (
				analysis_run_id, result_type, player_name, mlb_team,
				matched_pitcher_name, projected_start_count, total_projected_fpts,
				result_rank, flags_json, details_json, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, runID, result.ResultType, result.PlayerName, result.MLBTeam, result.MatchedPitcherName, result.ProjectedStartCount, result.TotalProjectedFPTS, result.ResultRank, string(flagsJSON), string(detailsJSON), now)
		if err != nil {
			return 0, fmt.Errorf("insert analysis_result: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit analysis tx: %w", err)
	}
	return runID, nil
}

func (r *Repository) LatestRun(ctx context.Context) (*AnalysisRunRow, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, analysis_type, import_run_id, roster_source_path,
		       window_start, window_end, created_at, status, COALESCE(summary_json, '{}')
		FROM analysis_runs ORDER BY id DESC LIMIT 1
	`)
	var out AnalysisRunRow
	var importRun sql.NullInt64
	var createdAtRaw string
	if err := row.Scan(&out.ID, &out.AnalysisType, &importRun, &out.RosterPath, &out.WindowStart, &out.WindowEnd, &createdAtRaw, &out.Status, &out.SummaryJSON); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("query latest analysis run: %w", err)
	}
	if importRun.Valid {
		v := importRun.Int64
		out.ImportRunID = &v
	}
	tm, err := time.Parse(time.RFC3339, createdAtRaw)
	if err != nil {
		return nil, fmt.Errorf("parse latest analysis created_at: %w", err)
	}
	out.CreatedAt = tm
	return &out, nil
}

type AnalysisResultRow struct {
	ResultType          string
	PlayerName          string
	MLBTeam             string
	MatchedPitcherName  string
	ProjectedStartCount int
	TotalProjectedFPTS  *float64
	ResultRank          *int
	FlagsJSON           string
	DetailsJSON         string
}

func (r *Repository) ResultsByRun(ctx context.Context, runID int64) ([]AnalysisResultRow, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT result_type, player_name, COALESCE(mlb_team, ''), COALESCE(matched_pitcher_name, ''),
		       projected_start_count, total_projected_fpts, result_rank,
		       COALESCE(flags_json, '[]'), COALESCE(details_json, '{}')
		FROM analysis_results
		WHERE analysis_run_id = ?
		ORDER BY COALESCE(result_rank, 999999), player_name ASC
	`, runID)
	if err != nil {
		return nil, fmt.Errorf("query analysis results: %w", err)
	}
	defer rows.Close()
	out := []AnalysisResultRow{}
	for rows.Next() {
		var row AnalysisResultRow
		var total sql.NullFloat64
		var rank sql.NullInt64
		if err := rows.Scan(&row.ResultType, &row.PlayerName, &row.MLBTeam, &row.MatchedPitcherName, &row.ProjectedStartCount, &total, &rank, &row.FlagsJSON, &row.DetailsJSON); err != nil {
			return nil, fmt.Errorf("scan analysis result row: %w", err)
		}
		if total.Valid {
			v := total.Float64
			row.TotalProjectedFPTS = &v
		}
		if rank.Valid {
			v := int(rank.Int64)
			row.ResultRank = &v
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate analysis results: %w", err)
	}
	return out, nil
}
