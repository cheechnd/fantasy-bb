package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"fantasy-baseball/internal/transactions"
	tranrepo "fantasy-baseball/internal/transactions/repository"
)

var (
	ErrPlanNotFound         = fmt.Errorf("transaction plan not found")
	ErrPlanItemNotFound     = fmt.Errorf("transaction plan item not found")
	ErrPlanItemPlanMismatch = fmt.Errorf("transaction plan item does not belong to plan")
)

type Repository struct {
	db        *sql.DB
	plansRepo *tranrepo.Repository
}

func New(db *sql.DB) *Repository {
	return &Repository{
		db:        db,
		plansRepo: tranrepo.New(db),
	}
}

func (r *Repository) ReviewByPlanID(ctx context.Context, planID int64) (*transactions.PlanReview, error) {
	plan, items, err := r.plansRepo.PlanByID(ctx, planID)
	if err != nil {
		return nil, err
	}
	if plan == nil {
		return nil, ErrPlanNotFound
	}

	stateByItem := map[int64]transactions.ReviewedPlanItem{}
	stateCounts, err := r.statesForPlan(ctx, planID, stateByItem)
	if err != nil {
		return nil, err
	}

	outItems := make([]transactions.ReviewedPlanItem, 0, len(items))
	for _, item := range items {
		reviewed := transactions.ReviewedPlanItem{
			PlanItem:      item,
			ReviewState:   transactions.ReviewStatePending,
			ReviewUpdated: item.CreatedAt,
		}
		if st, ok := stateByItem[item.ID]; ok {
			reviewed.ReviewState = st.ReviewState
			reviewed.ReviewNote = st.ReviewNote
			reviewed.ReviewUpdated = st.ReviewUpdated
		}
		outItems = append(outItems, reviewed)
	}

	return &transactions.PlanReview{
		Plan:        plan,
		Items:       outItems,
		StateCounts: stateCounts,
	}, nil
}

func (r *Repository) TransitionState(ctx context.Context, planID, itemID int64, newState transactions.ReviewState, note string) (*transactions.ReviewDecision, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transition tx: %w", err)
	}
	defer tx.Rollback()

	var foundPlanID int64
	err = tx.QueryRowContext(ctx, `SELECT transaction_plan_id FROM transaction_plan_items WHERE id = ?`, itemID).Scan(&foundPlanID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrPlanItemNotFound
		}
		return nil, fmt.Errorf("query plan item: %w", err)
	}
	if foundPlanID != planID {
		return nil, ErrPlanItemPlanMismatch
	}

	var prevRaw, noteRaw, updatedAtRaw string
	err = tx.QueryRowContext(ctx, `
		SELECT current_state, COALESCE(note, ''), updated_at
		FROM transaction_review_states
		WHERE transaction_plan_item_id = ?
	`, itemID).Scan(&prevRaw, &noteRaw, &updatedAtRaw)
	if err != nil {
		if err == sql.ErrNoRows {
			now := time.Now().UTC()
			prevRaw = string(transactions.ReviewStatePending)
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO transaction_review_states (transaction_plan_item_id, current_state, note, updated_at)
				VALUES (?, ?, '', ?)
			`, itemID, prevRaw, now.Format(time.RFC3339)); err != nil {
				return nil, fmt.Errorf("insert missing review state: %w", err)
			}
		} else {
			return nil, fmt.Errorf("query current review state: %w", err)
		}
	}

	prevState := transactions.ReviewState(prevRaw)
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
		UPDATE transaction_review_states
		SET current_state = ?, note = ?, updated_at = ?
		WHERE transaction_plan_item_id = ?
	`, newState, note, now.Format(time.RFC3339), itemID); err != nil {
		return nil, fmt.Errorf("update review state: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO transaction_review_history (
			transaction_plan_item_id, previous_state, new_state, note, changed_at
		) VALUES (?, ?, ?, ?, ?)
	`, itemID, prevState, newState, note, now.Format(time.RFC3339)); err != nil {
		return nil, fmt.Errorf("insert review history: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit transition tx: %w", err)
	}
	return &transactions.ReviewDecision{
		TransactionPlanItemID: itemID,
		PlanID:                planID,
		PreviousState:         prevState,
		NewState:              newState,
		Note:                  note,
		ChangedAt:             now,
	}, nil
}

func (r *Repository) ResetPlan(ctx context.Context, planID int64) (int64, error) {
	itemRows, err := r.db.QueryContext(ctx, `
		SELECT id FROM transaction_plan_items
		WHERE transaction_plan_id = ?
	`, planID)
	if err != nil {
		return 0, fmt.Errorf("query plan items for reset: %w", err)
	}
	defer itemRows.Close()

	itemIDs := make([]int64, 0)
	for itemRows.Next() {
		var id int64
		if err := itemRows.Scan(&id); err != nil {
			return 0, fmt.Errorf("scan reset plan item id: %w", err)
		}
		itemIDs = append(itemIDs, id)
	}
	if err := itemRows.Err(); err != nil {
		return 0, fmt.Errorf("iterate reset plan item ids: %w", err)
	}
	if len(itemIDs) == 0 {
		return 0, ErrPlanNotFound
	}

	var changed int64
	for _, itemID := range itemIDs {
		decision, err := r.TransitionState(ctx, planID, itemID, transactions.ReviewStatePending, "")
		if err != nil {
			return 0, err
		}
		if decision.PreviousState != transactions.ReviewStatePending || decision.Note != "" {
			changed++
		}
	}
	return changed, nil
}

func (r *Repository) ResetItem(ctx context.Context, planID, itemID int64) (*transactions.ReviewDecision, error) {
	return r.TransitionState(ctx, planID, itemID, transactions.ReviewStatePending, "")
}

func (r *Repository) Queue(ctx context.Context, limit int) ([]transactions.ApprovalQueueItem, error) {
	limit = sanitizeLimit(limit, 100, 20)
	rows, err := r.db.QueryContext(ctx, `
		SELECT rs.transaction_plan_item_id, tpi.transaction_plan_id, tpi.bucket,
		       tpi.add_player_name, COALESCE(tpi.add_player_team, ''),
		       tpi.drop_player_name, COALESCE(tpi.drop_player_team, ''),
		       tpi.delta_fpts, COALESCE(rs.note, ''), rs.updated_at, rs.current_state
		FROM transaction_review_states rs
		INNER JOIN transaction_plan_items tpi ON tpi.id = rs.transaction_plan_item_id
		WHERE rs.current_state = ?
		ORDER BY rs.updated_at DESC, rs.transaction_plan_item_id DESC
		LIMIT ?
	`, transactions.ReviewStateApproved, limit)
	if err != nil {
		return nil, fmt.Errorf("query approval queue: %w", err)
	}
	defer rows.Close()

	out := make([]transactions.ApprovalQueueItem, 0)
	for rows.Next() {
		var item transactions.ApprovalQueueItem
		var delta sql.NullFloat64
		var approvedRaw string
		if err := rows.Scan(
			&item.TransactionPlanItemID, &item.PlanID, &item.Bucket,
			&item.AddPlayerName, &item.AddPlayerTeam,
			&item.DropPlayerName, &item.DropPlayerTeam,
			&delta, &item.Note, &approvedRaw, &item.State,
		); err != nil {
			return nil, fmt.Errorf("scan approval queue row: %w", err)
		}
		if delta.Valid {
			v := delta.Float64
			item.DeltaFPTS = &v
		}
		at, err := time.Parse(time.RFC3339, approvedRaw)
		if err != nil {
			return nil, fmt.Errorf("parse approval timestamp: %w", err)
		}
		item.ApprovedAt = at
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate approval queue rows: %w", err)
	}
	return out, nil
}

func (r *Repository) Approvals(ctx context.Context, limit int, state *transactions.ReviewState) ([]transactions.ApprovalStateRow, error) {
	limit = sanitizeLimit(limit, 200, 50)
	query := `
		SELECT rs.transaction_plan_item_id, tpi.transaction_plan_id, tpi.bucket,
		       tpi.add_player_name, tpi.drop_player_name, tpi.delta_fpts,
		       rs.current_state, COALESCE(rs.note, ''), rs.updated_at
		FROM transaction_review_states rs
		INNER JOIN transaction_plan_items tpi ON tpi.id = rs.transaction_plan_item_id
	`
	args := []any{}
	if state != nil {
		query += ` WHERE rs.current_state = ?`
		args = append(args, *state)
	}
	query += ` ORDER BY rs.updated_at DESC, rs.transaction_plan_item_id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query approvals: %w", err)
	}
	defer rows.Close()

	out := make([]transactions.ApprovalStateRow, 0)
	for rows.Next() {
		var row transactions.ApprovalStateRow
		var delta sql.NullFloat64
		var updatedRaw string
		if err := rows.Scan(
			&row.TransactionPlanItemID, &row.PlanID, &row.Bucket,
			&row.AddPlayerName, &row.DropPlayerName, &delta,
			&row.CurrentState, &row.Note, &updatedRaw,
		); err != nil {
			return nil, fmt.Errorf("scan approvals row: %w", err)
		}
		if delta.Valid {
			v := delta.Float64
			row.DeltaFPTS = &v
		}
		tm, err := time.Parse(time.RFC3339, updatedRaw)
		if err != nil {
			return nil, fmt.Errorf("parse approval updated_at: %w", err)
		}
		row.UpdatedAt = tm
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate approvals rows: %w", err)
	}
	return out, nil
}

func (r *Repository) statesForPlan(ctx context.Context, planID int64, out map[int64]transactions.ReviewedPlanItem) (map[transactions.ReviewState]int64, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT tpi.id, rs.current_state, COALESCE(rs.note, ''), rs.updated_at
		FROM transaction_plan_items tpi
		LEFT JOIN transaction_review_states rs ON rs.transaction_plan_item_id = tpi.id
		WHERE tpi.transaction_plan_id = ?
	`, planID)
	if err != nil {
		return nil, fmt.Errorf("query plan review states: %w", err)
	}
	defer rows.Close()

	counts := map[transactions.ReviewState]int64{
		transactions.ReviewStatePending:  0,
		transactions.ReviewStateApproved: 0,
		transactions.ReviewStateRejected: 0,
		transactions.ReviewStateDeferred: 0,
	}
	for rows.Next() {
		var itemID int64
		var state sql.NullString
		var note sql.NullString
		var updatedAt sql.NullString
		if err := rows.Scan(&itemID, &state, &note, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan plan review state row: %w", err)
		}
		s := transactions.ReviewStatePending
		if state.Valid && state.String != "" {
			s = transactions.ReviewState(state.String)
		}
		counts[s]++
		r := transactions.ReviewedPlanItem{
			ReviewState: s,
			ReviewNote:  note.String,
		}
		if updatedAt.Valid && updatedAt.String != "" {
			tm, err := time.Parse(time.RFC3339, updatedAt.String)
			if err != nil {
				return nil, fmt.Errorf("parse review state updated_at: %w", err)
			}
			r.ReviewUpdated = tm
		}
		out[itemID] = r
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate plan review states: %w", err)
	}
	return counts, nil
}

func sanitizeLimit(v, max, def int) int {
	if v <= 0 {
		return def
	}
	if v > max {
		return max
	}
	return v
}
