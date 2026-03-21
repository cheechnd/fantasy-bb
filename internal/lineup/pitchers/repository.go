package pitchers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"fantasy-baseball/internal/execute"
)

type Repository struct{ db *sql.DB }

func NewRepository(db *sql.DB) *Repository { return &Repository{db: db} }

func (r *Repository) SavePlan(ctx context.Context, pitcherPlanID, syncRunID *int64, status string, summary map[string]any, items []PlanItem) (int64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin lineup plan tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)
	res, err := tx.ExecContext(ctx, `
		INSERT INTO lineup_plans (pitcher_plan_id, sync_run_id, created_at, status, summary_json)
		VALUES (?, ?, ?, ?, ?)
	`, nullInt64(pitcherPlanID), nullInt64(syncRunID), now, status, toJSON(summary, "{}"))
	if err != nil {
		return 0, fmt.Errorf("insert lineup plan: %w", err)
	}
	planID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("lineup plan id: %w", err)
	}

	for _, item := range items {
		res, err := tx.ExecContext(ctx, `
			INSERT INTO lineup_plan_items (lineup_plan_id, action_type, player_name, espn_player_id, current_slot, target_slot, rationale_json, flags_json, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, planID, item.ActionType, item.PlayerName, nullInt64(item.ESPNPlayerID), item.CurrentSlot, item.TargetSlot, toJSON(item.Rationale, "{}"), toJSON(item.Flags, "[]"), now)
		if err != nil {
			return 0, fmt.Errorf("insert lineup plan item: %w", err)
		}
		itemID, err := res.LastInsertId()
		if err != nil {
			return 0, fmt.Errorf("lineup plan item id: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO lineup_review_states (lineup_plan_item_id, current_state, note, updated_at)
			VALUES (?, ?, '', ?)
		`, itemID, ReviewStatePending, now); err != nil {
			return 0, fmt.Errorf("insert lineup review state: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO lineup_review_history (lineup_plan_item_id, previous_state, new_state, note, changed_at)
			VALUES (?, ?, ?, ?, ?)
		`, itemID, ReviewStatePending, ReviewStatePending, "created", now); err != nil {
			return 0, fmt.Errorf("insert lineup review history: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit lineup plan tx: %w", err)
	}
	return planID, nil
}

func (r *Repository) LatestPlan(ctx context.Context) (*Plan, []PlanItem, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, pitcher_plan_id, sync_run_id, created_at, status, COALESCE(summary_json, '{}')
		FROM lineup_plans ORDER BY id DESC LIMIT 1
	`)
	p, err := scanPlan(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	items, err := r.PlanItems(ctx, p.ID)
	if err != nil {
		return nil, nil, err
	}
	return p, items, nil
}

func (r *Repository) PlanByID(ctx context.Context, planID int64) (*Plan, []PlanItem, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, pitcher_plan_id, sync_run_id, created_at, status, COALESCE(summary_json, '{}')
		FROM lineup_plans WHERE id = ?
	`, planID)
	p, err := scanPlan(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	items, err := r.PlanItems(ctx, p.ID)
	if err != nil {
		return nil, nil, err
	}
	return p, items, nil
}

func (r *Repository) PlanItems(ctx context.Context, planID int64) ([]PlanItem, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, lineup_plan_id, action_type, player_name, espn_player_id,
		       COALESCE(current_slot, ''), COALESCE(target_slot, ''),
		       COALESCE(rationale_json, '{}'), COALESCE(flags_json, '[]'), created_at
		FROM lineup_plan_items WHERE lineup_plan_id = ? ORDER BY id ASC
	`, planID)
	if err != nil {
		return nil, fmt.Errorf("query lineup plan items: %w", err)
	}
	defer rows.Close()
	out := make([]PlanItem, 0)
	for rows.Next() {
		var it PlanItem
		var espnID sql.NullInt64
		var rationaleJSON, flagsJSON, createdRaw string
		if err := rows.Scan(&it.ID, &it.LineupPlanID, &it.ActionType, &it.PlayerName, &espnID, &it.CurrentSlot, &it.TargetSlot, &rationaleJSON, &flagsJSON, &createdRaw); err != nil {
			return nil, fmt.Errorf("scan lineup plan item: %w", err)
		}
		if espnID.Valid {
			v := espnID.Int64
			it.ESPNPlayerID = &v
		}
		_ = json.Unmarshal([]byte(rationaleJSON), &it.Rationale)
		_ = json.Unmarshal([]byte(flagsJSON), &it.Flags)
		tm, err := time.Parse(time.RFC3339, createdRaw)
		if err != nil {
			return nil, fmt.Errorf("parse lineup plan item created_at: %w", err)
		}
		it.CreatedAt = tm
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate lineup plan items: %w", err)
	}
	return out, nil
}

func (r *Repository) TransitionState(ctx context.Context, planID, itemID int64, target ReviewState, note string) (*ReviewDecision, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT rs.current_state
		FROM lineup_review_states rs
		JOIN lineup_plan_items i ON i.id = rs.lineup_plan_item_id
		WHERE rs.lineup_plan_item_id = ? AND i.lineup_plan_id = ?
	`, itemID, planID)
	var current ReviewState
	if err := row.Scan(&current); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("lineup item %d not found in plan %d", itemID, planID)
		}
		return nil, fmt.Errorf("load lineup state: %w", err)
	}
	if current == target {
		return nil, fmt.Errorf("lineup item %d already in state %s", itemID, target)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin lineup state tx: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE lineup_review_states SET current_state=?, note=?, updated_at=? WHERE lineup_plan_item_id=?`, target, note, now, itemID); err != nil {
		return nil, fmt.Errorf("update lineup state: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO lineup_review_history (lineup_plan_item_id, previous_state, new_state, note, changed_at) VALUES (?, ?, ?, ?, ?)`, itemID, current, target, note, now); err != nil {
		return nil, fmt.Errorf("insert lineup history: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit lineup state tx: %w", err)
	}
	tm, _ := time.Parse(time.RFC3339, now)
	return &ReviewDecision{LineupPlanItemID: itemID, PlanID: planID, PreviousState: current, NewState: target, Note: note, ChangedAt: tm}, nil
}

func (r *Repository) ReviewedItems(ctx context.Context, planID int64) ([]ReviewedPlanItem, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT i.id, i.lineup_plan_id, i.action_type, i.player_name, i.espn_player_id,
		       COALESCE(i.current_slot, ''), COALESCE(i.target_slot, ''),
		       COALESCE(i.rationale_json, '{}'), COALESCE(i.flags_json, '[]'), i.created_at,
		       rs.current_state, COALESCE(rs.note, ''), rs.updated_at
		FROM lineup_plan_items i
		JOIN lineup_review_states rs ON rs.lineup_plan_item_id = i.id
		WHERE i.lineup_plan_id = ?
		ORDER BY i.id ASC
	`, planID)
	if err != nil {
		return nil, fmt.Errorf("query reviewed lineup items: %w", err)
	}
	defer rows.Close()
	out := make([]ReviewedPlanItem, 0)
	for rows.Next() {
		var it ReviewedPlanItem
		var espnID sql.NullInt64
		var rationaleJSON, flagsJSON, createdRaw, updatedRaw string
		if err := rows.Scan(&it.ID, &it.LineupPlanID, &it.ActionType, &it.PlayerName, &espnID, &it.CurrentSlot, &it.TargetSlot, &rationaleJSON, &flagsJSON, &createdRaw, &it.ReviewState, &it.ReviewNote, &updatedRaw); err != nil {
			return nil, fmt.Errorf("scan reviewed lineup item: %w", err)
		}
		if espnID.Valid {
			v := espnID.Int64
			it.ESPNPlayerID = &v
		}
		_ = json.Unmarshal([]byte(rationaleJSON), &it.Rationale)
		_ = json.Unmarshal([]byte(flagsJSON), &it.Flags)
		c, err := time.Parse(time.RFC3339, createdRaw)
		if err != nil {
			return nil, fmt.Errorf("parse lineup item created_at: %w", err)
		}
		u, err := time.Parse(time.RFC3339, updatedRaw)
		if err != nil {
			return nil, fmt.Errorf("parse lineup review updated_at: %w", err)
		}
		it.CreatedAt = c
		it.ReviewUpdated = u
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate reviewed lineup items: %w", err)
	}
	return out, nil
}

func (r *Repository) Queue(ctx context.Context, limit int) ([]QueueItem, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT i.id, i.lineup_plan_id, i.action_type, i.player_name,
		       COALESCE(i.current_slot, ''), COALESCE(i.target_slot, ''),
		       COALESCE(rs.note, ''), rs.updated_at, rs.current_state
		FROM lineup_plan_items i
		JOIN lineup_review_states rs ON rs.lineup_plan_item_id = i.id
		WHERE rs.current_state = ?
		ORDER BY rs.updated_at DESC, i.id DESC
		LIMIT ?
	`, ReviewStateApproved, limit)
	if err != nil {
		return nil, fmt.Errorf("query lineup queue: %w", err)
	}
	defer rows.Close()
	out := make([]QueueItem, 0)
	for rows.Next() {
		var q QueueItem
		var approvedRaw string
		if err := rows.Scan(&q.LineupPlanItemID, &q.PlanID, &q.ActionType, &q.PlayerName, &q.CurrentSlot, &q.TargetSlot, &q.Note, &approvedRaw, &q.State); err != nil {
			return nil, fmt.Errorf("scan lineup queue row: %w", err)
		}
		tm, err := time.Parse(time.RFC3339, approvedRaw)
		if err != nil {
			return nil, fmt.Errorf("parse lineup queue approved_at: %w", err)
		}
		q.ApprovedAt = tm
		out = append(out, q)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate lineup queue: %w", err)
	}
	return out, nil
}

func (r *Repository) ReviewedItemByID(ctx context.Context, itemID int64) (*ReviewedPlanItem, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT i.id, i.lineup_plan_id, i.action_type, i.player_name, i.espn_player_id,
		       COALESCE(i.current_slot, ''), COALESCE(i.target_slot, ''),
		       COALESCE(i.rationale_json, '{}'), COALESCE(i.flags_json, '[]'), i.created_at,
		       rs.current_state, COALESCE(rs.note, ''), rs.updated_at
		FROM lineup_plan_items i
		JOIN lineup_review_states rs ON rs.lineup_plan_item_id = i.id
		WHERE i.id = ?
	`, itemID)
	var it ReviewedPlanItem
	var espnID sql.NullInt64
	var rationaleJSON, flagsJSON, createdRaw, updatedRaw string
	if err := row.Scan(&it.ID, &it.LineupPlanID, &it.ActionType, &it.PlayerName, &espnID, &it.CurrentSlot, &it.TargetSlot, &rationaleJSON, &flagsJSON, &createdRaw, &it.ReviewState, &it.ReviewNote, &updatedRaw); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scan lineup reviewed item: %w", err)
	}
	if espnID.Valid {
		v := espnID.Int64
		it.ESPNPlayerID = &v
	}
	_ = json.Unmarshal([]byte(rationaleJSON), &it.Rationale)
	_ = json.Unmarshal([]byte(flagsJSON), &it.Flags)
	c, err := time.Parse(time.RFC3339, createdRaw)
	if err != nil {
		return nil, fmt.Errorf("parse lineup reviewed item created_at: %w", err)
	}
	u, err := time.Parse(time.RFC3339, updatedRaw)
	if err != nil {
		return nil, fmt.Errorf("parse lineup reviewed item updated_at: %w", err)
	}
	it.CreatedAt = c
	it.ReviewUpdated = u
	return &it, nil
}

func (r *Repository) CreateExecutionAttempt(ctx context.Context, itemID, planID int64, status execute.ExecutionStatus, ver execute.VerificationStatus, req, details map[string]any) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO lineup_execution_attempts (approved_lineup_item_id, lineup_plan_id, started_at, execution_status, verification_status, request_summary_json, details_json)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, itemID, planID, now, status, ver, toJSON(req, "{}"), toJSON(details, "{}"))
	if err != nil {
		return 0, fmt.Errorf("insert lineup execution attempt: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("lineup execution attempt id: %w", err)
	}
	return id, nil
}

func (r *Repository) AddExecutionEvent(ctx context.Context, attemptID int64, eventType string, data map[string]any) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO lineup_execution_attempt_events (lineup_execution_attempt_id, event_type, event_data_json, created_at) VALUES (?, ?, ?, ?)`, attemptID, eventType, toJSON(data, "{}"), time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("insert lineup execution event: %w", err)
	}
	return nil
}

func (r *Repository) CompleteExecutionAttempt(ctx context.Context, attemptID int64, status execute.ExecutionStatus, ver execute.VerificationStatus, resp, details map[string]any, errMsg string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE lineup_execution_attempts
		SET completed_at = ?, execution_status = ?, verification_status = ?, response_summary_json = ?, details_json = ?, error_message = ?
		WHERE id = ?
	`, time.Now().UTC().Format(time.RFC3339), status, ver, toJSON(resp, "{}"), toJSON(details, "{}"), errMsg, attemptID)
	if err != nil {
		return fmt.Errorf("complete lineup execution attempt: %w", err)
	}
	return nil
}

func (r *Repository) LatestExecution(ctx context.Context) (*ExecutionAttempt, error) {
	rows, err := r.ListExecutionHistory(ctx, 1)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	a, _, err := r.ExecutionByID(ctx, rows[0].ID)
	return a, err
}

func (r *Repository) ListExecutionHistory(ctx context.Context, limit int) ([]ExecutionAttempt, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, approved_lineup_item_id, lineup_plan_id, started_at, completed_at, execution_status, verification_status,
		       COALESCE(request_summary_json, '{}'), COALESCE(response_summary_json, '{}'), COALESCE(error_message, ''), COALESCE(details_json, '{}')
		FROM lineup_execution_attempts
		ORDER BY id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("query lineup execution history: %w", err)
	}
	defer rows.Close()
	out := make([]ExecutionAttempt, 0)
	for rows.Next() {
		a, err := scanExecutionAttempt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate lineup execution history: %w", err)
	}
	return out, nil
}

func (r *Repository) ExecutionByID(ctx context.Context, executionID int64) (*ExecutionAttempt, []ExecutionEvent, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, approved_lineup_item_id, lineup_plan_id, started_at, completed_at, execution_status, verification_status,
		       COALESCE(request_summary_json, '{}'), COALESCE(response_summary_json, '{}'), COALESCE(error_message, ''), COALESCE(details_json, '{}')
		FROM lineup_execution_attempts
		WHERE id = ?
	`, executionID)
	a, err := scanExecutionAttempt(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	events, err := r.ExecutionEvents(ctx, executionID)
	if err != nil {
		return nil, nil, err
	}
	a.Events = events
	return a, events, nil
}

func (r *Repository) ExecutionEvents(ctx context.Context, executionID int64) ([]ExecutionEvent, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, lineup_execution_attempt_id, event_type, COALESCE(event_data_json, '{}'), created_at
		FROM lineup_execution_attempt_events
		WHERE lineup_execution_attempt_id = ?
		ORDER BY id ASC
	`, executionID)
	if err != nil {
		return nil, fmt.Errorf("query lineup execution events: %w", err)
	}
	defer rows.Close()
	out := make([]ExecutionEvent, 0)
	for rows.Next() {
		var ev ExecutionEvent
		var dataJSON, createdRaw string
		if err := rows.Scan(&ev.ID, &ev.AttemptID, &ev.EventType, &dataJSON, &createdRaw); err != nil {
			return nil, fmt.Errorf("scan lineup execution event: %w", err)
		}
		_ = json.Unmarshal([]byte(dataJSON), &ev.EventData)
		tm, err := time.Parse(time.RFC3339, createdRaw)
		if err != nil {
			return nil, fmt.Errorf("parse lineup execution event created_at: %w", err)
		}
		ev.CreatedAt = tm
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate lineup execution events: %w", err)
	}
	return out, nil
}

func (r *Repository) HasSuccessfulExecutionForItem(ctx context.Context, itemID int64) (bool, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT 1 FROM lineup_execution_attempts
		WHERE approved_lineup_item_id = ? AND execution_status = ?
		ORDER BY id DESC LIMIT 1
	`, itemID, execute.ExecutionStatusSucceeded)
	var one int
	if err := row.Scan(&one); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("query successful lineup execution: %w", err)
	}
	return true, nil
}

func (r *Repository) LatestExecutionByItem(ctx context.Context, itemID int64) (*ExecutionAttempt, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, approved_lineup_item_id, lineup_plan_id, started_at, completed_at, execution_status, verification_status,
		       COALESCE(request_summary_json, '{}'), COALESCE(response_summary_json, '{}'), COALESCE(error_message, ''), COALESCE(details_json, '{}')
		FROM lineup_execution_attempts
		WHERE approved_lineup_item_id = ?
		ORDER BY id DESC LIMIT 1
	`, itemID)
	a, err := scanExecutionAttempt(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return a, nil
}

func scanPlan(row *sql.Row) (*Plan, error) {
	var p Plan
	var pitcherPlanID, syncRunID sql.NullInt64
	var createdRaw, summaryJSON string
	if err := row.Scan(&p.ID, &pitcherPlanID, &syncRunID, &createdRaw, &p.Status, &summaryJSON); err != nil {
		return nil, err
	}
	if pitcherPlanID.Valid {
		v := pitcherPlanID.Int64
		p.PitcherPlanID = &v
	}
	if syncRunID.Valid {
		v := syncRunID.Int64
		p.SyncRunID = &v
	}
	if err := json.Unmarshal([]byte(summaryJSON), &p.Summary); err != nil {
		p.Summary = map[string]any{}
	}
	tm, err := time.Parse(time.RFC3339, createdRaw)
	if err != nil {
		return nil, fmt.Errorf("parse lineup plan created_at: %w", err)
	}
	p.CreatedAt = tm
	return &p, nil
}

type scanner interface{ Scan(dest ...any) error }

func scanExecutionAttempt(s scanner) (*ExecutionAttempt, error) {
	var a ExecutionAttempt
	var completedRaw sql.NullString
	var reqJSON, respJSON, detailsJSON, startedRaw, errMsg string
	if err := s.Scan(&a.ID, &a.ApprovedLineupItemID, &a.LineupPlanID, &startedRaw, &completedRaw, &a.ExecutionStatus, &a.VerificationStatus, &reqJSON, &respJSON, &errMsg, &detailsJSON); err != nil {
		return nil, err
	}
	st, err := time.Parse(time.RFC3339, startedRaw)
	if err != nil {
		return nil, fmt.Errorf("parse lineup execution started_at: %w", err)
	}
	a.StartedAt = st
	if completedRaw.Valid && completedRaw.String != "" {
		ct, err := time.Parse(time.RFC3339, completedRaw.String)
		if err != nil {
			return nil, fmt.Errorf("parse lineup execution completed_at: %w", err)
		}
		a.CompletedAt = &ct
	}
	a.ErrorMessage = errMsg
	_ = json.Unmarshal([]byte(reqJSON), &a.RequestSummary)
	_ = json.Unmarshal([]byte(respJSON), &a.ResponseSummary)
	_ = json.Unmarshal([]byte(detailsJSON), &a.Details)
	return &a, nil
}

func nullInt64(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}
func toJSON(v any, fallback string) string {
	if v == nil {
		return fallback
	}
	b, err := json.Marshal(v)
	if err != nil || len(b) == 0 {
		return fallback
	}
	return string(b)
}
