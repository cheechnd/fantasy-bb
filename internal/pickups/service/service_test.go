package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	espn "fantasy-baseball/internal/espn"
	espnrepo "fantasy-baseball/internal/espn/repository"
	"fantasy-baseball/internal/forecaster"
	"fantasy-baseball/internal/pickups"
	pickrepo "fantasy-baseball/internal/pickups/repository"
	pitchrepo "fantasy-baseball/internal/pitchers/repository"
	pitchsvc "fantasy-baseball/internal/pitchers/service"
	"fantasy-baseball/internal/store/sqlite"
)

func TestRecommendBuildsAndPersistsCategories(t *testing.T) {
	store := mustOpenStore(t)
	defer store.Close()

	foreRepo := forecaster.NewRepository(store.DB())
	espnRepo := espnrepo.New(store.DB())
	pickRepo := pickrepo.New(store.DB())
	pSvc := pitchsvc.New(foreRepo, pitchrepo.New(store.DB()))
	svc := New(foreRepo, espnRepo, pickRepo, pSvc, Config{})

	from := time.Date(2026, 9, 15, 0, 0, 0, 0, time.Local)
	to := time.Date(2026, 9, 22, 0, 0, 0, 0, time.Local)

	importRunID := seedForecasterImport(t, foreRepo, from)
	syncRunID := seedESPNSync(t, espnRepo)
	candidateRunID := seedCandidateRun(t, espnRepo, syncRunID)

	res, err := svc.Recommend(context.Background(), pickups.RecommendOptions{
		From:           from,
		To:             to,
		SyncRunID:      &syncRunID,
		ImportRunID:    &importRunID,
		CandidateRunID: &candidateRunID,
		TopN:           10,
	})
	if err != nil {
		t.Fatalf("Recommend: %v", err)
	}
	if res.RecommendationRunID == 0 {
		t.Fatalf("expected persisted recommendation run id")
	}
	if len(res.TopCandidates) == 0 {
		t.Fatalf("unexpected empty top candidates")
	}
	foundStreamerAce := false
	for _, row := range res.TopCandidates {
		if row.PlayerName == "Streamer Ace" {
			foundStreamerAce = true
			break
		}
	}
	if !foundStreamerAce {
		t.Fatalf("expected Streamer Ace in top candidates: %+v", res.TopCandidates)
	}
	if len(res.Unmatched) == 0 || res.Unmatched[0].PlayerName != "Unknown Arm" {
		t.Fatalf("expected unmatched candidate, got %+v", res.Unmatched)
	}

	run, items, err := svc.Last(context.Background())
	if err != nil {
		t.Fatalf("Last: %v", err)
	}
	if run == nil || run.ID != res.RecommendationRunID {
		t.Fatalf("expected latest recommendation run %d, got %#v", res.RecommendationRunID, run)
	}
	if len(items) == 0 {
		t.Fatalf("expected persisted recommendation items")
	}

	runByID, itemsByID, err := svc.Show(context.Background(), res.RecommendationRunID)
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	if runByID == nil || len(itemsByID) == 0 {
		t.Fatalf("expected show by id payload")
	}
}

func TestBlockedStatusIncludedInDescriptiveOutput(t *testing.T) {
	store := mustOpenStore(t)
	defer store.Close()

	foreRepo := forecaster.NewRepository(store.DB())
	espnRepo := espnrepo.New(store.DB())
	pickRepo := pickrepo.New(store.DB())
	pSvc := pitchsvc.New(foreRepo, pitchrepo.New(store.DB()))
	svc := New(foreRepo, espnRepo, pickRepo, pSvc, Config{})

	from := time.Date(2026, 9, 15, 0, 0, 0, 0, time.Local)
	to := time.Date(2026, 9, 22, 0, 0, 0, 0, time.Local)
	importRunID := seedForecasterImport(t, foreRepo, from)
	syncRunID := seedESPNSync(t, espnRepo)
	candidateRunID := seedCandidateRun(t, espnRepo, syncRunID)

	res, err := svc.Recommend(context.Background(), pickups.RecommendOptions{
		From:           from,
		To:             to,
		SyncRunID:      &syncRunID,
		ImportRunID:    &importRunID,
		CandidateRunID: &candidateRunID,
		TopN:           20,
	})
	if err != nil {
		t.Fatalf("Recommend: %v", err)
	}

	foundOut := false
	for _, row := range res.TopCandidates {
		if row.PlayerName == "Out Candidate" {
			foundOut = true
			break
		}
	}
	if !foundOut {
		t.Fatalf("OUT candidate should appear in descriptive top candidates")
	}
}

func TestIsHardBlockedStatusIncludesDLVariants(t *testing.T) {
	if !isHardBlockedStatus("SIXTY_DAY_DL") {
		t.Fatalf("expected SIXTY_DAY_DL to be hard blocked")
	}
	if !isHardBlockedStatus("15_DAY_DL") {
		t.Fatalf("expected 15_DAY_DL to be hard blocked")
	}
	if isHardBlockedStatus("DAY_TO_DAY") {
		t.Fatalf("DAY_TO_DAY should be soft risk, not hard blocked")
	}
}

func seedForecasterImport(t *testing.T, repo *forecaster.Repository, base time.Time) int64 {
	t.Helper()
	d1 := base
	d2 := base.AddDate(0, 0, 2)
	d3 := base.AddDate(0, 0, 1)
	starts := []forecaster.ProbableStartInput{
		{SourceDateRaw: "Tue", GameDate: &d1, Team: "SEA", Opponent: "OAK", PitcherName: "Streamer Ace", ThrowsHand: "R", Status: forecaster.StatusScheduled, ProjectedFPTS: floatPtr(9.1)},
		{SourceDateRaw: "Thu", GameDate: &d2, Team: "SEA", Opponent: "LAA", PitcherName: "Streamer Ace", ThrowsHand: "R", Status: forecaster.StatusScheduled, ProjectedFPTS: floatPtr(10.2)},
		{SourceDateRaw: "Wed", GameDate: &d3, Team: "NYY", Opponent: "BOS", PitcherName: "Risky TBD", ThrowsHand: "R", Status: forecaster.StatusTBD, ProjectedFPTS: floatPtr(7.0)},
		{SourceDateRaw: "Tue", GameDate: &d1, Team: "MIA", Opponent: "ATL", PitcherName: "Weak Roster Arm", ThrowsHand: "R", Status: forecaster.StatusScheduled, ProjectedFPTS: floatPtr(3.5)},
		{SourceDateRaw: "Tue", GameDate: &d1, Team: "PHI", Opponent: "NYM", PitcherName: "Solid Roster Arm", ThrowsHand: "L", Status: forecaster.StatusScheduled, ProjectedFPTS: floatPtr(12.1)},
		{SourceDateRaw: "Wed", GameDate: &d3, Team: "BOS", Opponent: "TOR", PitcherName: "Out Candidate", ThrowsHand: "R", Status: forecaster.StatusScheduled, ProjectedFPTS: floatPtr(12.8)},
	}
	run, err := repo.InsertImport(context.Background(), forecaster.SourceTypeFile, "fixture.html", 1, starts, nil, "success", "{}")
	if err != nil {
		t.Fatalf("InsertImport: %v", err)
	}
	return run.ID
}

func seedESPNSync(t *testing.T, repo *espnrepo.Repository) int64 {
	t.Helper()
	runID, err := repo.PersistSync(context.Background(), espnrepo.PersistSyncInput{
		SyncType: "roster",
		LeagueID: "123",
		TeamID:   "8",
		Season:   2026,
		Status:   "success",
		Summary:  map[string]any{"ok": true},
		Payloads: []espn.RawPayload{{PayloadType: "league_roster", SourceEndpoint: "http://example.test", ResponseStatus: 200, PayloadJSON: "{}"}},
		League:   espn.LeagueSnapshot{LeagueID: "123", TeamID: "8", Season: 2026, LeagueName: "Test League", TeamName: "Test Team", CreatedAt: time.Now().UTC()},
		Roster: []espn.RosterSnapshot{
			{PlayerName: "Weak Roster Arm", NormalizedName: "weak roster arm", MLBTeam: "MIA", RosterSlot: "P", IsPitcher: true, Role: "SP", StatusTag: "OUT", CreatedAt: time.Now().UTC()},
			{PlayerName: "Solid Roster Arm", NormalizedName: "solid roster arm", MLBTeam: "PHI", RosterSlot: "P", IsPitcher: true, Role: "SP", StatusTag: "ACTIVE", CreatedAt: time.Now().UTC()},
		},
	})
	if err != nil {
		t.Fatalf("PersistSync: %v", err)
	}
	return runID
}

func seedCandidateRun(t *testing.T, repo *espnrepo.Repository, syncRunID int64) int64 {
	t.Helper()
	aceID := int64(1001)
	riskyID := int64(1002)
	unknownID := int64(1003)
	outID := int64(1004)
	runID, err := repo.PersistCandidates(context.Background(), espnrepo.PersistCandidateInput{
		SyncRunID:    &syncRunID,
		QueryType:    "pitchers",
		QueryText:    "",
		Filters:      map[string]any{"limit": 25},
		Status:       "success",
		WarningCount: 0,
		Summary:      map[string]any{"candidate_count": 4},
		Payload:      espn.RawPayload{PayloadType: "free_agents_pitchers", SourceEndpoint: "http://example.test/fa", ResponseStatus: 200, PayloadJSON: "{}"},
		Candidates: []espn.FreeAgentCandidate{
			{ESPNPlayerID: &aceID, PlayerName: "Streamer Ace", NormalizedName: "streamer ace", MLBTeam: "SEA", IsPitcher: true, Role: "SP", StatusTag: "ACTIVE", RawPlayerJSON: "{}", CreatedAt: time.Now().UTC()},
			{ESPNPlayerID: &riskyID, PlayerName: "Risky TBD", NormalizedName: "risky tbd", MLBTeam: "NYY", IsPitcher: true, Role: "SP", StatusTag: "ACTIVE", RawPlayerJSON: "{}", CreatedAt: time.Now().UTC()},
			{ESPNPlayerID: &unknownID, PlayerName: "Unknown Arm", NormalizedName: "unknown arm", MLBTeam: "DET", IsPitcher: true, Role: "SP", StatusTag: "ACTIVE", RawPlayerJSON: "{}", CreatedAt: time.Now().UTC()},
			{ESPNPlayerID: &outID, PlayerName: "Out Candidate", NormalizedName: "out candidate", MLBTeam: "BOS", IsPitcher: true, Role: "SP", StatusTag: "OUT", RawPlayerJSON: "{}", CreatedAt: time.Now().UTC()},
		},
	})
	if err != nil {
		t.Fatalf("PersistCandidates: %v", err)
	}
	return runID
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

func floatPtr(v float64) *float64 { return &v }
