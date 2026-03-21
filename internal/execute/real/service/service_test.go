package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fantasy-baseball/internal/config"
	"fantasy-baseball/internal/espn"
	esrepo "fantasy-baseball/internal/espn/repository"
	"fantasy-baseball/internal/execute"
	realrepo "fantasy-baseball/internal/execute/real/repository"
	exerepo "fantasy-baseball/internal/execute/repository"
	pfsvc "fantasy-baseball/internal/execute/service"
	"fantasy-baseball/internal/store/sqlite"
	"fantasy-baseball/internal/transactions"
	tranrepo "fantasy-baseball/internal/transactions/repository"
	reviewrepo "fantasy-baseball/internal/transactions/review/repository"
)

func TestExecuteOneRequiresConfirmation(t *testing.T) {
	svc, cfg, itemID, closeFn, _, _ := seededRealService(t, realSeedInput{
		addName:     "Add Arm",
		dropName:    "Drop Arm",
		rosterNames: []string{"Drop Arm"},
		candidates:  []string{"Add Arm"},
	})
	defer closeFn()

	res, err := svc.ExecuteOne(context.Background(), cfg, execute.RealExecutionOptions{ItemID: itemID, Confirm: false})
	if err != nil {
		t.Fatalf("ExecuteOne: %v", err)
	}
	if res.WillWrite {
		t.Fatalf("expected no write without confirm")
	}
	if res.Attempt != nil {
		t.Fatalf("expected no persisted attempt when confirmation is missing")
	}
}

func TestExecuteOneAbortsWhenPreflightBlocked(t *testing.T) {
	svc, cfg, itemID, closeFn, writer, _ := seededRealService(t, realSeedInput{
		addName:     "Add Arm",
		dropName:    "Drop Arm",
		rosterNames: []string{"Drop Arm"},
		candidates:  []string{"Other Arm"},
	})
	defer closeFn()

	res, err := svc.ExecuteOne(context.Background(), cfg, execute.RealExecutionOptions{ItemID: itemID, Confirm: true})
	if err != nil {
		t.Fatalf("ExecuteOne: %v", err)
	}
	if res.Attempt == nil {
		t.Fatalf("expected aborted attempt to be saved")
	}
	if res.Attempt.ExecutionStatus != execute.ExecutionStatusAborted {
		t.Fatalf("expected aborted status, got %s", res.Attempt.ExecutionStatus)
	}
	if writer.calls != 0 {
		t.Fatalf("expected writer not called on blocked preflight")
	}
}

func TestExecuteOneSuccessVerified(t *testing.T) {
	svc, cfg, itemID, closeFn, writer, verifier := seededRealService(t, realSeedInput{
		addName:     "Add Arm",
		dropName:    "Drop Arm",
		rosterNames: []string{"Drop Arm"},
		candidates:  []string{"Add Arm"},
	})
	defer closeFn()
	writer.result = WriteResult{OK: true, Endpoint: "https://x/transactions", ResponseStatus: 200}
	verifier.status = execute.VerificationStatusVerified

	res, err := svc.ExecuteOne(context.Background(), cfg, execute.RealExecutionOptions{ItemID: itemID, Confirm: true})
	if err != nil {
		t.Fatalf("ExecuteOne: %v", err)
	}
	if res.Attempt == nil {
		t.Fatalf("expected execution attempt")
	}
	if res.Attempt.ExecutionStatus != execute.ExecutionStatusSucceeded {
		t.Fatalf("expected succeeded, got %s", res.Attempt.ExecutionStatus)
	}
	if res.Attempt.VerificationStatus != execute.VerificationStatusVerified {
		t.Fatalf("expected verified, got %s", res.Attempt.VerificationStatus)
	}
	if writer.calls != 1 {
		t.Fatalf("expected one write call, got %d", writer.calls)
	}
	if verifier.calls != 1 {
		t.Fatalf("expected one verifier call, got %d", verifier.calls)
	}
}

func TestExecuteOneWriteFailure(t *testing.T) {
	svc, cfg, itemID, closeFn, writer, _ := seededRealService(t, realSeedInput{
		addName:     "Add Arm",
		dropName:    "Drop Arm",
		rosterNames: []string{"Drop Arm"},
		candidates:  []string{"Add Arm"},
	})
	defer closeFn()
	writer.result = WriteResult{OK: false, Endpoint: "https://x/transactions", ResponseStatus: 500}
	writer.err = fmt.Errorf("boom")

	res, err := svc.ExecuteOne(context.Background(), cfg, execute.RealExecutionOptions{ItemID: itemID, Confirm: true})
	if err != nil {
		t.Fatalf("ExecuteOne: %v", err)
	}
	if res.Attempt == nil {
		t.Fatalf("expected execution attempt")
	}
	if res.Attempt.ExecutionStatus != execute.ExecutionStatusFailed {
		t.Fatalf("expected failed, got %s", res.Attempt.ExecutionStatus)
	}
	if !strings.Contains(res.Attempt.ErrorMessage, "boom") {
		t.Fatalf("expected error message persisted, got %q", res.Attempt.ErrorMessage)
	}
}

func TestExecuteOneBlocksDuplicateSuccessfulExecution(t *testing.T) {
	svc, cfg, itemID, closeFn, writer, verifier := seededRealService(t, realSeedInput{
		addName:     "Add Arm",
		dropName:    "Drop Arm",
		rosterNames: []string{"Drop Arm"},
		candidates:  []string{"Add Arm"},
	})
	defer closeFn()
	writer.result = WriteResult{OK: true, Endpoint: "https://x/transactions", ResponseStatus: 200}
	verifier.status = execute.VerificationStatusVerified

	if _, err := svc.ExecuteOne(context.Background(), cfg, execute.RealExecutionOptions{ItemID: itemID, Confirm: true}); err != nil {
		t.Fatalf("first ExecuteOne: %v", err)
	}
	if _, err := svc.ExecuteOne(context.Background(), cfg, execute.RealExecutionOptions{ItemID: itemID, Confirm: true}); err == nil {
		t.Fatalf("expected duplicate execution error")
	}
}

func TestHistoryAndByID(t *testing.T) {
	svc, cfg, itemID, closeFn, writer, verifier := seededRealService(t, realSeedInput{
		addName:     "Add Arm",
		dropName:    "Drop Arm",
		rosterNames: []string{"Drop Arm"},
		candidates:  []string{"Add Arm"},
	})
	defer closeFn()
	writer.result = WriteResult{OK: true, Endpoint: "https://x/transactions", ResponseStatus: 200}
	verifier.status = execute.VerificationStatusUnverified

	res, err := svc.ExecuteOne(context.Background(), cfg, execute.RealExecutionOptions{ItemID: itemID, Confirm: true})
	if err != nil {
		t.Fatalf("ExecuteOne: %v", err)
	}
	rows, err := svc.History(context.Background(), 10)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(rows) == 0 {
		t.Fatalf("expected at least one history row")
	}
	got, err := svc.ByID(context.Background(), res.Attempt.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if got == nil {
		t.Fatalf("expected attempt by id")
	}
	if len(got.Events) == 0 {
		t.Fatalf("expected execution events")
	}
}

func TestExecuteOneSubmittedWhenVerificationPending(t *testing.T) {
	svc, cfg, itemID, closeFn, writer, verifier := seededRealService(t, realSeedInput{
		addName:     "Add Arm",
		dropName:    "Drop Arm",
		rosterNames: []string{"Drop Arm"},
		candidates:  []string{"Add Arm"},
	})
	defer closeFn()
	writer.result = WriteResult{OK: true, Endpoint: "https://x/transactions", ResponseStatus: 200}
	verifier.status = execute.VerificationStatusPending

	res, err := svc.ExecuteOne(context.Background(), cfg, execute.RealExecutionOptions{ItemID: itemID, Confirm: true})
	if err != nil {
		t.Fatalf("ExecuteOne: %v", err)
	}
	if res.Attempt == nil {
		t.Fatalf("expected attempt")
	}
	if res.Attempt.ExecutionStatus != execute.ExecutionStatusSubmitted {
		t.Fatalf("expected submitted status, got %s", res.Attempt.ExecutionStatus)
	}
	if res.Attempt.VerificationStatus != execute.VerificationStatusPending {
		t.Fatalf("expected pending verification, got %s", res.Attempt.VerificationStatus)
	}
}

func TestExecuteOneBlocksOnPriorAmbiguousAttempt(t *testing.T) {
	svc, cfg, itemID, closeFn, writer, verifier := seededRealService(t, realSeedInput{
		addName:     "Add Arm",
		dropName:    "Drop Arm",
		rosterNames: []string{"Drop Arm"},
		candidates:  []string{"Add Arm"},
	})
	defer closeFn()
	writer.result = WriteResult{OK: true, Endpoint: "https://x/transactions", ResponseStatus: 200}
	verifier.status = execute.VerificationStatusPending

	if _, err := svc.ExecuteOne(context.Background(), cfg, execute.RealExecutionOptions{ItemID: itemID, Confirm: true}); err != nil {
		t.Fatalf("first ExecuteOne: %v", err)
	}
	if _, err := svc.ExecuteOne(context.Background(), cfg, execute.RealExecutionOptions{ItemID: itemID, Confirm: true}); err == nil {
		t.Fatalf("expected block on prior ambiguous/submitted attempt")
	}
}

func TestVerifyAttemptRecheck(t *testing.T) {
	svc, cfg, itemID, closeFn, writer, verifier := seededRealService(t, realSeedInput{
		addName:     "Add Arm",
		dropName:    "Drop Arm",
		rosterNames: []string{"Drop Arm"},
		candidates:  []string{"Add Arm"},
	})
	defer closeFn()
	writer.result = WriteResult{OK: true, Endpoint: "https://x/transactions", ResponseStatus: 200}
	verifier.status = execute.VerificationStatusPending

	res, err := svc.ExecuteOne(context.Background(), cfg, execute.RealExecutionOptions{ItemID: itemID, Confirm: true})
	if err != nil {
		t.Fatalf("ExecuteOne: %v", err)
	}
	verifier.status = execute.VerificationStatusVerified
	verifier.details = map[string]any{"inference": "likely_executed"}

	vr, err := svc.VerifyAttempt(context.Background(), cfg, res.Attempt.ID)
	if err != nil {
		t.Fatalf("VerifyAttempt: %v", err)
	}
	if vr.Attempt == nil {
		t.Fatalf("expected updated attempt")
	}
	if vr.Attempt.VerificationStatus != execute.VerificationStatusVerified {
		t.Fatalf("expected verified status, got %s", vr.Attempt.VerificationStatus)
	}
}

func TestPendingAttempts(t *testing.T) {
	svc, cfg, itemID, closeFn, writer, verifier := seededRealService(t, realSeedInput{
		addName:     "Add Arm",
		dropName:    "Drop Arm",
		rosterNames: []string{"Drop Arm"},
		candidates:  []string{"Add Arm"},
	})
	defer closeFn()
	writer.result = WriteResult{OK: true, Endpoint: "https://x/transactions", ResponseStatus: 200}
	verifier.status = execute.VerificationStatusPending

	if _, err := svc.ExecuteOne(context.Background(), cfg, execute.RealExecutionOptions{ItemID: itemID, Confirm: true}); err != nil {
		t.Fatalf("ExecuteOne: %v", err)
	}
	rows, err := svc.Pending(context.Background(), 10)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(rows) == 0 {
		t.Fatalf("expected unresolved attempts")
	}
}

func TestReconcileAttempt(t *testing.T) {
	svc, cfg, itemID, closeFn, writer, verifier := seededRealService(t, realSeedInput{
		addName:     "Add Arm",
		dropName:    "Drop Arm",
		rosterNames: []string{"Drop Arm"},
		candidates:  []string{"Add Arm"},
	})
	defer closeFn()
	writer.result = WriteResult{OK: true, Endpoint: "https://x/transactions", ResponseStatus: 200}
	verifier.status = execute.VerificationStatusUnverified
	verifier.details = map[string]any{"inference": "likely_not_executed"}

	res, err := svc.ExecuteOne(context.Background(), cfg, execute.RealExecutionOptions{ItemID: itemID, Confirm: true})
	if err != nil {
		t.Fatalf("ExecuteOne: %v", err)
	}
	rr, err := svc.ReconcileAttempt(context.Background(), cfg, res.Attempt.ID)
	if err != nil {
		t.Fatalf("ReconcileAttempt: %v", err)
	}
	if rr.Inference == "" {
		t.Fatalf("expected reconciliation inference")
	}
}

func TestVerifyAttemptRespectsRecheckLimit(t *testing.T) {
	svc, cfg, itemID, closeFn, writer, verifier := seededRealService(t, realSeedInput{
		addName:     "Add Arm",
		dropName:    "Drop Arm",
		rosterNames: []string{"Drop Arm"},
		candidates:  []string{"Add Arm"},
	})
	defer closeFn()
	cfg.Execution.Hardening.VerificationRecheckLimit = 1
	writer.result = WriteResult{OK: true, Endpoint: "https://x/transactions", ResponseStatus: 200}
	verifier.status = execute.VerificationStatusPending

	res, err := svc.ExecuteOne(context.Background(), cfg, execute.RealExecutionOptions{ItemID: itemID, Confirm: true})
	if err != nil {
		t.Fatalf("ExecuteOne: %v", err)
	}
	// first recheck allowed
	if _, err := svc.VerifyAttempt(context.Background(), cfg, res.Attempt.ID); err != nil {
		t.Fatalf("VerifyAttempt #1: %v", err)
	}
	// second should be blocked by limit
	if _, err := svc.VerifyAttempt(context.Background(), cfg, res.Attempt.ID); err == nil {
		t.Fatalf("expected recheck limit error")
	}
}

type fakeWriter struct {
	calls  int
	result WriteResult
	err    error
}

func (f *fakeWriter) ExecuteAddDrop(_ context.Context, _ config.Config, _ WriteRequest) (WriteResult, error) {
	f.calls++
	return f.result, f.err
}

type fakeVerifier struct {
	calls   int
	status  execute.VerificationStatus
	details map[string]any
	err     error
}

func (f *fakeVerifier) Verify(_ context.Context, _ config.Config, _ WriteRequest, _ WriteResult) (execute.VerificationStatus, map[string]any, error) {
	f.calls++
	if f.details == nil {
		f.details = map[string]any{"verified": false}
	}
	return f.status, f.details, f.err
}

type realSeedInput struct {
	addName     string
	dropName    string
	rosterNames []string
	candidates  []string
}

func seededRealService(t *testing.T, in realSeedInput) (*Service, config.Config, int64, func(), *fakeWriter, *fakeVerifier) {
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
	addID := int64(101)
	dropID := int64(202)
	planID, err := tr.SavePlan(context.Background(), transactions.CreatePlanInput{
		WindowStart: "2026-09-15",
		WindowEnd:   "2026-09-21",
		Status:      "success",
		Summary:     map[string]any{"counts": map[string]int{"strong_move": 1}},
		Items: []transactions.PlanItem{{
			Bucket:                  transactions.BucketStrongMove,
			AddPlayerName:           in.addName,
			DropPlayerName:          in.dropName,
			AddESPNPlayerID:         &addID,
			DropESPNPlayerID:        &dropID,
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
	for _, name := range in.rosterNames {
		roster = append(roster, espn.RosterSnapshot{
			PlayerName: name, NormalizedName: strings.ToLower(name), IsPitcher: true, CreatedAt: time.Now().UTC(),
		})
	}
	cands := make([]espn.FreeAgentCandidate, 0, len(in.candidates))
	for _, name := range in.candidates {
		cands = append(cands, espn.FreeAgentCandidate{
			PlayerName: name, NormalizedName: strings.ToLower(name), IsPitcher: true, CreatedAt: time.Now().UTC(),
		})
	}
	syncRunID, err := er.PersistSync(context.Background(), esrepo.PersistSyncInput{
		SyncType: "roster", LeagueID: "1", TeamID: "1", Season: 2026, Status: "success",
		League: espn.LeagueSnapshot{LeagueID: "1", TeamID: "1", Season: 2026, LeagueName: "L", TeamName: "T", CreatedAt: time.Now().UTC()},
		Roster: roster,
	})
	if err != nil {
		t.Fatalf("PersistSync: %v", err)
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

	preflight := pfsvc.New(
		exerepo.New(store.DB()),
		reviewrepo.New(store.DB()),
		esrepo.New(store.DB()),
		tranrepo.New(store.DB()),
		execute.ServiceConfig{
			DefaultLimit:                 10,
			MaxLimit:                     25,
			CandidateRefreshLimit:        25,
			StaleHoursThreshold:          0,
			RequireLiveRosterCheck:       true,
			RequireLiveAvailabilityCheck: true,
		},
	)
	writer := &fakeWriter{result: WriteResult{OK: true, Endpoint: "test", ResponseStatus: 200}}
	verifier := &fakeVerifier{status: execute.VerificationStatusVerified}
	svc := New(
		preflight,
		reviewrepo.New(store.DB()),
		tranrepo.New(store.DB()),
		realrepo.New(store.DB()),
		writer,
		verifier,
	)
	cfg := config.Default()
	cfg.Execution.Real.Enabled = true
	cfg.Execution.Real.RequireConfirmation = true
	cfg.Execution.Real.AllowRepeatExecution = false
	return svc, cfg, itemID, func() { store.Close() }, writer, verifier
}
