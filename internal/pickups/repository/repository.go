package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"fantasy-baseball/internal/pickups"
)

type Repository struct {
	db *sql.DB
}

func New(db *sql.DB) *Repository {
	return &Repository{db: db}
}

type CreateRecommendationInput struct {
	SyncRunID      *int64
	ImportRunID    *int64
	CandidateRunID *int64
	WindowStart    string
	WindowEnd      string
	Status         string
	Summary        map[string]any
	Items          []pickups.RecommendationItem
}

func (r *Repository) SaveRecommendation(ctx context.Context, in CreateRecommendationInput) (int64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin pickup recommendation tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)
	summaryJSON := "{}"
	if len(in.Summary) > 0 {
		b, _ := json.Marshal(in.Summary)
		summaryJSON = string(b)
	}
	res, err := tx.ExecContext(ctx, `
		INSERT INTO pickup_recommendation_runs (
			sync_run_id, import_run_id, candidate_run_id,
			window_start, window_end, created_at, status, summary_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, nullInt64(in.SyncRunID), nullInt64(in.ImportRunID), nullInt64(in.CandidateRunID), in.WindowStart, in.WindowEnd, now, in.Status, summaryJSON)
	if err != nil {
		return 0, fmt.Errorf("insert pickup recommendation run: %w", err)
	}
	runID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("pickup recommendation run id: %w", err)
	}

	for _, item := range in.Items {
		flagsJSON, _ := json.Marshal(item.Flags)
		notesJSON, _ := json.Marshal(item.Notes)
		detailsJSON, _ := json.Marshal(item.Details)
		_, err := tx.ExecContext(ctx, `
			INSERT INTO pickup_recommendation_items (
				recommendation_run_id, item_type, player_name, mlb_team,
				espn_player_id, matched_pitcher_name, projected_start_count,
				total_projected_fpts, comparison_target_name, comparison_delta_fpts,
				result_rank, flags_json, notes_json, details_json, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, runID, item.ItemType, item.PlayerName, item.MLBTeam, nullInt64(item.ESPNPlayerID), item.MatchedPitcherName, item.ProjectedStartCount, item.TotalProjectedFPTS, item.ComparisonTargetName, item.ComparisonDeltaFPTS, nullInt(item.ResultRank), string(flagsJSON), string(notesJSON), string(detailsJSON), now)
		if err != nil {
			return 0, fmt.Errorf("insert pickup recommendation item: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit pickup recommendation tx: %w", err)
	}
	return runID, nil
}

func (r *Repository) LatestRecommendation(ctx context.Context) (*pickups.RecommendationRun, []pickups.RecommendationItem, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, sync_run_id, import_run_id, candidate_run_id,
		       window_start, window_end, created_at, status,
		       COALESCE(summary_json, '{}')
		FROM pickup_recommendation_runs
		ORDER BY id DESC
		LIMIT 1
	`)
	run, err := scanRecommendationRun(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	items, err := r.RecommendationItems(ctx, run.ID)
	if err != nil {
		return nil, nil, err
	}
	return run, items, nil
}

func (r *Repository) LatestRecommendationForSources(ctx context.Context, syncRunID, importRunID *int64) (*pickups.RecommendationRun, []pickups.RecommendationItem, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, sync_run_id, import_run_id, candidate_run_id,
		       window_start, window_end, created_at, status,
		       COALESCE(summary_json, '{}')
		FROM pickup_recommendation_runs
		WHERE (? IS NULL OR sync_run_id = ?)
		  AND (? IS NULL OR import_run_id = ?)
		ORDER BY id DESC
		LIMIT 1
	`, nullInt64(syncRunID), nullInt64(syncRunID), nullInt64(importRunID), nullInt64(importRunID))
	run, err := scanRecommendationRun(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	items, err := r.RecommendationItems(ctx, run.ID)
	if err != nil {
		return nil, nil, err
	}
	return run, items, nil
}

func (r *Repository) RecommendationByID(ctx context.Context, runID int64) (*pickups.RecommendationRun, []pickups.RecommendationItem, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, sync_run_id, import_run_id, candidate_run_id,
		       window_start, window_end, created_at, status,
		       COALESCE(summary_json, '{}')
		FROM pickup_recommendation_runs
		WHERE id = ?
	`, runID)
	run, err := scanRecommendationRun(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	items, err := r.RecommendationItems(ctx, run.ID)
	if err != nil {
		return nil, nil, err
	}
	return run, items, nil
}

func (r *Repository) RecommendationItems(ctx context.Context, runID int64) ([]pickups.RecommendationItem, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, recommendation_run_id, item_type, player_name,
		       COALESCE(mlb_team, ''), espn_player_id,
		       COALESCE(matched_pitcher_name, ''), projected_start_count,
		       total_projected_fpts, COALESCE(comparison_target_name, ''), comparison_delta_fpts,
		       result_rank, COALESCE(flags_json, '[]'), COALESCE(notes_json, '[]'),
		       COALESCE(details_json, '{}'), created_at
		FROM pickup_recommendation_items
		WHERE recommendation_run_id = ?
		ORDER BY COALESCE(result_rank, 999999), player_name ASC
	`, runID)
	if err != nil {
		return nil, fmt.Errorf("query pickup recommendation items: %w", err)
	}
	defer rows.Close()

	out := []pickups.RecommendationItem{}
	for rows.Next() {
		var item pickups.RecommendationItem
		var playerID sql.NullInt64
		var total sql.NullFloat64
		var delta sql.NullFloat64
		var rank sql.NullInt64
		var flagsJSON, notesJSON, detailsJSON, createdAtRaw string
		if err := rows.Scan(&item.ID, &item.RecommendationRunID, &item.ItemType, &item.PlayerName, &item.MLBTeam, &playerID, &item.MatchedPitcherName, &item.ProjectedStartCount, &total, &item.ComparisonTargetName, &delta, &rank, &flagsJSON, &notesJSON, &detailsJSON, &createdAtRaw); err != nil {
			return nil, fmt.Errorf("scan pickup recommendation item: %w", err)
		}
		if playerID.Valid {
			v := playerID.Int64
			item.ESPNPlayerID = &v
		}
		if total.Valid {
			v := total.Float64
			item.TotalProjectedFPTS = &v
		}
		if delta.Valid {
			v := delta.Float64
			item.ComparisonDeltaFPTS = &v
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
			return nil, fmt.Errorf("parse pickup recommendation item created_at: %w", err)
		}
		item.CreatedAt = tm
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pickup recommendation items: %w", err)
	}
	return out, nil
}

func scanRecommendationRun(row *sql.Row) (*pickups.RecommendationRun, error) {
	var out pickups.RecommendationRun
	var syncRun, importRun, candidateRun sql.NullInt64
	var createdAtRaw string
	if err := row.Scan(&out.ID, &syncRun, &importRun, &candidateRun, &out.WindowStart, &out.WindowEnd, &createdAtRaw, &out.Status, &out.SummaryJSON); err != nil {
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
	if candidateRun.Valid {
		v := candidateRun.Int64
		out.CandidateRunID = &v
	}
	tm, err := time.Parse(time.RFC3339, createdAtRaw)
	if err != nil {
		return nil, fmt.Errorf("parse pickup recommendation run created_at: %w", err)
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
