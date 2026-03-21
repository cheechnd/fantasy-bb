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

type CreateAttemptInput struct {
	ApprovedItemID     int64
	SourcePlanID       int64
	PreflightRunID     *int64
	ExecutionStatus    execute.ExecutionStatus
	VerificationStatus execute.VerificationStatus
	AddPlayerName      string
	DropPlayerName     string
	RequestSummary     map[string]any
	Details            map[string]any
}

func (r *Repository) CreateAttempt(ctx context.Context, in CreateAttemptInput) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	reqJSON := toJSON(in.RequestSummary, "{}")
	detailsJSON := toJSON(in.Details, "{}")
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO execution_attempts (
			approved_item_id, source_plan_id, preflight_run_id,
			started_at, execution_status, verification_status,
			add_player_name, drop_player_name, request_summary_json, details_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, in.ApprovedItemID, in.SourcePlanID, nullInt64(in.PreflightRunID), now, in.ExecutionStatus, in.VerificationStatus, in.AddPlayerName, in.DropPlayerName, reqJSON, detailsJSON)
	if err != nil {
		return 0, fmt.Errorf("insert execution attempt: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("execution attempt id: %w", err)
	}
	return id, nil
}

func (r *Repository) AddEvent(ctx context.Context, attemptID int64, eventType string, data map[string]any) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO execution_attempt_events (execution_attempt_id, event_type, event_data_json, created_at)
		VALUES (?, ?, ?, ?)
	`, attemptID, eventType, toJSON(data, "{}"), time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("insert execution attempt event: %w", err)
	}
	return nil
}

type CompleteInput struct {
	ExecutionStatus    execute.ExecutionStatus
	VerificationStatus execute.VerificationStatus
	ResponseSummary    map[string]any
	ErrorMessage       string
	Details            map[string]any
}

func (r *Repository) CompleteAttempt(ctx context.Context, attemptID int64, in CompleteInput) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE execution_attempts
		SET completed_at = ?, execution_status = ?, verification_status = ?,
		    response_summary_json = ?, error_message = ?, details_json = ?
		WHERE id = ?
	`, time.Now().UTC().Format(time.RFC3339), in.ExecutionStatus, in.VerificationStatus, toJSON(in.ResponseSummary, "{}"), in.ErrorMessage, toJSON(in.Details, "{}"), attemptID)
	if err != nil {
		return fmt.Errorf("update execution attempt: %w", err)
	}
	return nil
}

func (r *Repository) AttemptByID(ctx context.Context, attemptID int64) (*execute.Attempt, []execute.AttemptEvent, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, approved_item_id, source_plan_id, preflight_run_id, started_at, completed_at,
		       execution_status, verification_status, add_player_name, drop_player_name,
		       COALESCE(request_summary_json, '{}'), COALESCE(response_summary_json, '{}'),
		       COALESCE(error_message, ''), COALESCE(details_json, '{}')
		FROM execution_attempts
		WHERE id = ?
	`, attemptID)
	attempt, err := scanAttempt(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	events, err := r.EventsByAttemptID(ctx, attemptID)
	if err != nil {
		return nil, nil, err
	}
	return attempt, events, nil
}

func (r *Repository) ListAttempts(ctx context.Context, limit int) ([]execute.Attempt, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, approved_item_id, source_plan_id, preflight_run_id, started_at, completed_at,
		       execution_status, verification_status, add_player_name, drop_player_name,
		       COALESCE(request_summary_json, '{}'), COALESCE(response_summary_json, '{}'),
		       COALESCE(error_message, ''), COALESCE(details_json, '{}')
		FROM execution_attempts
		ORDER BY id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("query execution attempts: %w", err)
	}
	defer rows.Close()

	out := []execute.Attempt{}
	for rows.Next() {
		a, err := scanAttemptRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate execution attempts: %w", err)
	}
	return out, nil
}

func (r *Repository) LatestAttempt(ctx context.Context) (*execute.Attempt, []execute.AttemptEvent, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, approved_item_id, source_plan_id, preflight_run_id, started_at, completed_at,
		       execution_status, verification_status, add_player_name, drop_player_name,
		       COALESCE(request_summary_json, '{}'), COALESCE(response_summary_json, '{}'),
		       COALESCE(error_message, ''), COALESCE(details_json, '{}')
		FROM execution_attempts
		ORDER BY id DESC
		LIMIT 1
	`)
	a, err := scanAttempt(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	ev, err := r.EventsByAttemptID(ctx, a.ID)
	if err != nil {
		return nil, nil, err
	}
	return a, ev, nil
}

func (r *Repository) EventsByAttemptID(ctx context.Context, attemptID int64) ([]execute.AttemptEvent, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, execution_attempt_id, event_type, COALESCE(event_data_json, '{}'), created_at
		FROM execution_attempt_events
		WHERE execution_attempt_id = ?
		ORDER BY id ASC
	`, attemptID)
	if err != nil {
		return nil, fmt.Errorf("query execution attempt events: %w", err)
	}
	defer rows.Close()
	out := []execute.AttemptEvent{}
	for rows.Next() {
		var ev execute.AttemptEvent
		var dataJSON, createdRaw string
		if err := rows.Scan(&ev.ID, &ev.ExecutionAttemptID, &ev.EventType, &dataJSON, &createdRaw); err != nil {
			return nil, fmt.Errorf("scan execution attempt event: %w", err)
		}
		_ = json.Unmarshal([]byte(dataJSON), &ev.EventData)
		tm, err := time.Parse(time.RFC3339, createdRaw)
		if err != nil {
			return nil, fmt.Errorf("parse execution attempt event created_at: %w", err)
		}
		ev.CreatedAt = tm
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate execution attempt events: %w", err)
	}
	return out, nil
}

func (r *Repository) HasSuccessfulAttempt(ctx context.Context, approvedItemID int64) (bool, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT 1
		FROM execution_attempts
		WHERE approved_item_id = ? AND execution_status = ?
		ORDER BY id DESC
		LIMIT 1
	`, approvedItemID, execute.ExecutionStatusSucceeded)
	var v int
	if err := row.Scan(&v); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("query successful execution attempt: %w", err)
	}
	return true, nil
}

func scanAttempt(row *sql.Row) (*execute.Attempt, error) {
	a, err := scanAttemptRows(row)
	if err != nil {
		return nil, err
	}
	return a, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanAttemptRows(r rowScanner) (*execute.Attempt, error) {
	var out execute.Attempt
	var preflightRunID sql.NullInt64
	var startedRaw string
	var completedRaw sql.NullString
	var reqJSON, respJSON, detailsJSON string
	if err := r.Scan(
		&out.ID, &out.ApprovedItemID, &out.SourcePlanID, &preflightRunID, &startedRaw, &completedRaw,
		&out.ExecutionStatus, &out.VerificationStatus, &out.AddPlayerName, &out.DropPlayerName,
		&reqJSON, &respJSON, &out.ErrorMessage, &detailsJSON,
	); err != nil {
		return nil, err
	}
	if preflightRunID.Valid {
		v := preflightRunID.Int64
		out.PreflightRunID = &v
	}
	startedAt, err := time.Parse(time.RFC3339, startedRaw)
	if err != nil {
		return nil, fmt.Errorf("parse execution attempt started_at: %w", err)
	}
	out.StartedAt = startedAt
	if completedRaw.Valid && completedRaw.String != "" {
		tm, err := time.Parse(time.RFC3339, completedRaw.String)
		if err != nil {
			return nil, fmt.Errorf("parse execution attempt completed_at: %w", err)
		}
		out.CompletedAt = &tm
	}
	_ = json.Unmarshal([]byte(reqJSON), &out.RequestSummary)
	_ = json.Unmarshal([]byte(respJSON), &out.ResponseSummary)
	_ = json.Unmarshal([]byte(detailsJSON), &out.Details)
	return &out, nil
}

func toJSON(v map[string]any, fallback string) string {
	if len(v) == 0 {
		return fallback
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fallback
	}
	return string(b)
}

func nullInt64(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}
