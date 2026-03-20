package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"fantasy-baseball/internal/config"
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
