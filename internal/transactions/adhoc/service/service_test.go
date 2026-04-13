package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fantasy-baseball/internal/espn"
	esrepo "fantasy-baseball/internal/espn/repository"
	"fantasy-baseball/internal/store/sqlite"
	"fantasy-baseball/internal/transactions"
	adhocrepo "fantasy-baseball/internal/transactions/adhoc/repository"
	tranrepo "fantasy-baseball/internal/transactions/repository"
	reviewrepo "fantasy-baseball/internal/transactions/review/repository"
)

func TestCreateAndResolveSuccess(t *testing.T) {
	svc, closeFn := seededService(t, seedInput{
		roster: []espn.RosterSnapshot{{PlayerName: "Shota Imanaga", NormalizedName: "shotaimanaga", IsPitcher: true, ESPNPlayerID: ptr64(202)}},
		cands:  []espn.FreeAgentCandidate{{PlayerName: "Aaron Nola", NormalizedName: "aaronnola", IsPitcher: true, ESPNPlayerID: ptr64(101)}},
	})
	defer closeFn()

	req, err := svc.CreateAndResolve(context.Background(), "Aaron Nola", "Shota Imanaga")
	if err != nil {
		t.Fatalf("CreateAndResolve: %v", err)
	}
	if req.ResolutionStatus != transactions.AdHocResolutionResolved {
		t.Fatalf("expected resolved, got %s", req.ResolutionStatus)
	}
	if req.RequestState != transactions.AdHocStateResolved {
		t.Fatalf("expected resolved state, got %s", req.RequestState)
	}
}

func TestCreateAndResolveAmbiguousAdd(t *testing.T) {
	svc, closeFn := seededService(t, seedInput{
		roster: []espn.RosterSnapshot{{PlayerName: "Shota Imanaga", NormalizedName: "shotaimanaga", IsPitcher: true, ESPNPlayerID: ptr64(202)}},
		cands: []espn.FreeAgentCandidate{
			{PlayerName: "Aaron Nola", NormalizedName: "aaronnola", IsPitcher: true},
			{PlayerName: "Aaron Nola", NormalizedName: "aaronnola", IsPitcher: true},
		},
	})
	defer closeFn()

	req, err := svc.CreateAndResolve(context.Background(), "Aaron Nola", "Shota Imanaga")
	if err != nil {
		t.Fatalf("CreateAndResolve: %v", err)
	}
	if req.ResolutionStatus != transactions.AdHocResolutionAmbiguous {
		t.Fatalf("expected ambiguous, got %s", req.ResolutionStatus)
	}
}

func TestCreateAndResolveInvalidTargetType(t *testing.T) {
	svc, closeFn := seededService(t, seedInput{
		roster: []espn.RosterSnapshot{{PlayerName: "Mookie Betts", NormalizedName: "mookiebetts", IsPitcher: false}},
		cands:  []espn.FreeAgentCandidate{{PlayerName: "Aaron Nola", NormalizedName: "aaronnola", IsPitcher: true}},
	})
	defer closeFn()

	req, err := svc.CreateAndResolve(context.Background(), "Aaron Nola", "Mookie Betts")
	if err != nil {
		t.Fatalf("CreateAndResolve: %v", err)
	}
	if req.ResolutionStatus != transactions.AdHocResolutionInvalidType {
		t.Fatalf("expected invalid target type, got %s", req.ResolutionStatus)
	}
}

func TestCreateAndResolveAddOnWaivers(t *testing.T) {
	svc, closeFn := seededService(t, seedInput{
		roster: []espn.RosterSnapshot{{PlayerName: "Shota Imanaga", NormalizedName: "shotaimanaga", IsPitcher: true, ESPNPlayerID: ptr64(202)}},
		cands: []espn.FreeAgentCandidate{{
			PlayerName:        "Aaron Nola",
			NormalizedName:    "aaronnola",
			IsPitcher:         true,
			ESPNPlayerID:      ptr64(101),
			AcquisitionStatus: espn.AcquisitionStatusWaivers,
		}},
	})
	defer closeFn()

	req, err := svc.CreateAndResolve(context.Background(), "Aaron Nola", "Shota Imanaga")
	if err != nil {
		t.Fatalf("CreateAndResolve: %v", err)
	}
	if req.ResolutionStatus != transactions.AdHocResolutionUnresolved {
		t.Fatalf("expected unresolved for waiver add target, got %s", req.ResolutionStatus)
	}
	note, _ := req.ResolutionNotes["add"].(string)
	if !strings.Contains(strings.ToLower(note), "waivers") {
		t.Fatalf("expected waiver note, got %q", note)
	}
}

func TestEnsureExecutionCandidate(t *testing.T) {
	svc, closeFn := seededService(t, seedInput{
		roster: []espn.RosterSnapshot{{PlayerName: "Shota Imanaga", NormalizedName: "shotaimanaga", IsPitcher: true, ESPNPlayerID: ptr64(202)}},
		cands:  []espn.FreeAgentCandidate{{PlayerName: "Aaron Nola", NormalizedName: "aaronnola", IsPitcher: true, ESPNPlayerID: ptr64(101)}},
	})
	defer closeFn()
	req, err := svc.CreateAndResolve(context.Background(), "Aaron Nola", "Shota Imanaga")
	if err != nil {
		t.Fatalf("CreateAndResolve: %v", err)
	}
	updated, itemID, err := svc.EnsureExecutionCandidate(context.Background(), req.ID)
	if err != nil {
		t.Fatalf("EnsureExecutionCandidate: %v", err)
	}
	if itemID <= 0 || updated.LinkedPlanItemID == nil {
		t.Fatalf("expected linked plan item")
	}
}

func TestEnsureExecutionCandidateUnresolvedErrorIncludesNotes(t *testing.T) {
	svc, closeFn := seededService(t, seedInput{
		roster: []espn.RosterSnapshot{
			{PlayerName: "David Peterson", NormalizedName: "davidpeterson", IsPitcher: true, ESPNPlayerID: ptr64(202)},
		},
		cands: []espn.FreeAgentCandidate{
			{PlayerName: "Other Arm", NormalizedName: "otherarm", IsPitcher: true, ESPNPlayerID: ptr64(101)},
		},
	})
	defer closeFn()

	req, err := svc.CreateAndResolve(context.Background(), "Edward Cabrera", "David Peterson")
	if err != nil {
		t.Fatalf("CreateAndResolve: %v", err)
	}
	if req.RequestState != transactions.AdHocStateUnresolved {
		t.Fatalf("expected unresolved request state, got %s", req.RequestState)
	}
	_, _, err = svc.EnsureExecutionCandidate(context.Background(), req.ID)
	if err == nil {
		t.Fatalf("expected unresolved execution candidate error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "request id, not execution id") {
		t.Fatalf("expected request id clarification in error: %s", msg)
	}
	if !strings.Contains(msg, "add:") {
		t.Fatalf("expected add note in error: %s", msg)
	}
}

func TestCreateAndResolveAddOnly(t *testing.T) {
	svc, closeFn := seededService(t, seedInput{
		roster: []espn.RosterSnapshot{{PlayerName: "Shota Imanaga", NormalizedName: "shotaimanaga", IsPitcher: true, ESPNPlayerID: ptr64(202)}},
		cands:  []espn.FreeAgentCandidate{{PlayerName: "Roki Sasaki", NormalizedName: "rokisasaki", IsPitcher: true, ESPNPlayerID: ptr64(303)}},
	})
	defer closeFn()

	req, err := svc.CreateAndResolve(context.Background(), "Roki Sasaki", "")
	if err != nil {
		t.Fatalf("CreateAndResolve: %v", err)
	}
	if req.ResolutionStatus != transactions.AdHocResolutionResolved {
		t.Fatalf("expected resolved, got %s", req.ResolutionStatus)
	}
	if req.ResolvedDropESPNPlayerID != nil || req.ResolvedDropPlayerName != "" {
		t.Fatalf("expected empty drop resolution for add-only request, got %+v", req)
	}
	updated, itemID, err := svc.EnsureExecutionCandidate(context.Background(), req.ID)
	if err != nil {
		t.Fatalf("EnsureExecutionCandidate: %v", err)
	}
	if itemID <= 0 || updated.LinkedPlanItemID == nil {
		t.Fatalf("expected linked plan item")
	}
}

func TestCreateAndResolveUsesLatestRunCandidateCountNotAdHocLimit(t *testing.T) {
	cands := make([]espn.FreeAgentCandidate, 0, 30)
	for i := 0; i < 29; i++ {
		name := fmt.Sprintf("Pitcher %02d", i)
		cands = append(cands, espn.FreeAgentCandidate{
			PlayerName:     name,
			NormalizedName: "pitcher" + fmt.Sprintf("%02d", i),
			IsPitcher:      true,
			ESPNPlayerID:   ptr64(int64(1000 + i)),
		})
	}
	cands = append(cands, espn.FreeAgentCandidate{
		PlayerName:     "Zed Target",
		NormalizedName: "zedtarget",
		IsPitcher:      true,
		ESPNPlayerID:   ptr64(2020),
	})

	svc, closeFn := seededService(t, seedInput{
		roster: []espn.RosterSnapshot{{PlayerName: "Shota Imanaga", NormalizedName: "shotaimanaga", IsPitcher: true, ESPNPlayerID: ptr64(202)}},
		cands:  cands,
	})
	defer closeFn()

	req, err := svc.CreateAndResolve(context.Background(), "Zed Target", "Shota Imanaga")
	if err != nil {
		t.Fatalf("CreateAndResolve: %v", err)
	}
	if req.ResolutionStatus != transactions.AdHocResolutionResolved {
		t.Fatalf("expected resolved, got %s", req.ResolutionStatus)
	}
	if req.ResolvedAddPlayerName != "Zed Target" {
		t.Fatalf("expected resolved add target, got %q", req.ResolvedAddPlayerName)
	}
}

type seedInput struct {
	roster []espn.RosterSnapshot
	cands  []espn.FreeAgentCandidate
}

func seededService(t *testing.T, in seedInput) (*Service, func()) {
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
	er := esrepo.New(store.DB())
	now := time.Now().UTC()
	syncID, err := er.PersistSync(context.Background(), esrepo.PersistSyncInput{
		SyncType: "roster",
		LeagueID: "1",
		TeamID:   "1",
		Season:   2026,
		Status:   "success",
		League: espn.LeagueSnapshot{
			LeagueID: "1", TeamID: "1", Season: 2026, LeagueName: "L", TeamName: "T", CreatedAt: now,
		},
		Roster: in.roster,
	})
	if err != nil {
		t.Fatalf("PersistSync: %v", err)
	}
	_, err = er.PersistCandidates(context.Background(), esrepo.PersistCandidateInput{
		SyncRunID: &syncID,
		QueryType: "pitchers",
		Status:    "success",
		Payload: espn.RawPayload{
			PayloadType: "free_agents_pitchers", SourceEndpoint: "test", ResponseStatus: 200, PayloadJSON: "{}",
		},
		Candidates: in.cands,
	})
	if err != nil {
		t.Fatalf("PersistCandidates: %v", err)
	}

	svc := New(
		adhocrepo.New(store.DB()),
		esrepo.New(store.DB()),
		tranrepo.New(store.DB()),
		reviewrepo.New(store.DB()),
		Config{
			Enabled:                    true,
			MaxRecentRequests:          25,
			RequirePitchersOnly:        true,
			ReuseBoundedCandidateLimit: 25,
		},
	)
	return svc, func() { store.Close() }
}

func ptr64(v int64) *int64 { return &v }
