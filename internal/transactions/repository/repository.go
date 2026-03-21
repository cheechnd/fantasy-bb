package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"fantasy-baseball/internal/transactions"
)

type Repository struct {
	db *sql.DB
}

func New(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) SavePlan(ctx context.Context, in transactions.CreatePlanInput) (int64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin transaction plan tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)
	summaryJSON := "{}"
	if len(in.Summary) > 0 {
		b, _ := json.Marshal(in.Summary)
		summaryJSON = string(b)
	}
	res, err := tx.ExecContext(ctx, `
		INSERT INTO transaction_plans (
			sync_run_id, import_run_id, pitcher_plan_id, pickup_recommendation_run_id,
			window_start, window_end, created_at, status, plan_summary_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, nullInt64(in.SyncRunID), nullInt64(in.ImportRunID), nullInt64(in.PitcherPlanID), nullInt64(in.PickupRecommendationRunID), in.WindowStart, in.WindowEnd, now, in.Status, summaryJSON)
	if err != nil {
		return 0, fmt.Errorf("insert transaction plan: %w", err)
	}
	planID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("transaction plan id: %w", err)
	}

	for _, item := range in.Items {
		flagsJSON, _ := json.Marshal(item.Flags)
		notesJSON, _ := json.Marshal(item.Notes)
		detailsJSON, _ := json.Marshal(item.Details)
		resItem, err := tx.ExecContext(ctx, `
			INSERT INTO transaction_plan_items (
				transaction_plan_id, bucket, add_player_name, add_player_team, add_espn_player_id,
				drop_player_name, drop_player_team, drop_espn_player_id,
				add_projected_start_count, add_total_projected_fpts,
				drop_projected_start_count, drop_total_projected_fpts,
				delta_fpts, result_rank, flags_json, notes_json, details_json, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, planID, item.Bucket, item.AddPlayerName, item.AddPlayerTeam, nullInt64(item.AddESPNPlayerID), item.DropPlayerName, item.DropPlayerTeam, nullInt64(item.DropESPNPlayerID), item.AddProjectedStartCount, item.AddTotalProjectedFPTS, item.DropProjectedStartCount, item.DropTotalProjectedFPTS, item.DeltaFPTS, nullInt(item.ResultRank), string(flagsJSON), string(notesJSON), string(detailsJSON), now)
		if err != nil {
			return 0, fmt.Errorf("insert transaction plan item: %w", err)
		}
		itemID, err := resItem.LastInsertId()
		if err != nil {
			return 0, fmt.Errorf("transaction plan item id: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO transaction_review_states (transaction_plan_item_id, current_state, note, updated_at)
			VALUES (?, 'pending', '', ?)
		`, itemID, now); err != nil {
			return 0, fmt.Errorf("insert default transaction review state: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO transaction_review_history (transaction_plan_item_id, previous_state, new_state, note, changed_at)
			VALUES (?, 'pending', 'pending', 'initial state', ?)
		`, itemID, now); err != nil {
			return 0, fmt.Errorf("insert initial transaction review history: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit transaction plan tx: %w", err)
	}
	return planID, nil
}

func (r *Repository) LatestPlan(ctx context.Context) (*transactions.Plan, []transactions.PlanItem, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, sync_run_id, import_run_id, pitcher_plan_id, pickup_recommendation_run_id,
		       window_start, window_end, created_at, status, COALESCE(plan_summary_json, '{}')
		FROM transaction_plans
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

func (r *Repository) PlanByID(ctx context.Context, planID int64) (*transactions.Plan, []transactions.PlanItem, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, sync_run_id, import_run_id, pitcher_plan_id, pickup_recommendation_run_id,
		       window_start, window_end, created_at, status, COALESCE(plan_summary_json, '{}')
		FROM transaction_plans
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

func (r *Repository) PlanItems(ctx context.Context, planID int64) ([]transactions.PlanItem, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, transaction_plan_id, bucket,
		       add_player_name, COALESCE(add_player_team, ''), add_espn_player_id,
		       drop_player_name, COALESCE(drop_player_team, ''), drop_espn_player_id,
		       add_projected_start_count, add_total_projected_fpts,
		       drop_projected_start_count, drop_total_projected_fpts,
		       delta_fpts, result_rank, COALESCE(flags_json, '[]'), COALESCE(notes_json, '[]'),
		       COALESCE(details_json, '{}'), created_at
		FROM transaction_plan_items
		WHERE transaction_plan_id = ?
		ORDER BY CASE bucket
			WHEN 'strong_move' THEN 1
			WHEN 'marginal_move' THEN 2
			WHEN 'risky_move' THEN 3
			ELSE 4
		END, COALESCE(result_rank, 999999), add_player_name ASC, drop_player_name ASC
	`, planID)
	if err != nil {
		return nil, fmt.Errorf("query transaction plan items: %w", err)
	}
	defer rows.Close()

	out := []transactions.PlanItem{}
	for rows.Next() {
		var item transactions.PlanItem
		var addID, dropID sql.NullInt64
		var addTotal, dropTotal, delta sql.NullFloat64
		var rank sql.NullInt64
		var flagsJSON, notesJSON, detailsJSON, createdAtRaw string
		if err := rows.Scan(
			&item.ID, &item.TransactionPlanID, &item.Bucket,
			&item.AddPlayerName, &item.AddPlayerTeam, &addID,
			&item.DropPlayerName, &item.DropPlayerTeam, &dropID,
			&item.AddProjectedStartCount, &addTotal,
			&item.DropProjectedStartCount, &dropTotal,
			&delta, &rank, &flagsJSON, &notesJSON, &detailsJSON, &createdAtRaw,
		); err != nil {
			return nil, fmt.Errorf("scan transaction plan item: %w", err)
		}
		if addID.Valid {
			v := addID.Int64
			item.AddESPNPlayerID = &v
		}
		if dropID.Valid {
			v := dropID.Int64
			item.DropESPNPlayerID = &v
		}
		if addTotal.Valid {
			v := addTotal.Float64
			item.AddTotalProjectedFPTS = &v
		}
		if dropTotal.Valid {
			v := dropTotal.Float64
			item.DropTotalProjectedFPTS = &v
		}
		if delta.Valid {
			v := delta.Float64
			item.DeltaFPTS = &v
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
			return nil, fmt.Errorf("parse transaction plan item created_at: %w", err)
		}
		item.CreatedAt = tm
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate transaction plan items: %w", err)
	}
	return out, nil
}

func scanPlanRow(row *sql.Row) (*transactions.Plan, error) {
	var out transactions.Plan
	var syncRunID, importRunID, pitcherPlanID, pickupRunID sql.NullInt64
	var createdAtRaw string
	if err := row.Scan(&out.ID, &syncRunID, &importRunID, &pitcherPlanID, &pickupRunID, &out.WindowStart, &out.WindowEnd, &createdAtRaw, &out.Status, &out.SummaryJSON); err != nil {
		return nil, err
	}
	if syncRunID.Valid {
		v := syncRunID.Int64
		out.SyncRunID = &v
	}
	if importRunID.Valid {
		v := importRunID.Int64
		out.ImportRunID = &v
	}
	if pitcherPlanID.Valid {
		v := pitcherPlanID.Int64
		out.PitcherPlanID = &v
	}
	if pickupRunID.Valid {
		v := pickupRunID.Int64
		out.PickupRecommendationRunID = &v
	}
	_ = json.Unmarshal([]byte(out.SummaryJSON), &out.Summary)
	tm, err := time.Parse(time.RFC3339, createdAtRaw)
	if err != nil {
		return nil, fmt.Errorf("parse transaction plan created_at: %w", err)
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
