package repository

import (
	"context"
	"path/filepath"
	"testing"

	"fantasy-baseball/internal/pickups"
	"fantasy-baseball/internal/store/sqlite"
)

func TestSaveAndRetrieveRecommendation(t *testing.T) {
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
	total := 15.2
	rank := 1

	runID, err := repo.SaveRecommendation(context.Background(), CreateRecommendationInput{
		SyncRunID:      nil,
		ImportRunID:    nil,
		CandidateRunID: nil,
		WindowStart:    "2026-09-15",
		WindowEnd:      "2026-09-22",
		Status:         "success",
		Summary:        map[string]any{"top_candidates": 1},
		Items: []pickups.RecommendationItem{{
			ItemType:            pickups.ItemTypeTopCandidate,
			PlayerName:          "Streamer Ace",
			MLBTeam:             "SEA",
			ProjectedStartCount: 2,
			TotalProjectedFPTS:  &total,
			ResultRank:          &rank,
			Flags:               []string{"two_start_week"},
			Notes:               []string{"strong upgrade"},
			Details:             map[string]any{"starts": 2},
		}},
	})
	if err != nil {
		t.Fatalf("SaveRecommendation: %v", err)
	}
	if runID == 0 {
		t.Fatalf("expected run id")
	}

	latestRun, latestItems, err := repo.LatestRecommendation(context.Background())
	if err != nil {
		t.Fatalf("LatestRecommendation: %v", err)
	}
	if latestRun == nil || latestRun.ID != runID {
		t.Fatalf("unexpected latest run: %#v", latestRun)
	}
	if len(latestItems) != 1 || latestItems[0].PlayerName != "Streamer Ace" {
		t.Fatalf("unexpected latest items: %+v", latestItems)
	}

	byIDRun, byIDItems, err := repo.RecommendationByID(context.Background(), runID)
	if err != nil {
		t.Fatalf("RecommendationByID: %v", err)
	}
	if byIDRun == nil || byIDRun.ID != runID {
		t.Fatalf("unexpected by id run: %#v", byIDRun)
	}
	if len(byIDItems) != 1 || byIDItems[0].ItemType != pickups.ItemTypeTopCandidate {
		t.Fatalf("unexpected by id items: %+v", byIDItems)
	}
}
