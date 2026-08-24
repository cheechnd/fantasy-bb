package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fantasy-baseball/internal/espn"
	esrepo "fantasy-baseball/internal/espn/repository"
	"fantasy-baseball/internal/execute"
	exerepo "fantasy-baseball/internal/execute/repository"
	"fantasy-baseball/internal/store/sqlite"
	"fantasy-baseball/internal/transactions"
	tranrepo "fantasy-baseball/internal/transactions/repository"
	reviewrepo "fantasy-baseball/internal/transactions/review/repository"
)

func TestPreflightExecutable(t *testing.T) {
	svc, closeFn := seededService(t, seededInputs{
		addName:     "Add Arm",
		dropName:    "Drop Arm",
		rosterNames: []string{"Drop Arm"},
		candidates:  []string{"Add Arm"},
	})
	defer closeFn()

	run, err := svc.Preflight(context.Background(), execute.Options{Limit: 10})
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if len(run.Items) != 1 || run.Items[0].ValidationStatus != execute.StatusExecutable {
		t.Fatalf("expected executable item, got %+v", run.Items)
	}
}

func TestPreflightBlockedUnavailable(t *testing.T) {
	svc, closeFn := seededService(t, seededInputs{
		addName:     "Add Arm",
		dropName:    "Drop Arm",
		rosterNames: []string{"Drop Arm"},
		candidates:  []string{"Other Arm"},
	})
	defer closeFn()

	run, err := svc.Preflight(context.Background(), execute.Options{})
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if run.Items[0].ValidationStatus != execute.StatusBlocked {
		t.Fatalf("expected blocked, got %s", run.Items[0].ValidationStatus)
	}
}

func TestPreflightBlockedOnWaivers(t *testing.T) {
	svc, closeFn := seededService(t, seededInputs{
		addName:                  "Add Arm",
		dropName:                 "Drop Arm",
		rosterNames:              []string{"Drop Arm"},
		candidates:               []string{"Add Arm"},
		candidateStatuses:        []string{espn.AcquisitionStatusWaivers},
		candidateWaiverDatetimes: []*string{strPtr("2026-04-14T03:00:00-04:00")},
	})
	defer closeFn()

	run, err := svc.Preflight(context.Background(), execute.Options{})
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if run.Items[0].ValidationStatus != execute.StatusBlocked {
		t.Fatalf("expected blocked, got %s", run.Items[0].ValidationStatus)
	}
	found := false
	for _, reason := range run.Items[0].ValidationReasons {
		if reason.Code == "add_target_on_waivers" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected add_target_on_waivers reason, got %+v", run.Items[0].ValidationReasons)
	}
	addCandidate, ok := run.Items[0].Details["add_candidate"].(map[string]any)
	if !ok {
		t.Fatalf("expected add_candidate details, got %+v", run.Items[0].Details)
	}
	got, ok := addCandidate["waiver_process_datetime"].(string)
	if !ok || got != "2026-04-14T03:00:00-04:00" {
		t.Fatalf("expected waiver process datetime in preflight details, got %#v", addCandidate["waiver_process_datetime"])
	}
}

func TestPreflightConflictDropMissing(t *testing.T) {
	svc, closeFn := seededService(t, seededInputs{
		addName:     "Add Arm",
		dropName:    "Drop Arm",
		rosterNames: []string{"Different Arm"},
		candidates:  []string{"Add Arm"},
	})
	defer closeFn()

	run, err := svc.Preflight(context.Background(), execute.Options{})
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if run.Items[0].ValidationStatus != execute.StatusConflict {
		t.Fatalf("expected conflict, got %s", run.Items[0].ValidationStatus)
	}
}

func TestPreflightBlockedAlreadyRostered(t *testing.T) {
	svc, closeFn := seededService(t, seededInputs{
		addName:     "Add Arm",
		dropName:    "Drop Arm",
		rosterNames: []string{"Drop Arm", "Add Arm"},
		candidates:  []string{"Add Arm"},
	})
	defer closeFn()

	run, err := svc.Preflight(context.Background(), execute.Options{})
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if run.Items[0].ValidationStatus != execute.StatusBlocked {
		t.Fatalf("expected blocked, got %s", run.Items[0].ValidationStatus)
	}
}

func TestPreflightUnknownAmbiguousCandidate(t *testing.T) {
	svc, closeFn := seededService(t, seededInputs{
		addName:     "Add Arm",
		dropName:    "Drop Arm",
		rosterNames: []string{"Drop Arm"},
		candidates:  []string{"Add Arm", "Add Arm"},
	})
	defer closeFn()

	run, err := svc.Preflight(context.Background(), execute.Options{})
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if run.Items[0].ValidationStatus != execute.StatusUnknown {
		t.Fatalf("expected unknown, got %s", run.Items[0].ValidationStatus)
	}
}

func TestPreflightExecutableAddOnly(t *testing.T) {
	svc, closeFn := seededService(t, seededInputs{
		addName:     "Add Arm",
		dropName:    "",
		rosterNames: []string{"Different Arm"},
		candidates:  []string{"Add Arm"},
	})
	defer closeFn()

	run, err := svc.Preflight(context.Background(), execute.Options{Limit: 10})
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if len(run.Items) != 1 || run.Items[0].ValidationStatus != execute.StatusExecutable {
		t.Fatalf("expected executable add-only item, got %+v", run.Items)
	}
}

func TestPreflightAddOnlyBlockedWhenRosterCapacityFull(t *testing.T) {
	svc, closeFn := seededService(t, seededInputs{
		addName:        "Add Arm",
		dropName:       "",
		rosterNames:    []string{"A", "B"},
		candidates:     []string{"Add Arm"},
		leagueSettings: `{"lineupSlotCounts":{"13":1,"16":1}}`,
	})
	defer closeFn()

	run, err := svc.Preflight(context.Background(), execute.Options{Limit: 10})
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if len(run.Items) != 1 || run.Items[0].ValidationStatus != execute.StatusBlocked {
		t.Fatalf("expected blocked add-only item, got %+v", run.Items)
	}
	found := false
	for _, r := range run.Items[0].ValidationReasons {
		if r.Code == "roster_capacity_full" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected roster_capacity_full reason, got %+v", run.Items[0].ValidationReasons)
	}
}

func TestPreflightAddOnlyUsesNonILRosterCapacity(t *testing.T) {
	svc, closeFn := seededService(t, seededInputs{
		addName:        "Add Arm",
		dropName:       "",
		rosterNames:    []string{"A", "IL Arm"},
		rosterSlots:    []string{"P", "IL"},
		candidates:     []string{"Add Arm"},
		leagueSettings: `{"lineupSlotCounts":{"13":1,"17":1}}`,
	})
	defer closeFn()

	run, err := svc.Preflight(context.Background(), execute.Options{Limit: 10})
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if len(run.Items) != 1 || run.Items[0].ValidationStatus != execute.StatusBlocked {
		t.Fatalf("expected blocked add-only item with full normal roster, got %+v", run.Items)
	}
	ctx, _ := run.Items[0].Details["roster_context"].(map[string]any)
	if !jsonNumberEquals(ctx["active_roster_capacity_excluding_il"], 1) {
		t.Fatalf("expected active_roster_capacity_excluding_il=1, got %+v", ctx)
	}
	if !jsonNumberEquals(ctx["il_roster_capacity"], 1) {
		t.Fatalf("expected il_roster_capacity=1, got %+v", ctx)
	}
	if !jsonNumberEquals(ctx["current_active_roster_total_excluding_il"], 1) {
		t.Fatalf("expected current_active_roster_total_excluding_il=1, got %+v", ctx)
	}
	if !jsonNumberEquals(ctx["current_il_roster_total"], 1) {
		t.Fatalf("expected current_il_roster_total=1, got %+v", ctx)
	}
}

func TestPreflightNextDayAddOnlyUsesEffectiveRosterCapacity(t *testing.T) {
	svc, closeFn := seededService(t, seededInputs{
		addName:                  "Add Arm",
		dropName:                 "",
		rosterNames:              []string{"A", "B"},
		effectiveRosterNames:     []string{"A"},
		effectiveScoringPeriodID: 49,
		candidates:               []string{"Add Arm"},
		leagueSettings:           `{"lineupSlotCounts":{"13":1,"16":1}}`,
	})
	defer closeFn()

	run, err := svc.Preflight(context.Background(), execute.Options{Limit: 10, EffectiveNextDay: true})
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if len(run.Items) != 1 || run.Items[0].ValidationStatus != execute.StatusExecutable {
		t.Fatalf("expected executable next-day add-only item, got %+v", run.Items)
	}
	ctx, _ := run.Items[0].Details["roster_context"].(map[string]any)
	if ctx["effective_next_day"] != true {
		t.Fatalf("expected effective_next_day detail, got %+v", ctx)
	}
	if ctx["effective_active_roster_capacity_full_excluding_il"] == true {
		t.Fatalf("expected effective roster capacity to be open, got %+v", ctx)
	}
}

func TestPreflightWarnsOnInactiveAddTargetStatus(t *testing.T) {
	svc, closeFn := seededService(t, seededInputs{
		addName:             "Add Arm",
		dropName:            "Drop Arm",
		rosterNames:         []string{"Drop Arm"},
		candidates:          []string{"Add Arm"},
		candidateStatusTags: []string{"FIFTEEN_DAY_DL"},
	})
	defer closeFn()

	run, err := svc.Preflight(context.Background(), execute.Options{Limit: 10})
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if run.Items[0].ValidationStatus != execute.StatusExecutable {
		t.Fatalf("expected warning not to block execution, got %s", run.Items[0].ValidationStatus)
	}
	found := false
	for _, r := range run.Items[0].ValidationReasons {
		if r.Code == "add_target_status_warning" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected add_target_status_warning, got %+v", run.Items[0].ValidationReasons)
	}
}

func TestPreflightStaleByTime(t *testing.T) {
	svc, closeFn := seededService(t, seededInputs{
		addName:        "Add Arm",
		dropName:       "Drop Arm",
		rosterNames:    []string{"Drop Arm"},
		candidates:     []string{"Add Arm"},
		approvalAgeHrs: 48,
	})
	defer closeFn()

	run, err := svc.Preflight(context.Background(), execute.Options{})
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if run.Items[0].ValidationStatus != execute.StatusStale {
		t.Fatalf("expected stale, got %s", run.Items[0].ValidationStatus)
	}
}

func TestQueueApprovedOnly(t *testing.T) {
	svc, closeFn := seededService(t, seededInputs{
		addName:     "Add Arm",
		dropName:    "Drop Arm",
		rosterNames: []string{"Drop Arm"},
		candidates:  []string{"Add Arm"},
		withPending: true,
	})
	defer closeFn()

	rows, err := svc.Queue(context.Background(), 25)
	if err != nil {
		t.Fatalf("Queue: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected only approved queue row, got %d", len(rows))
	}
}

func TestPreflightNoApprovedItems(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "fb.db")
	if _, err := sqlite.EnsureDatabaseFile(dbPath); err != nil {
		t.Fatalf("EnsureDatabaseFile: %v", err)
	}
	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()
	if _, err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	svc := New(
		exerepo.New(store.DB()),
		reviewrepo.New(store.DB()),
		esrepo.New(store.DB()),
		tranrepo.New(store.DB()),
		execute.ServiceConfig{
			DefaultLimit:                 10,
			MaxLimit:                     25,
			CandidateRefreshLimit:        25,
			StaleHoursThreshold:          12,
			RequireLiveRosterCheck:       true,
			RequireLiveAvailabilityCheck: true,
		},
	)
	_, err = svc.Preflight(context.Background(), execute.Options{})
	if err == nil {
		t.Fatalf("expected error when queue has no approved items")
	}
}

type seededInputs struct {
	addName                  string
	dropName                 string
	rosterNames              []string
	rosterSlots              []string
	effectiveRosterNames     []string
	effectiveRosterSlots     []string
	effectiveScoringPeriodID int
	candidates               []string
	candidateStatuses        []string
	candidateWaiverDatetimes []*string
	candidateStatusTags      []string
	leagueSettings           string
	approvalAgeHrs           int
	withPending              bool
}

func seededService(t *testing.T, in seededInputs) (*Service, func()) {
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

	tr := tranrepo.New(store.DB())
	rr := reviewrepo.New(store.DB())
	er := esrepo.New(store.DB())
	addTotal := 15.0
	dropTotal := 9.0
	delta := 6.0
	planID, err := tr.SavePlan(context.Background(), transactions.CreatePlanInput{
		WindowStart: "2026-09-15",
		WindowEnd:   "2026-09-21",
		Status:      "success",
		Summary:     map[string]any{"counts": map[string]int{"strong_move": 1}},
		Items: []transactions.PlanItem{{
			Bucket:                  transactions.BucketStrongMove,
			AddPlayerName:           in.addName,
			DropPlayerName:          in.dropName,
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

	roster := make([]espn.RosterSnapshot, 0, len(in.rosterNames))
	for idx, name := range in.rosterNames {
		slot := ""
		if idx < len(in.rosterSlots) {
			slot = in.rosterSlots[idx]
		}
		roster = append(roster, espn.RosterSnapshot{
			PlayerName: name, NormalizedName: strings.ToLower(name), RosterSlot: slot, IsPitcher: true, CreatedAt: time.Now().UTC(),
		})
	}
	cands := make([]espn.FreeAgentCandidate, 0, len(in.candidates))
	for idx, name := range in.candidates {
		acq := ""
		if idx < len(in.candidateStatuses) {
			acq = in.candidateStatuses[idx]
		}
		statusTag := "ACTIVE"
		if idx < len(in.candidateStatusTags) {
			statusTag = in.candidateStatusTags[idx]
		}
		var waiverProcessDatetime *string
		if idx < len(in.candidateWaiverDatetimes) {
			waiverProcessDatetime = in.candidateWaiverDatetimes[idx]
		}
		cands = append(cands, espn.FreeAgentCandidate{
			PlayerName: name, NormalizedName: strings.ToLower(name), IsPitcher: true, AcquisitionStatus: acq, WaiverProcessDatetime: waiverProcessDatetime, StatusTag: statusTag, CreatedAt: time.Now().UTC(),
		})
	}
	syncRunID, err := er.PersistSync(context.Background(), esrepo.PersistSyncInput{
		SyncType: "roster", LeagueID: "1", TeamID: "1", Season: 2026, Status: "success",
		League: espn.LeagueSnapshot{LeagueID: "1", TeamID: "1", Season: 2026, LeagueName: "L", TeamName: "T", SettingsJSON: in.leagueSettings, CreatedAt: time.Now().UTC()},
		Roster: roster,
	})
	if err != nil {
		t.Fatalf("PersistSync: %v", err)
	}
	if len(in.effectiveRosterNames) > 0 {
		effectiveRoster := make([]espn.RosterSnapshot, 0, len(in.effectiveRosterNames))
		for idx, name := range in.effectiveRosterNames {
			slot := ""
			if idx < len(in.effectiveRosterSlots) {
				slot = in.effectiveRosterSlots[idx]
			}
			effectiveRoster = append(effectiveRoster, espn.RosterSnapshot{
				PlayerName: name, NormalizedName: strings.ToLower(name), RosterSlot: slot, IsPitcher: true, CreatedAt: time.Now().UTC(),
			})
		}
		sp := in.effectiveScoringPeriodID
		if sp <= 0 {
			sp = 49
		}
		if _, err := er.PersistSync(context.Background(), esrepo.PersistSyncInput{
			SyncType:         "roster",
			LeagueID:         "1",
			TeamID:           "1",
			Season:           2026,
			Status:           "success",
			ScoringPeriodID:  &sp,
			EffectiveNextDay: true,
			League:           espn.LeagueSnapshot{LeagueID: "1", TeamID: "1", Season: 2026, LeagueName: "L", TeamName: "T", SettingsJSON: in.leagueSettings, CreatedAt: time.Now().UTC()},
			Roster:           effectiveRoster,
		}); err != nil {
			t.Fatalf("PersistSync effective: %v", err)
		}
	}
	_, err = er.PersistCandidates(context.Background(), esrepo.PersistCandidateInput{
		SyncRunID: &syncRunID,
		QueryType: "pitchers", Status: "success",
		Payload:    espn.RawPayload{PayloadType: "free_agents_pitchers", SourceEndpoint: "test", ResponseStatus: 200, PayloadJSON: "{}"},
		Candidates: cands,
	})
	if err != nil {
		t.Fatalf("PersistCandidates: %v", err)
	}
	items, err := tr.PlanItems(context.Background(), planID)
	if err != nil || len(items) == 0 {
		t.Fatalf("PlanItems: %v len=%d", err, len(items))
	}
	itemID := items[0].ID
	if _, err := rr.TransitionState(context.Background(), planID, itemID, transactions.ReviewStateApproved, "approved"); err != nil {
		t.Fatalf("TransitionState approve: %v", err)
	}
	if in.approvalAgeHrs > 0 {
		old := time.Now().UTC().Add(-time.Duration(in.approvalAgeHrs) * time.Hour).Format(time.RFC3339)
		if _, err := store.DB().Exec(`UPDATE transaction_review_states SET updated_at=? WHERE transaction_plan_item_id=?`, old, itemID); err != nil {
			t.Fatalf("update approval timestamp: %v", err)
		}
	}
	if in.withPending {
		planID2, err := tr.SavePlan(context.Background(), transactions.CreatePlanInput{
			WindowStart: "2026-09-15",
			WindowEnd:   "2026-09-21",
			Status:      "success",
			Summary:     map[string]any{"counts": map[string]int{"strong_move": 1}},
			Items: []transactions.PlanItem{{
				Bucket:                 transactions.BucketStrongMove,
				AddPlayerName:          "Pending Add",
				DropPlayerName:         "Pending Drop",
				AddProjectedStartCount: 1,
			}},
		})
		if err != nil {
			t.Fatalf("SavePlan pending: %v", err)
		}
		_, _, _ = tr.PlanByID(context.Background(), planID2)
	}

	svc := New(
		exerepo.New(store.DB()),
		reviewrepo.New(store.DB()),
		esrepo.New(store.DB()),
		tranrepo.New(store.DB()),
		execute.ServiceConfig{
			DefaultLimit:                 10,
			MaxLimit:                     25,
			CandidateRefreshLimit:        25,
			StaleHoursThreshold:          12,
			RequireLiveRosterCheck:       true,
			RequireLiveAvailabilityCheck: true,
		},
	)
	return svc, func() { store.Close() }
}

func jsonNumberEquals(v any, want int) bool {
	switch n := v.(type) {
	case int:
		return n == want
	case int64:
		return int(n) == want
	case float64:
		return int(n) == want
	default:
		return false
	}
}

func strPtr(v string) *string { return &v }
