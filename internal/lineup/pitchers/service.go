package pitchers

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"fantasy-baseball/internal/config"
	"fantasy-baseball/internal/espn"
	esrepo "fantasy-baseball/internal/espn/repository"
	"fantasy-baseball/internal/execute"
	"fantasy-baseball/internal/pitchers/matching"
	pitchplan "fantasy-baseball/internal/pitchers/planner"
)

type Writer interface {
	ExecuteLineupMove(ctx context.Context, cfg config.Config, req LineupWriteRequest) (LineupWriteResult, error)
}

type Verifier interface {
	VerifyLineupMove(ctx context.Context, cfg config.Config, req LineupWriteRequest, writeRes LineupWriteResult) (execute.VerificationStatus, map[string]any, error)
}

type LineupWriteRequest struct {
	ApprovedItemID    int64
	PlanID            int64
	PlayerName        string
	ESPNPlayerID      int64
	FromSlot          string
	ToSlot            string
	ScoringPeriodID   *int
	ScoringPeriodDate *string
	EffectiveNextDay  bool
}

type LineupWriteResult struct {
	OK             bool
	Endpoint       string
	ResponseStatus int
	ResponseJSON   map[string]any
	Message        string
}

type Service struct {
	repo            *Repository
	pitcherPlanRepo *pitchplan.Repository
	espnRepo        *esrepo.Repository
	writer          Writer
	verifier        Verifier
}

func NewService(repo *Repository, pitcherPlanRepo *pitchplan.Repository, espnRepo *esrepo.Repository, writer Writer, verifier Verifier) *Service {
	return &Service{repo: repo, pitcherPlanRepo: pitcherPlanRepo, espnRepo: espnRepo, writer: writer, verifier: verifier}
}

func (s *Service) GeneratePlan(ctx context.Context, pitcherPlanID, syncRunID *int64) (*Plan, error) {
	var pPlan *pitchplan.Plan
	var pItems []pitchplan.PlanItem
	var err error
	if pitcherPlanID != nil {
		pPlan, pItems, err = s.pitcherPlanRepo.PlanByID(ctx, *pitcherPlanID)
	} else {
		pPlan, pItems, err = s.pitcherPlanRepo.LatestPlan(ctx)
	}
	if err != nil {
		return nil, err
	}
	if pPlan == nil {
		return nil, fmt.Errorf("no pitcher plan found; run `fb pitchers plan` first")
	}

	resolvedSyncRunID := syncRunID
	if resolvedSyncRunID == nil {
		resolvedSyncRunID = pPlan.SyncRunID
	}
	rows, err := s.espnRepo.LatestRoster(ctx, resolvedSyncRunID, true)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("no ESPN pitcher roster snapshot found; run `fb espn sync roster` first")
	}
	if resolvedSyncRunID == nil {
		v := rows[0].SyncRunID
		resolvedSyncRunID = &v
	}

	league, err := s.espnRepo.LatestLeague(ctx, resolvedSyncRunID)
	if err != nil {
		return nil, err
	}
	slotCaps := pitcherSlotCapacities(nil)
	if league != nil {
		slotCaps = pitcherSlotCapacities(league)
	}
	items, summary := buildLineupItems(pItems, rows, slotCaps)
	planID, err := s.repo.SavePlan(ctx, &pPlan.ID, resolvedSyncRunID, "success", summary, items)
	if err != nil {
		return nil, err
	}
	plan, savedItems, err := s.repo.PlanByID(ctx, planID)
	if err != nil {
		return nil, err
	}
	if plan == nil {
		return nil, fmt.Errorf("saved lineup plan %d not found", planID)
	}
	plan.Items = savedItems
	return plan, nil
}

func (s *Service) CreateAdHocPlan(ctx context.Context, playerName, toSlot string, syncRunID *int64) (*Plan, error) {
	return s.CreateAdHocPlanWithOptions(ctx, playerName, toSlot, ContextOptions{SyncRunID: syncRunID})
}

func (s *Service) CreateAdHocPlanWithOptions(ctx context.Context, playerName, toSlot string, opts ContextOptions) (*Plan, error) {
	playerName = strings.TrimSpace(playerName)
	if playerName == "" {
		return nil, fmt.Errorf("--player is required")
	}
	targetSlot, err := normalizeAdHocTargetSlot(toSlot)
	if err != nil {
		return nil, err
	}
	if err := s.validateContextOptions(ctx, opts); err != nil {
		return nil, err
	}

	rows, err := s.espnRepo.LatestRosterForContext(ctx, opts.SyncRunID, opts.ScoringPeriodID, opts.EffectiveNextDay, true)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("no ESPN pitcher roster snapshot found; run `fb espn sync roster` first")
	}
	resolvedSyncRunID := opts.SyncRunID
	if resolvedSyncRunID == nil {
		v := rows[0].SyncRunID
		resolvedSyncRunID = &v
	}

	rosterByName := rosterIndex(rows)
	row := findRosterRowByName(rosterByName, playerName)
	if row == nil {
		return nil, fmt.Errorf("pitcher %q not found on latest roster", playerName)
	}
	current := strings.ToUpper(strings.TrimSpace(row.RosterSlot))
	if current == targetSlot {
		return nil, fmt.Errorf("pitcher %q is already in slot %s", row.PlayerName, targetSlot)
	}

	actionType := ActionBenchPitcher
	flags := []string{"ad_hoc_bench"}
	if isActiveSlot(targetSlot) {
		actionType = ActionActivatePitcher
		flags = []string{"ad_hoc_activate"}
	}
	item := PlanItem{
		ActionType:   actionType,
		PlayerName:   row.PlayerName,
		ESPNPlayerID: row.ESPNPlayerID,
		CurrentSlot:  current,
		TargetSlot:   targetSlot,
		Rationale: map[string]any{
			"source": "ad_hoc",
			"input": map[string]any{
				"player":  playerName,
				"to_slot": targetSlot,
			},
			"context": targetContextDetails(opts, s.targetScoringPeriodDate(opts)),
		},
		Flags:     flags,
		CreatedAt: time.Now().UTC(),
	}
	summary := map[string]any{
		"source": "ad_hoc",
		"counts": map[string]int{
			"ad_hoc_actions": 1,
		},
	}
	planID, err := s.repo.SavePlan(ctx, nil, resolvedSyncRunID, "ad_hoc", summary, []PlanItem{item})
	if err != nil {
		return nil, err
	}
	plan, savedItems, err := s.repo.PlanByID(ctx, planID)
	if err != nil {
		return nil, err
	}
	if plan == nil {
		return nil, fmt.Errorf("saved ad hoc lineup plan %d not found", planID)
	}
	plan.Items = savedItems
	return plan, nil
}

func (s *Service) LatestPlan(ctx context.Context) (*Plan, error) {
	p, items, err := s.repo.LatestPlan(ctx)
	if err != nil || p == nil {
		return p, err
	}
	p.Items = items
	return p, nil
}

func (s *Service) PlanByID(ctx context.Context, planID int64) (*Plan, error) {
	p, items, err := s.repo.PlanByID(ctx, planID)
	if err != nil || p == nil {
		return p, err
	}
	p.Items = items
	return p, nil
}

func (s *Service) Review(ctx context.Context, planID int64) ([]ReviewedPlanItem, error) {
	return s.repo.ReviewedItems(ctx, planID)
}

func (s *Service) Transition(ctx context.Context, planID, itemID int64, target ReviewState, note string) (*ReviewDecision, error) {
	return s.repo.TransitionState(ctx, planID, itemID, target, note)
}

func (s *Service) Queue(ctx context.Context, limit int) ([]QueueItem, error) {
	return s.repo.Queue(ctx, limit)
}

func (s *Service) Preflight(ctx context.Context, itemID *int64, limit int) (*PreflightResult, error) {
	return s.PreflightWithOptions(ctx, itemID, limit, ContextOptions{})
}

func (s *Service) PreflightWithOptions(ctx context.Context, itemID *int64, limit int, opts ContextOptions) (*PreflightResult, error) {
	if err := s.validateContextOptions(ctx, opts); err != nil {
		return nil, err
	}
	var candidates []QueueItem
	var err error
	if itemID != nil {
		it, err := s.repo.ReviewedItemByID(ctx, *itemID)
		if err != nil {
			return nil, err
		}
		if it == nil {
			return nil, fmt.Errorf("approved lineup item %d not found", *itemID)
		}
		if it.ReviewState != ReviewStateApproved {
			return nil, fmt.Errorf("lineup item %d is not approved (current state: %s)", *itemID, it.ReviewState)
		}
		candidates = []QueueItem{{
			LineupPlanItemID: it.ID,
			PlanID:           it.LineupPlanID,
			ActionType:       it.ActionType,
			PlayerName:       it.PlayerName,
			CurrentSlot:      it.CurrentSlot,
			TargetSlot:       it.TargetSlot,
			State:            it.ReviewState,
			ApprovedAt:       it.ReviewUpdated,
			Note:             it.ReviewNote,
		}}
	} else {
		candidates, err = s.repo.Queue(ctx, limit)
		if err != nil {
			return nil, err
		}
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no approved lineup actions in queue")
	}

	latestSync, err := s.espnRepo.LatestSyncRunForContext(ctx, opts.ScoringPeriodID, opts.EffectiveNextDay)
	if err != nil {
		return nil, err
	}
	targetScoringPeriodID := opts.ScoringPeriodID
	if targetScoringPeriodID == nil && latestSync != nil && latestSync.ScoringPeriodID != nil {
		v := *latestSync.ScoringPeriodID
		targetScoringPeriodID = &v
	}
	rows, err := s.espnRepo.LatestRosterForContext(ctx, opts.SyncRunID, opts.ScoringPeriodID, opts.EffectiveNextDay, true)
	if err != nil {
		return nil, err
	}
	league, err := s.espnRepo.LatestLeagueForContext(ctx, opts.SyncRunID, opts.ScoringPeriodID, opts.EffectiveNextDay)
	if err != nil {
		return nil, err
	}
	rosterByName := rosterIndex(rows)
	slotCaps := pitcherSlotCapacities(league)
	slotUsage := pitcherSlotUsage(rows)

	out := make([]PreflightItem, 0, len(candidates))
	now := time.Now().UTC()
	targetDate := s.targetScoringPeriodDate(opts)
	for _, q := range candidates {
		reasons := []execute.Reason{}
		status := execute.StatusExecutable
		resolvedCurrent := strings.ToUpper(strings.TrimSpace(q.CurrentSlot))
		row := findRosterRowByName(rosterByName, q.PlayerName)
		if row == nil {
			status = execute.StatusConflict
			reasons = append(reasons, execute.Reason{Code: "player_not_on_roster", Message: "pitcher is no longer on roster"})
		} else {
			current := strings.ToUpper(strings.TrimSpace(row.RosterSlot))
			resolvedCurrent = current
			isActive := isActiveSlot(current)
			targetActive := isTargetActive(q.ActionType)
			if q.ActionType != ActionActivatePitcher && q.ActionType != ActionBenchPitcher {
				status = execute.StatusBlocked
				reasons = append(reasons, execute.Reason{Code: "non_actionable", Message: "lineup item is not actionable"})
			} else if targetActive && isActive {
				status = execute.StatusBlocked
				reasons = append(reasons, execute.Reason{Code: "already_active", Message: "pitcher is already active"})
			} else if !targetActive && !isActive {
				status = execute.StatusBlocked
				reasons = append(reasons, execute.Reason{Code: "already_benched", Message: "pitcher is already benched/out of active slot"})
			}
			targetSlot := strings.ToUpper(strings.TrimSpace(q.TargetSlot))
			if status == execute.StatusExecutable && targetActive && targetSlot != "" && current != targetSlot {
				if isTargetSlotFull(targetSlot, slotUsage, slotCaps) {
					status = execute.StatusBlocked
					reasons = append(reasons, execute.Reason{Code: "target_slot_full", Message: fmt.Sprintf("target slot %s is currently full", targetSlot)})
				}
			}
			if latestSync != nil && row.SyncRunID != latestSync.ID {
				if status == execute.StatusExecutable {
					status = execute.StatusStale
				}
				reasons = append(reasons, execute.Reason{Code: "stale_sync", Message: "lineup action source sync is stale versus latest roster sync"})
			}
		}
		if len(reasons) == 0 {
			reasons = append(reasons, execute.Reason{Code: "ok", Message: "preflight checks passed"})
		}
		out = append(out, PreflightItem{
			LineupPlanItemID:        q.LineupPlanItemID,
			PlanID:                  q.PlanID,
			PlayerName:              q.PlayerName,
			ActionType:              q.ActionType,
			ValidationStatus:        status,
			Reasons:                 reasons,
			CurrentSlot:             resolvedCurrent,
			TargetSlot:              q.TargetSlot,
			TargetScoringPeriodID:   targetScoringPeriodID,
			TargetScoringPeriodDate: targetDate,
			EffectiveNextDay:        opts.EffectiveNextDay,
			CheckedAt:               now,
		})
	}
	return &PreflightResult{Items: out}, nil
}

func (s *Service) Execute(ctx context.Context, cfg config.Config, itemID int64, confirm bool) (*ExecutionAttempt, *PreflightItem, bool, string, error) {
	return s.ExecuteWithOptions(ctx, cfg, itemID, confirm, ContextOptions{})
}

func (s *Service) ExecuteWithOptions(ctx context.Context, cfg config.Config, itemID int64, confirm bool, opts ContextOptions) (*ExecutionAttempt, *PreflightItem, bool, string, error) {
	if itemID <= 0 {
		return nil, nil, false, "", fmt.Errorf("--item must be > 0")
	}
	if err := s.validateContextOptions(ctx, opts); err != nil {
		return nil, nil, false, "", err
	}
	it, err := s.repo.ReviewedItemByID(ctx, itemID)
	if err != nil {
		return nil, nil, false, "", err
	}
	if it == nil {
		return nil, nil, false, "", fmt.Errorf("approved lineup item %d not found", itemID)
	}
	if it.ReviewState != ReviewStateApproved {
		return nil, nil, false, "", fmt.Errorf("lineup item %d is not approved (current state: %s)", itemID, it.ReviewState)
	}
	if !cfg.Lineup.Pitchers.Enabled {
		return nil, nil, false, "", fmt.Errorf("lineup.pitchers.enabled=false in config")
	}
	if !cfg.Execution.Real.Enabled {
		return nil, nil, false, "", fmt.Errorf("real execution is disabled by config (execution.real.enabled=false)")
	}
	if cfg.Execution.Real.RequireConfirmation && !confirm {
		// allow preview below
	}

	pf, err := s.PreflightWithOptions(ctx, &itemID, 1, opts)
	if err != nil {
		return nil, nil, false, "", err
	}
	if len(pf.Items) == 0 {
		return nil, nil, false, "", fmt.Errorf("preflight returned no items")
	}
	pre := pf.Items[0]
	if !confirm {
		return nil, &pre, false, "confirmation required; rerun with --confirm", nil
	}
	if pre.ValidationStatus != execute.StatusExecutable {
		attemptID, _ := s.repo.CreateExecutionAttempt(ctx, itemID, it.LineupPlanID, execute.ExecutionStatusAborted, execute.VerificationStatusUnknown, map[string]any{
			"action_type":  it.ActionType,
			"player":       it.PlayerName,
			"current_slot": it.CurrentSlot,
			"target_slot":  it.TargetSlot,
		}, map[string]any{"reason": fmt.Sprintf("immediate preflight returned %s", pre.ValidationStatus)})
		_ = s.repo.AddExecutionEvent(ctx, attemptID, "execution_aborted", map[string]any{"preflight_status": pre.ValidationStatus})
		_ = s.repo.CompleteExecutionAttempt(ctx, attemptID, execute.ExecutionStatusAborted, execute.VerificationStatusUnknown, map[string]any{"ok": false}, map[string]any{"preflight": pre}, fmt.Sprintf("immediate preflight returned %s", pre.ValidationStatus))
		a, _, _ := s.repo.ExecutionByID(ctx, attemptID)
		return a, &pre, false, fmt.Sprintf("execution aborted: immediate preflight returned %s", pre.ValidationStatus), nil
	}
	if !cfg.Execution.Real.AllowRepeatExecution {
		has, err := s.repo.HasSuccessfulExecutionForItem(ctx, itemID)
		if err != nil {
			return nil, nil, false, "", err
		}
		if has {
			return nil, &pre, false, "", fmt.Errorf("lineup execution blocked: approved lineup item %d already has a successful execution attempt", itemID)
		}
	}
	if cfg.Execution.Hardening.BlockOnAmbiguousPriorAttempt {
		last, err := s.repo.LatestExecutionByItem(ctx, itemID)
		if err != nil {
			return nil, nil, false, "", err
		}
		if last != nil && (last.ExecutionStatus == execute.ExecutionStatusAmbiguous || last.VerificationStatus == execute.VerificationStatusPending) {
			return nil, &pre, false, "", fmt.Errorf("lineup execution blocked: item %d has unresolved prior attempt (%s/%s)", itemID, last.ExecutionStatus, last.VerificationStatus)
		}
	}

	if it.ESPNPlayerID == nil {
		attemptID, _ := s.repo.CreateExecutionAttempt(ctx, itemID, it.LineupPlanID, execute.ExecutionStatusAborted, execute.VerificationStatusUnknown, map[string]any{"action_type": it.ActionType, "player": it.PlayerName}, nil)
		_ = s.repo.CompleteExecutionAttempt(ctx, attemptID, execute.ExecutionStatusAborted, execute.VerificationStatusUnknown, nil, nil, "missing espn_player_id for lineup action")
		a, _, _ := s.repo.ExecutionByID(ctx, attemptID)
		return a, &pre, false, "execution aborted: missing ESPN player ID", nil
	}

	req := LineupWriteRequest{
		ApprovedItemID:    itemID,
		PlanID:            it.LineupPlanID,
		PlayerName:        it.PlayerName,
		ESPNPlayerID:      *it.ESPNPlayerID,
		FromSlot:          pre.CurrentSlot,
		ToSlot:            it.TargetSlot,
		ScoringPeriodID:   pre.TargetScoringPeriodID,
		ScoringPeriodDate: pre.TargetScoringPeriodDate,
		EffectiveNextDay:  opts.EffectiveNextDay,
	}
	attemptID, err := s.repo.CreateExecutionAttempt(ctx, itemID, it.LineupPlanID, execute.ExecutionStatusStarted, execute.VerificationStatusUnknown, map[string]any{
		"action_type":                it.ActionType,
		"player":                     req.PlayerName,
		"espn_player_id":             req.ESPNPlayerID,
		"from_slot":                  req.FromSlot,
		"to_slot":                    req.ToSlot,
		"target_scoring_period_id":   req.ScoringPeriodID,
		"target_scoring_period_date": req.ScoringPeriodDate,
		"effective_next_day":         req.EffectiveNextDay,
	}, nil)
	if err != nil {
		return nil, nil, false, "", err
	}
	_ = s.repo.AddExecutionEvent(ctx, attemptID, "preflight_passed", map[string]any{"status": pre.ValidationStatus})
	_ = s.repo.AddExecutionEvent(ctx, attemptID, "write_started", nil)

	wres, werr := s.writer.ExecuteLineupMove(ctx, cfg, req)
	if werr != nil {
		_ = s.repo.AddExecutionEvent(ctx, attemptID, "write_failed", map[string]any{"error": werr.Error()})
		_ = s.repo.CompleteExecutionAttempt(ctx, attemptID, execute.ExecutionStatusFailed, execute.VerificationStatusUnknown, map[string]any{"ok": false, "endpoint": wres.Endpoint, "response_status": wres.ResponseStatus}, nil, werr.Error())
		a, _, _ := s.repo.ExecutionByID(ctx, attemptID)
		return a, &pre, true, fmt.Sprintf("execution failed: %s", werr.Error()), nil
	}
	_ = s.repo.AddExecutionEvent(ctx, attemptID, "write_succeeded", map[string]any{"endpoint": wres.Endpoint, "response_status": wres.ResponseStatus})

	_ = s.repo.AddExecutionEvent(ctx, attemptID, "verification_started", nil)
	verStatus, verDetails, verErr := s.verifier.VerifyLineupMove(ctx, cfg, req, wres)
	if verErr != nil {
		verStatus = execute.VerificationStatusVerificationFailed
		verDetails = map[string]any{"error": verErr.Error()}
		_ = s.repo.AddExecutionEvent(ctx, attemptID, "verification_failed", verDetails)
	} else {
		if verStatus == execute.VerificationStatusVerified {
			_ = s.repo.AddExecutionEvent(ctx, attemptID, "verification_succeeded", verDetails)
		} else if verStatus == execute.VerificationStatusPending {
			_ = s.repo.AddExecutionEvent(ctx, attemptID, "verification_pending", verDetails)
		} else {
			_ = s.repo.AddExecutionEvent(ctx, attemptID, "verification_inconclusive", verDetails)
		}
	}
	execStatus := deriveExecutionStatus(verStatus)
	_ = s.repo.CompleteExecutionAttempt(ctx, attemptID, execStatus, verStatus, map[string]any{"ok": wres.OK, "endpoint": wres.Endpoint, "response_status": wres.ResponseStatus, "response_json": wres.ResponseJSON, "message": wres.Message}, verDetails, "")
	a, _, _ := s.repo.ExecutionByID(ctx, attemptID)
	return a, &pre, true, executionMessage(execStatus, verStatus), nil
}

func (s *Service) ExecutionHistory(ctx context.Context, limit int) ([]ExecutionAttempt, error) {
	return s.repo.ListExecutionHistory(ctx, limit)
}

func (s *Service) ExecutionResult(ctx context.Context, id int64) (*ExecutionAttempt, error) {
	a, _, err := s.repo.ExecutionByID(ctx, id)
	return a, err
}

func (s *Service) LatestExecution(ctx context.Context) (*ExecutionAttempt, error) {
	return s.repo.LatestExecution(ctx)
}

func (s *Service) validateContextOptions(ctx context.Context, opts ContextOptions) error {
	if opts.ScoringPeriodID != nil && opts.EffectiveNextDay {
		return fmt.Errorf("use only one of --scoring-period-id or --next-day")
	}
	if opts.ScoringPeriodID != nil && *opts.ScoringPeriodID <= 0 {
		return fmt.Errorf("--scoring-period-id must be > 0")
	}
	if opts.SyncRunID == nil {
		return nil
	}
	run, err := s.espnRepo.SyncRunByID(ctx, *opts.SyncRunID)
	if err != nil {
		return err
	}
	if run == nil {
		return fmt.Errorf("ESPN sync run %d not found", *opts.SyncRunID)
	}
	if run.SyncType != "roster" {
		return fmt.Errorf("ESPN sync run %d is %q, not roster", *opts.SyncRunID, run.SyncType)
	}
	if opts.ScoringPeriodID != nil {
		if run.ScoringPeriodID == nil || *run.ScoringPeriodID != *opts.ScoringPeriodID {
			got := "today"
			if run.ScoringPeriodID != nil {
				got = strconv.Itoa(*run.ScoringPeriodID)
			}
			return fmt.Errorf("--sync-run %d belongs to scoring period %s, not %d", *opts.SyncRunID, got, *opts.ScoringPeriodID)
		}
	}
	if opts.EffectiveNextDay && !run.EffectiveNextDay {
		return fmt.Errorf("--sync-run %d is not a next-day roster sync", *opts.SyncRunID)
	}
	if !opts.EffectiveNextDay && opts.ScoringPeriodID == nil && run.EffectiveNextDay {
		return fmt.Errorf("--sync-run %d is next-day; pass --next-day to target that scoring period", *opts.SyncRunID)
	}
	if !opts.EffectiveNextDay && opts.ScoringPeriodID == nil && run.ScoringPeriodID != nil {
		return fmt.Errorf("--sync-run %d belongs to scoring period %d; pass --scoring-period-id %d to target that scoring period", *opts.SyncRunID, *run.ScoringPeriodID, *run.ScoringPeriodID)
	}
	return nil
}

func (s *Service) targetScoringPeriodDate(opts ContextOptions) *string {
	return opts.ScoringPeriodDate
}

func targetContextDetails(opts ContextOptions, targetDate *string) map[string]any {
	out := map[string]any{
		"effective_next_day":         opts.EffectiveNextDay,
		"target_scoring_period_date": targetDate,
	}
	if opts.SyncRunID != nil {
		out["sync_run_id"] = *opts.SyncRunID
	}
	if opts.ScoringPeriodID != nil {
		out["target_scoring_period_id"] = *opts.ScoringPeriodID
	}
	return out
}

func buildLineupItems(planItems []pitchplan.PlanItem, roster []espn.RosterSnapshot, slotCaps map[string]int) ([]PlanItem, map[string]any) {
	rosterByName := rosterIndex(roster)
	slotUsage := pitcherSlotUsage(roster)
	planByName := map[string]pitchplan.PlanItem{}
	for _, p := range planItems {
		k := matching.NormalizeName(p.PlayerName)
		if k == "" {
			continue
		}
		planByName[k] = p
	}
	usedBenchTargets := map[string]bool{}
	out := make([]PlanItem, 0)
	summary := map[string]int{
		"activate_actions":     0,
		"bench_actions":        0,
		"no_action_needed":     0,
		"ambiguous_or_blocked": 0,
	}

	for _, it := range planItems {
		nameKey := matching.NormalizeName(it.PlayerName)
		row := findRosterRowByName(rosterByName, it.PlayerName)
		if row == nil && it.ESPNPlayerID != nil {
			for _, r := range roster {
				if r.ESPNPlayerID != nil && *r.ESPNPlayerID == *it.ESPNPlayerID {
					row = &r
					break
				}
			}
		}
		if row == nil {
			out = append(out, PlanItem{ActionType: ActionAmbiguousOrBlocked, PlayerName: it.PlayerName, ESPNPlayerID: it.ESPNPlayerID, Rationale: map[string]any{"reason": "pitcher_not_found_in_live_roster"}, Flags: []string{"blocked"}, CreatedAt: time.Now().UTC()})
			summary["ambiguous_or_blocked"]++
			continue
		}
		currentSlot := strings.ToUpper(strings.TrimSpace(row.RosterSlot))
		active := isActiveSlot(currentSlot)

		switch it.Bucket {
		case pitchplan.BucketAutoStart, pitchplan.BucketLikelyStart:
			if active {
				summary["no_action_needed"]++
				continue
			}
			targetSlot, ok := firstOpenActiveSlot(slotUsage, slotCaps)
			if !ok {
				swap, found := chooseBenchSwapTarget(roster, planByName, usedBenchTargets, row.PlayerName)
				if found {
					swapSlot := strings.ToUpper(strings.TrimSpace(swap.RosterSlot))
					out = append(out, PlanItem{
						ActionType:   ActionBenchPitcher,
						PlayerName:   swap.PlayerName,
						ESPNPlayerID: swap.ESPNPlayerID,
						CurrentSlot:  swapSlot,
						TargetSlot:   "BE",
						Rationale: map[string]any{
							"reason": "slot_rebalance_for_activation",
						},
						Flags:     []string{"slot_rebalance"},
						CreatedAt: time.Now().UTC(),
					})
					usedBenchTargets[matching.NormalizeName(swap.PlayerName)] = true
					if slotUsage[swapSlot] > 0 {
						slotUsage[swapSlot]--
					}
					summary["bench_actions"]++
					targetSlot, ok = firstOpenActiveSlot(slotUsage, slotCaps)
				}
			}
			if !ok {
				out = append(out, PlanItem{
					ActionType:   ActionAmbiguousOrBlocked,
					PlayerName:   row.PlayerName,
					ESPNPlayerID: row.ESPNPlayerID,
					CurrentSlot:  currentSlot,
					TargetSlot:   "",
					Rationale: map[string]any{
						"player_key": nameKey,
						"reason":     "no_open_active_pitcher_slot",
					},
					Flags:     []string{"blocked", "target_slot_full"},
					CreatedAt: time.Now().UTC(),
				})
				summary["ambiguous_or_blocked"]++
				continue
			}
			out = append(out, PlanItem{
				ActionType:   ActionActivatePitcher,
				PlayerName:   row.PlayerName,
				ESPNPlayerID: row.ESPNPlayerID,
				CurrentSlot:  currentSlot,
				TargetSlot:   targetSlot,
				Rationale:    map[string]any{"player_key": nameKey},
				Flags:        []string{"plan_derived"},
				CreatedAt:    time.Now().UTC(),
			})
			slotUsage[targetSlot]++
			summary["activate_actions"]++
		case pitchplan.BucketBench, pitchplan.BucketNoStartScheduled:
			if !active {
				summary["no_action_needed"]++
				continue
			}
			out = append(out, PlanItem{
				ActionType:   ActionBenchPitcher,
				PlayerName:   row.PlayerName,
				ESPNPlayerID: row.ESPNPlayerID,
				CurrentSlot:  currentSlot,
				TargetSlot:   "BE",
				Rationale:    map[string]any{"player_key": nameKey},
				Flags:        []string{"plan_derived"},
				CreatedAt:    time.Now().UTC(),
			})
			summary["bench_actions"]++
		default:
			summary["no_action_needed"]++
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ActionType != out[j].ActionType {
			return out[i].ActionType < out[j].ActionType
		}
		return strings.ToLower(out[i].PlayerName) < strings.ToLower(out[j].PlayerName)
	})
	return out, map[string]any{"counts": summary}
}

func rosterIndex(rows []espn.RosterSnapshot) map[string]espn.RosterSnapshot {
	out := map[string]espn.RosterSnapshot{}
	for _, r := range rows {
		k := matching.NormalizeName(r.PlayerName)
		if k == "" {
			continue
		}
		if _, ok := out[k]; !ok {
			out[k] = r
		}
	}
	return out
}

func findRosterRowByName(index map[string]espn.RosterSnapshot, name string) *espn.RosterSnapshot {
	k := matching.NormalizeName(name)
	if k == "" {
		return nil
	}
	if v, ok := index[k]; ok {
		return &v
	}
	return nil
}

func isActiveSlot(slot string) bool {
	s := strings.ToUpper(strings.TrimSpace(slot))
	return s == "P" || s == "SP" || s == "RP"
}

func isTargetActive(action ActionType) bool { return action == ActionActivatePitcher }

func normalizeAdHocTargetSlot(raw string) (string, error) {
	s := strings.ToUpper(strings.TrimSpace(raw))
	switch s {
	case "P", "SP", "RP":
		return s, nil
	case "BE", "BENCH":
		return "BE", nil
	default:
		return "", fmt.Errorf("--to-slot must be one of: P, SP, RP, BE")
	}
}

func pitcherSlotCapacities(league *espn.LeagueSnapshot) map[string]int {
	out := map[string]int{
		"P":  0,
		"SP": 0,
		"RP": 0,
	}
	if league == nil || strings.TrimSpace(league.SettingsJSON) == "" {
		return out
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(league.SettingsJSON), &raw); err != nil {
		return out
	}
	rawCounts, ok := raw["lineupSlotCounts"].(map[string]any)
	if !ok {
		return out
	}
	parseCount := func(v any) int {
		switch t := v.(type) {
		case float64:
			return int(t)
		case int:
			return t
		case int64:
			return int(t)
		case string:
			n, _ := strconv.Atoi(strings.TrimSpace(t))
			return n
		default:
			return 0
		}
	}
	for k, v := range rawCounts {
		key := strings.ToUpper(strings.TrimSpace(k))
		n := parseCount(v)
		switch key {
		case "13", "P":
			out["P"] = n
		case "14", "SP":
			out["SP"] = n
		case "15", "RP":
			out["RP"] = n
		}
	}
	return out
}

func pitcherSlotUsage(rows []espn.RosterSnapshot) map[string]int {
	out := map[string]int{
		"P":  0,
		"SP": 0,
		"RP": 0,
	}
	for _, r := range rows {
		slot := strings.ToUpper(strings.TrimSpace(r.RosterSlot))
		if _, ok := out[slot]; ok {
			out[slot]++
		}
	}
	return out
}

func firstOpenActiveSlot(usage, caps map[string]int) (string, bool) {
	known := false
	for _, slot := range []string{"P", "SP", "RP"} {
		cap := caps[slot]
		if cap <= 0 {
			continue
		}
		known = true
		if usage[slot] < cap {
			return slot, true
		}
	}
	if !known {
		return "P", true
	}
	return "", false
}

func isTargetSlotFull(slot string, usage, caps map[string]int) bool {
	cap := caps[slot]
	if cap <= 0 {
		return false
	}
	return usage[slot] >= cap
}

func chooseBenchSwapTarget(roster []espn.RosterSnapshot, planByName map[string]pitchplan.PlanItem, used map[string]bool, addPlayerName string) (espn.RosterSnapshot, bool) {
	type candidate struct {
		row      espn.RosterSnapshot
		priority int
		starts   int
		total    float64
	}
	addKey := matching.NormalizeName(addPlayerName)
	cands := make([]candidate, 0)

	for _, r := range roster {
		if !isActiveSlot(r.RosterSlot) {
			continue
		}
		key := matching.NormalizeName(r.PlayerName)
		if key == "" || key == addKey || used[key] {
			continue
		}
		p, ok := planByName[key]
		if !ok {
			continue
		}
		priority, eligible := benchSwapPriority(p)
		if !eligible {
			continue
		}
		total := 0.0
		if p.TotalProjectedFPTS != nil {
			total = *p.TotalProjectedFPTS
		}
		cands = append(cands, candidate{
			row:      r,
			priority: priority,
			starts:   p.ProjectedStartCount,
			total:    total,
		})
	}
	if len(cands) == 0 {
		return espn.RosterSnapshot{}, false
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].priority != cands[j].priority {
			return cands[i].priority < cands[j].priority
		}
		if cands[i].starts != cands[j].starts {
			return cands[i].starts < cands[j].starts
		}
		if cands[i].total != cands[j].total {
			return cands[i].total < cands[j].total
		}
		return strings.ToLower(cands[i].row.PlayerName) < strings.ToLower(cands[j].row.PlayerName)
	})
	return cands[0].row, true
}

func benchSwapPriority(p pitchplan.PlanItem) (int, bool) {
	switch p.Bucket {
	case pitchplan.BucketNoStartScheduled:
		return 1, true
	case pitchplan.BucketBench:
		return 2, true
	case pitchplan.BucketMonitor:
		if hasFlag(p.Flags, "unmatched") {
			return 3, true
		}
		if p.ProjectedStartCount == 0 {
			return 4, true
		}
		return 5, true
	default:
		return 0, false
	}
}

func hasFlag(flags []string, want string) bool {
	for _, f := range flags {
		if strings.EqualFold(strings.TrimSpace(f), strings.TrimSpace(want)) {
			return true
		}
	}
	return false
}

func deriveExecutionStatus(v execute.VerificationStatus) execute.ExecutionStatus {
	switch v {
	case execute.VerificationStatusVerified:
		return execute.ExecutionStatusSucceeded
	case execute.VerificationStatusPending:
		return execute.ExecutionStatusSubmitted
	case execute.VerificationStatusUnverified:
		return execute.ExecutionStatusAmbiguous
	case execute.VerificationStatusVerificationFailed:
		return execute.ExecutionStatusAmbiguous
	default:
		return execute.ExecutionStatusAmbiguous
	}
}

func executionMessage(execStatus execute.ExecutionStatus, ver execute.VerificationStatus) string {
	switch execStatus {
	case execute.ExecutionStatusSucceeded:
		return "execution succeeded and verified"
	case execute.ExecutionStatusSubmitted:
		return "execution submitted; verification pending"
	case execute.ExecutionStatusAmbiguous:
		return fmt.Sprintf("execution ambiguous (%s)", ver)
	case execute.ExecutionStatusFailed:
		return "execution failed"
	case execute.ExecutionStatusAborted:
		return "execution aborted"
	default:
		return "execution attempted"
	}
}
