package repository

import (
	"context"
	"path/filepath"
	"testing"

	"fantasy-baseball/internal/store/sqlite"
	"fantasy-baseball/internal/transactions"
)

func TestSaveAndLoadPlan(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "fb.db")
	if _, err := sqlite.EnsureDatabaseFile(dbPath); err != nil {
		t.Fatalf("EnsureDatabaseFile: %v", err)
	}
	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer store.Close()
	if _, err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	repo := New(store.DB())
	addTotal := 18.2
	dropTotal := 8.1
	delta := 10.1
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
			Flags:                   []string{"two_start_week"},
			Notes:                   []string{"strong projected weekly upgrade"},
			Details:                 map[string]interface{}{"window": "test"},
		}},
	})
	if err != nil {
		t.Fatalf("SavePlan: %v", err)
	}
	if planID == 0 {
		t.Fatalf("expected plan id")
	}

	latestPlan, latestItems, err := repo.LatestPlan(context.Background())
	if err != nil {
		t.Fatalf("LatestPlan: %v", err)
	}
	if latestPlan == nil || latestPlan.ID != planID {
		t.Fatalf("expected latest plan %d, got %#v", planID, latestPlan)
	}
	if len(latestItems) != 1 || latestItems[0].AddPlayerName != "Add Arm" {
		t.Fatalf("unexpected latest items: %+v", latestItems)
	}

	byIDPlan, byIDItems, err := repo.PlanByID(context.Background(), planID)
	if err != nil {
		t.Fatalf("PlanByID: %v", err)
	}
	if byIDPlan == nil || byIDPlan.ID != planID {
		t.Fatalf("unexpected by-id plan: %#v", byIDPlan)
	}
	if len(byIDItems) != 1 || byIDItems[0].Bucket != transactions.BucketStrongMove {
		t.Fatalf("unexpected by-id items: %+v", byIDItems)
	}
}
