package repository

import (
	"context"
	"path/filepath"
	"testing"

	"fantasy-baseball/internal/execute"
	"fantasy-baseball/internal/store/sqlite"
	"fantasy-baseball/internal/transactions"
	tranrepo "fantasy-baseball/internal/transactions/repository"
)

func TestSaveAndLoadRun(t *testing.T) {
	store := mustStore(t)
	defer store.Close()

	approvedItemID, planID := seedApprovedRef(t, store)
	item := execute.RunItem{
		ApprovedItemID:   approvedItemID,
		SourcePlanID:     planID,
		AddPlayerName:    "Add Arm",
		DropPlayerName:   "Drop Arm",
		ValidationStatus: execute.StatusExecutable,
		ValidationReasons: []execute.Reason{{
			Code: "ok", Message: "all checks passed",
		}},
		ActionPreview: execute.ActionPreview{
			ActionType:         "add_drop_pitcher",
			ApprovedItemID:     approvedItemID,
			SourcePlanID:       planID,
			AddPlayerName:      "Add Arm",
			DropPlayerName:     "Drop Arm",
			ExecutionReadiness: "executable",
		},
	}
	repo := New(store.DB())
	runID, err := repo.SaveRun(context.Background(), CreateRunInput{
		RunType: execute.RunTypePreflight,
		Status:  "success",
		Summary: map[string]any{"ok": true},
		Items:   []execute.RunItem{item},
	})
	if err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	run, items, err := repo.RunByID(context.Background(), runID)
	if err != nil {
		t.Fatalf("RunByID: %v", err)
	}
	if run == nil || len(items) != 1 {
		t.Fatalf("unexpected run payload: %#v items=%d", run, len(items))
	}
	if items[0].ValidationStatus != execute.StatusExecutable {
		t.Fatalf("unexpected status: %s", items[0].ValidationStatus)
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

func seedApprovedRef(t *testing.T, s *sqlite.Store) (int64, int64) {
	t.Helper()
	repo := tranrepo.New(s.DB())
	addTotal := 10.0
	dropTotal := 5.0
	delta := 5.0
	planID, err := repo.SavePlan(context.Background(), transactions.CreatePlanInput{
		WindowStart: "2026-09-15",
		WindowEnd:   "2026-09-21",
		Status:      "success",
		Summary:     map[string]any{"counts": map[string]int{"strong_move": 1}},
		Items: []transactions.PlanItem{{
			Bucket:                  transactions.BucketStrongMove,
			AddPlayerName:           "Add Arm",
			DropPlayerName:          "Drop Arm",
			AddProjectedStartCount:  1,
			DropProjectedStartCount: 1,
			AddTotalProjectedFPTS:   &addTotal,
			DropTotalProjectedFPTS:  &dropTotal,
			DeltaFPTS:               &delta,
		}},
	})
	if err != nil {
		t.Fatalf("SavePlan: %v", err)
	}
	items, err := repo.PlanItems(context.Background(), planID)
	if err != nil || len(items) == 0 {
		t.Fatalf("PlanItems: %v len=%d", err, len(items))
	}
	return items[0].ID, planID
}
