package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fantasy-baseball/internal/forecaster"
	"fantasy-baseball/internal/pitchers"
	pitchrepo "fantasy-baseball/internal/pitchers/repository"
	"fantasy-baseball/internal/store/sqlite"
)

func setupService(t *testing.T) (*Service, int64) {
	t.Helper()
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
	fRepo := forecaster.NewRepository(store.DB())
	pRepo := pitchrepo.New(store.DB())
	svc := New(fRepo, pRepo)

	date1 := time.Date(2026, 9, 15, 0, 0, 0, 0, time.Local)
	date2 := date1.AddDate(0, 0, 3)
	date3 := date1.AddDate(0, 0, 1)
	starts := []forecaster.ProbableStartInput{
		{SourceDateRaw: "Tue, 9/15", GameDate: &date1, Team: "NYY", Opponent: "BOS", PitcherName: "Gerrit Cole", ThrowsHand: "R", Status: forecaster.StatusScheduled, ProjectedFPTS: floatPtr(12.0)},
		{SourceDateRaw: "Fri, 9/18", GameDate: &date2, Team: "NYY", Opponent: "TOR", PitcherName: "Gerrit Cole", ThrowsHand: "R", Status: forecaster.StatusScheduled, ProjectedFPTS: floatPtr(10.0)},
		{SourceDateRaw: "Wed, 9/16", GameDate: &date3, Team: "PHI", Opponent: "NYM", PitcherName: "Zack Wheeler", ThrowsHand: "R", Status: forecaster.StatusScheduled, ProjectedFPTS: floatPtr(11.5)},
		{SourceDateRaw: "Wed, 9/16", GameDate: &date3, Team: "CHC", Opponent: "MIL", PitcherName: "Jordan Wicks", ThrowsHand: "L", Status: forecaster.StatusScheduled, ProjectedFPTS: floatPtr(8.2)},
	}
	run, err := fRepo.InsertImport(context.Background(), forecaster.SourceTypeFile, "test.html", 1, starts, nil, "success", "{}")
	if err != nil {
		t.Fatalf("InsertImport: %v", err)
	}
	return svc, run.ID
}

func TestAnalyzeWeekAndTwoStart(t *testing.T) {
	svc, importRunID := setupService(t)
	from := time.Date(2026, 9, 15, 0, 0, 0, 0, time.Local)
	to := from.AddDate(0, 0, 6)
	report, err := svc.AnalyzeWeek(context.Background(), pitchers.AnalysisOptions{
		From:        from,
		To:          to,
		ImportRunID: &importRunID,
		RosterInputs: []pitchers.RosterInput{
			{PlayerName: "Gerrit Cole", MLBTeam: "NYY", Locked: true},
			{PlayerName: "Zack Wheeler", MLBTeam: "PHI"},
			{PlayerName: "Missing Guy"},
		},
		RosterSource: "espn:sync_run:55",
	})
	if err != nil {
		t.Fatalf("AnalyzeWeek: %v", err)
	}
	if len(report.RankedPitchers) != 2 {
		t.Fatalf("expected 2 matched pitchers, got %d", len(report.RankedPitchers))
	}
	if report.RankedPitchers[0].PlayerName != "Gerrit Cole" || report.RankedPitchers[0].StartCount != 2 {
		t.Fatalf("unexpected top projection: %+v", report.RankedPitchers[0])
	}
	if len(report.TwoStartPitchers) != 1 {
		t.Fatalf("expected one two-start pitcher, got %d", len(report.TwoStartPitchers))
	}
	if len(report.UnmatchedPlayers) != 1 || !strings.Contains(report.UnmatchedPlayers[0].InputPlayerName, "Missing") {
		t.Fatalf("expected unmatched roster row, got %+v", report.UnmatchedPlayers)
	}

	twoStart, err := svc.TwoStart(context.Background(), pitchers.AnalysisOptions{
		From:        from,
		To:          to,
		ImportRunID: &importRunID,
		RosterInputs: []pitchers.RosterInput{
			{PlayerName: "Gerrit Cole", MLBTeam: "NYY"},
			{PlayerName: "Zack Wheeler", MLBTeam: "PHI"},
		},
		RosterSource: "espn:sync_run:55",
	})
	if err != nil {
		t.Fatalf("TwoStart: %v", err)
	}
	if len(twoStart.RankedPitchers) != 1 || twoStart.RankedPitchers[0].PlayerName != "Gerrit Cole" {
		t.Fatalf("unexpected two-start results: %+v", twoStart.RankedPitchers)
	}
}

func TestAnalyzeWeekWithRosterInputs(t *testing.T) {
	svc, importRunID := setupService(t)
	from := time.Date(2026, 9, 15, 0, 0, 0, 0, time.Local)
	to := from.AddDate(0, 0, 6)
	report, err := svc.AnalyzeWeek(context.Background(), pitchers.AnalysisOptions{
		From:        from,
		To:          to,
		ImportRunID: &importRunID,
		RosterInputs: []pitchers.RosterInput{
			{PlayerName: "Gerrit Cole", MLBTeam: "NYY"},
			{PlayerName: "Zack Wheeler", MLBTeam: "PHI"},
		},
		RosterSource: "espn:sync_run:12",
	})
	if err != nil {
		t.Fatalf("AnalyzeWeek with roster inputs: %v", err)
	}
	if len(report.RankedPitchers) != 2 {
		t.Fatalf("expected 2 ranked pitchers, got %d", len(report.RankedPitchers))
	}
	run, _, err := svc.LastReport(context.Background())
	if err != nil {
		t.Fatalf("LastReport: %v", err)
	}
	if run == nil || run.RosterPath != "espn:sync_run:12" {
		t.Fatalf("expected persisted roster source marker, got %+v", run)
	}
}

func TestLastReportPersistence(t *testing.T) {
	svc, importRunID := setupService(t)
	from := time.Date(2026, 9, 15, 0, 0, 0, 0, time.Local)
	to := from.AddDate(0, 0, 6)
	_, err := svc.AnalyzeWeek(context.Background(), pitchers.AnalysisOptions{
		From:        from,
		To:          to,
		ImportRunID: &importRunID,
		RosterInputs: []pitchers.RosterInput{
			{PlayerName: "Gerrit Cole", MLBTeam: "NYY"},
			{PlayerName: "No Match"},
		},
		RosterSource: "espn:sync_run:99",
	})
	if err != nil {
		t.Fatalf("AnalyzeWeek: %v", err)
	}

	run, rows, err := svc.LastReport(context.Background())
	if err != nil {
		t.Fatalf("LastReport: %v", err)
	}
	if run == nil || len(rows) == 0 {
		t.Fatalf("expected persisted analysis run/results, got run=%v rows=%d", run, len(rows))
	}
}

func TestAnalyzeWeekRequiresRosterInputs(t *testing.T) {
	svc, importRunID := setupService(t)
	from := time.Date(2026, 9, 15, 0, 0, 0, 0, time.Local)
	to := from.AddDate(0, 0, 6)
	_, err := svc.AnalyzeWeek(context.Background(), pitchers.AnalysisOptions{From: from, To: to, ImportRunID: &importRunID})
	if err == nil || !strings.Contains(err.Error(), "roster inputs are required") {
		t.Fatalf("expected roster inputs error, got %v", err)
	}
}

func floatPtr(v float64) *float64 { return &v }
