package repository

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"fantasy-baseball/internal/store/sqlite"
	"fantasy-baseball/internal/transactions"
	tranrepo "fantasy-baseball/internal/transactions/repository"
)

func TestReview_DefaultPendingState(t *testing.T) {
	store := mustStore(t)
	defer store.Close()

	planID, itemID := seedPlan(t, store)
	repo := New(store.DB())
	review, err := repo.ReviewByPlanID(context.Background(), planID)
	if err != nil {
		t.Fatalf("ReviewByPlanID: %v", err)
	}
	if len(review.Items) != 1 {
		t.Fatalf("expected one review item, got %d", len(review.Items))
	}
	if review.Items[0].ID != itemID {
		t.Fatalf("expected item %d, got %d", itemID, review.Items[0].ID)
	}
	if review.Items[0].ReviewState != transactions.ReviewStatePending {
		t.Fatalf("expected pending, got %s", review.Items[0].ReviewState)
	}
}

func TestReview_TransitionsAndQueue(t *testing.T) {
	store := mustStore(t)
	defer store.Close()
	planID, itemID := seedPlan(t, store)
	repo := New(store.DB())

	decision, err := repo.TransitionState(context.Background(), planID, itemID, transactions.ReviewStateApproved, "looks good")
	if err != nil {
		t.Fatalf("TransitionState approve: %v", err)
	}
	if decision.PreviousState != transactions.ReviewStatePending || decision.NewState != transactions.ReviewStateApproved {
		t.Fatalf("unexpected decision: %+v", decision)
	}

	queue, err := repo.Queue(context.Background(), 10)
	if err != nil {
		t.Fatalf("Queue: %v", err)
	}
	if len(queue) != 1 {
		t.Fatalf("expected 1 queue row, got %d", len(queue))
	}
	if queue[0].TransactionPlanItemID != itemID {
		t.Fatalf("unexpected queue row: %+v", queue[0])
	}

	var historyCount int
	if err := store.DB().QueryRowContext(context.Background(), `
		SELECT COUNT(*) FROM transaction_review_history WHERE transaction_plan_item_id = ?
	`, itemID).Scan(&historyCount); err != nil {
		t.Fatalf("history count query: %v", err)
	}
	if historyCount < 2 {
		t.Fatalf("expected at least 2 history rows (initial + transition), got %d", historyCount)
	}
}

func TestReview_QueueExcludesSuccessfullyExecutedItems(t *testing.T) {
	store := mustStore(t)
	defer store.Close()
	planID, itemID := seedPlan(t, store)
	repo := New(store.DB())

	if _, err := repo.TransitionState(context.Background(), planID, itemID, transactions.ReviewStateApproved, "ready"); err != nil {
		t.Fatalf("TransitionState approve: %v", err)
	}
	if _, err := store.DB().ExecContext(context.Background(), `
		INSERT INTO execution_attempts (
			approved_item_id, source_plan_id, started_at, execution_status, verification_status,
			add_player_name, drop_player_name, request_summary_json, response_summary_json, details_json
		) VALUES (?, ?, ?, 'succeeded', 'verified', 'Add Arm', 'Drop Arm', '{}', '{}', '{}')
	`, itemID, planID, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("insert execution attempt: %v", err)
	}

	queue, err := repo.Queue(context.Background(), 10)
	if err != nil {
		t.Fatalf("Queue: %v", err)
	}
	if len(queue) != 0 {
		t.Fatalf("expected queue item to be excluded after successful execution, got %d rows", len(queue))
	}
}

func TestReview_ApprovalsFilterAndReset(t *testing.T) {
	store := mustStore(t)
	defer store.Close()
	planID, itemID := seedPlan(t, store)
	repo := New(store.DB())

	if _, err := repo.TransitionState(context.Background(), planID, itemID, transactions.ReviewStateDeferred, "wait"); err != nil {
		t.Fatalf("TransitionState defer: %v", err)
	}

	state := transactions.ReviewStateDeferred
	rows, err := repo.Approvals(context.Background(), 20, &state)
	if err != nil {
		t.Fatalf("Approvals deferred: %v", err)
	}
	if len(rows) != 1 || rows[0].CurrentState != transactions.ReviewStateDeferred {
		t.Fatalf("unexpected approvals rows: %+v", rows)
	}

	changed, err := repo.ResetPlan(context.Background(), planID)
	if err != nil {
		t.Fatalf("ResetPlan: %v", err)
	}
	if changed != 1 {
		t.Fatalf("expected 1 changed item, got %d", changed)
	}

	review, err := repo.ReviewByPlanID(context.Background(), planID)
	if err != nil {
		t.Fatalf("ReviewByPlanID after reset: %v", err)
	}
	if review.Items[0].ReviewState != transactions.ReviewStatePending {
		t.Fatalf("expected pending after reset, got %s", review.Items[0].ReviewState)
	}
}

func TestReview_InvalidPlanItemCombination(t *testing.T) {
	store := mustStore(t)
	defer store.Close()
	planID, _ := seedPlan(t, store)
	repo := New(store.DB())

	_, err := repo.TransitionState(context.Background(), planID, 999999, transactions.ReviewStateApproved, "")
	if err == nil {
		t.Fatalf("expected error for invalid item id")
	}
}

func mustStore(t *testing.T) *sqlite.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "fb.db")
	if _, err := sqlite.EnsureDatabaseFile(dbPath); err != nil {
		t.Fatalf("EnsureDatabaseFile: %v", err)
	}
	s, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

func seedPlan(t *testing.T, store *sqlite.Store) (int64, int64) {
	t.Helper()
	repo := tranrepo.New(store.DB())
	addTotal := 16.2
	dropTotal := 5.0
	delta := 11.2
	rank := 1
	planID, err := repo.SavePlan(context.Background(), transactions.CreatePlanInput{
		WindowStart: "2026-09-15",
		WindowEnd:   "2026-09-21",
		Status:      "success",
		Summary:     map[string]interface{}{"counts": map[string]int{"strong_move": 1}},
		Items: []transactions.PlanItem{{
			Bucket:                  transactions.BucketStrongMove,
			AddPlayerName:           "Add Arm",
			AddPlayerTeam:           "SEA",
			DropPlayerName:          "Drop Arm",
			DropPlayerTeam:          "MIA",
			AddProjectedStartCount:  2,
			AddTotalProjectedFPTS:   &addTotal,
			DropProjectedStartCount: 1,
			DropTotalProjectedFPTS:  &dropTotal,
			DeltaFPTS:               &delta,
			ResultRank:              &rank,
		}},
	})
	if err != nil {
		t.Fatalf("SavePlan: %v", err)
	}
	items, err := repo.PlanItems(context.Background(), planID)
	if err != nil {
		t.Fatalf("PlanItems: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one plan item, got %d", len(items))
	}
	return planID, items[0].ID
}
