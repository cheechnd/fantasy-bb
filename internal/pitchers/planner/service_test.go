package planner

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"fantasy-baseball/internal/espn"
	"fantasy-baseball/internal/pitchers"
	"fantasy-baseball/internal/store/sqlite"
)

func TestBuildPlanItemsBuckets(t *testing.T) {
	rules := RuleConfig{
		AutoStartMinTotalFPTS:    20,
		LikelyStartMinTotalFPTS:  12,
		MonitorMinTotalFPTS:      6,
		TBDPenalty:               3,
		MissingProjectionPenalty: 4,
		AmbiguousMatchPenalty:    5,
	}
	report := pitchers.AnalysisReport{
		RankedPitchers: []pitchers.PitcherProjection{
			{PlayerName: "Ace One", StartCount: 2, TotalProjectedFPTS: 18.5, HighestSingleFPTS: 12.2, Flags: []string{"two_start_week"}},
			{PlayerName: "Solid Two", StartCount: 1, TotalProjectedFPTS: 13.0, HighestSingleFPTS: 13.0},
			{PlayerName: "Risky Three", StartCount: 1, TotalProjectedFPTS: 9.0, HighestSingleFPTS: 9.0, Flags: []string{"tbd"}},
			{PlayerName: "Low Four", StartCount: 1, TotalProjectedFPTS: 4.5, HighestSingleFPTS: 4.5},
			{PlayerName: "NoStart Five", StartCount: 0, TotalProjectedFPTS: 0.0},
		},
		UnmatchedPlayers: []pitchers.MatchResult{{InputPlayerName: "Unknown Six", MatchStatus: pitchers.MatchStatusUnmatched, Explanation: "not found"}},
		AmbiguousPlayers: []pitchers.MatchResult{{InputPlayerName: "Ambiguous Seven", MatchStatus: pitchers.MatchStatusAmbiguous, CandidateDisplayList: []string{"A", "B"}}},
	}
	rows, summary := BuildPlanItems(report, nil, rules)

	if len(rows) != 7 {
		t.Fatalf("expected 7 plan items, got %d", len(rows))
	}
	if summary[BucketAutoStart] != 0 {
		t.Fatalf("expected 0 auto_start, got %d", summary[BucketAutoStart])
	}
	if summary[BucketLikelyStart] != 2 {
		t.Fatalf("expected 2 likely_start, got %d", summary[BucketLikelyStart])
	}
	if summary[BucketMonitor] != 2 {
		t.Fatalf("expected 2 monitor, got %d", summary[BucketMonitor])
	}
	if summary[BucketBench] != 1 {
		t.Fatalf("expected 1 bench, got %d", summary[BucketBench])
	}
	if summary[BucketNoStartScheduled] != 2 {
		t.Fatalf("expected 2 no_start_scheduled, got %d", summary[BucketNoStartScheduled])
	}
}

func TestGenerateAndRetrievePlanPersistence(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "fb.db")
	if _, err := sqlite.EnsureDatabaseFile(dbPath); err != nil {
		t.Fatalf("EnsureDatabaseFile: %v", err)
	}
	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	repo := NewRepository(store.DB())
	svc := NewService(repo)

	espnID := int64(12345)
	rules := RuleConfig{
		AutoStartMinTotalFPTS:    20,
		LikelyStartMinTotalFPTS:  12,
		MonitorMinTotalFPTS:      6,
		TBDPenalty:               3,
		MissingProjectionPenalty: 4,
		AmbiguousMatchPenalty:    5,
	}
	report := pitchers.AnalysisReport{
		AnalysisRunID:  88,
		WindowStart:    "2026-09-15",
		WindowEnd:      "2026-09-21",
		RankedPitchers: []pitchers.PitcherProjection{{PlayerName: "Zack Wheeler", StartCount: 2, TotalProjectedFPTS: 30.1, HighestSingleFPTS: 16.0, MatchedPitcherName: "Zack Wheeler"}},
	}
	plan, err := svc.GenerateAndSave(context.Background(), GenerateInput{
		WindowStart: report.WindowStart,
		WindowEnd:   report.WindowEnd,
		Rules:       rules,
		Analysis:    report,
		RosterSnapshots: []espn.RosterSnapshot{{
			SyncRunID:    9,
			ESPNPlayerID: &espnID,
			PlayerName:   "Zack Wheeler",
			MLBTeam:      "PHI",
			CreatedAt:    time.Now().UTC(),
		}},
	})
	if err != nil {
		t.Fatalf("GenerateAndSave: %v", err)
	}
	if plan == nil || plan.ID == 0 {
		t.Fatalf("expected saved plan id, got %#v", plan)
	}
	if len(plan.Items) != 1 {
		t.Fatalf("expected 1 plan item, got %d", len(plan.Items))
	}
	if plan.Items[0].ESPNPlayerID == nil || *plan.Items[0].ESPNPlayerID != espnID {
		t.Fatalf("expected espn player id %d, got %#v", espnID, plan.Items[0].ESPNPlayerID)
	}
	if plan.Items[0].Bucket != BucketLikelyStart {
		t.Fatalf("expected likely_start bucket, got %s", plan.Items[0].Bucket)
	}

	latest, err := svc.Latest(context.Background())
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if latest == nil || latest.ID != plan.ID {
		t.Fatalf("expected latest plan %d, got %#v", plan.ID, latest)
	}

	byID, err := svc.ByID(context.Background(), plan.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if byID == nil || byID.ID != plan.ID {
		t.Fatalf("expected by id plan %d, got %#v", plan.ID, byID)
	}
}
