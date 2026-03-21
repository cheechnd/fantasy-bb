package planner

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"fantasy-baseball/internal/espn"
	"fantasy-baseball/internal/pitchers"
	"fantasy-baseball/internal/pitchers/matching"
)

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

type GenerateInput struct {
	SyncRunID       *int64
	ImportRunID     *int64
	AnalysisRunID   *int64
	WindowStart     string
	WindowEnd       string
	Rules           RuleConfig
	Analysis        pitchers.AnalysisReport
	RosterSnapshots []espn.RosterSnapshot
}

func (s *Service) GenerateAndSave(ctx context.Context, in GenerateInput) (*Plan, error) {
	items, summary := BuildPlanItems(in.Analysis, in.RosterSnapshots, in.Rules)
	planID, err := s.repo.SavePlan(ctx, CreateInput{
		SyncRunID:     in.SyncRunID,
		ImportRunID:   in.ImportRunID,
		AnalysisRunID: in.AnalysisRunID,
		WindowStart:   in.WindowStart,
		WindowEnd:     in.WindowEnd,
		Status:        "success",
		Summary: map[string]interface{}{
			"counts": summary,
		},
		Items: items,
	})
	if err != nil {
		return nil, err
	}
	plan, rows, err := s.repo.PlanByID(ctx, planID)
	if err != nil {
		return nil, err
	}
	if plan == nil {
		return nil, fmt.Errorf("saved plan %d was not found", planID)
	}
	plan.Items = rows
	if plan.Summary.Counts == nil {
		plan.Summary.Counts = summary
	}
	return plan, nil
}

func (s *Service) Latest(ctx context.Context) (*Plan, error) {
	plan, rows, err := s.repo.LatestPlan(ctx)
	if err != nil {
		return nil, err
	}
	if plan == nil {
		return nil, nil
	}
	plan.Items = rows
	if plan.Summary.Counts == nil {
		plan.Summary.Counts = map[Bucket]int{}
		for _, item := range rows {
			plan.Summary.Counts[item.Bucket]++
		}
	}
	return plan, nil
}

func (s *Service) ByID(ctx context.Context, planID int64) (*Plan, error) {
	plan, rows, err := s.repo.PlanByID(ctx, planID)
	if err != nil {
		return nil, err
	}
	if plan == nil {
		return nil, nil
	}
	plan.Items = rows
	if plan.Summary.Counts == nil {
		plan.Summary.Counts = map[Bucket]int{}
		for _, item := range rows {
			plan.Summary.Counts[item.Bucket]++
		}
	}
	return plan, nil
}

func BuildPlanItems(report pitchers.AnalysisReport, roster []espn.RosterSnapshot, rules RuleConfig) ([]PlanItem, map[Bucket]int) {
	rosterByName := map[string]espn.RosterSnapshot{}
	for _, r := range roster {
		key := matching.NormalizeName(r.PlayerName)
		if key == "" {
			continue
		}
		if _, exists := rosterByName[key]; !exists {
			rosterByName[key] = r
		}
	}

	items := make([]PlanItem, 0, len(report.RankedPitchers)+len(report.UnmatchedPlayers)+len(report.AmbiguousPlayers))
	for _, p := range report.RankedPitchers {
		flags := uniqueFlags(p.Flags)
		notes := []string{}
		if p.StartCount == 0 {
			notes = append(notes, "no projected starts in selected window")
		}
		score := applyScore(p, rules)
		uncertain := hasAny(flags, "tbd", "missing_projection", "ambiguous_match")
		bucket := decideBucket(p, flags, score, uncertain, rules)
		if uncertain && p.StartCount > 0 {
			notes = append(notes, "uncertainty present; monitor before locking lineup")
		}

		row := PlanItem{
			Bucket:              bucket,
			PlayerName:          p.PlayerName,
			MLBTeam:             p.MLBTeam,
			MatchedPitcherName:  p.MatchedPitcherName,
			ProjectedStartCount: p.StartCount,
			TotalProjectedFPTS:  floatPtr(p.TotalProjectedFPTS),
			Flags:               flags,
			Notes:               notes,
			Details: map[string]interface{}{
				"score":                     score,
				"average_projected_fpts":    p.AverageProjectedFPTS,
				"highest_single_start_fpts": p.HighestSingleFPTS,
				"starts":                    p.Starts,
			},
			CreatedAt: time.Now().UTC(),
		}
		if len(row.Notes) > 0 && !hasAny(row.Flags, "has_notes") {
			row.Flags = append(row.Flags, "has_notes")
		}
		if snap, ok := rosterByName[matching.NormalizeName(p.PlayerName)]; ok {
			if row.MLBTeam == "" {
				row.MLBTeam = snap.MLBTeam
			}
			row.ESPNPlayerID = snap.ESPNPlayerID
		}
		items = append(items, row)
	}

	for _, u := range report.UnmatchedPlayers {
		row := PlanItem{
			Bucket:              BucketNoStartScheduled,
			PlayerName:          u.InputPlayerName,
			MLBTeam:             u.InputMLBTeam,
			ProjectedStartCount: 0,
			Flags:               []string{"unmatched"},
			Notes:               []string{firstNonEmpty(u.Explanation, "no matching probable start data")},
			Details: map[string]interface{}{
				"match_status": u.MatchStatus,
			},
			CreatedAt: time.Now().UTC(),
		}
		if len(row.Notes) > 0 && !hasAny(row.Flags, "has_notes") {
			row.Flags = append(row.Flags, "has_notes")
		}
		if snap, ok := rosterByName[matching.NormalizeName(u.InputPlayerName)]; ok {
			if row.MLBTeam == "" {
				row.MLBTeam = snap.MLBTeam
			}
			row.ESPNPlayerID = snap.ESPNPlayerID
		}
		items = append(items, row)
	}

	for _, a := range report.AmbiguousPlayers {
		row := PlanItem{
			Bucket:              BucketMonitor,
			PlayerName:          a.InputPlayerName,
			MLBTeam:             a.InputMLBTeam,
			ProjectedStartCount: 0,
			Flags:               []string{"ambiguous_match"},
			Notes:               []string{firstNonEmpty(a.Explanation, "multiple matching probable starts")},
			Details: map[string]interface{}{
				"match_status": a.MatchStatus,
				"candidates":   a.CandidateDisplayList,
			},
			CreatedAt: time.Now().UTC(),
		}
		if len(row.Notes) > 0 && !hasAny(row.Flags, "has_notes") {
			row.Flags = append(row.Flags, "has_notes")
		}
		if snap, ok := rosterByName[matching.NormalizeName(a.InputPlayerName)]; ok {
			if row.MLBTeam == "" {
				row.MLBTeam = snap.MLBTeam
			}
			row.ESPNPlayerID = snap.ESPNPlayerID
		}
		items = append(items, row)
	}

	rankPlanItems(items)
	summary := map[Bucket]int{}
	for _, row := range items {
		summary[row.Bucket]++
	}
	return items, summary
}

func applyScore(p pitchers.PitcherProjection, rules RuleConfig) float64 {
	score := p.HighestSingleFPTS
	if hasAny(p.Flags, "tbd") {
		score -= rules.TBDPenalty
	}
	if hasAny(p.Flags, "missing_projection") {
		score -= rules.MissingProjectionPenalty
	}
	if hasAny(p.Flags, "ambiguous_match") {
		score -= rules.AmbiguousMatchPenalty
	}
	return score
}

func decideBucket(p pitchers.PitcherProjection, flags []string, score float64, uncertain bool, rules RuleConfig) Bucket {
	if p.StartCount == 0 {
		return BucketNoStartScheduled
	}
	if uncertain {
		return BucketMonitor
	}
	if score >= rules.AutoStartMinTotalFPTS {
		return BucketAutoStart
	}
	if score >= rules.LikelyStartMinTotalFPTS {
		return BucketLikelyStart
	}
	if score >= rules.MonitorMinTotalFPTS {
		return BucketMonitor
	}
	if hasAny(flags, "locked") {
		return BucketMonitor
	}
	return BucketBench
}

func rankPlanItems(items []PlanItem) {
	bucketOrder := map[Bucket]int{
		BucketAutoStart:        1,
		BucketLikelyStart:      2,
		BucketMonitor:          3,
		BucketBench:            4,
		BucketNoStartScheduled: 5,
	}
	sort.SliceStable(items, func(i, j int) bool {
		if bucketOrder[items[i].Bucket] != bucketOrder[items[j].Bucket] {
			return bucketOrder[items[i].Bucket] < bucketOrder[items[j].Bucket]
		}
		leftTotal := -9999.0
		if items[i].TotalProjectedFPTS != nil {
			leftTotal = *items[i].TotalProjectedFPTS
		}
		rightTotal := -9999.0
		if items[j].TotalProjectedFPTS != nil {
			rightTotal = *items[j].TotalProjectedFPTS
		}
		if leftTotal != rightTotal {
			return leftTotal > rightTotal
		}
		if items[i].ProjectedStartCount != items[j].ProjectedStartCount {
			return items[i].ProjectedStartCount > items[j].ProjectedStartCount
		}
		return strings.ToLower(items[i].PlayerName) < strings.ToLower(items[j].PlayerName)
	})
	counters := map[Bucket]int{}
	for i := range items {
		counters[items[i].Bucket]++
		r := counters[items[i].Bucket]
		items[i].ResultRank = &r
	}
}

func uniqueFlags(flags []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(flags))
	for _, f := range flags {
		n := strings.ToLower(strings.TrimSpace(f))
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

func hasAny(flags []string, targets ...string) bool {
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

func floatPtr(v float64) *float64 { return &v }

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
