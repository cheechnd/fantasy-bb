package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	esrepo "fantasy-baseball/internal/espn/repository"
	"fantasy-baseball/internal/forecaster"
	"fantasy-baseball/internal/pickups"
	pickrepo "fantasy-baseball/internal/pickups/repository"
	pitchplan "fantasy-baseball/internal/pitchers/planner"
	"fantasy-baseball/internal/transactions"
	tranrepo "fantasy-baseball/internal/transactions/repository"
)

type Service struct {
	foreRepo *forecaster.Repository
	espnRepo *esrepo.Repository
	planRepo *pitchplan.Repository
	pickRepo *pickrepo.Repository
	tranRepo *tranrepo.Repository
	cfg      transactions.ServiceConfig
}

func New(
	foreRepo *forecaster.Repository,
	espnRepo *esrepo.Repository,
	planRepo *pitchplan.Repository,
	pickRepo *pickrepo.Repository,
	tranRepo *tranrepo.Repository,
	cfg transactions.ServiceConfig,
) *Service {
	return &Service{
		foreRepo: foreRepo,
		espnRepo: espnRepo,
		planRepo: planRepo,
		pickRepo: pickRepo,
		tranRepo: tranRepo,
		cfg:      cfg,
	}
}

func (s *Service) GenerateAndSave(ctx context.Context, opts transactions.Options) (*transactions.Plan, error) {
	resolved, err := s.resolveSources(ctx, opts)
	if err != nil {
		return nil, err
	}

	items := s.buildPlanItems(resolved.plan, resolved.pickupRun, resolved.pickupItems, resolved.windowStart, resolved.windowEnd)
	topLimit := opts.TopN
	if topLimit <= 0 {
		topLimit = s.cfg.TopMoveLimit
	}
	if topLimit <= 0 {
		topLimit = 10
	}
	if len(items) > topLimit {
		items = items[:topLimit]
	}

	summary := map[string]interface{}{
		"counts": bucketCounts(items),
		"sources": map[string]interface{}{
			"sync_run_id":                  resolved.syncRunID,
			"import_run_id":                resolved.importRunID,
			"pitcher_plan_id":              resolved.plan.ID,
			"pickup_recommendation_run_id": resolved.pickupRun.ID,
		},
	}
	if resolved.note != "" {
		summary["note"] = resolved.note
	}

	planID, err := s.tranRepo.SavePlan(ctx, transactions.CreatePlanInput{
		SyncRunID:                 resolved.syncRunID,
		ImportRunID:               resolved.importRunID,
		PitcherPlanID:             &resolved.plan.ID,
		PickupRecommendationRunID: &resolved.pickupRun.ID,
		WindowStart:               resolved.windowStart,
		WindowEnd:                 resolved.windowEnd,
		Status:                    "success",
		Summary:                   summary,
		Items:                     items,
	})
	if err != nil {
		return nil, err
	}
	return s.ByID(ctx, planID)
}

func (s *Service) Latest(ctx context.Context) (*transactions.Plan, error) {
	plan, items, err := s.tranRepo.LatestPlan(ctx)
	if err != nil {
		return nil, err
	}
	if plan == nil {
		return nil, nil
	}
	plan.Items = items
	if plan.Summary.Counts == nil {
		plan.Summary.Counts = bucketCounts(items)
	}
	return plan, nil
}

func (s *Service) ByID(ctx context.Context, planID int64) (*transactions.Plan, error) {
	plan, items, err := s.tranRepo.PlanByID(ctx, planID)
	if err != nil {
		return nil, err
	}
	if plan == nil {
		return nil, nil
	}
	plan.Items = items
	if plan.Summary.Counts == nil {
		plan.Summary.Counts = bucketCounts(items)
	}
	return plan, nil
}

type resolvedSources struct {
	syncRunID   *int64
	importRunID *int64
	plan        *pitchplan.Plan
	pickupRun   *pickups.RecommendationRun
	pickupItems []pickups.RecommendationItem
	windowStart string
	windowEnd   string
	note        string
}

func (s *Service) resolveSources(ctx context.Context, opts transactions.Options) (resolvedSources, error) {
	out := resolvedSources{
		windowStart: opts.From.Format("2006-01-02"),
		windowEnd:   opts.To.Format("2006-01-02"),
	}

	var plan *pitchplan.Plan
	if opts.PitcherPlanID != nil {
		p, items, err := s.planRepo.PlanByID(ctx, *opts.PitcherPlanID)
		if err != nil {
			return out, err
		}
		if p != nil {
			p.Items = items
		}
		plan = p
	} else {
		p, items, err := s.planRepo.LatestPlan(ctx)
		if err != nil {
			return out, err
		}
		if p != nil {
			p.Items = items
		}
		plan = p
	}
	if plan == nil {
		return out, fmt.Errorf("no pitcher plan found; run `fb pitchers plan` first")
	}
	out.plan = plan

	var pickupRun *pickups.RecommendationRun
	var pickupItems []pickups.RecommendationItem
	if opts.PickupRunID != nil {
		r, rows, err := s.pickRepo.RecommendationByID(ctx, *opts.PickupRunID)
		if err != nil {
			return out, err
		}
		pickupRun = r
		pickupItems = rows
	} else {
		r, rows, err := s.pickRepo.LatestRecommendation(ctx)
		if err != nil {
			return out, err
		}
		pickupRun = r
		pickupItems = rows
	}
	if pickupRun == nil {
		return out, fmt.Errorf("no pickup recommendations found; run `fb pickups recommend` first")
	}
	if len(pickupItems) == 0 {
		return out, fmt.Errorf("pickup recommendation run %d has no items", pickupRun.ID)
	}
	out.pickupRun = pickupRun
	out.pickupItems = pickupItems

	if opts.SyncRunID != nil {
		out.syncRunID = opts.SyncRunID
	} else if plan.SyncRunID != nil {
		out.syncRunID = plan.SyncRunID
	} else if pickupRun.SyncRunID != nil {
		out.syncRunID = pickupRun.SyncRunID
	} else {
		latestSync, err := s.espnRepo.LatestSyncRun(ctx)
		if err != nil {
			return out, err
		}
		if latestSync != nil {
			v := latestSync.ID
			out.syncRunID = &v
		}
	}

	if opts.ImportRunID != nil {
		out.importRunID = opts.ImportRunID
	} else if plan.ImportRunID != nil {
		out.importRunID = plan.ImportRunID
	} else if pickupRun.ImportRunID != nil {
		out.importRunID = pickupRun.ImportRunID
	} else {
		latestImport, err := s.foreRepo.LatestImportRun(ctx)
		if err != nil {
			return out, err
		}
		if latestImport != nil {
			v := latestImport.ID
			out.importRunID = &v
		}
	}

	if plan.WindowStart != out.windowStart || plan.WindowEnd != out.windowEnd {
		out.note = fmt.Sprintf("using pitcher plan window %s to %s with requested window %s to %s", plan.WindowStart, plan.WindowEnd, out.windowStart, out.windowEnd)
	}
	return out, nil
}

type pickupCandidate struct {
	Item        pickups.RecommendationItem
	Total       float64
	Uncertainty float64
	Uncertain   bool
	NormName    string
}

type dropCandidate struct {
	Item      pitchplan.PlanItem
	Total     float64
	NormName  string
	DropScore float64
}

func (s *Service) buildPlanItems(plan *pitchplan.Plan, pickupRun *pickups.RecommendationRun, pickupItems []pickups.RecommendationItem, windowStart string, windowEnd string) []transactions.PlanItem {
	drops := s.selectDropCandidates(plan.Items)
	adds := s.selectPickupCandidates(plan.Items, pickupItems)
	if len(drops) == 0 || len(adds) == 0 {
		return []transactions.PlanItem{}
	}

	maxPairings := s.cfg.MaxPairings
	if maxPairings <= 0 {
		maxPairings = 25
	}
	perAdd := 2
	seen := map[string]struct{}{}
	out := make([]transactions.PlanItem, 0, min(maxPairings, len(adds)*perAdd))

	for _, add := range adds {
		pairs := 0
		for _, drop := range drops {
			if pairs >= perAdd || len(out) >= maxPairings {
				break
			}
			if add.NormName == drop.NormName {
				continue
			}
			key := add.NormName + "|" + drop.NormName
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			pairs++

			item := buildMoveItem(add, drop, s.cfg, pickupRun.ID, plan.ID, windowStart, windowEnd)
			out = append(out, item)
		}
		if len(out) >= maxPairings {
			break
		}
	}
	rankMoves(out)
	return out
}

func (s *Service) selectDropCandidates(rows []pitchplan.PlanItem) []dropCandidate {
	out := []dropCandidate{}
	for _, row := range rows {
		if !isDropBucket(row.Bucket, s.cfg.AllowCompareAgainstLikelyStart) {
			continue
		}
		if hasAny(row.Flags, "locked", "must_hold", "protected", "no_drop") {
			continue
		}
		total := 0.0
		if row.TotalProjectedFPTS != nil {
			total = *row.TotalProjectedFPTS
		}
		score := dropPriorityScore(row, total)
		out = append(out, dropCandidate{
			Item:      row,
			Total:     total,
			NormName:  normalize(row.PlayerName),
			DropScore: score,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].DropScore != out[j].DropScore {
			return out[i].DropScore > out[j].DropScore
		}
		if out[i].Total != out[j].Total {
			return out[i].Total < out[j].Total
		}
		return strings.ToLower(out[i].Item.PlayerName) < strings.ToLower(out[j].Item.PlayerName)
	})
	return out
}

func (s *Service) selectPickupCandidates(roster []pitchplan.PlanItem, rows []pickups.RecommendationItem) []pickupCandidate {
	rosterSet := map[string]struct{}{}
	for _, r := range roster {
		rosterSet[normalize(r.PlayerName)] = struct{}{}
	}
	byName := map[string]pickupCandidate{}
	for _, row := range rows {
		if row.ItemType == pickups.ItemTypeUnmatched {
			continue
		}
		n := normalize(row.PlayerName)
		if _, exists := rosterSet[n]; exists {
			continue
		}
		total := 0.0
		if row.TotalProjectedFPTS != nil {
			total = *row.TotalProjectedFPTS
		}
		penalty := uncertaintyPenalty(row.Flags, s.cfg)
		cand := pickupCandidate{
			Item:        row,
			Total:       total,
			Uncertainty: penalty,
			Uncertain:   penalty > 0,
			NormName:    n,
		}
		prev, ok := byName[n]
		if !ok || cand.Total > prev.Total {
			byName[n] = cand
		}
	}
	out := make([]pickupCandidate, 0, len(byName))
	for _, c := range byName {
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Total != out[j].Total {
			return out[i].Total > out[j].Total
		}
		return strings.ToLower(out[i].Item.PlayerName) < strings.ToLower(out[j].Item.PlayerName)
	})
	return out
}

func buildMoveItem(add pickupCandidate, drop dropCandidate, cfg transactions.ServiceConfig, pickupRunID, planID int64, windowStart, windowEnd string) transactions.PlanItem {
	delta := add.Total - drop.Total
	adjusted := delta - add.Uncertainty
	bucket := classifyMoveBucket(delta, adjusted, add, cfg)

	flags := []string{}
	flags = append(flags, add.Item.Flags...)
	for _, f := range drop.Item.Flags {
		flags = append(flags, "drop_"+f)
	}
	flags = unique(flags)

	notes := []string{
		fmt.Sprintf("delta %.1f FPTS (add %.1f - drop %.1f)", delta, add.Total, drop.Total),
	}
	if add.Uncertainty > 0 {
		notes = append(notes, fmt.Sprintf("uncertainty penalty applied: -%.1f", add.Uncertainty))
	}
	switch bucket {
	case transactions.BucketStrongMove:
		notes = append(notes, "strong projected weekly upgrade")
	case transactions.BucketMarginalMove:
		notes = append(notes, "positive but modest weekly upgrade")
	case transactions.BucketRiskyMove:
		notes = append(notes, "positive move with uncertainty")
	case transactions.BucketWatchOnly:
		notes = append(notes, "watch only; limited confidence or delta")
	}

	item := transactions.PlanItem{
		Bucket:                  bucket,
		AddPlayerName:           add.Item.PlayerName,
		AddPlayerTeam:           add.Item.MLBTeam,
		AddESPNPlayerID:         add.Item.ESPNPlayerID,
		DropPlayerName:          drop.Item.PlayerName,
		DropPlayerTeam:          drop.Item.MLBTeam,
		DropESPNPlayerID:        drop.Item.ESPNPlayerID,
		AddProjectedStartCount:  add.Item.ProjectedStartCount,
		AddTotalProjectedFPTS:   floatPtr(add.Total),
		DropProjectedStartCount: drop.Item.ProjectedStartCount,
		DropTotalProjectedFPTS:  floatPtr(drop.Total),
		DeltaFPTS:               floatPtr(delta),
		Flags:                   flags,
		Notes:                   notes,
		Details: map[string]interface{}{
			"adjusted_delta_fpts":          adjusted,
			"pickup_item_type":             add.Item.ItemType,
			"add_flags":                    add.Item.Flags,
			"drop_flags":                   drop.Item.Flags,
			"pickup_recommendation_run_id": pickupRunID,
			"pitcher_plan_id":              planID,
			"window_start":                 windowStart,
			"window_end":                   windowEnd,
		},
		CreatedAt: time.Now().UTC(),
	}
	return item
}

func classifyMoveBucket(delta, adjusted float64, add pickupCandidate, cfg transactions.ServiceConfig) transactions.Bucket {
	if adjusted >= cfg.StrongMoveDeltaFPTS && !add.Uncertain {
		return transactions.BucketStrongMove
	}
	if adjusted >= cfg.MarginalMoveDeltaFPTS && !add.Uncertain {
		return transactions.BucketMarginalMove
	}
	if delta >= cfg.RiskyMoveMinDeltaFPTS {
		return transactions.BucketRiskyMove
	}
	return transactions.BucketWatchOnly
}

func rankMoves(items []transactions.PlanItem) {
	sort.SliceStable(items, func(i, j int) bool {
		leftOrder := bucketOrder(items[i].Bucket)
		rightOrder := bucketOrder(items[j].Bucket)
		if leftOrder != rightOrder {
			return leftOrder < rightOrder
		}
		leftDelta := -9999.0
		if items[i].DeltaFPTS != nil {
			leftDelta = *items[i].DeltaFPTS
		}
		rightDelta := -9999.0
		if items[j].DeltaFPTS != nil {
			rightDelta = *items[j].DeltaFPTS
		}
		if leftDelta != rightDelta {
			return leftDelta > rightDelta
		}
		if items[i].AddPlayerName != items[j].AddPlayerName {
			return strings.ToLower(items[i].AddPlayerName) < strings.ToLower(items[j].AddPlayerName)
		}
		return strings.ToLower(items[i].DropPlayerName) < strings.ToLower(items[j].DropPlayerName)
	})
	counters := map[transactions.Bucket]int{}
	for i := range items {
		counters[items[i].Bucket]++
		r := counters[items[i].Bucket]
		items[i].ResultRank = &r
	}
}

func bucketOrder(bucket transactions.Bucket) int {
	switch bucket {
	case transactions.BucketStrongMove:
		return 1
	case transactions.BucketMarginalMove:
		return 2
	case transactions.BucketRiskyMove:
		return 3
	default:
		return 4
	}
}

func isDropBucket(bucket pitchplan.Bucket, allowLikely bool) bool {
	switch bucket {
	case pitchplan.BucketNoStartScheduled, pitchplan.BucketBench, pitchplan.BucketMonitor:
		return true
	case pitchplan.BucketLikelyStart:
		return allowLikely
	default:
		return false
	}
}

func dropPriorityScore(row pitchplan.PlanItem, total float64) float64 {
	score := 0.0
	switch row.Bucket {
	case pitchplan.BucketNoStartScheduled:
		score += 100
	case pitchplan.BucketBench:
		score += 80
	case pitchplan.BucketMonitor:
		score += 60
	case pitchplan.BucketLikelyStart:
		score += 40
	}
	if row.ProjectedStartCount == 0 {
		score += 15
	}
	score += max(0, 20-total)
	if hasAny(row.Flags, "unmatched", "tbd", "missing_projection", "ambiguous_match") {
		score += 8
	}
	return score
}

func uncertaintyPenalty(flags []string, cfg transactions.ServiceConfig) float64 {
	total := 0.0
	if hasAny(flags, "tbd") {
		total += cfg.UncertaintyPenaltyTBD
	}
	if hasAny(flags, "missing_projection") {
		total += cfg.UncertaintyPenaltyMissingProj
	}
	if hasAny(flags, "ambiguous_match", "unmatched") {
		total += cfg.UncertaintyPenaltyAmbiguous
	}
	if hasAny(flags, "status_blocked", "status_risk") {
		total += cfg.UncertaintyPenaltyTBD
	}
	return total
}

func bucketCounts(items []transactions.PlanItem) map[transactions.Bucket]int {
	out := map[transactions.Bucket]int{}
	for _, item := range items {
		out[item.Bucket]++
	}
	return out
}

func normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	replacer := strings.NewReplacer(".", "", ",", "", "'", "", "-", " ")
	s = replacer.Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	return s
}

func hasAny(flags []string, targets ...string) bool {
	if len(flags) == 0 {
		return false
	}
	set := map[string]struct{}{}
	for _, f := range flags {
		set[strings.ToLower(strings.TrimSpace(f))] = struct{}{}
	}
	for _, t := range targets {
		if _, ok := set[strings.ToLower(strings.TrimSpace(t))]; ok {
			return true
		}
	}
	return false
}

func unique(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		n := strings.ToLower(strings.TrimSpace(v))
		if n == "" {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func floatPtr(v float64) *float64 { return &v }

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
