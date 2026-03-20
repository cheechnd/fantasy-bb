package planner

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) SavePlan(ctx context.Context, in CreateInput) (int64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin pitcher plan tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)
	summaryJSON := "{}"
	if len(in.Summary) > 0 {
		b, _ := json.Marshal(in.Summary)
		summaryJSON = string(b)
	}

	res, err := tx.ExecContext(ctx, `
		INSERT INTO pitcher_plans (
			sync_run_id, import_run_id, analysis_run_id,
			window_start, window_end, created_at, status, plan_summary_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, nullInt64(in.SyncRunID), nullInt64(in.ImportRunID), nullInt64(in.AnalysisRunID), in.WindowStart, in.WindowEnd, now, in.Status, summaryJSON)
	if err != nil {
		return 0, fmt.Errorf("insert pitcher_plan: %w", err)
	}
	planID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("pitcher plan id: %w", err)
	}

	for _, item := range in.Items {
		flagsJSON, _ := json.Marshal(item.Flags)
		notesJSON, _ := json.Marshal(item.Notes)
		detailsJSON, _ := json.Marshal(item.Details)
		_, err := tx.ExecContext(ctx, `
			INSERT INTO pitcher_plan_items (
				plan_id, bucket, player_name, mlb_team, espn_player_id,
				matched_pitcher_name, projected_start_count, total_projected_fpts,
				result_rank, flags_json, notes_json, details_json, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, planID, item.Bucket, item.PlayerName, item.MLBTeam, nullInt64(item.ESPNPlayerID), item.MatchedPitcherName, item.ProjectedStartCount, item.TotalProjectedFPTS, nullInt(item.ResultRank), string(flagsJSON), string(notesJSON), string(detailsJSON), now)
		if err != nil {
			return 0, fmt.Errorf("insert pitcher_plan_item: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit pitcher plan tx: %w", err)
	}
	return planID, nil
}

func (r *Repository) LatestPlan(ctx context.Context) (*Plan, []PlanItem, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, sync_run_id, import_run_id, analysis_run_id,
		       window_start, window_end, created_at, status,
		       COALESCE(plan_summary_json, '{}')
		FROM pitcher_plans
		ORDER BY id DESC
		LIMIT 1
	`)
	plan, err := scanPlanRow(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	items, err := r.PlanItems(ctx, plan.ID)
	if err != nil {
		return nil, nil, err
	}
	return plan, items, nil
}

func (r *Repository) PlanByID(ctx context.Context, planID int64) (*Plan, []PlanItem, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, sync_run_id, import_run_id, analysis_run_id,
		       window_start, window_end, created_at, status,
		       COALESCE(plan_summary_json, '{}')
		FROM pitcher_plans
		WHERE id = ?
	`, planID)
	plan, err := scanPlanRow(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	items, err := r.PlanItems(ctx, plan.ID)
	if err != nil {
		return nil, nil, err
	}
	return plan, items, nil
}

func (r *Repository) PlanItems(ctx context.Context, planID int64) ([]PlanItem, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, plan_id, bucket, player_name, COALESCE(mlb_team, ''), espn_player_id,
		       COALESCE(matched_pitcher_name, ''), projected_start_count,
		       total_projected_fpts, result_rank,
		       COALESCE(flags_json, '[]'), COALESCE(notes_json, '[]'),
		       COALESCE(details_json, '{}'), created_at
		FROM pitcher_plan_items
		WHERE plan_id = ?
		ORDER BY CASE bucket
			WHEN 'auto_start' THEN 1
			WHEN 'likely_start' THEN 2
			WHEN 'monitor' THEN 3
			WHEN 'bench' THEN 4
			ELSE 5
		END, COALESCE(result_rank, 999999), player_name ASC
	`, planID)
	if err != nil {
		return nil, fmt.Errorf("query pitcher plan items: %w", err)
	}
	defer rows.Close()

	out := []PlanItem{}
	for rows.Next() {
		var item PlanItem
		var espnID sql.NullInt64
		var total sql.NullFloat64
		var rank sql.NullInt64
		var flagsJSON string
		var notesJSON string
		var detailsJSON string
		var createdAtRaw string
		if err := rows.Scan(&item.ID, &item.PlanID, &item.Bucket, &item.PlayerName, &item.MLBTeam, &espnID, &item.MatchedPitcherName, &item.ProjectedStartCount, &total, &rank, &flagsJSON, &notesJSON, &detailsJSON, &createdAtRaw); err != nil {
			return nil, fmt.Errorf("scan pitcher plan item: %w", err)
		}
		if espnID.Valid {
			v := espnID.Int64
			item.ESPNPlayerID = &v
		}
		if total.Valid {
			v := total.Float64
			item.TotalProjectedFPTS = &v
		}
		if rank.Valid {
			v := int(rank.Int64)
			item.ResultRank = &v
		}
		_ = json.Unmarshal([]byte(flagsJSON), &item.Flags)
		_ = json.Unmarshal([]byte(notesJSON), &item.Notes)
		_ = json.Unmarshal([]byte(detailsJSON), &item.Details)
		tm, err := time.Parse(time.RFC3339, createdAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse pitcher plan item created_at: %w", err)
		}
		item.CreatedAt = tm
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pitcher plan items: %w", err)
	}
	return out, nil
}

func scanPlanRow(row *sql.Row) (*Plan, error) {
	var out Plan
	var syncRun sql.NullInt64
	var importRun sql.NullInt64
	var analysisRun sql.NullInt64
	var createdAtRaw string
	if err := row.Scan(&out.ID, &syncRun, &importRun, &analysisRun, &out.WindowStart, &out.WindowEnd, &createdAtRaw, &out.Status, &out.SummaryJSON); err != nil {
		return nil, err
	}
	if syncRun.Valid {
		v := syncRun.Int64
		out.SyncRunID = &v
	}
	if importRun.Valid {
		v := importRun.Int64
		out.ImportRunID = &v
	}
	if analysisRun.Valid {
		v := analysisRun.Int64
		out.AnalysisRunID = &v
	}
	_ = json.Unmarshal([]byte(out.SummaryJSON), &out.Summary)
	tm, err := time.Parse(time.RFC3339, createdAtRaw)
	if err != nil {
		return nil, fmt.Errorf("parse pitcher plan created_at: %w", err)
	}
	out.CreatedAt = tm
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
