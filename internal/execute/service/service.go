package service

import (
	"context"
	"fmt"
	"sort"
	"time"

	"fantasy-baseball/internal/espn"
	esrepo "fantasy-baseball/internal/espn/repository"
	"fantasy-baseball/internal/execute"
	exerepo "fantasy-baseball/internal/execute/repository"
	"fantasy-baseball/internal/pitchers/matching"
	"fantasy-baseball/internal/transactions"
	tranrepo "fantasy-baseball/internal/transactions/repository"
	reviewrepo "fantasy-baseball/internal/transactions/review/repository"
)

type Service struct {
	execRepo   *exerepo.Repository
	reviewRepo *reviewrepo.Repository
	espnRepo   *esrepo.Repository
	tranRepo   *tranrepo.Repository
	cfg        execute.ServiceConfig
}

func New(execRepo *exerepo.Repository, reviewRepo *reviewrepo.Repository, espnRepo *esrepo.Repository, tranRepo *tranrepo.Repository, cfg execute.ServiceConfig) *Service {
	return &Service{
		execRepo:   execRepo,
		reviewRepo: reviewRepo,
		espnRepo:   espnRepo,
		tranRepo:   tranRepo,
		cfg:        cfg,
	}
}

func (s *Service) Preflight(ctx context.Context, opts execute.Options) (*execute.Run, error) {
	return s.generate(ctx, execute.RunTypePreflight, opts)
}

func (s *Service) DryRun(ctx context.Context, opts execute.Options) (*execute.Run, error) {
	return s.generate(ctx, execute.RunTypeDryRun, opts)
}

func (s *Service) Queue(ctx context.Context, limit int) ([]execute.QueueRow, error) {
	queue, err := s.reviewRepo.Queue(ctx, clampLimit(limit, s.cfg.DefaultLimit, s.cfg.MaxLimit))
	if err != nil {
		return nil, err
	}
	out := make([]execute.QueueRow, 0, len(queue))
	for _, q := range queue {
		row := execute.QueueRow{
			ApprovedItemID: q.TransactionPlanItemID,
			SourcePlanID:   q.PlanID,
			AddPlayerName:  q.AddPlayerName,
			DropPlayerName: q.DropPlayerName,
			ApprovedAt:     q.ApprovedAt,
			ApprovalNote:   q.Note,
		}
		last, err := s.execRepo.LatestResultByApprovedItem(ctx, q.TransactionPlanItemID)
		if err != nil {
			return nil, err
		}
		if last != nil {
			st := last.ValidationStatus
			row.LastValidation = &st
			runID := last.ExecutionRunID
			row.LastExecutionRunID = &runID
			tm := last.CreatedAt
			row.LastCheckedAt = &tm
		}
		out = append(out, row)
	}
	return out, nil
}

func (s *Service) Last(ctx context.Context) (*execute.Run, error) {
	run, items, err := s.execRepo.LatestRun(ctx)
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, nil
	}
	run.Items = items
	return run, nil
}

func (s *Service) Show(ctx context.Context, runID int64) (*execute.Run, error) {
	run, items, err := s.execRepo.RunByID(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, nil
	}
	run.Items = items
	return run, nil
}

func (s *Service) generate(ctx context.Context, runType execute.RunType, opts execute.Options) (*execute.Run, error) {
	approved, err := s.reviewRepo.Queue(ctx, clampLimit(opts.Limit, s.cfg.DefaultLimit, s.cfg.MaxLimit))
	if err != nil {
		return nil, err
	}
	if len(approved) == 0 {
		return nil, fmt.Errorf("no approved transaction items found; run `fb transactions approve ...` first")
	}
	if opts.ItemID != nil {
		filtered := approved[:0]
		for _, row := range approved {
			if row.TransactionPlanItemID == *opts.ItemID {
				filtered = append(filtered, row)
			}
		}
		approved = filtered
		if len(approved) == 0 {
			return nil, fmt.Errorf("approved item %d not found in queue", *opts.ItemID)
		}
	}

	var roster []espn.RosterSnapshot
	if s.cfg.RequireLiveRosterCheck {
		roster, err = s.espnRepo.LatestRoster(ctx, nil, false)
		if err != nil {
			return nil, err
		}
	}
	latestSync, err := s.espnRepo.LatestSyncRun(ctx)
	if err != nil {
		return nil, err
	}
	latestCandidateRun, err := s.espnRepo.LatestCandidateRun(ctx)
	if err != nil {
		return nil, err
	}
	candidates := []espn.FreeAgentCandidate{}
	if s.cfg.RequireLiveAvailabilityCheck && latestCandidateRun != nil {
		runID := latestCandidateRun.ID
		candidates, err = s.espnRepo.ListCandidates(ctx, &runID, s.cfg.CandidateRefreshLimit)
		if err != nil {
			return nil, err
		}
	}

	items := make([]execute.RunItem, 0, len(approved))
	statusCounts := map[execute.ValidationStatus]int{}
	for _, row := range approved {
		item := s.validateItem(ctx, row, runType, roster, latestSync, latestCandidateRun, candidates)
		items = append(items, item)
		statusCounts[item.ValidationStatus]++
	}
	sort.SliceStable(items, func(i, j int) bool {
		ri := 99
		if items[i].ReadinessRank != nil {
			ri = *items[i].ReadinessRank
		}
		rj := 99
		if items[j].ReadinessRank != nil {
			rj = *items[j].ReadinessRank
		}
		if ri != rj {
			return ri < rj
		}
		return items[i].ApprovedItemID < items[j].ApprovedItemID
	})

	summary := map[string]any{
		"status_counts": statusCounts,
		"run_type":      runType,
		"item_count":    len(items),
	}
	runID, err := s.execRepo.SaveRun(ctx, exerepo.CreateRunInput{
		RunType: runType,
		Status:  "success",
		Summary: summary,
		Items:   items,
	})
	if err != nil {
		return nil, err
	}
	return s.Show(ctx, runID)
}

func (s *Service) validateItem(ctx context.Context, q espnQueueRow, runType execute.RunType, roster []espn.RosterSnapshot, latestSync *espn.SyncRun, latestCandidateRun *espn.CandidateRun, candidates []espn.FreeAgentCandidate) execute.RunItem {
	reasons := make([]execute.Reason, 0)
	blocked, conflict, stale, unknown := false, false, false, false

	addKey := matching.NormalizeName(q.AddPlayerName)
	dropKey := matching.NormalizeName(q.DropPlayerName)

	plan, _, err := s.tranRepo.PlanByID(ctx, q.PlanID)
	if err != nil || plan == nil {
		conflict = true
		reasons = append(reasons, execute.Reason{Code: "missing_source_plan", Message: "source transaction plan is missing"})
	}

	rosterSet := map[string]int{}
	if s.cfg.RequireLiveRosterCheck {
		if len(roster) == 0 {
			unknown = true
			reasons = append(reasons, execute.Reason{Code: "live_roster_missing", Message: "no live roster snapshot available"})
		} else {
			for _, row := range roster {
				k := matching.NormalizeName(row.PlayerName)
				if k == "" {
					continue
				}
				rosterSet[k]++
			}
			if rosterSet[dropKey] == 0 {
				conflict = true
				reasons = append(reasons, execute.Reason{Code: "drop_target_not_rostered", Message: "drop target is not currently on roster"})
			}
			if rosterSet[addKey] > 0 {
				blocked = true
				reasons = append(reasons, execute.Reason{Code: "add_target_already_rostered", Message: "add target is already on roster"})
			}
		}
	}

	candidateSet := map[string]int{}
	if s.cfg.RequireLiveAvailabilityCheck {
		if latestCandidateRun == nil || len(candidates) == 0 {
			unknown = true
			reasons = append(reasons, execute.Reason{Code: "live_availability_missing", Message: "no live bounded candidate pool available"})
		} else {
			for _, c := range candidates {
				k := matching.NormalizeName(c.PlayerName)
				if k == "" {
					continue
				}
				candidateSet[k]++
			}
			if candidateSet[addKey] == 0 {
				blocked = true
				reasons = append(reasons, execute.Reason{Code: "add_target_unavailable", Message: "add target not found in current candidate pool"})
			}
			if candidateSet[addKey] > 1 {
				unknown = true
				reasons = append(reasons, execute.Reason{Code: "add_target_ambiguous", Message: "multiple matching add-target identities found"})
			}
		}
	}

	if s.cfg.StaleHoursThreshold > 0 {
		ageHours := time.Since(q.ApprovedAt).Hours()
		if ageHours > float64(s.cfg.StaleHoursThreshold) {
			stale = true
			reasons = append(reasons, execute.Reason{Code: "approval_stale", Message: fmt.Sprintf("approval is older than %d hours", s.cfg.StaleHoursThreshold)})
		}
	}
	if latestSync != nil && latestSync.CompletedAt.After(q.ApprovedAt) {
		stale = true
		reasons = append(reasons, execute.Reason{Code: "roster_updated_since_approval", Message: "roster sync is newer than approval"})
	}
	if latestCandidateRun != nil && latestCandidateRun.CompletedAt.After(q.ApprovedAt) {
		stale = true
		reasons = append(reasons, execute.Reason{Code: "candidates_updated_since_approval", Message: "candidate snapshot is newer than approval"})
	}

	status := deriveStatus(conflict, blocked, unknown, stale)
	if len(reasons) == 0 {
		reasons = append(reasons, execute.Reason{Code: "ok", Message: "all preflight checks passed"})
	}

	var syncRunID *int64
	if latestSync != nil {
		v := latestSync.ID
		syncRunID = &v
	}
	var candidateRunID *int64
	if latestCandidateRun != nil {
		v := latestCandidateRun.ID
		candidateRunID = &v
	}
	preview := execute.ActionPreview{
		ActionType:              "add_drop_pitcher",
		ApprovedItemID:          q.TransactionPlanItemID,
		SourcePlanID:            q.PlanID,
		AddPlayerName:           q.AddPlayerName,
		DropPlayerName:          q.DropPlayerName,
		RosterSyncRunID:         syncRunID,
		CandidateRunID:          candidateRunID,
		RosterCheckPassed:       s.cfg.RequireLiveRosterCheck && rosterSet[dropKey] > 0,
		AvailabilityCheckPassed: s.cfg.RequireLiveAvailabilityCheck && candidateSet[addKey] == 1,
		AddAlreadyRostered:      rosterSet[addKey] > 0,
		ExecutionReadiness:      string(status),
		CheckedAt:               time.Now().UTC().Format(time.RFC3339),
	}
	if runType == execute.RunTypeDryRun {
		preview.ActionType = "dry_run_add_drop_pitcher"
	}

	rank := readinessRank(status)
	return execute.RunItem{
		ApprovedItemID:    q.TransactionPlanItemID,
		SourcePlanID:      q.PlanID,
		AddPlayerName:     q.AddPlayerName,
		DropPlayerName:    q.DropPlayerName,
		ValidationStatus:  status,
		ReadinessRank:     &rank,
		ValidationReasons: reasons,
		ActionPreview:     preview,
		Details: map[string]any{
			"approval_note": q.Note,
			"approved_at":   q.ApprovedAt.Format(time.RFC3339),
			"run_type":      runType,
		},
		CreatedAt: time.Now().UTC(),
	}
}

func deriveStatus(conflict, blocked, unknown, stale bool) execute.ValidationStatus {
	switch {
	case conflict:
		return execute.StatusConflict
	case blocked:
		return execute.StatusBlocked
	case unknown:
		return execute.StatusUnknown
	case stale:
		return execute.StatusStale
	default:
		return execute.StatusExecutable
	}
}

func readinessRank(status execute.ValidationStatus) int {
	switch status {
	case execute.StatusExecutable:
		return 1
	case execute.StatusStale:
		return 2
	case execute.StatusUnknown:
		return 3
	case execute.StatusBlocked:
		return 4
	default:
		return 5
	}
}

func clampLimit(limit, def, max int) int {
	if def <= 0 {
		def = 10
	}
	if max <= 0 {
		max = 25
	}
	if limit <= 0 {
		limit = def
	}
	if limit > max {
		limit = max
	}
	return limit
}

type espnQueueRow = transactions.ApprovalQueueItem
