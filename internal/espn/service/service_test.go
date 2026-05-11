package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fantasy-baseball/internal/config"
	"fantasy-baseball/internal/espn"
	"fantasy-baseball/internal/espn/repository"
	"fantasy-baseball/internal/forecaster"
	"fantasy-baseball/internal/pitchers"
	pitchrepo "fantasy-baseball/internal/pitchers/repository"
	pitchsvc "fantasy-baseball/internal/pitchers/service"
	"fantasy-baseball/internal/store/sqlite"
)

func TestParseLeaguePayload(t *testing.T) {
	payload := mustReadFixture(t, "testdata/league_roster.json")
	parsed, err := parseLeaguePayload(payload, "7")
	if err != nil {
		t.Fatalf("parseLeaguePayload: %v", err)
	}
	if parsed.League.LeagueName != "Jake Test League" {
		t.Fatalf("league name = %q", parsed.League.LeagueName)
	}
	if len(parsed.Roster) != 4 {
		t.Fatalf("roster count = %d, want 4", len(parsed.Roster))
	}
	if parsed.Roster[0].PlayerName != "Gerrit Cole" || !parsed.Roster[0].IsPitcher {
		t.Fatalf("unexpected pitcher row: %+v", parsed.Roster[0])
	}
	if parsed.Roster[0].MLBTeam != "NYY" {
		t.Fatalf("expected fallback mlb team NYY, got %q", parsed.Roster[0].MLBTeam)
	}
	if parsed.Roster[3].IsPitcher {
		t.Fatalf("expected hitter row to not be pitcher")
	}
	if len(parsed.Warnings) == 0 {
		t.Fatalf("expected warning for uncertain role classification")
	}
}

func TestSyncRosterPersistAndRosterInputs(t *testing.T) {
	store := mustOpenStore(t)
	defer store.Close()

	srv := newTestServer(t, mustReadFixture(t, "testdata/league_roster.json"))
	defer srv.Close()

	svc := New(repository.New(store.DB()))
	cfg := baseTestConfig(srv.URL)
	t.Setenv(cfg.Auth.ESPNS2Env, "cookie-s2")
	t.Setenv(cfg.Auth.SWIDEnv, "{cookie-swid}")

	summary, err := svc.SyncRoster(context.Background(), cfg, SyncOptions{})
	if err != nil {
		t.Fatalf("SyncRoster: %v", err)
	}
	if summary.SyncRunID == nil {
		t.Fatalf("expected sync run id")
	}
	if summary.RosteredPlayers != 4 || summary.PitcherCount != 3 {
		t.Fatalf("unexpected summary: %+v", summary)
	}

	rows, err := svc.ShowRoster(context.Background(), ShowRosterFilter{PitchersOnly: true})
	if err != nil {
		t.Fatalf("ShowRoster: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("pitcher rows = %d, want 3", len(rows))
	}
	warnings, err := svc.Warnings(context.Background(), summary.SyncRunID, 50)
	if err != nil {
		t.Fatalf("Warnings: %v", err)
	}
	if len(warnings) == 0 {
		t.Fatalf("expected persisted warnings")
	}
	if warnings[0].WarningType == "" || warnings[0].Message == "" {
		t.Fatalf("invalid warning row: %+v", warnings[0])
	}

	inputs, source, err := svc.RosterInputsForPitchers(context.Background(), summary.SyncRunID)
	if err != nil {
		t.Fatalf("RosterInputsForPitchers: %v", err)
	}
	if len(inputs) != 2 {
		t.Fatalf("inputs len = %d, want 2 starters-only", len(inputs))
	}
	for _, in := range inputs {
		if in.PlayerName == "Raisel Iglesias" {
			t.Fatalf("reliever should be excluded from starter analysis inputs")
		}
	}
	if source == "" {
		t.Fatalf("expected roster source marker")
	}
}

func TestSyncRosterDryRunDoesNotPersist(t *testing.T) {
	store := mustOpenStore(t)
	defer store.Close()

	srv := newTestServer(t, mustReadFixture(t, "testdata/league_roster.json"))
	defer srv.Close()

	svc := New(repository.New(store.DB()))
	cfg := baseTestConfig(srv.URL)
	t.Setenv(cfg.Auth.ESPNS2Env, "cookie-s2")
	t.Setenv(cfg.Auth.SWIDEnv, "{cookie-swid}")

	summary, err := svc.SyncRoster(context.Background(), cfg, SyncOptions{DryRun: true})
	if err != nil {
		t.Fatalf("SyncRoster dry-run: %v", err)
	}
	if !summary.DryRun {
		t.Fatalf("expected dry-run summary")
	}
	latest, err := svc.LatestSync(context.Background())
	if err != nil {
		t.Fatalf("LatestSync: %v", err)
	}
	if latest != nil {
		t.Fatalf("expected no persisted sync run in dry-run")
	}
}

func TestSyncRosterNextDayResolvesAndPersistsScoringPeriod(t *testing.T) {
	store := mustOpenStore(t)
	defer store.Close()

	basePayload := string(mustReadFixture(t, "testdata/league_roster.json"))
	currentPayload := []byte(strings.Replace(basePayload, "{", `{"status":{"currentScoringPeriod":48},`, 1))
	nextPayload := []byte(strings.Replace(basePayload, "{", `{"scoringPeriodId":49,`, 1))
	requestedNext := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("scoringPeriodId") == "49" {
			requestedNext = true
			_, _ = w.Write(nextPayload)
			return
		}
		_, _ = w.Write(currentPayload)
	}))
	defer srv.Close()

	svc := New(repository.New(store.DB()))
	cfg := baseTestConfig(srv.URL)
	t.Setenv(cfg.Auth.ESPNS2Env, "cookie-s2")
	t.Setenv(cfg.Auth.SWIDEnv, "{cookie-swid}")

	summary, err := svc.SyncRoster(context.Background(), cfg, SyncOptions{EffectiveNextDay: true})
	if err != nil {
		t.Fatalf("SyncRoster next-day: %v", err)
	}
	if !requestedNext {
		t.Fatalf("expected scoringPeriodId=49 fetch")
	}
	if summary.ScoringPeriodID == nil || *summary.ScoringPeriodID != 49 || !summary.EffectiveNextDay {
		t.Fatalf("unexpected next-day summary: %+v", summary)
	}
	rows, err := svc.ShowRoster(context.Background(), ShowRosterFilter{EffectiveNextDay: true})
	if err != nil {
		t.Fatalf("ShowRoster next-day: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("next-day roster rows = %d, want 4", len(rows))
	}
}

func TestParseFreeAgentCandidatesPayload(t *testing.T) {
	payload := mustReadFixture(t, "testdata/free_agents_pitchers.json")
	rows, warnings := parseFreeAgentCandidatesPayload(payload, "", "", 25)
	if len(rows) != 2 {
		t.Fatalf("expected 2 pitcher candidates, got %d", len(rows))
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %d", len(warnings))
	}
	rows, _ = parseFreeAgentCandidatesPayload(payload, "megill", "", 25)
	if len(rows) != 1 || rows[0].PlayerName != "Tylor Megill" {
		t.Fatalf("search filter mismatch: %+v", rows)
	}
	rows, _ = parseFreeAgentCandidatesPayload(payload, "", "DET", 25)
	if len(rows) != 1 || rows[0].MLBTeam != "DET" {
		t.Fatalf("team filter mismatch: %+v", rows)
	}
}

func TestParseFreeAgentCandidatesPayloadAcquisitionStatus(t *testing.T) {
	payload := []byte(`{
	  "players": [
	    {"id": 1, "fullName": "Immediate Arm", "proTeamAbbrev": "NYY", "defaultPositionId": 1, "eligibleSlots": [1,13], "status": "FREEAGENT", "injuryStatus": "ACTIVE"},
	    {"id": 2, "fullName": "Waiver Arm", "proTeamAbbrev": "BOS", "defaultPositionId": 1, "eligibleSlots": [1,13], "status": "WAIVERS", "injuryStatus": "ACTIVE"}
	  ]
	}`)
	rows, warnings := parseFreeAgentCandidatesPayload(payload, "", "", 25)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %d", len(warnings))
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	statusByName := map[string]string{}
	for _, row := range rows {
		statusByName[row.PlayerName] = row.AcquisitionStatus
	}
	if statusByName["Immediate Arm"] != espn.AcquisitionStatusFreeAgent {
		t.Fatalf("expected FREEAGENT status, got %q", statusByName["Immediate Arm"])
	}
	if statusByName["Waiver Arm"] != espn.AcquisitionStatusWaivers {
		t.Fatalf("expected WAIVERS status, got %q", statusByName["Waiver Arm"])
	}
}

func TestParseFreeAgentCandidatesPayloadEntryStatusOverridesNestedPlayerMap(t *testing.T) {
	payload := []byte(`{
	  "players": [
	    {
	      "status": "WAIVERS",
	      "waiverProcessDate": 1776150000000,
	      "player": {
	        "id": 99,
	        "fullName": "Michael King",
	        "proTeamAbbrev": "SD",
	        "defaultPositionId": 1,
	        "eligibleSlots": [1,13],
	        "injuryStatus": "ACTIVE"
	      }
	    }
	  ]
	}`)
	rows, warnings := parseFreeAgentCandidatesPayload(payload, "", "", 25)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %d", len(warnings))
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].AcquisitionStatus != espn.AcquisitionStatusWaivers {
		t.Fatalf("expected WAIVERS, got %q", rows[0].AcquisitionStatus)
	}
}

func TestSyncFreeAgentPitchersPersistsCandidates(t *testing.T) {
	store := mustOpenStore(t)
	defer store.Close()

	leaguePayload := mustReadFixture(t, "testdata/league_roster.json")
	faPayload := mustReadFixture(t, "testdata/free_agents_pitchers.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.RawQuery, "view=kona_player_info") {
			_, _ = w.Write(faPayload)
			return
		}
		_, _ = w.Write(leaguePayload)
	}))
	defer srv.Close()

	svc := New(repository.New(store.DB()))
	cfg := baseTestConfig(srv.URL)
	cfg.Pickups.Pitchers.MaxCandidateLimit = 5
	cfg.Pickups.Pitchers.DefaultCandidateLimit = 3
	t.Setenv(cfg.Auth.ESPNS2Env, "cookie-s2")
	t.Setenv(cfg.Auth.SWIDEnv, "{cookie-swid}")

	_, err := svc.SyncRoster(context.Background(), cfg, SyncOptions{})
	if err != nil {
		t.Fatalf("SyncRoster: %v", err)
	}
	summary, err := svc.SyncFreeAgentPitchers(context.Background(), cfg, FreeAgentOptions{Limit: 50})
	if err != nil {
		t.Fatalf("SyncFreeAgentPitchers: %v", err)
	}
	if summary.CandidateRunID == nil {
		t.Fatalf("expected candidate run id")
	}
	if summary.EffectiveLimit != 5 {
		t.Fatalf("expected capped limit 5, got %d", summary.EffectiveLimit)
	}
	rows, err := svc.Candidates(context.Background(), summary.CandidateRunID, 100)
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 persisted candidates, got %d", len(rows))
	}
}

func TestESPNRosterInputsFeedPitcherAnalysis(t *testing.T) {
	store := mustOpenStore(t)
	defer store.Close()

	foreRepo := forecaster.NewRepository(store.DB())
	pRepo := pitchrepo.New(store.DB())
	pService := pitchsvc.New(foreRepo, pRepo)
	eService := New(repository.New(store.DB()))

	day1 := time.Date(2026, 3, 24, 0, 0, 0, 0, time.Local)
	day2 := day1.AddDate(0, 0, 3)
	starts := []forecaster.ProbableStartInput{
		{SourceDateRaw: "Tue", GameDate: &day1, Team: "NYY", Opponent: "BOS", PitcherName: "Gerrit Cole", ThrowsHand: "R", Status: forecaster.StatusScheduled, ProjectedFPTS: floatPtr(11.2)},
		{SourceDateRaw: "Fri", GameDate: &day2, Team: "DET", Opponent: "MIN", PitcherName: "Tarik Skubal", ThrowsHand: "L", Status: forecaster.StatusScheduled, ProjectedFPTS: floatPtr(10.7)},
	}
	importRun, err := foreRepo.InsertImport(context.Background(), forecaster.SourceTypeFile, "fixture", 1, starts, nil, "success", "{}")
	if err != nil {
		t.Fatalf("InsertImport: %v", err)
	}

	srv := newTestServer(t, mustReadFixture(t, "testdata/league_roster.json"))
	defer srv.Close()
	cfg := baseTestConfig(srv.URL)
	t.Setenv(cfg.Auth.ESPNS2Env, "cookie-s2")
	t.Setenv(cfg.Auth.SWIDEnv, "{cookie-swid}")
	summary, err := eService.SyncRoster(context.Background(), cfg, SyncOptions{})
	if err != nil {
		t.Fatalf("SyncRoster: %v", err)
	}
	inputs, source, err := eService.RosterInputsForPitchers(context.Background(), summary.SyncRunID)
	if err != nil {
		t.Fatalf("RosterInputsForPitchers: %v", err)
	}

	report, err := pService.AnalyzeWeek(context.Background(), pitchers.AnalysisOptions{
		From:         day1,
		To:           day2,
		ImportRunID:  &importRun.ID,
		RosterInputs: inputs,
		RosterSource: source,
	})
	if err != nil {
		t.Fatalf("AnalyzeWeek: %v", err)
	}
	if len(report.RankedPitchers) != 2 {
		t.Fatalf("expected 2 ranked pitchers from ESPN inputs, got %d", len(report.RankedPitchers))
	}
}

func newTestServer(t *testing.T, payload []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}))
}

func baseTestConfig(baseURL string) config.Config {
	cfg := config.Default()
	cfg.League.LeagueID = "12345"
	cfg.League.TeamID = "7"
	cfg.League.Season = 2026
	cfg.ESPN.BaseURL = baseURL
	return cfg
}

func mustOpenStore(t *testing.T) *sqlite.Store {
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
	return store
}

func mustReadFixture(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return b
}

func floatPtr(v float64) *float64 { return &v }
