package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	esrepo "fantasy-baseball/internal/espn/repository"
	"fantasy-baseball/internal/forecaster"
	"fantasy-baseball/internal/pickups"
	pickrepo "fantasy-baseball/internal/pickups/repository"
	pitchplan "fantasy-baseball/internal/pitchers/planner"
	"fantasy-baseball/internal/store/sqlite"
	"fantasy-baseball/internal/transactions"
	tranrepo "fantasy-baseball/internal/transactions/repository"
)

func TestGenerateAndSavePlan(t *testing.T) {
	store := mustOpenStore(t)
	defer store.Close()

	svc, planID, pickupRunID := seedServiceWithArtifacts(t, store)
	from := time.Date(2026, 9, 15, 0, 0, 0, 0, time.Local)
	to := time.Date(2026, 9, 22, 0, 0, 0, 0, time.Local)
	plan, err := svc.GenerateAndSave(context.Background(), transactions.Options{
		From:          from,
		To:            to,
		PitcherPlanID: &planID,
		PickupRunID:   &pickupRunID,
		TopN:          10,
	})
	if err != nil {
		t.Fatalf("GenerateAndSave: %v", err)
	}
	if plan == nil || plan.ID == 0 {
		t.Fatalf("expected saved transaction plan")
	}
	if len(plan.Items) == 0 {
		t.Fatalf("expected transaction moves")
	}
	if plan.Items[0].DeltaFPTS == nil || *plan.Items[0].DeltaFPTS <= 0 {
		t.Fatalf("expected positive delta move: %+v", plan.Items[0])
	}
}

func TestDropCandidateExcludesLockedAndMustHold(t *testing.T) {
	cfg := defaultCfg()
	svc := &Service{cfg: cfg}
	rows := []pitchplan.PlanItem{
		{Bucket: pitchplan.BucketBench, PlayerName: "Locked Arm", Flags: []string{"locked"}, ProjectedStartCount: 0},
		{Bucket: pitchplan.BucketMonitor, PlayerName: "Must Hold Arm", Flags: []string{"must_hold"}, ProjectedStartCount: 1},
		{Bucket: pitchplan.BucketNoStartScheduled, PlayerName: "Drop Me", ProjectedStartCount: 0},
	}
	drops := svc.selectDropCandidates(rows)
	if len(drops) != 1 || drops[0].Item.PlayerName != "Drop Me" {
		t.Fatalf("unexpected drop candidates: %+v", drops)
	}
}

func TestPairingRespectsMaxPairings(t *testing.T) {
	cfg := defaultCfg()
	cfg.MaxPairings = 3
	svc := &Service{cfg: cfg}
	plan := &pitchplan.Plan{
		ID:          10,
		WindowStart: "2026-09-15",
		WindowEnd:   "2026-09-22",
		Items: []pitchplan.PlanItem{
			{Bucket: pitchplan.BucketNoStartScheduled, PlayerName: "Drop A"},
			{Bucket: pitchplan.BucketBench, PlayerName: "Drop B"},
			{Bucket: pitchplan.BucketMonitor, PlayerName: "Drop C"},
		},
	}
	pickupRun := &pickups.RecommendationRun{ID: 11}
	pickupItems := []pickups.RecommendationItem{
		{ItemType: pickups.ItemTypeTopCandidate, PlayerName: "Add A", ProjectedStartCount: 2, TotalProjectedFPTS: floatPtr(15.0)},
		{ItemType: pickups.ItemTypeTopCandidate, PlayerName: "Add B", ProjectedStartCount: 2, TotalProjectedFPTS: floatPtr(14.0)},
		{ItemType: pickups.ItemTypeTopCandidate, PlayerName: "Add C", ProjectedStartCount: 1, TotalProjectedFPTS: floatPtr(12.0)},
	}
	items := svc.buildPlanItems(plan, pickupRun, pickupItems, plan.WindowStart, plan.WindowEnd)
	if len(items) != 3 {
		t.Fatalf("expected max_pairings bound of 3, got %d", len(items))
	}
}

func TestClassificationBuckets(t *testing.T) {
	cfg := defaultCfg()
	addStrong := pickupCandidate{Total: 20.0, Uncertainty: 0, Uncertain: false}
	if got := classifyMoveBucket(8.0, 8.0, addStrong, cfg); got != transactions.BucketStrongMove {
		t.Fatalf("expected strong_move, got %s", got)
	}
	if got := classifyMoveBucket(2.0, 2.0, addStrong, cfg); got != transactions.BucketMarginalMove {
		t.Fatalf("expected marginal_move, got %s", got)
	}
	addRisk := pickupCandidate{Total: 20.0, Uncertainty: 2.0, Uncertain: true}
	if got := classifyMoveBucket(2.0, 0.0, addRisk, cfg); got != transactions.BucketRiskyMove {
		t.Fatalf("expected risky_move, got %s", got)
	}
	if got := classifyMoveBucket(-0.1, -0.1, addStrong, cfg); got != transactions.BucketWatchOnly {
		t.Fatalf("expected watch_only, got %s", got)
	}
}

func TestBuildMoveItem_TwoStartIsInformationalOnly(t *testing.T) {
	cfg := defaultCfg()
	add := pickupCandidate{
		Item: pickups.RecommendationItem{
			PlayerName:          "Two Start Arm",
			MLBTeam:             "ATL",
			ProjectedStartCount: 2,
			Flags:               []string{"two_start_week"},
		},
		Total:       16.0,
		AvgPerStart: 8.0,
	}
	drop := dropCandidate{
		Item: pitchplan.PlanItem{
			PlayerName:          "High Quality Arm",
			MLBTeam:             "PHI",
			ProjectedStartCount: 1,
		},
		Total:       14.0,
		AvgPerStart: 14.0,
	}
	item := buildMoveItem(add, drop, cfg, 1, 1, "2026-09-15", "2026-09-22")
	if item.Bucket != transactions.BucketWatchOnly {
		t.Fatalf("expected watch_only due to negative per-start delta, got %s", item.Bucket)
	}
	if !hasAny(item.Flags, "two_start_week") {
		t.Fatalf("expected two_start_week informational flag, got %+v", item.Flags)
	}
}

func TestLatestAndByID(t *testing.T) {
	store := mustOpenStore(t)
	defer store.Close()
	svc, planID, pickupRunID := seedServiceWithArtifacts(t, store)
	from := time.Date(2026, 9, 15, 0, 0, 0, 0, time.Local)
	to := time.Date(2026, 9, 22, 0, 0, 0, 0, time.Local)
	plan, err := svc.GenerateAndSave(context.Background(), transactions.Options{
		From:          from,
		To:            to,
		PitcherPlanID: &planID,
		PickupRunID:   &pickupRunID,
		TopN:          5,
	})
	if err != nil {
		t.Fatalf("GenerateAndSave: %v", err)
	}

	latest, err := svc.Latest(context.Background())
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if latest == nil || latest.ID != plan.ID {
		t.Fatalf("unexpected latest plan: %#v", latest)
	}
	byID, err := svc.ByID(context.Background(), plan.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if byID == nil || len(byID.Items) == 0 {
		t.Fatalf("expected plan by id with items")
	}
}

func seedServiceWithArtifacts(t *testing.T, store *sqlite.Store) (*Service, int64, int64) {
	t.Helper()
	foreRepo := forecaster.NewRepository(store.DB())
	espnRepo := esrepo.New(store.DB())
	planRepo := pitchplan.NewRepository(store.DB())
	pickRepo := pickrepo.New(store.DB())
	tranRepo := tranrepo.New(store.DB())
	svc := New(foreRepo, espnRepo, planRepo, pickRepo, tranRepo, defaultCfg())

	planID, err := planRepo.SavePlan(context.Background(), pitchplan.CreateInput{
		WindowStart: "2026-09-15",
		WindowEnd:   "2026-09-22",
		Status:      "success",
		Summary:     map[string]interface{}{"counts": map[string]int{"bench": 1, "monitor": 1}},
		Items: []pitchplan.PlanItem{
			{Bucket: pitchplan.BucketNoStartScheduled, PlayerName: "Drop Zero", MLBTeam: "MIA", ProjectedStartCount: 0, TotalProjectedFPTS: floatPtr(0.0), Flags: []string{"has_notes"}},
			{Bucket: pitchplan.BucketMonitor, PlayerName: "Drop Low", MLBTeam: "PIT", ProjectedStartCount: 1, TotalProjectedFPTS: floatPtr(6.1)},
			{Bucket: pitchplan.BucketLikelyStart, PlayerName: "Keep Strong", MLBTeam: "PHI", ProjectedStartCount: 2, TotalProjectedFPTS: floatPtr(24.0), Flags: []string{"locked"}},
		},
	})
	if err != nil {
		t.Fatalf("SavePlan: %v", err)
	}

	rank1 := 1
	rank2 := 2
	pickupRunID, err := pickRepo.SaveRecommendation(context.Background(), pickrepo.CreateRecommendationInput{
		WindowStart: "2026-09-15",
		WindowEnd:   "2026-09-22",
		Status:      "success",
		Summary:     map[string]any{"top_candidates": 2},
		Items: []pickups.RecommendationItem{
			{ItemType: pickups.ItemTypeTopCandidate, PlayerName: "Add Ace", MLBTeam: "SEA", ProjectedStartCount: 2, TotalProjectedFPTS: floatPtr(18.0), ResultRank: &rank1, Flags: []string{"two_start_week"}},
			{ItemType: pickups.ItemTypeTopCandidate, PlayerName: "Add Risk", MLBTeam: "NYY", ProjectedStartCount: 1, TotalProjectedFPTS: floatPtr(9.0), ResultRank: &rank2, Flags: []string{"tbd"}},
		},
	})
	if err != nil {
		t.Fatalf("SaveRecommendation: %v", err)
	}
	return svc, planID, pickupRunID
}

func mustOpenStore(t *testing.T) *sqlite.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "fb.db")
	if _, err := sqlite.EnsureDatabaseFile(dbPath); err != nil {
		t.Fatalf("EnsureDatabaseFile: %v", err)
	}
	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	if _, err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return store
}

func defaultCfg() transactions.ServiceConfig {
	return transactions.ServiceConfig{
		TopMoveLimit:                   10,
		MaxPairings:                    25,
		StrongMoveDeltaFPTS:            5.0,
		MarginalMoveDeltaFPTS:          1.5,
		RiskyMoveMinDeltaFPTS:          0.5,
		UncertaintyPenaltyTBD:          2.0,
		UncertaintyPenaltyMissingProj:  3.0,
		UncertaintyPenaltyAmbiguous:    4.0,
		AllowCompareAgainstLikelyStart: false,
	}
}
