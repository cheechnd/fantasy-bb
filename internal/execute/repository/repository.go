package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"fantasy-baseball/internal/execute"
)

type Repository struct {
	db *sql.DB
}

func New(db *sql.DB) *Repository {
	return &Repository{db: db}
}

type CreateRunInput struct {
	RunType execute.RunType
	Status  string
	Summary map[string]any
	Items   []execute.RunItem
}

func (r *Repository) SaveRun(ctx context.Context, in CreateRunInput) (int64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin execution run tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)
	summaryJSON := "{}"
	if len(in.Summary) > 0 {
		b, _ := json.Marshal(in.Summary)
		summaryJSON = string(b)
	}
	res, err := tx.ExecContext(ctx, `
		INSERT INTO execution_runs (run_type, created_at, status, item_count, summary_json)
		VALUES (?, ?, ?, ?, ?)
	`, in.RunType, now, in.Status, len(in.Items), summaryJSON)
	if err != nil {
		return 0, fmt.Errorf("insert execution run: %w", err)
	}
	runID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("execution run id: %w", err)
	}

	for _, item := range in.Items {
		reasonsJSON, _ := json.Marshal(item.ValidationReasons)
		previewJSON, _ := json.Marshal(item.ActionPreview)
		detailsJSON, _ := json.Marshal(item.Details)
		_, err := tx.ExecContext(ctx, `
			INSERT INTO execution_run_items (
				execution_run_id, approved_item_id, source_plan_id, add_player_name, drop_player_name,
				validation_status, readiness_rank, validation_reasons_json, action_preview_json, details_json, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, runID, item.ApprovedItemID, item.SourcePlanID, item.AddPlayerName, item.DropPlayerName, item.ValidationStatus, nullInt(item.ReadinessRank), string(reasonsJSON), string(previewJSON), string(detailsJSON), now)
		if err != nil {
			return 0, fmt.Errorf("insert execution run item: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit execution run tx: %w", err)
	}
	return runID, nil
}

func (r *Repository) LatestRun(ctx context.Context) (*execute.Run, []execute.RunItem, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, run_type, created_at, status, item_count, COALESCE(summary_json, '{}')
		FROM execution_runs
		ORDER BY id DESC
		LIMIT 1
	`)
	run, err := scanRun(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	items, err := r.RunItems(ctx, run.ID)
	if err != nil {
		return nil, nil, err
	}
	return run, items, nil
}

func (r *Repository) RunByID(ctx context.Context, runID int64) (*execute.Run, []execute.RunItem, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, run_type, created_at, status, item_count, COALESCE(summary_json, '{}')
		FROM execution_runs
		WHERE id = ?
	`, runID)
	run, err := scanRun(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	items, err := r.RunItems(ctx, run.ID)
	if err != nil {
		return nil, nil, err
	}
	return run, items, nil
}

func (r *Repository) RunItems(ctx context.Context, runID int64) ([]execute.RunItem, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, execution_run_id, approved_item_id, source_plan_id, add_player_name, drop_player_name,
		       validation_status, readiness_rank, COALESCE(validation_reasons_json, '[]'),
		       COALESCE(action_preview_json, '{}'), COALESCE(details_json, '{}'), created_at
		FROM execution_run_items
		WHERE execution_run_id = ?
		ORDER BY id ASC
	`, runID)
	if err != nil {
		return nil, fmt.Errorf("query execution run items: %w", err)
	}
	defer rows.Close()

	out := []execute.RunItem{}
	for rows.Next() {
		var item execute.RunItem
		var rank sql.NullInt64
		var reasonsJSON, previewJSON, detailsJSON, createdAtRaw string
		if err := rows.Scan(
			&item.ID, &item.ExecutionRunID, &item.ApprovedItemID, &item.SourcePlanID,
			&item.AddPlayerName, &item.DropPlayerName, &item.ValidationStatus, &rank,
			&reasonsJSON, &previewJSON, &detailsJSON, &createdAtRaw,
		); err != nil {
			return nil, fmt.Errorf("scan execution run item: %w", err)
		}
		if rank.Valid {
			v := int(rank.Int64)
			item.ReadinessRank = &v
		}
		_ = json.Unmarshal([]byte(reasonsJSON), &item.ValidationReasons)
		_ = json.Unmarshal([]byte(previewJSON), &item.ActionPreview)
		_ = json.Unmarshal([]byte(detailsJSON), &item.Details)
		tm, err := time.Parse(time.RFC3339, createdAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse execution run item created_at: %w", err)
		}
		item.CreatedAt = tm
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate execution run items: %w", err)
	}
	return out, nil
}

func (r *Repository) LatestResultByApprovedItem(ctx context.Context, approvedItemID int64) (*execute.RunItem, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT eri.id, eri.execution_run_id, eri.approved_item_id, eri.source_plan_id, eri.add_player_name, eri.drop_player_name,
		       eri.validation_status, eri.readiness_rank, COALESCE(eri.validation_reasons_json, '[]'),
		       COALESCE(eri.action_preview_json, '{}'), COALESCE(eri.details_json, '{}'), eri.created_at
		FROM execution_run_items eri
		INNER JOIN execution_runs er ON er.id = eri.execution_run_id
		WHERE eri.approved_item_id = ?
		ORDER BY er.id DESC, eri.id DESC
		LIMIT 1
	`, approvedItemID)
	var item execute.RunItem
	var rank sql.NullInt64
	var reasonsJSON, previewJSON, detailsJSON, createdAtRaw string
	if err := row.Scan(
		&item.ID, &item.ExecutionRunID, &item.ApprovedItemID, &item.SourcePlanID,
		&item.AddPlayerName, &item.DropPlayerName, &item.ValidationStatus, &rank,
		&reasonsJSON, &previewJSON, &detailsJSON, &createdAtRaw,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("query latest execution result by approved item: %w", err)
	}
	if rank.Valid {
		v := int(rank.Int64)
		item.ReadinessRank = &v
	}
	_ = json.Unmarshal([]byte(reasonsJSON), &item.ValidationReasons)
	_ = json.Unmarshal([]byte(previewJSON), &item.ActionPreview)
	_ = json.Unmarshal([]byte(detailsJSON), &item.Details)
	tm, err := time.Parse(time.RFC3339, createdAtRaw)
	if err != nil {
		return nil, fmt.Errorf("parse latest execution result created_at: %w", err)
	}
	item.CreatedAt = tm
	return &item, nil
}

func scanRun(row *sql.Row) (*execute.Run, error) {
	var run execute.Run
	var createdAtRaw string
	var summaryJSON string
	if err := row.Scan(&run.ID, &run.RunType, &createdAtRaw, &run.Status, &run.ItemCount, &summaryJSON); err != nil {
		return nil, err
	}
	tm, err := time.Parse(time.RFC3339, createdAtRaw)
	if err != nil {
		return nil, fmt.Errorf("parse execution run created_at: %w", err)
	}
	run.CreatedAt = tm
	_ = json.Unmarshal([]byte(summaryJSON), &run.Summary)
	return &run, nil
}

func nullInt(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}
