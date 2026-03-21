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

type CreateInput struct {
	RequestedAddPlayerName  string
	RequestedDropPlayerName string
	NormalizedAddLookup     string
	NormalizedDropLookup    string
}

type ResolveInput struct {
	ID                       int64
	RequestState             transactions.AdHocRequestState
	ResolutionStatus         transactions.AdHocResolutionStatus
	ResolvedAddPlayerName    string
	ResolvedAddESPNPlayerID  *int64
	ResolvedDropPlayerName   string
	ResolvedDropESPNPlayerID *int64
	ResolutionNotes          map[string]any
}

func (r *Repository) Create(ctx context.Context, in CreateInput) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO ad_hoc_transaction_requests (
			requested_add_player_name, requested_drop_player_name,
			normalized_add_lookup, normalized_drop_lookup,
			request_state, resolution_status, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, in.RequestedAddPlayerName, in.RequestedDropPlayerName, in.NormalizedAddLookup, in.NormalizedDropLookup, transactions.AdHocStateCreated, transactions.AdHocResolutionUnresolved, now, now)
	if err != nil {
		return 0, fmt.Errorf("insert ad hoc request: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("ad hoc request id: %w", err)
	}
	return id, nil
}

func (r *Repository) Resolve(ctx context.Context, in ResolveInput) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE ad_hoc_transaction_requests
		SET request_state = ?, resolution_status = ?,
		    resolved_add_player_name = ?, resolved_add_espn_player_id = ?,
		    resolved_drop_player_name = ?, resolved_drop_espn_player_id = ?,
		    resolution_notes_json = ?, updated_at = ?
		WHERE id = ?
	`, in.RequestState, in.ResolutionStatus, in.ResolvedAddPlayerName, nullInt64(in.ResolvedAddESPNPlayerID), in.ResolvedDropPlayerName, nullInt64(in.ResolvedDropESPNPlayerID), toJSON(in.ResolutionNotes, "{}"), time.Now().UTC().Format(time.RFC3339), in.ID)
	if err != nil {
		return fmt.Errorf("update ad hoc resolution: %w", err)
	}
	return nil
}

func (r *Repository) LinkExecutionCandidate(ctx context.Context, requestID int64, planID, planItemID *int64, state transactions.AdHocRequestState) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE ad_hoc_transaction_requests
		SET linked_plan_id = COALESCE(?, linked_plan_id),
		    linked_plan_item_id = COALESCE(?, linked_plan_item_id),
		    request_state = ?, updated_at = ?
		WHERE id = ?
	`, nullInt64(planID), nullInt64(planItemID), state, time.Now().UTC().Format(time.RFC3339), requestID)
	if err != nil {
		return fmt.Errorf("update ad hoc execution candidate link: %w", err)
	}
	return nil
}

func (r *Repository) LinkExecutionAttempt(ctx context.Context, requestID, attemptID int64, state transactions.AdHocRequestState) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE ad_hoc_transaction_requests
		SET linked_execution_attempt_id = ?, request_state = ?, updated_at = ?
		WHERE id = ?
	`, attemptID, state, time.Now().UTC().Format(time.RFC3339), requestID)
	if err != nil {
		return fmt.Errorf("update ad hoc execution attempt link: %w", err)
	}
	return nil
}

func (r *Repository) ByID(ctx context.Context, requestID int64) (*transactions.AdHocRequest, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, requested_add_player_name, requested_drop_player_name,
		       normalized_add_lookup, normalized_drop_lookup,
		       COALESCE(resolved_add_player_name,''), resolved_add_espn_player_id,
		       COALESCE(resolved_drop_player_name,''), resolved_drop_espn_player_id,
		       request_state, resolution_status, COALESCE(resolution_notes_json,'{}'),
		       linked_plan_id, linked_plan_item_id, linked_execution_attempt_id,
		       created_at, updated_at
		FROM ad_hoc_transaction_requests
		WHERE id = ?
	`, requestID)
	return scanAdHocRequest(row)
}

func (r *Repository) List(ctx context.Context, limit int, state *transactions.AdHocRequestState) ([]transactions.AdHocRequest, error) {
	if limit <= 0 {
		limit = 25
	}
	query := `
		SELECT id, requested_add_player_name, requested_drop_player_name,
		       normalized_add_lookup, normalized_drop_lookup,
		       COALESCE(resolved_add_player_name,''), resolved_add_espn_player_id,
		       COALESCE(resolved_drop_player_name,''), resolved_drop_espn_player_id,
		       request_state, resolution_status, COALESCE(resolution_notes_json,'{}'),
		       linked_plan_id, linked_plan_item_id, linked_execution_attempt_id,
		       created_at, updated_at
		FROM ad_hoc_transaction_requests
	`
	args := []any{}
	if state != nil {
		query += ` WHERE request_state = ?`
		args = append(args, *state)
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query ad hoc requests: %w", err)
	}
	defer rows.Close()
	out := make([]transactions.AdHocRequest, 0)
	for rows.Next() {
		item, err := scanAdHocRequestRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate ad hoc requests: %w", err)
	}
	return out, nil
}

func (r *Repository) AddEvent(ctx context.Context, requestID int64, eventType string, eventData map[string]any) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO ad_hoc_transaction_request_events (request_id, event_type, event_data_json, created_at)
		VALUES (?, ?, ?, ?)
	`, requestID, eventType, toJSON(eventData, "{}"), time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("insert ad hoc request event: %w", err)
	}
	return nil
}

func nullInt64(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
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

type rowScanner interface {
	Scan(dest ...any) error
}

func scanAdHocRequest(row *sql.Row) (*transactions.AdHocRequest, error) {
	return scanAdHocRequestRows(row)
}

func scanAdHocRequestRows(r rowScanner) (*transactions.AdHocRequest, error) {
	var out transactions.AdHocRequest
	var addID, dropID sql.NullInt64
	var linkedPlanID, linkedPlanItemID, linkedExecID sql.NullInt64
	var notesJSON, createdRaw, updatedRaw string
	if err := r.Scan(
		&out.ID, &out.RequestedAddPlayerName, &out.RequestedDropPlayerName,
		&out.NormalizedAddLookup, &out.NormalizedDropLookup,
		&out.ResolvedAddPlayerName, &addID,
		&out.ResolvedDropPlayerName, &dropID,
		&out.RequestState, &out.ResolutionStatus, &notesJSON,
		&linkedPlanID, &linkedPlanItemID, &linkedExecID,
		&createdRaw, &updatedRaw,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if addID.Valid {
		v := addID.Int64
		out.ResolvedAddESPNPlayerID = &v
	}
	if dropID.Valid {
		v := dropID.Int64
		out.ResolvedDropESPNPlayerID = &v
	}
	if linkedPlanID.Valid {
		v := linkedPlanID.Int64
		out.LinkedPlanID = &v
	}
	if linkedPlanItemID.Valid {
		v := linkedPlanItemID.Int64
		out.LinkedPlanItemID = &v
	}
	if linkedExecID.Valid {
		v := linkedExecID.Int64
		out.LinkedExecutionAttemptID = &v
	}
	_ = json.Unmarshal([]byte(notesJSON), &out.ResolutionNotes)
	createdAt, err := time.Parse(time.RFC3339, createdRaw)
	if err != nil {
		return nil, fmt.Errorf("parse ad hoc request created_at: %w", err)
	}
	updatedAt, err := time.Parse(time.RFC3339, updatedRaw)
	if err != nil {
		return nil, fmt.Errorf("parse ad hoc request updated_at: %w", err)
	}
	out.CreatedAt = createdAt
	out.UpdatedAt = updatedAt
	return &out, nil
}

