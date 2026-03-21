package monitor

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sqlitestore "fantasy-baseball/internal/store/sqlite"

	_ "modernc.org/sqlite"
)

func TestSummaryPropagatesEvaluationError(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "monitor_err.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	repo := NewRepository(db)
	svc := NewService(repo, Config{})

	_, err = svc.Summary(context.Background())
	if err == nil {
		t.Fatal("expected summary error when source tables are missing")
	}
	if !strings.Contains(err.Error(), "evaluate plans") {
		t.Fatalf("expected evaluate plans error, got: %v", err)
	}
}

func TestSummaryPersistsSingleRun(t *testing.T) {
	ctx := context.Background()
	store := openMigratedStore(t)
	defer store.Close()

	repo := NewRepository(store.DB())
	svc := NewService(repo, Config{
		PlansStaleHours:               12,
		LineupStaleHours:              4,
		PickupsStaleHours:             6,
		CandidatePoolStaleHours:       4,
		ApprovalStaleHours:            2,
		ExecutionFollowupHours:        1,
		RequireLiveRecheckForApproved: true,
	})

	run, err := svc.Summary(ctx)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if run == nil {
		t.Fatal("expected summary run")
	}
	if run.RunType != "summary" {
		t.Fatalf("run type = %q, want summary", run.RunType)
	}

	var count int
	if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM monitor_runs`).Scan(&count); err != nil {
		t.Fatalf("count monitor_runs: %v", err)
	}
	if count != 1 {
		t.Fatalf("monitor run count = %d, want 1", count)
	}
}

func TestShowApprovalTypeFiltersStrictly(t *testing.T) {
	ctx := context.Background()
	store := openMigratedStore(t)
	defer store.Close()
	seedApprovedRows(t, ctx, store.DB())

	repo := NewRepository(store.DB())
	svc := NewService(repo, Config{
		PlansStaleHours:               12,
		LineupStaleHours:              4,
		PickupsStaleHours:             6,
		CandidatePoolStaleHours:       4,
		ApprovalStaleHours:            2,
		ExecutionFollowupHours:        1,
		RequireLiveRecheckForApproved: true,
	})

	runApproval, err := svc.Show(ctx, "approval", 1)
	if err != nil {
		t.Fatalf("Show approval: %v", err)
	}
	if len(runApproval.Items) != 1 {
		t.Fatalf("approval items = %d, want 1", len(runApproval.Items))
	}
	if runApproval.Items[0].ArtifactType != "approval" {
		t.Fatalf("approval item type = %q, want approval", runApproval.Items[0].ArtifactType)
	}

	runLineup, err := svc.Show(ctx, "lineup_approval", 1)
	if err != nil {
		t.Fatalf("Show lineup_approval: %v", err)
	}
	if len(runLineup.Items) != 1 {
		t.Fatalf("lineup approval items = %d, want 1", len(runLineup.Items))
	}
	if runLineup.Items[0].ArtifactType != "lineup_approval" {
		t.Fatalf("lineup approval item type = %q, want lineup_approval", runLineup.Items[0].ArtifactType)
	}
}

func openMigratedStore(t *testing.T) *sqlitestore.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "monitor.db")
	store, err := sqlitestore.Open(dbPath)
	if err != nil {
		t.Fatalf("sqlite open: %v", err)
	}
	if _, err := store.Migrate(context.Background()); err != nil {
		store.Close()
		t.Fatalf("migrate: %v", err)
	}
	return store
}

func seedApprovedRows(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := db.ExecContext(ctx, `
		INSERT INTO transaction_plans (id, window_start, window_end, created_at, status, plan_summary_json)
		VALUES (1, '2026-09-15', '2026-09-24', ?, 'success', '{}')
	`, now)
	if err != nil {
		t.Fatalf("insert transaction_plans: %v", err)
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO transaction_plan_items (
			id, transaction_plan_id, bucket, add_player_name, add_player_team, add_espn_player_id,
			drop_player_name, drop_player_team, drop_espn_player_id,
			add_projected_start_count, add_total_projected_fpts,
			drop_projected_start_count, drop_total_projected_fpts,
			delta_fpts, result_rank, flags_json, notes_json, details_json, created_at
		) VALUES (1, 1, 'strong_move', 'Add P', 'PHI', NULL, 'Drop P', 'CHC', NULL, 1, 10.0, 1, 8.0, 2.0, 1, '[]', '[]', '{}', ?)
	`, now)
	if err != nil {
		t.Fatalf("insert transaction_plan_items: %v", err)
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO transaction_review_states (transaction_plan_item_id, current_state, note, updated_at)
		VALUES (1, 'approved', '', ?)
	`, now)
	if err != nil {
		t.Fatalf("insert transaction_review_states: %v", err)
	}

	_, err = db.ExecContext(ctx, `
		INSERT INTO lineup_plans (id, pitcher_plan_id, sync_run_id, created_at, status, summary_json)
		VALUES (1, NULL, NULL, ?, 'success', '{}')
	`, now)
	if err != nil {
		t.Fatalf("insert lineup_plans: %v", err)
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO lineup_plan_items (id, lineup_plan_id, action_type, player_name, espn_player_id, current_slot, target_slot, rationale_json, flags_json, created_at)
		VALUES (1, 1, 'activate_pitcher', 'Lineup P', NULL, 'BE', 'P', '{}', '[]', ?)
	`, now)
	if err != nil {
		t.Fatalf("insert lineup_plan_items: %v", err)
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO lineup_review_states (lineup_plan_item_id, current_state, note, updated_at)
		VALUES (1, 'approved', '', ?)
	`, now)
	if err != nil {
		t.Fatalf("insert lineup_review_states: %v", err)
	}
}
