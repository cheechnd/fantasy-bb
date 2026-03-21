package monitor

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"fantasy-baseball/internal/pitchers/matching"
)

type Repository struct{ db *sql.DB }

func NewRepository(db *sql.DB) *Repository { return &Repository{db: db} }

type PitcherPlanRow struct {
	ID          int64
	CreatedAt   time.Time
	SyncRunID   *int64
	ImportRunID *int64
}

type LineupPlanRow struct {
	ID            int64
	CreatedAt     time.Time
	PitcherPlanID *int64
	SyncRunID     *int64
}

type PickupRunRow struct {
	ID             int64
	CreatedAt      time.Time
	SyncRunID      *int64
	ImportRunID    *int64
	CandidateRunID *int64
}

type RecommendationPlayerRow struct {
	PlayerName string
	ItemType   string
}

type LineupPlanItemRow struct {
	PlayerName  string
	ActionType  string
	CurrentSlot string
	TargetSlot  string
}

type ApprovedTransactionRow struct {
	ItemID     int64
	PlanID     int64
	UpdatedAt  time.Time
	AddPlayer  string
	DropPlayer string
}

type ApprovedLineupRow struct {
	ItemID     int64
	PlanID     int64
	UpdatedAt  time.Time
	Player     string
	TargetSlot string
}

type AdHocRow struct {
	ID                       int64
	State                    string
	Resolution               string
	UpdatedAt                time.Time
	RequestedAdd             string
	RequestedDrop            string
	ResolvedAdd              string
	ResolvedDrop             string
	LinkedExecutionAttemptID *int64
}

type ExecRow struct {
	ID                 int64
	ApprovedItemID     int64
	ExecutionStatus    string
	VerificationStatus string
	StartedAt          time.Time
	LastVerifiedAt     *time.Time
}

type LineupExecRow struct {
	ID                   int64
	ApprovedLineupItemID int64
	ExecutionStatus      string
	VerificationStatus   string
	StartedAt            time.Time
}

func (r *Repository) SaveRun(ctx context.Context, runType string, summary map[string]any, items []Item) (int64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin monitor run tx: %w", err)
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := tx.ExecContext(ctx, `INSERT INTO monitor_runs (run_type, created_at, status, item_count, summary_json) VALUES (?, ?, ?, ?, ?)`, runType, now, "success", len(items), toJSON(summary, "{}"))
	if err != nil {
		return 0, fmt.Errorf("insert monitor run: %w", err)
	}
	runID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("monitor run id: %w", err)
	}
	for _, it := range items {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO monitor_run_items (monitor_run_id, artifact_type, artifact_id, monitor_status, reasons_json, recommended_action, details_json, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, runID, it.ArtifactType, it.ArtifactID, it.MonitorStatus, toJSON(it.Reasons, "[]"), it.RecommendedAction, toJSON(it.Details, "{}"), now)
		if err != nil {
			return 0, fmt.Errorf("insert monitor run item: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit monitor run tx: %w", err)
	}
	return runID, nil
}

func (r *Repository) RunByID(ctx context.Context, runID int64) (*Run, error) {
	row := r.db.QueryRowContext(ctx, `SELECT id, run_type, created_at, status, item_count, COALESCE(summary_json,'{}') FROM monitor_runs WHERE id = ?`, runID)
	run, err := scanRun(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	items, err := r.RunItems(ctx, runID)
	if err != nil {
		return nil, err
	}
	run.Items = items
	return run, nil
}

func (r *Repository) LatestRunByType(ctx context.Context, runType string) (*Run, error) {
	row := r.db.QueryRowContext(ctx, `SELECT id, run_type, created_at, status, item_count, COALESCE(summary_json,'{}') FROM monitor_runs WHERE run_type = ? ORDER BY id DESC LIMIT 1`, runType)
	run, err := scanRun(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	items, err := r.RunItems(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	run.Items = items
	return run, nil
}

func (r *Repository) RunItems(ctx context.Context, runID int64) ([]Item, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, monitor_run_id, artifact_type, artifact_id, monitor_status,
		       COALESCE(reasons_json,'[]'), recommended_action, COALESCE(details_json,'{}'), created_at
		FROM monitor_run_items
		WHERE monitor_run_id = ?
		ORDER BY id ASC
	`, runID)
	if err != nil {
		return nil, fmt.Errorf("query monitor run items: %w", err)
	}
	defer rows.Close()
	out := []Item{}
	for rows.Next() {
		var it Item
		var reasonsJSON, detailsJSON, createdRaw string
		if err := rows.Scan(&it.ID, &it.MonitorRunID, &it.ArtifactType, &it.ArtifactID, &it.MonitorStatus, &reasonsJSON, &it.RecommendedAction, &detailsJSON, &createdRaw); err != nil {
			return nil, fmt.Errorf("scan monitor run item: %w", err)
		}
		_ = json.Unmarshal([]byte(reasonsJSON), &it.Reasons)
		_ = json.Unmarshal([]byte(detailsJSON), &it.Details)
		tm, err := time.Parse(time.RFC3339, createdRaw)
		if err != nil {
			return nil, fmt.Errorf("parse monitor run item created_at: %w", err)
		}
		it.CreatedAt = tm
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate monitor run items: %w", err)
	}
	return out, nil
}

func (r *Repository) LatestSyncRun(ctx context.Context) (int64, time.Time, error) {
	row := r.db.QueryRowContext(ctx, `SELECT id, COALESCE(completed_at, started_at) FROM espn_sync_runs ORDER BY id DESC LIMIT 1`)
	var id int64
	var raw string
	if err := row.Scan(&id, &raw); err != nil {
		if err == sql.ErrNoRows {
			return 0, time.Time{}, nil
		}
		return 0, time.Time{}, err
	}
	tm, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return 0, time.Time{}, err
	}
	return id, tm, nil
}

func (r *Repository) LatestImportRun(ctx context.Context) (int64, time.Time, error) {
	row := r.db.QueryRowContext(ctx, `SELECT id, imported_at FROM forecaster_import_runs ORDER BY id DESC LIMIT 1`)
	var id int64
	var raw string
	if err := row.Scan(&id, &raw); err != nil {
		if err == sql.ErrNoRows {
			return 0, time.Time{}, nil
		}
		return 0, time.Time{}, err
	}
	tm, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return 0, time.Time{}, err
	}
	return id, tm, nil
}

func (r *Repository) PitcherPlans(ctx context.Context, limit int, latestOnly bool) ([]PitcherPlanRow, error) {
	if latestOnly {
		limit = 1
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id, created_at, sync_run_id, import_run_id FROM pitcher_plans ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PitcherPlanRow{}
	for rows.Next() {
		var p PitcherPlanRow
		var syncID, importID sql.NullInt64
		var raw string
		if err := rows.Scan(&p.ID, &raw, &syncID, &importID); err != nil {
			return nil, err
		}
		tm, _ := time.Parse(time.RFC3339, raw)
		p.CreatedAt = tm
		if syncID.Valid {
			v := syncID.Int64
			p.SyncRunID = &v
		}
		if importID.Valid {
			v := importID.Int64
			p.ImportRunID = &v
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *Repository) LineupPlans(ctx context.Context, limit int, latestOnly bool) ([]LineupPlanRow, error) {
	if latestOnly {
		limit = 1
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id, created_at, pitcher_plan_id, sync_run_id FROM lineup_plans ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LineupPlanRow{}
	for rows.Next() {
		var p LineupPlanRow
		var pitchID, syncID sql.NullInt64
		var raw string
		if err := rows.Scan(&p.ID, &raw, &pitchID, &syncID); err != nil {
			return nil, err
		}
		tm, _ := time.Parse(time.RFC3339, raw)
		p.CreatedAt = tm
		if pitchID.Valid {
			v := pitchID.Int64
			p.PitcherPlanID = &v
		}
		if syncID.Valid {
			v := syncID.Int64
			p.SyncRunID = &v
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *Repository) PickupRuns(ctx context.Context, limit int, latestOnly bool) ([]PickupRunRow, error) {
	if latestOnly {
		limit = 1
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id, created_at, sync_run_id, import_run_id, candidate_run_id FROM pickup_recommendation_runs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PickupRunRow{}
	for rows.Next() {
		var p PickupRunRow
		var syncID, importID, candID sql.NullInt64
		var raw string
		if err := rows.Scan(&p.ID, &raw, &syncID, &importID, &candID); err != nil {
			return nil, err
		}
		tm, _ := time.Parse(time.RFC3339, raw)
		p.CreatedAt = tm
		if syncID.Valid {
			v := syncID.Int64
			p.SyncRunID = &v
		}
		if importID.Valid {
			v := importID.Int64
			p.ImportRunID = &v
		}
		if candID.Valid {
			v := candID.Int64
			p.CandidateRunID = &v
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *Repository) PitcherPlanPlayers(ctx context.Context, planID int64) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT DISTINCT player_name FROM pitcher_plan_items WHERE plan_id = ?`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

func (r *Repository) LineupPlanItems(ctx context.Context, planID int64) ([]LineupPlanItemRow, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT player_name, action_type, COALESCE(current_slot,''), COALESCE(target_slot,'') FROM lineup_plan_items WHERE lineup_plan_id = ?`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LineupPlanItemRow{}
	for rows.Next() {
		var v LineupPlanItemRow
		if err := rows.Scan(&v.PlayerName, &v.ActionType, &v.CurrentSlot, &v.TargetSlot); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r *Repository) PickupRunPlayers(ctx context.Context, runID int64) ([]RecommendationPlayerRow, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT player_name, item_type FROM pickup_recommendation_items WHERE recommendation_run_id = ?`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RecommendationPlayerRow{}
	for rows.Next() {
		var p RecommendationPlayerRow
		if err := rows.Scan(&p.PlayerName, &p.ItemType); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *Repository) ApprovedTransactions(ctx context.Context, limit int) ([]ApprovedTransactionRow, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT rs.transaction_plan_item_id, tpi.transaction_plan_id, rs.updated_at,
		       tpi.add_player_name, tpi.drop_player_name
		FROM transaction_review_states rs
		JOIN transaction_plan_items tpi ON tpi.id = rs.transaction_plan_item_id
		WHERE rs.current_state = 'approved'
		ORDER BY rs.updated_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ApprovedTransactionRow{}
	for rows.Next() {
		var r0 ApprovedTransactionRow
		var raw string
		if err := rows.Scan(&r0.ItemID, &r0.PlanID, &raw, &r0.AddPlayer, &r0.DropPlayer); err != nil {
			return nil, err
		}
		tm, _ := time.Parse(time.RFC3339, raw)
		r0.UpdatedAt = tm
		out = append(out, r0)
	}
	return out, rows.Err()
}

func (r *Repository) ApprovedLineup(ctx context.Context, limit int) ([]ApprovedLineupRow, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT rs.lineup_plan_item_id, i.lineup_plan_id, rs.updated_at,
		       i.player_name, COALESCE(i.target_slot, '')
		FROM lineup_review_states rs
		JOIN lineup_plan_items i ON i.id = rs.lineup_plan_item_id
		WHERE rs.current_state = 'approved'
		ORDER BY rs.updated_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ApprovedLineupRow{}
	for rows.Next() {
		var r0 ApprovedLineupRow
		var raw string
		if err := rows.Scan(&r0.ItemID, &r0.PlanID, &raw, &r0.Player, &r0.TargetSlot); err != nil {
			return nil, err
		}
		tm, _ := time.Parse(time.RFC3339, raw)
		r0.UpdatedAt = tm
		out = append(out, r0)
	}
	return out, rows.Err()
}

func (r *Repository) AdHocRequests(ctx context.Context, limit int) ([]AdHocRow, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, request_state, resolution_status, updated_at,
		       requested_add_player_name, requested_drop_player_name,
		       COALESCE(resolved_add_player_name, ''), COALESCE(resolved_drop_player_name, ''),
		       linked_execution_attempt_id
		FROM ad_hoc_transaction_requests
		ORDER BY id DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AdHocRow{}
	for rows.Next() {
		var a AdHocRow
		var raw string
		var execID sql.NullInt64
		if err := rows.Scan(&a.ID, &a.State, &a.Resolution, &raw, &a.RequestedAdd, &a.RequestedDrop, &a.ResolvedAdd, &a.ResolvedDrop, &execID); err != nil {
			return nil, err
		}
		tm, _ := time.Parse(time.RFC3339, raw)
		a.UpdatedAt = tm
		if execID.Valid {
			v := execID.Int64
			a.LinkedExecutionAttemptID = &v
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *Repository) PendingExecutions(ctx context.Context, limit int) ([]ExecRow, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, approved_item_id, execution_status, verification_status, started_at, last_verified_at
		FROM execution_attempts
		WHERE execution_status IN ('ambiguous','submitted')
		   OR verification_status IN ('verification_pending','unverified','unknown','verification_failed')
		ORDER BY id DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ExecRow{}
	for rows.Next() {
		var e ExecRow
		var startedRaw string
		var lastRaw sql.NullString
		if err := rows.Scan(&e.ID, &e.ApprovedItemID, &e.ExecutionStatus, &e.VerificationStatus, &startedRaw, &lastRaw); err != nil {
			return nil, err
		}
		e.StartedAt, _ = time.Parse(time.RFC3339, startedRaw)
		if lastRaw.Valid && lastRaw.String != "" {
			tm, _ := time.Parse(time.RFC3339, lastRaw.String)
			e.LastVerifiedAt = &tm
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *Repository) PendingLineupExecutions(ctx context.Context, limit int) ([]LineupExecRow, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, approved_lineup_item_id, execution_status, verification_status, started_at
		FROM lineup_execution_attempts
		WHERE execution_status IN ('ambiguous','submitted')
		   OR verification_status IN ('verification_pending','unverified','unknown','verification_failed')
		ORDER BY id DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LineupExecRow{}
	for rows.Next() {
		var e LineupExecRow
		var startedRaw string
		if err := rows.Scan(&e.ID, &e.ApprovedLineupItemID, &e.ExecutionStatus, &e.VerificationStatus, &startedRaw); err != nil {
			return nil, err
		}
		e.StartedAt, _ = time.Parse(time.RFC3339, startedRaw)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *Repository) HasSuccessfulExecutionForTransactionItem(ctx context.Context, itemID int64) (bool, error) {
	return hasOne(ctx, r.db, `SELECT 1 FROM execution_attempts WHERE approved_item_id = ? AND execution_status = 'succeeded' ORDER BY id DESC LIMIT 1`, itemID)
}

func (r *Repository) HasSuccessfulExecutionForLineupItem(ctx context.Context, itemID int64) (bool, error) {
	return hasOne(ctx, r.db, `SELECT 1 FROM lineup_execution_attempts WHERE approved_lineup_item_id = ? AND execution_status = 'succeeded' ORDER BY id DESC LIMIT 1`, itemID)
}

func (r *Repository) IsRosteredNow(ctx context.Context, name string) (bool, string, error) {
	syncID, _, err := r.LatestSyncRun(ctx)
	if err != nil {
		return false, "", err
	}
	if syncID == 0 {
		return false, "", nil
	}
	key := matching.NormalizeName(name)
	if key == "" {
		return false, "", nil
	}
	row := r.db.QueryRowContext(ctx, `SELECT COALESCE(roster_slot,'') FROM espn_roster_snapshots WHERE sync_run_id = ? AND normalized_name = ? LIMIT 1`, syncID, key)
	var slot string
	if err := row.Scan(&slot); err != nil {
		if err == sql.ErrNoRows {
			return false, "", nil
		}
		return false, "", err
	}
	return true, slot, nil
}

func (r *Repository) IsCandidateNow(ctx context.Context, name string) (bool, error) {
	row := r.db.QueryRowContext(ctx, `SELECT id FROM espn_candidate_runs ORDER BY id DESC LIMIT 1`)
	var runID int64
	if err := row.Scan(&runID); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	key := matching.NormalizeName(name)
	if key == "" {
		return false, nil
	}
	return hasOne(ctx, r.db, `SELECT 1 FROM espn_free_agent_candidates WHERE candidate_run_id = ? AND normalized_name = ? LIMIT 1`, runID, key)
}

func (r *Repository) CandidateRunCreatedAt(ctx context.Context, runID int64) (time.Time, error) {
	row := r.db.QueryRowContext(ctx, `SELECT completed_at FROM espn_candidate_runs WHERE id = ?`, runID)
	var raw string
	if err := row.Scan(&raw); err != nil {
		if err == sql.ErrNoRows {
			return time.Time{}, nil
		}
		return time.Time{}, err
	}
	tm, _ := time.Parse(time.RFC3339, raw)
	return tm, nil
}

func hasOne(ctx context.Context, db *sql.DB, q string, args ...any) (bool, error) {
	row := db.QueryRowContext(ctx, q, args...)
	var one int
	if err := row.Scan(&one); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func scanRun(row *sql.Row) (*Run, error) {
	var r0 Run
	var createdRaw, summaryJSON string
	if err := row.Scan(&r0.ID, &r0.RunType, &createdRaw, &r0.Status, &r0.ItemCount, &summaryJSON); err != nil {
		return nil, err
	}
	tm, err := time.Parse(time.RFC3339, createdRaw)
	if err != nil {
		return nil, err
	}
	r0.CreatedAt = tm
	_ = json.Unmarshal([]byte(summaryJSON), &r0.Summary)
	return &r0, nil
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
