package pitchers

import (
	"context"
	"fmt"
	"sort"
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
	ApprovedItemID int64
	PlanID         int64
	PlayerName     string
	ESPNPlayerID   int64
	FromSlot       string
	ToSlot         string
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

	items, summary := buildLineupItems(pItems, rows)
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

	latestSync, err := s.espnRepo.LatestSyncRun(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.espnRepo.LatestRoster(ctx, nil, true)
	if err != nil {
		return nil, err
	}
	rosterByName := rosterIndex(rows)

	out := make([]PreflightItem, 0, len(candidates))
	now := time.Now().UTC()
	for _, q := range candidates {
		reasons := []execute.Reason{}
		status := execute.StatusExecutable
		row := findRosterRowByName(rosterByName, q.PlayerName)
		if row == nil {
			status = execute.StatusConflict
			reasons = append(reasons, execute.Reason{Code: "player_not_on_roster", Message: "pitcher is no longer on roster"})
		} else {
			current := strings.ToUpper(strings.TrimSpace(row.RosterSlot))
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
			LineupPlanItemID: q.LineupPlanItemID,
			PlanID:           q.PlanID,
			PlayerName:       q.PlayerName,
			ActionType:       q.ActionType,
			ValidationStatus: status,
			Reasons:          reasons,
			CurrentSlot:      q.CurrentSlot,
			TargetSlot:       q.TargetSlot,
			CheckedAt:        now,
		})
	}
	return &PreflightResult{Items: out}, nil
}

func (s *Service) Execute(ctx context.Context, cfg config.Config, itemID int64, confirm bool) (*ExecutionAttempt, *PreflightItem, bool, string, error) {
	if itemID <= 0 {
		return nil, nil, false, "", fmt.Errorf("--item must be > 0")
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

	pf, err := s.Preflight(ctx, &itemID, 1)
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
		ApprovedItemID: itemID,
		PlanID:         it.LineupPlanID,
		PlayerName:     it.PlayerName,
		ESPNPlayerID:   *it.ESPNPlayerID,
		FromSlot:       it.CurrentSlot,
		ToSlot:         it.TargetSlot,
	}
	attemptID, err := s.repo.CreateExecutionAttempt(ctx, itemID, it.LineupPlanID, execute.ExecutionStatusStarted, execute.VerificationStatusUnknown, map[string]any{
		"action_type":    it.ActionType,
		"player":         req.PlayerName,
		"espn_player_id": req.ESPNPlayerID,
		"from_slot":      req.FromSlot,
		"to_slot":        req.ToSlot,
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

func buildLineupItems(planItems []pitchplan.PlanItem, roster []espn.RosterSnapshot) ([]PlanItem, map[string]any) {
	rosterByName := rosterIndex(roster)
	out := make([]PlanItem, 0)
	summary := map[string]int{
		"recommended_starts":   0,
		"recommended_benches":  0,
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
			out = append(out, PlanItem{
				ActionType:   ActionActivatePitcher,
				PlayerName:   row.PlayerName,
				ESPNPlayerID: row.ESPNPlayerID,
				CurrentSlot:  currentSlot,
				TargetSlot:   "P",
				Rationale:    map[string]any{"pitcher_bucket": it.Bucket, "player_key": nameKey},
				Flags:        []string{"recommended_start"},
				CreatedAt:    time.Now().UTC(),
			})
			summary["recommended_starts"]++
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
				Rationale:    map[string]any{"pitcher_bucket": it.Bucket, "player_key": nameKey},
				Flags:        []string{"recommended_bench"},
				CreatedAt:    time.Now().UTC(),
			})
			summary["recommended_benches"]++
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
