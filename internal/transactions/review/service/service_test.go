package service

import (
	"context"
	"path/filepath"
	"testing"

	"fantasy-baseball/internal/store/sqlite"
	"fantasy-baseball/internal/transactions"
	tranrepo "fantasy-baseball/internal/transactions/repository"
	reviewrepo "fantasy-baseball/internal/transactions/review/repository"
)

func TestService_ApproveRejectDeferTransitions(t *testing.T) {
	svc, planID, itemID, closeFn := testService(t)
	defer closeFn()

	if _, err := svc.Approve(context.Background(), planID, itemID, "ship it"); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if _, err := svc.Defer(context.Background(), planID, itemID, "wait for weather"); err != nil {
		t.Fatalf("Defer from approved: %v", err)
	}
	if _, err := svc.Reject(context.Background(), planID, itemID, "too risky"); err != nil {
		t.Fatalf("Reject from deferred: %v", err)
	}
}

func TestService_RejectToApprovedDisallowed(t *testing.T) {
	svc, planID, itemID, closeFn := testService(t)
	defer closeFn()

	if _, err := svc.Reject(context.Background(), planID, itemID, "nope"); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	if _, err := svc.Approve(context.Background(), planID, itemID, "actually yes"); err == nil {
		t.Fatalf("expected invalid transition error")
	}
}

func TestService_ResetSingleAndPlan(t *testing.T) {
	svc, planID, itemID, closeFn := testService(t)
	defer closeFn()

	if _, err := svc.Approve(context.Background(), planID, itemID, "ok"); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	v, err := svc.Reset(context.Background(), planID, &itemID)
	if err != nil {
		t.Fatalf("Reset single: %v", err)
	}
	if _, ok := v.(*transactions.ReviewDecision); !ok {
		t.Fatalf("expected ReviewDecision on item reset")
	}

	v, err = svc.Reset(context.Background(), planID, nil)
	if err != nil {
		t.Fatalf("Reset plan: %v", err)
	}
	if _, ok := v.(map[string]any); !ok {
		t.Fatalf("expected map result on plan reset")
	}
}

func TestService_QueueAndApprovals(t *testing.T) {
	svc, planID, itemID, closeFn := testService(t)
	defer closeFn()
	if _, err := svc.Approve(context.Background(), planID, itemID, "ready"); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	queue, err := svc.Queue(context.Background(), 10)
	if err != nil {
		t.Fatalf("Queue: %v", err)
	}
	if len(queue) != 1 {
		t.Fatalf("expected one queue row, got %d", len(queue))
	}

	state := transactions.ReviewStateApproved
	rows, err := svc.Approvals(context.Background(), 10, &state)
	if err != nil {
		t.Fatalf("Approvals: %v", err)
	}
	if len(rows) == 0 {
		t.Fatalf("expected approvals rows")
	}
}

func testService(t *testing.T) (*Service, int64, int64, func()) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "fb.db")
	if _, err := sqlite.EnsureDatabaseFile(dbPath); err != nil {
		t.Fatalf("EnsureDatabaseFile: %v", err)
	}
	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	planRepo := tranrepo.New(store.DB())
	addTotal := 14.0
	dropTotal := 5.0
	delta := 9.0
	rank := 1
	planID, err := planRepo.SavePlan(context.Background(), transactions.CreatePlanInput{
		WindowStart: "2026-09-15",
		WindowEnd:   "2026-09-21",
		Status:      "success",
		Summary:     map[string]any{"counts": map[string]int{"strong_move": 1}},
		Items: []transactions.PlanItem{{
			Bucket:                  transactions.BucketStrongMove,
			AddPlayerName:           "Add A",
			DropPlayerName:          "Drop B",
			AddProjectedStartCount:  2,
			DropProjectedStartCount: 0,
			AddTotalProjectedFPTS:   &addTotal,
			DropTotalProjectedFPTS:  &dropTotal,
			DeltaFPTS:               &delta,
			ResultRank:              &rank,
		}},
	})
	if err != nil {
		t.Fatalf("SavePlan: %v", err)
	}
	items, err := planRepo.PlanItems(context.Background(), planID)
	if err != nil {
		t.Fatalf("PlanItems: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one plan item")
	}

	svc := New(reviewrepo.New(store.DB()))
	return svc, planID, items[0].ID, func() { store.Close() }
}
