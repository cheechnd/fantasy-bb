package repository

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"fantasy-baseball/internal/execute"
	"fantasy-baseball/internal/store/sqlite"
	"fantasy-baseball/internal/transactions"
	tranrepo "fantasy-baseball/internal/transactions/repository"
)

func TestLatestAttemptByApprovedItemAndPending(t *testing.T) {
	store := mustStore(t)
	defer store.Close()
	planID, itemID := seedPlan(t, store)
	repo := New(store.DB())

	id1, err := repo.CreateAttempt(context.Background(), CreateAttemptInput{
		ApprovedItemID:     itemID,
		SourcePlanID:       planID,
		ExecutionStatus:    execute.ExecutionStatusSubmitted,
		VerificationStatus: execute.VerificationStatusPending,
		AddPlayerName:      "Add Arm",
		DropPlayerName:     "Drop Arm",
	})
	if err != nil {
		t.Fatalf("CreateAttempt #1: %v", err)
	}
	if err := repo.CompleteAttempt(context.Background(), id1, CompleteInput{
		ExecutionStatus:    execute.ExecutionStatusSubmitted,
		VerificationStatus: execute.VerificationStatusPending,
	}); err != nil {
		t.Fatalf("CompleteAttempt #1: %v", err)
	}

	id2, err := repo.CreateAttempt(context.Background(), CreateAttemptInput{
		ApprovedItemID:     itemID,
		SourcePlanID:       planID,
		ExecutionStatus:    execute.ExecutionStatusAmbiguous,
		VerificationStatus: execute.VerificationStatusUnverified,
		AddPlayerName:      "Add Arm",
		DropPlayerName:     "Drop Arm",
	})
	if err != nil {
		t.Fatalf("CreateAttempt #2: %v", err)
	}
	if err := repo.CompleteAttempt(context.Background(), id2, CompleteInput{
		ExecutionStatus:    execute.ExecutionStatusAmbiguous,
		VerificationStatus: execute.VerificationStatusUnverified,
	}); err != nil {
		t.Fatalf("CompleteAttempt #2: %v", err)
	}

	latest, _, err := repo.LatestAttemptByApprovedItem(context.Background(), itemID)
	if err != nil {
		t.Fatalf("LatestAttemptByApprovedItem: %v", err)
	}
	if latest == nil || latest.ID != id2 {
		t.Fatalf("expected latest id %d, got %+v", id2, latest)
	}

	rows, err := repo.ListPendingAttempts(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListPendingAttempts: %v", err)
	}
	if len(rows) == 0 {
		t.Fatalf("expected pending rows")
	}
}

func TestUpdateVerification(t *testing.T) {
	store := mustStore(t)
	defer store.Close()
	planID, itemID := seedPlan(t, store)
	repo := New(store.DB())

	id, err := repo.CreateAttempt(context.Background(), CreateAttemptInput{
		ApprovedItemID:     itemID,
		SourcePlanID:       planID,
		ExecutionStatus:    execute.ExecutionStatusSubmitted,
		VerificationStatus: execute.VerificationStatusPending,
		AddPlayerName:      "Add Arm",
		DropPlayerName:     "Drop Arm",
	})
	if err != nil {
		t.Fatalf("CreateAttempt: %v", err)
	}
	now := time.Now().UTC()
	if err := repo.UpdateVerification(context.Background(), id, execute.VerificationStatusVerified, map[string]any{"inference": "likely_executed"}, now, execute.ExecutionStatusSucceeded, ""); err != nil {
		t.Fatalf("UpdateVerification: %v", err)
	}
	attempt, _, err := repo.AttemptByID(context.Background(), id)
	if err != nil {
		t.Fatalf("AttemptByID: %v", err)
	}
	if attempt == nil {
		t.Fatalf("expected attempt")
	}
	if attempt.VerificationStatus != execute.VerificationStatusVerified {
		t.Fatalf("expected verified, got %s", attempt.VerificationStatus)
	}
	if attempt.ExecutionStatus != execute.ExecutionStatusSucceeded {
		t.Fatalf("expected succeeded, got %s", attempt.ExecutionStatus)
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
	return planID, items[0].ID
}

