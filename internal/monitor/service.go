package monitor

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

type Service struct {
	repo *Repository
	cfg  Config
}

func NewService(repo *Repository, cfg Config) *Service { return &Service{repo: repo, cfg: cfg} }

func (s *Service) Summary(ctx context.Context) (*Run, error) {
	items := []Item{}
	appendItems := func(run *Run) {
		if run != nil {
			items = append(items, run.Items...)
		}
	}
	pr, _ := s.Plans(ctx, EvaluateOptions{Limit: 10, LatestOnly: false})
	appendItems(pr)
	lr, _ := s.Lineup(ctx, EvaluateOptions{Limit: 10, LatestOnly: false})
	appendItems(lr)
	pu, _ := s.Pickups(ctx, EvaluateOptions{Limit: 10, LatestOnly: false})
	appendItems(pu)
	ap, _ := s.Approvals(ctx, EvaluateOptions{Limit: 25})
	appendItems(ap)
	ah, _ := s.AdHoc(ctx, EvaluateOptions{Limit: 25})
	appendItems(ah)
	ex, _ := s.Execution(ctx, EvaluateOptions{Limit: 25})
	appendItems(ex)

	summary := s.buildSummary(items)
	runID, err := s.repo.SaveRun(ctx, "summary", map[string]any{"counts": summary.Counts}, items)
	if err != nil {
		return nil, err
	}
	run, err := s.repo.RunByID(ctx, runID)
	if err != nil {
		return nil, err
	}
	return run, nil
}

func (s *Service) Plans(ctx context.Context, opts EvaluateOptions) (*Run, error) {
	rows, err := s.repo.PitcherPlans(ctx, opts.Limit, opts.LatestOnly)
	if err != nil {
		return nil, err
	}
	latestSyncID, _, _ := s.repo.LatestSyncRun(ctx)
	latestImportID, _, _ := s.repo.LatestImportRun(ctx)
	now := time.Now().UTC()
	items := []Item{}
	for _, p := range rows {
		reasons := []Reason{}
		status := StatusFresh
		rec := ActionNoAction
		if ageHours(p.CreatedAt, now) > float64(s.cfg.PlansStaleHours) {
			status = StatusStale
			rec = ActionRegeneratePlan
			reasons = append(reasons, Reason{Code: "age_threshold", Message: fmt.Sprintf("plan older than %d hours", s.cfg.PlansStaleHours)})
		}
		if p.SyncRunID != nil && latestSyncID > 0 && *p.SyncRunID != latestSyncID {
			status = maxStatus(status, StatusStale)
			rec = ActionRegeneratePlan
			reasons = append(reasons, Reason{Code: "sync_superseded", Message: "source roster sync superseded by newer sync"})
		}
		if p.ImportRunID != nil && latestImportID > 0 && *p.ImportRunID != latestImportID {
			status = maxStatus(status, StatusStale)
			rec = ActionRegeneratePlan
			reasons = append(reasons, Reason{Code: "forecaster_superseded", Message: "source forecaster import superseded by newer import"})
		}
		players, _ := s.repo.PitcherPlanPlayers(ctx, p.ID)
		missing := 0
		for _, name := range players {
			ok, _, _ := s.repo.IsRosteredNow(ctx, name)
			if !ok {
				missing++
			}
		}
		if missing > 0 {
			status = maxStatus(status, StatusInvalidated)
			rec = ActionRegeneratePlan
			reasons = append(reasons, Reason{Code: "roster_drift", Message: fmt.Sprintf("%d planned pitchers no longer rostered", missing)})
		}
		if len(reasons) == 0 {
			reasons = append(reasons, Reason{Code: "ok", Message: "plan assumptions still appear valid"})
		}
		items = append(items, Item{ArtifactType: "plan", ArtifactID: p.ID, MonitorStatus: status, Reasons: reasons, RecommendedAction: rec, Details: map[string]any{"created_at": p.CreatedAt}})
	}
	return s.persist(ctx, "plans", items)
}

func (s *Service) Lineup(ctx context.Context, opts EvaluateOptions) (*Run, error) {
	rows, err := s.repo.LineupPlans(ctx, opts.Limit, opts.LatestOnly)
	if err != nil {
		return nil, err
	}
	latestSyncID, _, _ := s.repo.LatestSyncRun(ctx)
	now := time.Now().UTC()
	items := []Item{}
	for _, p := range rows {
		reasons := []Reason{}
		status := StatusFresh
		rec := ActionNoAction
		if ageHours(p.CreatedAt, now) > float64(s.cfg.LineupStaleHours) {
			status = StatusStale
			rec = ActionRegenerateLineup
			reasons = append(reasons, Reason{Code: "age_threshold", Message: fmt.Sprintf("lineup plan older than %d hours", s.cfg.LineupStaleHours)})
		}
		if p.SyncRunID != nil && latestSyncID > 0 && *p.SyncRunID != latestSyncID {
			status = maxStatus(status, StatusStale)
			rec = ActionRegenerateLineup
			reasons = append(reasons, Reason{Code: "sync_superseded", Message: "lineup source sync superseded by newer sync"})
		}
		litems, _ := s.repo.LineupPlanItems(ctx, p.ID)
		blocked := 0
		for _, li := range litems {
			ok, slot, _ := s.repo.IsRosteredNow(ctx, li.PlayerName)
			if !ok {
				blocked++
				continue
			}
			if strings.EqualFold(strings.TrimSpace(li.TargetSlot), strings.TrimSpace(slot)) {
				blocked++
			}
		}
		if blocked > 0 {
			status = maxStatus(status, StatusBlocked)
			rec = ActionRegenerateLineup
			reasons = append(reasons, Reason{Code: "lineup_state_changed", Message: fmt.Sprintf("%d lineup items already applied or blocked", blocked)})
		}
		if len(reasons) == 0 {
			reasons = append(reasons, Reason{Code: "ok", Message: "lineup plan appears actionable"})
		}
		items = append(items, Item{ArtifactType: "lineup_plan", ArtifactID: p.ID, MonitorStatus: status, Reasons: reasons, RecommendedAction: rec, Details: map[string]any{"created_at": p.CreatedAt}})
	}
	return s.persist(ctx, "lineup", items)
}

func (s *Service) Pickups(ctx context.Context, opts EvaluateOptions) (*Run, error) {
	rows, err := s.repo.PickupRuns(ctx, opts.Limit, opts.LatestOnly)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	items := []Item{}
	for _, r0 := range rows {
		reasons := []Reason{}
		status := StatusFresh
		rec := ActionNoAction
		if ageHours(r0.CreatedAt, now) > float64(s.cfg.PickupsStaleHours) {
			status = StatusStale
			rec = ActionRerunPickups
			reasons = append(reasons, Reason{Code: "age_threshold", Message: fmt.Sprintf("pickup run older than %d hours", s.cfg.PickupsStaleHours)})
		}
		if r0.CandidateRunID != nil {
			candAt, _ := s.repo.CandidateRunCreatedAt(ctx, *r0.CandidateRunID)
			if !candAt.IsZero() && ageHours(candAt, now) > float64(s.cfg.CandidatePoolStaleHours) {
				status = maxStatus(status, StatusStale)
				rec = ActionRefreshCandidatePool
				reasons = append(reasons, Reason{Code: "candidate_pool_stale", Message: fmt.Sprintf("candidate pool older than %d hours", s.cfg.CandidatePoolStaleHours)})
			}
		}
		players, _ := s.repo.PickupRunPlayers(ctx, r0.ID)
		missing := 0
		for _, p := range players {
			if p.ItemType != "top_candidate" && p.ItemType != "streamer" {
				continue
			}
			ok, _ := s.repo.IsCandidateNow(ctx, p.PlayerName)
			if !ok {
				missing++
			}
		}
		if missing > 0 {
			status = maxStatus(status, StatusInvalidated)
			rec = ActionRerunPickups
			reasons = append(reasons, Reason{Code: "candidate_unavailable", Message: fmt.Sprintf("%d recommended candidates missing from latest pool", missing)})
		}
		if len(reasons) == 0 {
			reasons = append(reasons, Reason{Code: "ok", Message: "pickup run still looks usable"})
		}
		items = append(items, Item{ArtifactType: "pickup", ArtifactID: r0.ID, MonitorStatus: status, Reasons: reasons, RecommendedAction: rec, Details: map[string]any{"created_at": r0.CreatedAt}})
	}
	return s.persist(ctx, "pickups", items)
}

func (s *Service) Approvals(ctx context.Context, opts EvaluateOptions) (*Run, error) {
	tr, err := s.repo.ApprovedTransactions(ctx, opts.Limit)
	if err != nil {
		return nil, err
	}
	lr, err := s.repo.ApprovedLineup(ctx, opts.Limit)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	items := []Item{}
	for _, a := range tr {
		status := StatusFresh
		rec := ActionNoAction
		reasons := []Reason{}
		if ageHours(a.UpdatedAt, now) > float64(s.cfg.ApprovalStaleHours) {
			status = StatusStale
			rec = ActionRePreflight
			reasons = append(reasons, Reason{Code: "approval_age", Message: fmt.Sprintf("approval older than %d hours", s.cfg.ApprovalStaleHours)})
		}
		done, _ := s.repo.HasSuccessfulExecutionForTransactionItem(ctx, a.ItemID)
		if done {
			status = maxStatus(status, StatusInvalidated)
			rec = ActionDiscardArtifact
			reasons = append(reasons, Reason{Code: "already_executed", Message: "approved transaction already executed successfully"})
		}
		addRostered, _, _ := s.repo.IsRosteredNow(ctx, a.AddPlayer)
		dropRostered, _, _ := s.repo.IsRosteredNow(ctx, a.DropPlayer)
		if addRostered {
			status = maxStatus(status, StatusBlocked)
			rec = ActionDiscardArtifact
			reasons = append(reasons, Reason{Code: "add_already_rostered", Message: "add target already rostered"})
		}
		if !dropRostered {
			status = maxStatus(status, StatusBlocked)
			rec = ActionDiscardArtifact
			reasons = append(reasons, Reason{Code: "drop_not_rostered", Message: "drop target no longer rostered"})
		}
		cand, _ := s.repo.IsCandidateNow(ctx, a.AddPlayer)
		if !cand {
			status = maxStatus(status, StatusBlocked)
			rec = ActionRePreflight
			reasons = append(reasons, Reason{Code: "add_not_available", Message: "add target not present in latest candidate pool"})
		}
		if len(reasons) == 0 {
			reasons = append(reasons, Reason{Code: "ok", Message: "approved transaction still appears actionable"})
		}
		items = append(items, Item{ArtifactType: "approval", ArtifactID: a.ItemID, MonitorStatus: status, Reasons: reasons, RecommendedAction: rec, Details: map[string]any{"plan_id": a.PlanID}})
	}
	for _, a := range lr {
		status := StatusFresh
		rec := ActionNoAction
		reasons := []Reason{}
		if ageHours(a.UpdatedAt, now) > float64(s.cfg.ApprovalStaleHours) {
			status = StatusStale
			rec = ActionRePreflight
			reasons = append(reasons, Reason{Code: "approval_age", Message: fmt.Sprintf("approval older than %d hours", s.cfg.ApprovalStaleHours)})
		}
		done, _ := s.repo.HasSuccessfulExecutionForLineupItem(ctx, a.ItemID)
		if done {
			status = maxStatus(status, StatusInvalidated)
			rec = ActionDiscardArtifact
			reasons = append(reasons, Reason{Code: "already_executed", Message: "approved lineup item already executed successfully"})
		}
		rostered, slot, _ := s.repo.IsRosteredNow(ctx, a.Player)
		if !rostered {
			status = maxStatus(status, StatusBlocked)
			rec = ActionDiscardArtifact
			reasons = append(reasons, Reason{Code: "player_not_rostered", Message: "lineup player no longer rostered"})
		} else if strings.EqualFold(slot, a.TargetSlot) {
			status = maxStatus(status, StatusBlocked)
			rec = ActionDiscardArtifact
			reasons = append(reasons, Reason{Code: "already_in_target_slot", Message: "player already in target slot"})
		}
		if len(reasons) == 0 {
			reasons = append(reasons, Reason{Code: "ok", Message: "approved lineup item still appears actionable"})
		}
		items = append(items, Item{ArtifactType: "lineup_approval", ArtifactID: a.ItemID, MonitorStatus: status, Reasons: reasons, RecommendedAction: rec, Details: map[string]any{"plan_id": a.PlanID}})
	}
	return s.persist(ctx, "approvals", items)
}

func (s *Service) AdHoc(ctx context.Context, opts EvaluateOptions) (*Run, error) {
	rows, err := s.repo.AdHocRequests(ctx, opts.Limit)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	items := []Item{}
	for _, a := range rows {
		status := StatusFresh
		rec := ActionNoAction
		reasons := []Reason{}
		if a.State == "unresolved" || a.Resolution == "unresolved" || a.Resolution == "ambiguous" {
			status = StatusInvalidated
			rec = ActionDiscardArtifact
			reasons = append(reasons, Reason{Code: "request_unresolved", Message: "ad hoc request is unresolved or ambiguous"})
		}
		if ageHours(a.UpdatedAt, now) > float64(s.cfg.ApprovalStaleHours) {
			status = maxStatus(status, StatusStale)
			if rec == ActionNoAction {
				rec = ActionRePreflight
			}
			reasons = append(reasons, Reason{Code: "request_age", Message: "ad hoc request is stale"})
		}
		addName := firstNonEmpty(a.ResolvedAdd, a.RequestedAdd)
		dropName := firstNonEmpty(a.ResolvedDrop, a.RequestedDrop)
		if addName != "" {
			cand, _ := s.repo.IsCandidateNow(ctx, addName)
			if !cand {
				status = maxStatus(status, StatusBlocked)
				rec = ActionDiscardArtifact
				reasons = append(reasons, Reason{Code: "add_not_available", Message: "add target not present in latest candidate pool"})
			}
		}
		if dropName != "" {
			rostered, _, _ := s.repo.IsRosteredNow(ctx, dropName)
			if !rostered {
				status = maxStatus(status, StatusBlocked)
				rec = ActionDiscardArtifact
				reasons = append(reasons, Reason{Code: "drop_not_rostered", Message: "drop target no longer rostered"})
			}
		}
		if len(reasons) == 0 {
			reasons = append(reasons, Reason{Code: "ok", Message: "ad hoc request still appears valid"})
		}
		items = append(items, Item{ArtifactType: "ad_hoc", ArtifactID: a.ID, MonitorStatus: status, Reasons: reasons, RecommendedAction: rec, Details: map[string]any{"state": a.State, "resolution": a.Resolution}})
	}
	return s.persist(ctx, "ad_hoc", items)
}

func (s *Service) Execution(ctx context.Context, opts EvaluateOptions) (*Run, error) {
	tr, err := s.repo.PendingExecutions(ctx, opts.Limit)
	if err != nil {
		return nil, err
	}
	lr, err := s.repo.PendingLineupExecutions(ctx, opts.Limit)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	items := []Item{}
	for _, e := range tr {
		status := StatusUnknown
		rec := ActionInspectExecution
		reasons := []Reason{}
		if e.ExecutionStatus == "ambiguous" || e.VerificationStatus == "verification_pending" {
			status = StatusStale
			rec = ActionReconcileExecution
			reasons = append(reasons, Reason{Code: "unresolved_attempt", Message: "execution attempt is unresolved"})
		}
		if ageHours(e.StartedAt, now) > float64(s.cfg.ExecutionFollowupHours) {
			status = maxStatus(status, StatusStale)
			rec = ActionReconcileExecution
			reasons = append(reasons, Reason{Code: "followup_due", Message: fmt.Sprintf("execution follow-up older than %d hours", s.cfg.ExecutionFollowupHours)})
		}
		if len(reasons) == 0 {
			reasons = append(reasons, Reason{Code: "needs_attention", Message: "execution status requires manual inspection"})
		}
		items = append(items, Item{ArtifactType: "execution", ArtifactID: e.ID, MonitorStatus: status, Reasons: reasons, RecommendedAction: rec, Details: map[string]any{"scope": "transaction"}})
	}
	for _, e := range lr {
		status := StatusUnknown
		rec := ActionInspectExecution
		reasons := []Reason{}
		if e.ExecutionStatus == "ambiguous" || e.VerificationStatus == "verification_pending" {
			status = StatusStale
			rec = ActionReconcileExecution
			reasons = append(reasons, Reason{Code: "unresolved_attempt", Message: "lineup execution attempt is unresolved"})
		}
		if ageHours(e.StartedAt, now) > float64(s.cfg.ExecutionFollowupHours) {
			status = maxStatus(status, StatusStale)
			rec = ActionReconcileExecution
			reasons = append(reasons, Reason{Code: "followup_due", Message: fmt.Sprintf("execution follow-up older than %d hours", s.cfg.ExecutionFollowupHours)})
		}
		if len(reasons) == 0 {
			reasons = append(reasons, Reason{Code: "needs_attention", Message: "lineup execution status requires manual inspection"})
		}
		items = append(items, Item{ArtifactType: "execution", ArtifactID: e.ID, MonitorStatus: status, Reasons: reasons, RecommendedAction: rec, Details: map[string]any{"scope": "lineup"}})
	}
	return s.persist(ctx, "execution", items)
}

func (s *Service) Show(ctx context.Context, typ string, id int64) (*Run, error) {
	if id <= 0 {
		return nil, fmt.Errorf("id must be > 0")
	}
	var run *Run
	var err error
	switch strings.ToLower(strings.TrimSpace(typ)) {
	case "plan":
		run, err = s.Plans(ctx, EvaluateOptions{Limit: 100})
	case "lineup_plan":
		run, err = s.Lineup(ctx, EvaluateOptions{Limit: 100})
	case "pickup":
		run, err = s.Pickups(ctx, EvaluateOptions{Limit: 100})
	case "approval", "lineup_approval":
		run, err = s.Approvals(ctx, EvaluateOptions{Limit: 200})
	case "ad_hoc":
		run, err = s.AdHoc(ctx, EvaluateOptions{Limit: 200})
	case "execution":
		run, err = s.Execution(ctx, EvaluateOptions{Limit: 200})
	default:
		return nil, fmt.Errorf("unsupported type %q", typ)
	}
	if err != nil {
		return nil, err
	}
	filtered := []Item{}
	for _, it := range run.Items {
		if it.ArtifactID == id && (strings.EqualFold(it.ArtifactType, typ) || typ == "approval" || typ == "lineup_approval") {
			filtered = append(filtered, it)
		}
	}
	run.Items = filtered
	run.ItemCount = len(filtered)
	return run, nil
}

func (s *Service) persist(ctx context.Context, runType string, items []Item) (*Run, error) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].ArtifactType != items[j].ArtifactType {
			return items[i].ArtifactType < items[j].ArtifactType
		}
		return items[i].ArtifactID < items[j].ArtifactID
	})
	summary := s.buildSummary(items)
	runID, err := s.repo.SaveRun(ctx, runType, map[string]any{"counts": summary.Counts}, items)
	if err != nil {
		return nil, err
	}
	return s.repo.RunByID(ctx, runID)
}

func (s *Service) buildSummary(items []Item) Summary {
	counts := map[string]map[Status]int{}
	for _, it := range items {
		if _, ok := counts[it.ArtifactType]; !ok {
			counts[it.ArtifactType] = map[Status]int{}
		}
		counts[it.ArtifactType][it.MonitorStatus]++
	}
	return Summary{Counts: counts, Items: items}
}

func ageHours(t, now time.Time) float64 {
	if t.IsZero() {
		return 0
	}
	return now.Sub(t).Hours()
}

func maxStatus(a, b Status) Status {
	order := map[Status]int{StatusFresh: 1, StatusStale: 2, StatusUnknown: 3, StatusBlocked: 4, StatusInvalidated: 5}
	if order[b] > order[a] {
		return b
	}
	return a
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
