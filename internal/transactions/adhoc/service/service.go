package service

import (
	"context"
	"fmt"
	"strings"

	"fantasy-baseball/internal/espn"
	esrepo "fantasy-baseball/internal/espn/repository"
	"fantasy-baseball/internal/pitchers/matching"
	"fantasy-baseball/internal/transactions"
	adhocrepo "fantasy-baseball/internal/transactions/adhoc/repository"
	tranrepo "fantasy-baseball/internal/transactions/repository"
	reviewrepo "fantasy-baseball/internal/transactions/review/repository"
)

type Config struct {
	Enabled                    bool
	MaxRecentRequests          int
	RequirePitchersOnly        bool
	ReuseBoundedCandidateLimit int
}

type Service struct {
	repo       *adhocrepo.Repository
	espnRepo   *esrepo.Repository
	tranRepo   *tranrepo.Repository
	reviewRepo *reviewrepo.Repository
	cfg        Config
}

func New(repo *adhocrepo.Repository, espnRepo *esrepo.Repository, tranRepo *tranrepo.Repository, reviewRepo *reviewrepo.Repository, cfg Config) *Service {
	return &Service{
		repo:       repo,
		espnRepo:   espnRepo,
		tranRepo:   tranRepo,
		reviewRepo: reviewRepo,
		cfg:        cfg,
	}
}

func (s *Service) CreateAndResolve(ctx context.Context, addName, dropName string) (*transactions.AdHocRequest, error) {
	if !s.cfg.Enabled {
		return nil, fmt.Errorf("ad hoc transactions are disabled by config")
	}
	addName = strings.TrimSpace(addName)
	dropName = strings.TrimSpace(dropName)
	if addName == "" {
		return nil, fmt.Errorf("--add is required")
	}
	id, err := s.repo.Create(ctx, adhocrepo.CreateInput{
		RequestedAddPlayerName:  addName,
		RequestedDropPlayerName: dropName,
		NormalizedAddLookup:     matching.NormalizeName(addName),
		NormalizedDropLookup:    matching.NormalizeName(dropName),
	})
	if err != nil {
		return nil, err
	}
	_ = s.repo.AddEvent(ctx, id, "request_created", map[string]any{
		"add":  addName,
		"drop": dropName,
	})

	req, err := s.resolve(ctx, id)
	if err != nil {
		return nil, err
	}
	return req, nil
}

func (s *Service) ByID(ctx context.Context, requestID int64) (*transactions.AdHocRequest, error) {
	return s.repo.ByID(ctx, requestID)
}

func (s *Service) List(ctx context.Context, limit int, state *transactions.AdHocRequestState) ([]transactions.AdHocRequest, error) {
	if limit <= 0 {
		limit = s.cfg.MaxRecentRequests
	}
	if limit <= 0 {
		limit = 25
	}
	return s.repo.List(ctx, limit, state)
}

func (s *Service) EnsureExecutionCandidate(ctx context.Context, requestID int64) (*transactions.AdHocRequest, int64, error) {
	req, err := s.repo.ByID(ctx, requestID)
	if err != nil {
		return nil, 0, err
	}
	if req == nil {
		return nil, 0, fmt.Errorf("ad hoc request %d not found", requestID)
	}
	if req.ResolutionStatus != transactions.AdHocResolutionResolved || req.RequestState == transactions.AdHocStateUnresolved {
		return req, 0, fmt.Errorf("ad hoc request %d is not resolved and cannot execute", requestID)
	}
	if req.LinkedPlanItemID != nil {
		return req, *req.LinkedPlanItemID, nil
	}
	addOnly := strings.TrimSpace(req.RequestedDropPlayerName) == ""
	if req.ResolvedAddESPNPlayerID == nil || (!addOnly && req.ResolvedDropESPNPlayerID == nil) {
		return req, 0, fmt.Errorf("ad hoc request %d missing resolved player IDs", requestID)
	}

	planID, err := s.tranRepo.SavePlan(ctx, transactions.CreatePlanInput{
		WindowStart: "adhoc",
		WindowEnd:   "adhoc",
		Status:      "success",
		Summary: map[string]interface{}{
			"source":     "ad_hoc",
			"request_id": requestID,
		},
		Items: []transactions.PlanItem{{
			Bucket:           transactions.BucketWatchOnly,
			AddPlayerName:    req.ResolvedAddPlayerName,
			AddESPNPlayerID:  req.ResolvedAddESPNPlayerID,
			DropPlayerName:   req.ResolvedDropPlayerName,
			DropESPNPlayerID: req.ResolvedDropESPNPlayerID,
			Flags:            adHocFlags(addOnly),
			Details: map[string]interface{}{
				"action_type": actionType(addOnly),
				"add_only":    addOnly,
			},
		}},
	})
	if err != nil {
		return req, 0, err
	}
	items, err := s.tranRepo.PlanItems(ctx, planID)
	if err != nil {
		return req, 0, err
	}
	if len(items) != 1 {
		return req, 0, fmt.Errorf("unexpected synthetic plan item count")
	}
	itemID := items[0].ID
	if _, err := s.reviewRepo.TransitionState(ctx, planID, itemID, transactions.ReviewStateApproved, fmt.Sprintf("ad_hoc_request:%d", requestID)); err != nil {
		return req, 0, err
	}
	if err := s.repo.LinkExecutionCandidate(ctx, requestID, &planID, &itemID, transactions.AdHocStatePreflight); err != nil {
		return req, 0, err
	}
	_ = s.repo.AddEvent(ctx, requestID, "preflight_passed", map[string]any{
		"linked_plan_id":      planID,
		"linked_plan_item_id": itemID,
	})
	updated, _ := s.repo.ByID(ctx, requestID)
	return updated, itemID, nil
}

func (s *Service) LinkExecutionResult(ctx context.Context, requestID int64, attemptID int64, success bool) error {
	state := transactions.AdHocStateFailed
	event := "execution_failed"
	if success {
		state = transactions.AdHocStateExecuted
		event = "execution_succeeded"
	}
	if err := s.repo.LinkExecutionAttempt(ctx, requestID, attemptID, state); err != nil {
		return err
	}
	_ = s.repo.AddEvent(ctx, requestID, event, map[string]any{"execution_attempt_id": attemptID})
	return nil
}

func (s *Service) resolve(ctx context.Context, requestID int64) (*transactions.AdHocRequest, error) {
	req, err := s.repo.ByID(ctx, requestID)
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, fmt.Errorf("ad hoc request %d not found", requestID)
	}

	roster, err := s.espnRepo.LatestRoster(ctx, nil, false)
	if err != nil {
		return nil, err
	}
	latestCandidateRun, err := s.espnRepo.LatestCandidateRun(ctx)
	if err != nil {
		return nil, err
	}
	// Keep ad-hoc resolution aligned with `fb espn show free-agents` by pinning to
	// a concrete latest run ID and reading enough rows to cover that run.
	candidateLimit := s.cfg.ReuseBoundedCandidateLimit
	if candidateLimit <= 0 {
		candidateLimit = 200
	}
	var candidates []espn.FreeAgentCandidate
	if latestCandidateRun != nil {
		runID := latestCandidateRun.ID
		if latestCandidateRun.CandidateCount > candidateLimit {
			candidateLimit = latestCandidateRun.CandidateCount
		}
		if candidateLimit > 500 {
			candidateLimit = 500
		}
		candidates, err = s.espnRepo.ListCandidates(ctx, &runID, candidateLimit)
	} else {
		candidates, err = s.espnRepo.ListCandidates(ctx, nil, candidateLimit)
	}
	if err != nil {
		return nil, err
	}

	notes := map[string]any{}
	addMatches := make([]struct {
		Name string
		ID   *int64
		Role string
	}, 0)
	addNonPitcherFound := false
	for _, c := range candidates {
		if matching.NormalizeName(c.PlayerName) != req.NormalizedAddLookup {
			continue
		}
		if s.cfg.RequirePitchersOnly && !c.IsPitcher {
			addNonPitcherFound = true
			continue
		}
		addMatches = append(addMatches, struct {
			Name string
			ID   *int64
			Role string
		}{Name: c.PlayerName, ID: c.ESPNPlayerID, Role: c.Role})
	}

	dropRequired := strings.TrimSpace(req.RequestedDropPlayerName) != ""
	dropMatches := make([]struct {
		Name string
		ID   *int64
		Role string
	}, 0)
	dropNonPitcherFound := false
	if dropRequired {
		for _, r := range roster {
			if matching.NormalizeName(r.PlayerName) != req.NormalizedDropLookup {
				continue
			}
			if s.cfg.RequirePitchersOnly && !r.IsPitcher {
				dropNonPitcherFound = true
				continue
			}
			dropMatches = append(dropMatches, struct {
				Name string
				ID   *int64
				Role string
			}{Name: r.PlayerName, ID: r.ESPNPlayerID, Role: r.Role})
		}
	}

	state := transactions.AdHocStateResolved
	resolution := transactions.AdHocResolutionResolved
	var addNameResolved, dropNameResolved string
	var addIDResolved, dropIDResolved *int64

	if len(addMatches) == 0 {
		resolution = transactions.AdHocResolutionUnresolved
		state = transactions.AdHocStateUnresolved
		if addNonPitcherFound {
			resolution = transactions.AdHocResolutionInvalidType
			notes["add"] = "add target is not a pitcher"
		} else {
			notes["add"] = "no matching available pitcher found in bounded candidate pool"
		}
	} else if len(addMatches) > 1 {
		resolution = transactions.AdHocResolutionAmbiguous
		state = transactions.AdHocStateUnresolved
		notes["add"] = "ambiguous add target; multiple matches in bounded candidate pool"
	} else {
		addNameResolved = addMatches[0].Name
		addIDResolved = addMatches[0].ID
	}

	if dropRequired {
		if len(dropMatches) == 0 {
			resolution = transactions.AdHocResolutionUnresolved
			state = transactions.AdHocStateUnresolved
			if dropNonPitcherFound {
				resolution = transactions.AdHocResolutionInvalidType
				notes["drop"] = "drop target is not a pitcher"
			} else {
				notes["drop"] = "no matching rostered pitcher found"
			}
		} else if len(dropMatches) > 1 {
			resolution = transactions.AdHocResolutionAmbiguous
			state = transactions.AdHocStateUnresolved
			notes["drop"] = "ambiguous drop target; multiple roster matches"
		} else {
			dropNameResolved = dropMatches[0].Name
			dropIDResolved = dropMatches[0].ID
		}
	}

	if addIDResolved == nil || (dropRequired && dropIDResolved == nil) {
		state = transactions.AdHocStateUnresolved
		if resolution == transactions.AdHocResolutionResolved {
			resolution = transactions.AdHocResolutionUnresolved
		}
	}

	if err := s.repo.Resolve(ctx, adhocrepo.ResolveInput{
		ID:                       requestID,
		RequestState:             state,
		ResolutionStatus:         resolution,
		ResolvedAddPlayerName:    addNameResolved,
		ResolvedAddESPNPlayerID:  addIDResolved,
		ResolvedDropPlayerName:   dropNameResolved,
		ResolvedDropESPNPlayerID: dropIDResolved,
		ResolutionNotes:          notes,
	}); err != nil {
		return nil, err
	}
	event := "resolution_succeeded"
	if state == transactions.AdHocStateUnresolved {
		event = "resolution_failed"
	}
	_ = s.repo.AddEvent(ctx, requestID, event, notes)
	return s.repo.ByID(ctx, requestID)
}

func adHocFlags(addOnly bool) []string {
	if addOnly {
		return []string{"ad_hoc_request", "ad_hoc_add_only"}
	}
	return []string{"ad_hoc_request"}
}

func actionType(addOnly bool) string {
	if addOnly {
		return "add_pitcher"
	}
	return "add_drop_pitcher"
}
