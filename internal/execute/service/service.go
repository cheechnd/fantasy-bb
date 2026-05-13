package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
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

type rosterPreflightContext struct {
	currentRoster      []espn.RosterSnapshot
	effectiveRoster    []espn.RosterSnapshot
	currentSync        *espn.SyncRun
	effectiveSync      *espn.SyncRun
	league             *espn.LeagueSnapshot
	scoringPeriodID    *int
	effectiveNextDay   bool
	usingEffectiveView bool
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

	currentSync, err := s.espnRepo.LatestSyncRun(ctx)
	if err != nil {
		return nil, err
	}
	latestLeague, err := s.espnRepo.LatestLeague(ctx, nil)
	if err != nil {
		return nil, err
	}
	rosterCtx := rosterPreflightContext{
		currentSync:      currentSync,
		effectiveSync:    currentSync,
		league:           latestLeague,
		scoringPeriodID:  opts.ScoringPeriodID,
		effectiveNextDay: opts.EffectiveNextDay,
	}
	if s.cfg.RequireLiveRosterCheck {
		rosterCtx.currentRoster, err = s.espnRepo.LatestRoster(ctx, nil, false)
		if err != nil {
			return nil, err
		}
		if opts.EffectiveNextDay || opts.ScoringPeriodID != nil {
			rosterCtx.usingEffectiveView = true
			rosterCtx.effectiveRoster, err = s.espnRepo.LatestRosterForContext(ctx, nil, opts.ScoringPeriodID, opts.EffectiveNextDay, false)
			if err != nil {
				return nil, err
			}
			rosterCtx.effectiveSync, err = s.espnRepo.LatestSyncRunForContext(ctx, opts.ScoringPeriodID, opts.EffectiveNextDay)
			if err != nil {
				return nil, err
			}
			if rosterCtx.effectiveSync != nil && rosterCtx.effectiveSync.ScoringPeriodID != nil && rosterCtx.scoringPeriodID == nil {
				v := *rosterCtx.effectiveSync.ScoringPeriodID
				rosterCtx.scoringPeriodID = &v
			}
			if league, err := s.espnRepo.LatestLeagueForContext(ctx, nil, opts.ScoringPeriodID, opts.EffectiveNextDay); err == nil && league != nil {
				rosterCtx.league = league
			} else if err != nil {
				return nil, err
			}
		} else {
			rosterCtx.effectiveRoster = rosterCtx.currentRoster
		}
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
		item := s.validateItem(ctx, row, runType, rosterCtx, latestCandidateRun, candidates)
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
	if opts.EffectiveNextDay {
		summary["effective_next_day"] = true
	}
	if rosterCtx.scoringPeriodID != nil {
		summary["preflight_scoring_period_id"] = *rosterCtx.scoringPeriodID
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

func (s *Service) validateItem(ctx context.Context, q espnQueueRow, runType execute.RunType, rosterCtx rosterPreflightContext, latestCandidateRun *espn.CandidateRun, candidates []espn.FreeAgentCandidate) execute.RunItem {
	reasons := make([]execute.Reason, 0)
	blocked, conflict, stale, unknown := false, false, false, false

	addKey := matching.NormalizeName(q.AddPlayerName)
	dropKey := matching.NormalizeName(q.DropPlayerName)
	addOnly := strings.TrimSpace(q.DropPlayerName) == ""

	plan, _, err := s.tranRepo.PlanByID(ctx, q.PlanID)
	if err != nil || plan == nil {
		conflict = true
		reasons = append(reasons, execute.Reason{Code: "missing_source_plan", Message: "source transaction plan is missing"})
	}

	rosterSet := map[string]int{}
	currentRosterSet := map[string]int{}
	if s.cfg.RequireLiveRosterCheck {
		roster := rosterCtx.effectiveRoster
		if len(rosterCtx.currentRoster) > 0 {
			for _, row := range rosterCtx.currentRoster {
				k := matching.NormalizeName(row.PlayerName)
				if k != "" {
					currentRosterSet[k]++
				}
			}
		}
		if len(roster) == 0 {
			unknown = true
			msg := "no live roster snapshot available"
			if rosterCtx.usingEffectiveView {
				msg = "no effective roster snapshot available; run `fb espn sync roster --next-day` first"
			}
			reasons = append(reasons, execute.Reason{Code: "live_roster_missing", Message: msg})
		} else {
			for _, row := range roster {
				k := matching.NormalizeName(row.PlayerName)
				if k == "" {
					continue
				}
				rosterSet[k]++
			}
			if !addOnly && rosterSet[dropKey] == 0 {
				conflict = true
				reasons = append(reasons, execute.Reason{Code: "drop_target_not_rostered", Message: "drop target is not currently on roster"})
			}
			if rosterSet[addKey] > 0 {
				blocked = true
				reasons = append(reasons, execute.Reason{Code: "add_target_already_rostered", Message: "add target is already on roster"})
			}
			if addOnly {
				if cap, ok := normalRosterCapacityFromLeagueSettings(rosterCtx.league); ok && cap > 0 && activeRosterExcludingILCount(roster) >= cap {
					blocked = true
					msg := "no open roster slot available for add-only move"
					if rosterCtx.usingEffectiveView {
						msg = "no open effective roster slot available for add-only move"
					}
					reasons = append(reasons, execute.Reason{Code: "roster_capacity_full", Message: msg})
				}
			}
		}
	}

	candidateSet := map[string]int{}
	waiverSet := map[string]int{}
	addCandidateStatus := ""
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
				if espn.IsImmediateFreeAgent(c.AcquisitionStatus) {
					candidateSet[k]++
					if k == addKey && addCandidateStatus == "" {
						addCandidateStatus = strings.TrimSpace(c.StatusTag)
					}
					continue
				}
				if espn.IsWaiver(c.AcquisitionStatus) {
					waiverSet[k]++
				}
			}
			if candidateSet[addKey] == 0 {
				blocked = true
				if waiverSet[addKey] > 0 {
					reasons = append(reasons, execute.Reason{Code: "add_target_on_waivers", Message: "add target is on waivers and not immediately available"})
				} else {
					reasons = append(reasons, execute.Reason{Code: "add_target_unavailable", Message: "add target not found in current immediate free-agent pool"})
				}
			}
			if candidateSet[addKey] > 1 {
				unknown = true
				reasons = append(reasons, execute.Reason{Code: "add_target_ambiguous", Message: "multiple matching add-target identities found"})
			}
			if candidateSet[addKey] > 0 && shouldWarnCandidateStatus(addCandidateStatus) {
				reasons = append(reasons, execute.Reason{
					Code:    "add_target_status_warning",
					Message: fmt.Sprintf("add target is marked %s by ESPN; verify projected start before adding/starting", addCandidateStatus),
				})
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
	if rosterCtx.effectiveSync != nil && rosterCtx.effectiveSync.CompletedAt.After(q.ApprovedAt) {
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
	if rosterCtx.effectiveSync != nil {
		v := rosterCtx.effectiveSync.ID
		syncRunID = &v
	}
	var candidateRunID *int64
	if latestCandidateRun != nil {
		v := latestCandidateRun.ID
		candidateRunID = &v
	}
	preview := execute.ActionPreview{
		ActionType:              actionType(addOnly),
		ApprovedItemID:          q.TransactionPlanItemID,
		SourcePlanID:            q.PlanID,
		AddPlayerName:           q.AddPlayerName,
		DropPlayerName:          q.DropPlayerName,
		RosterSyncRunID:         syncRunID,
		CandidateRunID:          candidateRunID,
		RosterCheckPassed:       s.cfg.RequireLiveRosterCheck && (addOnly || rosterSet[dropKey] > 0),
		AvailabilityCheckPassed: s.cfg.RequireLiveAvailabilityCheck && candidateSet[addKey] == 1,
		AddAlreadyRostered:      rosterSet[addKey] > 0,
		ExecutionReadiness:      string(status),
		CheckedAt:               time.Now().UTC().Format(time.RFC3339),
	}
	if runType == execute.RunTypeDryRun {
		if addOnly {
			preview.ActionType = "dry_run_add_pitcher"
		} else {
			preview.ActionType = "dry_run_add_drop_pitcher"
		}
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
			"approval_note":  q.Note,
			"approved_at":    q.ApprovedAt.Format(time.RFC3339),
			"run_type":       runType,
			"roster_context": rosterContextDetails(rosterCtx, rosterSet, currentRosterSet),
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

func shouldWarnCandidateStatus(status string) bool {
	s := strings.ToUpper(strings.TrimSpace(status))
	if s == "" || s == "ACTIVE" {
		return false
	}
	return true
}

func rosterContextDetails(ctx rosterPreflightContext, effectiveSet, currentSet map[string]int) map[string]any {
	details := map[string]any{
		"effective_next_day": ctx.effectiveNextDay,
	}
	if ctx.scoringPeriodID != nil {
		details["preflight_scoring_period_id"] = *ctx.scoringPeriodID
	}
	if ctx.currentSync != nil {
		details["current_roster_sync_run_id"] = ctx.currentSync.ID
	}
	if ctx.effectiveSync != nil {
		details["effective_roster_sync_run_id"] = ctx.effectiveSync.ID
	}
	currentTotal := len(ctx.currentRoster)
	effectiveTotal := len(ctx.effectiveRoster)
	details["current_roster_total"] = currentTotal
	details["effective_roster_total"] = effectiveTotal
	currentActiveTotal := activeRosterExcludingILCount(ctx.currentRoster)
	effectiveActiveTotal := activeRosterExcludingILCount(ctx.effectiveRoster)
	currentILTotal := ilRosterCount(ctx.currentRoster)
	effectiveILTotal := ilRosterCount(ctx.effectiveRoster)
	details["current_active_roster_total_excluding_il"] = currentActiveTotal
	details["effective_active_roster_total_excluding_il"] = effectiveActiveTotal
	details["current_il_roster_total"] = currentILTotal
	details["effective_il_roster_total"] = effectiveILTotal
	if cap, ilCap, ok := rosterCapacitiesFromLeagueSettings(ctx.league); ok && cap > 0 {
		totalCap := cap + ilCap
		details["total_roster_capacity_including_il"] = totalCap
		details["active_roster_capacity_excluding_il"] = cap
		details["il_roster_capacity"] = ilCap
		details["current_open_active_roster_slots_excluding_il"] = maxInt(0, cap-currentActiveTotal)
		details["effective_open_active_roster_slots_excluding_il"] = maxInt(0, cap-effectiveActiveTotal)
		details["current_active_roster_capacity_full_excluding_il"] = currentActiveTotal >= cap
		details["effective_active_roster_capacity_full_excluding_il"] = effectiveActiveTotal >= cap
		details["current_open_total_roster_slots_including_il"] = maxInt(0, totalCap-currentTotal)
		details["effective_open_total_roster_slots_including_il"] = maxInt(0, totalCap-effectiveTotal)
	}
	details["current_distinct_rostered"] = len(currentSet)
	details["effective_distinct_rostered"] = len(effectiveSet)
	return details
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func actionType(addOnly bool) string {
	if addOnly {
		return "add_pitcher"
	}
	return "add_drop_pitcher"
}

func normalRosterCapacityFromLeagueSettings(league *espn.LeagueSnapshot) (int, bool) {
	normal, _, ok := rosterCapacitiesFromLeagueSettings(league)
	return normal, ok
}

func rosterCapacitiesFromLeagueSettings(league *espn.LeagueSnapshot) (normal int, il int, ok bool) {
	if league == nil || strings.TrimSpace(league.SettingsJSON) == "" {
		return 0, 0, false
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(league.SettingsJSON), &raw); err != nil {
		return 0, 0, false
	}
	slotCounts, ok := raw["lineupSlotCounts"].(map[string]any)
	if !ok || len(slotCounts) == 0 {
		return 0, 0, false
	}
	for slotID, v := range slotCounts {
		count := 0
		switch t := v.(type) {
		case float64:
			if t > 0 {
				count = int(t)
			}
		case int:
			if t > 0 {
				count = t
			}
		case int64:
			if t > 0 {
				count = int(t)
			}
		}
		if count <= 0 {
			continue
		}
		if slotID == "17" {
			il += count
			continue
		}
		normal += count
	}
	if normal <= 0 && il <= 0 {
		return 0, 0, false
	}
	return normal, il, true
}

func activeRosterExcludingILCount(rows []espn.RosterSnapshot) int {
	count := 0
	for _, row := range rows {
		if !isILRosterSlot(row.RosterSlot) {
			count++
		}
	}
	return count
}

func ilRosterCount(rows []espn.RosterSnapshot) int {
	count := 0
	for _, row := range rows {
		if isILRosterSlot(row.RosterSlot) {
			count++
		}
	}
	return count
}

func isILRosterSlot(slot string) bool {
	return strings.EqualFold(strings.TrimSpace(slot), "IL")
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
