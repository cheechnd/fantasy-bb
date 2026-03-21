package pitchers

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fantasy-baseball/internal/config"
	"fantasy-baseball/internal/espn"
	esrepo "fantasy-baseball/internal/espn/repository"
	"fantasy-baseball/internal/execute"
	pitchplan "fantasy-baseball/internal/pitchers/planner"
	"fantasy-baseball/internal/store/sqlite"
)

type fakeWriter struct{}

func (fakeWriter) ExecuteLineupMove(context.Context, config.Config, LineupWriteRequest) (LineupWriteResult, error) {
	return LineupWriteResult{OK: true, Endpoint: "https://example", ResponseStatus: 200, ResponseJSON: map[string]any{"ok": true}}, nil
}

type fakeVerifier struct{}

func (fakeVerifier) VerifyLineupMove(context.Context, config.Config, LineupWriteRequest, LineupWriteResult) (execute.VerificationStatus, map[string]any, error) {
	return execute.VerificationStatusVerified, map[string]any{"inference": "likely_executed"}, nil
}

func TestGeneratePlanCreatesActivateAction(t *testing.T) {
	svc, _, closeFn := testService(t)
	defer closeFn()

	plan, err := svc.GeneratePlan(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("GeneratePlan: %v", err)
	}
	if plan == nil {
		t.Fatalf("expected lineup plan")
	}
	if len(plan.Items) != 1 {
		t.Fatalf("expected 1 lineup item, got %d", len(plan.Items))
	}
	if plan.Items[0].ActionType != ActionActivatePitcher {
		t.Fatalf("expected activate action, got %s", plan.Items[0].ActionType)
	}
}

func TestReviewQueuePreflightAndExecute(t *testing.T) {
	svc, cfg, closeFn := testService(t)
	defer closeFn()

	plan, err := svc.GeneratePlan(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("GeneratePlan: %v", err)
	}
	if len(plan.Items) == 0 {
		t.Fatalf("expected lineup items")
	}
	itemID := plan.Items[0].ID
	if _, err := svc.Transition(context.Background(), plan.ID, itemID, ReviewStateApproved, "ship"); err != nil {
		t.Fatalf("Transition approve: %v", err)
	}
	queue, err := svc.Queue(context.Background(), 10)
	if err != nil {
		t.Fatalf("Queue: %v", err)
	}
	if len(queue) != 1 {
		t.Fatalf("expected one queue row, got %d", len(queue))
	}
	pf, err := svc.Preflight(context.Background(), &itemID, 1)
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if len(pf.Items) != 1 {
		t.Fatalf("expected one preflight item, got %d", len(pf.Items))
	}
	if pf.Items[0].ValidationStatus != execute.StatusExecutable {
		t.Fatalf("expected executable preflight, got %s", pf.Items[0].ValidationStatus)
	}
	attempt, _, _, _, err := svc.Execute(context.Background(), cfg, itemID, true)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if attempt == nil {
		t.Fatalf("expected attempt")
	}
	if attempt.ExecutionStatus != execute.ExecutionStatusSucceeded {
		t.Fatalf("expected succeeded execution, got %s", attempt.ExecutionStatus)
	}
}

func TestPreflightBlocksWhenTargetSlotFull(t *testing.T) {
	svc, _, closeFn := testService(t)
	defer closeFn()

	plan, err := svc.GeneratePlan(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("GeneratePlan: %v", err)
	}
	if len(plan.Items) == 0 {
		t.Fatalf("expected lineup items")
	}
	itemID := plan.Items[0].ID
	if _, err := svc.Transition(context.Background(), plan.ID, itemID, ReviewStateApproved, "ready"); err != nil {
		t.Fatalf("Transition approve: %v", err)
	}

	// Create a newer roster snapshot where slot P is fully occupied.
	if err := seedSync(t, svc.espnRepo, "123", "8", 2026, map[string]any{"13": 1, "14": 0, "15": 0}, []espn.RosterSnapshot{
		{
			ESPNPlayerID:   int64Ptr(101),
			PlayerName:     "Test Pitcher",
			NormalizedName: "testpitcher",
			MLBTeam:        "NYM",
			RosterSlot:     "BE",
			IsPitcher:      true,
			Role:           "SP",
			StatusTag:      "ACTIVE",
			CreatedAt:      time.Now().UTC(),
		},
		{
			ESPNPlayerID:   int64Ptr(202),
			PlayerName:     "Active Pitcher",
			NormalizedName: "activepitcher",
			MLBTeam:        "LAD",
			RosterSlot:     "P",
			IsPitcher:      true,
			Role:           "SP",
			StatusTag:      "ACTIVE",
			CreatedAt:      time.Now().UTC(),
		},
	}); err != nil {
		t.Fatalf("seed sync: %v", err)
	}

	pf, err := svc.Preflight(context.Background(), &itemID, 1)
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if len(pf.Items) != 1 {
		t.Fatalf("expected one preflight item, got %d", len(pf.Items))
	}
	if pf.Items[0].ValidationStatus != execute.StatusBlocked {
		t.Fatalf("expected blocked preflight, got %s", pf.Items[0].ValidationStatus)
	}
	found := false
	for _, r := range pf.Items[0].Reasons {
		if r.Code == "target_slot_full" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected target_slot_full reason, got %+v", pf.Items[0].Reasons)
	}
}

func TestBuildLineupItemsCreatesBenchSwapWhenSlotsFull(t *testing.T) {
	totalStart := 20.0
	totalDrop := 0.0
	planItems := []pitchplan.PlanItem{
		{
			Bucket:              pitchplan.BucketLikelyStart,
			PlayerName:          "Add Starter",
			ProjectedStartCount: 1,
			TotalProjectedFPTS:  &totalStart,
		},
		{
			Bucket:              pitchplan.BucketMonitor,
			PlayerName:          "Drop Candidate",
			ProjectedStartCount: 0,
			TotalProjectedFPTS:  &totalDrop,
			Flags:               []string{"unmatched"},
		},
	}
	roster := []espn.RosterSnapshot{
		{
			PlayerName:     "Add Starter",
			NormalizedName: "addstarter",
			RosterSlot:     "BE",
			IsPitcher:      true,
		},
		{
			PlayerName:     "Drop Candidate",
			NormalizedName: "dropcandidate",
			RosterSlot:     "P",
			IsPitcher:      true,
		},
	}
	items, summary := buildLineupItems(planItems, roster, map[string]int{"P": 1, "SP": 0, "RP": 0})
	if len(items) != 2 {
		t.Fatalf("expected 2 lineup actions, got %d", len(items))
	}
	var sawBench, sawActivate bool
	for _, it := range items {
		if it.ActionType == ActionBenchPitcher && it.PlayerName == "Drop Candidate" {
			sawBench = true
		}
		if it.ActionType == ActionActivatePitcher && it.PlayerName == "Add Starter" {
			sawActivate = true
			if it.TargetSlot != "P" {
				t.Fatalf("expected activate target slot P, got %q", it.TargetSlot)
			}
		}
	}
	if !sawBench || !sawActivate {
		t.Fatalf("expected bench+activate actions, got %+v", items)
	}
	counts, _ := summary["counts"].(map[string]int)
	if counts["recommended_benches"] != 1 || counts["recommended_starts"] != 1 {
		t.Fatalf("unexpected summary counts: %+v", counts)
	}
}

func testService(t *testing.T) (*Service, config.Config, func()) {
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

	cfg := config.Default()
	cfg.DBPath = dbPath
	cfg.League.Platform = "espn"
	cfg.League.LeagueID = "123"
	cfg.League.TeamID = "8"
	cfg.League.Season = 2026
	cfg.Execution.Real.Enabled = true
	cfg.Execution.Real.RequireConfirmation = true

	pRepo := pitchplan.NewRepository(store.DB())
	rank := 1
	total := 12.5
	pitcherPlanID, err := pRepo.SavePlan(context.Background(), pitchplan.CreateInput{
		WindowStart: "2026-09-15",
		WindowEnd:   "2026-09-22",
		Status:      "success",
		Summary:     map[string]any{"counts": map[string]int{"likely_start": 1}},
		Items: []pitchplan.PlanItem{{
			Bucket:              pitchplan.BucketLikelyStart,
			PlayerName:          "Test Pitcher",
			ProjectedStartCount: 1,
			TotalProjectedFPTS:  &total,
			ResultRank:          &rank,
		}},
	})
	if err != nil {
		t.Fatalf("Save pitcher plan: %v", err)
	}

	eRepo := esrepo.New(store.DB())
	err = seedSync(t, eRepo, cfg.League.LeagueID, cfg.League.TeamID, cfg.League.Season, map[string]any{"13": 1, "14": 0, "15": 0}, []espn.RosterSnapshot{{
		SyncRunID:      0,
		ESPNPlayerID:   int64Ptr(101),
		PlayerName:     "Test Pitcher",
		NormalizedName: "testpitcher",
		MLBTeam:        "NYM",
		RosterSlot:     "BE",
		IsPitcher:      true,
		Role:           "SP",
		CreatedAt:      time.Now().UTC(),
	}})
	if err != nil {
		t.Fatalf("seed sync: %v", err)
	}

	svc := NewService(NewRepository(store.DB()), pRepo, eRepo, fakeWriter{}, fakeVerifier{})
	_ = pitcherPlanID
	return svc, cfg, func() { store.Close() }
}

func int64Ptr(v int64) *int64 { return &v }

func seedSync(t *testing.T, repo *esrepo.Repository, leagueID, teamID string, season int, slotCounts map[string]any, roster []espn.RosterSnapshot) error {
	t.Helper()
	settings := map[string]any{"lineupSlotCounts": slotCounts}
	settingsJSON, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	payloadJSON := string(settingsJSON)
	if strings.TrimSpace(payloadJSON) == "" {
		payloadJSON = "{}"
	}
	_, err = repo.PersistSync(context.Background(), esrepo.PersistSyncInput{
		SyncType: "roster",
		LeagueID: leagueID,
		TeamID:   teamID,
		Season:   season,
		Status:   "success",
		Summary:  map[string]any{"ok": true},
		Payloads: []espn.RawPayload{{PayloadType: "league_roster", SourceEndpoint: "x", ResponseStatus: 200, PayloadJSON: payloadJSON}},
		League: espn.LeagueSnapshot{
			LeagueID:     leagueID,
			TeamID:       teamID,
			Season:       season,
			LeagueName:   "L",
			TeamName:     "T",
			SettingsJSON: payloadJSON,
			CreatedAt:    time.Now().UTC(),
		},
		Roster: roster,
	})
	return err
}
