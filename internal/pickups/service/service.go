package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	espnrepo "fantasy-baseball/internal/espn/repository"
	"fantasy-baseball/internal/forecaster"
	"fantasy-baseball/internal/pickups"
	pickrepo "fantasy-baseball/internal/pickups/repository"
	pitchers "fantasy-baseball/internal/pitchers"
	"fantasy-baseball/internal/pitchers/matching"
	pitchsvc "fantasy-baseball/internal/pitchers/service"
)

type Config struct {
	MinStreamerTotalFPTS     float64
	StrongUpgradeDeltaFPTS   float64
	MarginalUpgradeDeltaFPTS float64
	RiskyMonitorMinTotalFPTS float64
}

type Service struct {
	foreRepo *forecaster.Repository
	espnRepo *espnrepo.Repository
	pickRepo *pickrepo.Repository
	pitchSvc *pitchsvc.Service
	cfg      Config
}

func New(foreRepo *forecaster.Repository, espnRepo *espnrepo.Repository, pickRepo *pickrepo.Repository, pitchSvc *pitchsvc.Service, cfg Config) *Service {
	return &Service{foreRepo: foreRepo, espnRepo: espnRepo, pickRepo: pickRepo, pitchSvc: pitchSvc, cfg: cfg}
}

func (s *Service) Recommend(ctx context.Context, opts pickups.RecommendOptions) (pickups.RecommendResult, error) {
	resolved, err := s.resolveSources(ctx, opts)
	if err != nil {
		return pickups.RecommendResult{}, err
	}

	starts, err := s.foreRepo.ListProbableStarts(ctx, forecaster.ListFilter{From: &opts.From, To: &opts.To, IncludeTBD: true, ImportRun: resolved.importRunID})
	if err != nil {
		return pickups.RecommendResult{}, err
	}

	rosterInputs, rosterWeak := s.rosterInputsAndWeakCandidates(resolved.rosterRows)
	rosterReport, err := s.pitchSvc.Report(ctx, pitchers.AnalysisOptions{
		From:         opts.From,
		To:           opts.To,
		ImportRunID:  resolved.importRunID,
		RosterInputs: rosterInputs,
		RosterSource: fmt.Sprintf("espn:sync_run:%d", resolved.syncRunIDVal),
	})
	if err != nil {
		return pickups.RecommendResult{}, err
	}

	candidateProjections := s.projectCandidates(resolved.candidates, starts)
	topN := opts.TopN
	if topN <= 0 {
		topN = 10
	}
	result := s.buildRecommendations(candidateProjections, rosterReport.RankedPitchers, rosterWeak, topN, opts)

	runID, err := s.pickRepo.SaveRecommendation(ctx, pickrepo.CreateRecommendationInput{
		SyncRunID:      &resolved.syncRunIDVal,
		ImportRunID:    resolved.importRunID,
		CandidateRunID: resolved.candidateRunID,
		WindowStart:    opts.From.Format("2006-01-02"),
		WindowEnd:      opts.To.Format("2006-01-02"),
		Status:         "success",
		Summary: map[string]any{
			"top_candidates": len(result.TopCandidates),
			"streamers":      len(result.TopStreamers),
			"upgrades":       0,
			"risky_monitor":  len(result.RiskyMonitor),
			"unmatched":      len(result.Unmatched),
		},
		Items: result.Items,
	})
	if err != nil {
		return pickups.RecommendResult{}, err
	}
	result.RecommendationRunID = runID
	result.SyncRunID = &resolved.syncRunIDVal
	result.ImportRunID = resolved.importRunID
	result.CandidateRunID = resolved.candidateRunID
	result.WindowStart = opts.From.Format("2006-01-02")
	result.WindowEnd = opts.To.Format("2006-01-02")
	return result, nil
}

func (s *Service) TopStreamers(ctx context.Context, opts pickups.RecommendOptions) (pickups.RecommendResult, error) {
	res, err := s.Recommend(ctx, opts)
	if err != nil {
		return pickups.RecommendResult{}, err
	}
	return pickups.RecommendResult{
		RecommendationRunID: res.RecommendationRunID,
		SyncRunID:           res.SyncRunID,
		ImportRunID:         res.ImportRunID,
		CandidateRunID:      res.CandidateRunID,
		WindowStart:         res.WindowStart,
		WindowEnd:           res.WindowEnd,
		TopStreamers:        res.TopStreamers,
		Items:               res.TopStreamers,
	}, nil
}

func (s *Service) Compare(ctx context.Context, opts pickups.RecommendOptions) (pickups.RecommendResult, error) {
	res, err := s.Recommend(ctx, opts)
	if err != nil {
		return pickups.RecommendResult{}, err
	}
	return pickups.RecommendResult{
		RecommendationRunID: res.RecommendationRunID,
		SyncRunID:           res.SyncRunID,
		ImportRunID:         res.ImportRunID,
		CandidateRunID:      res.CandidateRunID,
		WindowStart:         res.WindowStart,
		WindowEnd:           res.WindowEnd,
		Upgrades:            []pickups.RecommendationItem{},
		Items:               []pickups.RecommendationItem{},
	}, nil
}

func (s *Service) Last(ctx context.Context) (*pickups.RecommendationRun, []pickups.RecommendationItem, error) {
	return s.pickRepo.LatestRecommendation(ctx)
}

func (s *Service) Show(ctx context.Context, recommendationID int64) (*pickups.RecommendationRun, []pickups.RecommendationItem, error) {
	return s.pickRepo.RecommendationByID(ctx, recommendationID)
}

type resolvedSources struct {
	syncRunIDVal   int64
	importRunID    *int64
	candidateRunID *int64
	rosterRows     []espnRosterRow
	candidates     []espnCandidateRow
}

type espnRosterRow struct {
	PlayerName string
	MLBTeam    string
	Role       string
	StatusTag  string
}

type espnCandidateRow struct {
	PlayerName   string
	MLBTeam      string
	ESPNPlayerID *int64
	Role         string
	StatusTag    string
}

func (s *Service) resolveSources(ctx context.Context, opts pickups.RecommendOptions) (resolvedSources, error) {
	resolved := resolvedSources{}
	if opts.SyncRunID != nil {
		resolved.syncRunIDVal = *opts.SyncRunID
	} else {
		syncRun, err := s.espnRepo.LatestSyncRun(ctx)
		if err != nil {
			return resolved, err
		}
		if syncRun == nil {
			return resolved, fmt.Errorf("no ESPN sync found; run `fb espn sync roster` first")
		}
		resolved.syncRunIDVal = syncRun.ID
	}
	if opts.ImportRunID != nil {
		resolved.importRunID = opts.ImportRunID
	} else {
		latestImport, err := s.foreRepo.LatestImportRun(ctx)
		if err != nil {
			return resolved, err
		}
		if latestImport == nil {
			return resolved, fmt.Errorf("no forecaster import found; run `fb forecaster import` first")
		}
		resolved.importRunID = &latestImport.ID
	}
	if opts.CandidateRunID != nil {
		resolved.candidateRunID = opts.CandidateRunID
	} else {
		latestCandidateRun, err := s.espnRepo.LatestCandidateRun(ctx)
		if err != nil {
			return resolved, err
		}
		if latestCandidateRun == nil {
			return resolved, fmt.Errorf("no candidate run found; run `fb espn free-agents pitchers --limit N` first")
		}
		resolved.candidateRunID = &latestCandidateRun.ID
	}

	rosterRowsDB, err := s.espnRepo.LatestRoster(ctx, &resolved.syncRunIDVal, true)
	if err != nil {
		return resolved, err
	}
	if len(rosterRowsDB) == 0 {
		return resolved, fmt.Errorf("no ESPN roster rows found for sync run %d", resolved.syncRunIDVal)
	}
	for _, row := range rosterRowsDB {
		resolved.rosterRows = append(resolved.rosterRows, espnRosterRow{PlayerName: row.PlayerName, MLBTeam: row.MLBTeam, Role: row.Role, StatusTag: row.StatusTag})
	}

	candRowsDB, err := s.espnRepo.ListCandidates(ctx, resolved.candidateRunID, 500)
	if err != nil {
		return resolved, err
	}
	if len(candRowsDB) == 0 {
		return resolved, fmt.Errorf("candidate run %d has no rows", *resolved.candidateRunID)
	}
	for _, row := range candRowsDB {
		resolved.candidates = append(resolved.candidates, espnCandidateRow{PlayerName: row.PlayerName, MLBTeam: row.MLBTeam, ESPNPlayerID: row.ESPNPlayerID, Role: row.Role, StatusTag: row.StatusTag})
	}
	return resolved, nil
}

func (s *Service) rosterInputsAndWeakCandidates(rows []espnRosterRow) ([]pitchers.RosterInput, []pitchers.RosterInput) {
	inputs := make([]pitchers.RosterInput, 0, len(rows))
	weak := make([]pitchers.RosterInput, 0)
	for _, row := range rows {
		if strings.EqualFold(strings.TrimSpace(row.Role), "RP") {
			continue
		}
		in := pitchers.RosterInput{PlayerName: row.PlayerName, MLBTeam: row.MLBTeam, Role: row.Role, Status: strings.ToLower(strings.TrimSpace(row.StatusTag)), Notes: "source=espn"}
		inputs = append(inputs, in)
		if strings.Contains(strings.ToUpper(row.StatusTag), "OUT") || strings.Contains(strings.ToUpper(row.StatusTag), "INJ") || strings.Contains(strings.ToUpper(row.StatusTag), "DAY") {
			weak = append(weak, in)
		}
	}
	if len(weak) == 0 {
		weak = append(weak, inputs...)
	}
	return inputs, weak
}

func (s *Service) projectCandidates(candidates []espnCandidateRow, starts []forecaster.ProbableStart) []pickups.CandidateProjection {
	cands := matching.BuildCandidates(starts)
	out := []pickups.CandidateProjection{}
	for _, c := range candidates {
		m := matching.Match(c.PlayerName, c.MLBTeam, cands)
		proj := pickups.CandidateProjection{PlayerName: c.PlayerName, MLBTeam: c.MLBTeam, ESPNPlayerID: c.ESPNPlayerID}
		if m.MatchStatus != pitchers.MatchStatusMatched {
			proj.Unmatched = true
			proj.Flags = append(proj.Flags, string(m.MatchStatus))
			proj.Notes = append(proj.Notes, firstNonEmpty(m.Explanation, "no normalized name match in probable starts"))
			out = append(out, proj)
			continue
		}
		key := matching.NormalizeName(m.MatchedPitcherName)
		proj.MatchedPitcherName = m.MatchedPitcherName
		total := 0.0
		countWithProjection := 0
		for _, st := range starts {
			if matching.NormalizeName(st.PitcherName) != key {
				continue
			}
			sd := ""
			if st.GameDate != nil {
				sd = st.GameDate.Format("2006-01-02")
			}
			proj.Starts = append(proj.Starts, pickups.Start{Date: sd, Opponent: st.Opponent, ProjectedFPTS: st.ProjectedFPTS, Status: string(st.Status)})
			proj.ProjectedStartCount++
			if st.ProjectedFPTS == nil {
				if !containsFlag(proj.Flags, "missing_projection") {
					proj.Flags = append(proj.Flags, "missing_projection")
				}
			} else {
				total += *st.ProjectedFPTS
				countWithProjection++
				if *st.ProjectedFPTS > proj.HighestSingleFPTS {
					proj.HighestSingleFPTS = *st.ProjectedFPTS
				}
			}
			if st.Status == forecaster.StatusTBD && !containsFlag(proj.Flags, "tbd") {
				proj.Flags = append(proj.Flags, "tbd")
			}
		}
		if proj.ProjectedStartCount >= 2 {
			proj.Flags = append(proj.Flags, "two_start_week")
		}
		if proj.ProjectedStartCount == 0 {
			proj.Flags = append(proj.Flags, "no_start_scheduled")
		}
		statusUpper := strings.ToUpper(strings.TrimSpace(c.StatusTag))
		switch {
		case isHardBlockedStatus(statusUpper):
			proj.Flags = append(proj.Flags, "status_blocked")
			proj.Notes = append(proj.Notes, fmt.Sprintf("status=%s", statusUpper))
		case isSoftRiskStatus(statusUpper):
			proj.Flags = append(proj.Flags, "status_risk")
			proj.Notes = append(proj.Notes, fmt.Sprintf("status=%s", statusUpper))
		}
		proj.TotalProjectedFPTS = total
		if countWithProjection > 0 {
			proj.AverageProjectedFPTS = total / float64(countWithProjection)
		}
		sort.SliceStable(proj.Starts, func(i, j int) bool { return proj.Starts[i].Date < proj.Starts[j].Date })
		out = append(out, proj)
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Unmatched != out[j].Unmatched {
			return !out[i].Unmatched
		}
		if out[i].HighestSingleFPTS != out[j].HighestSingleFPTS {
			return out[i].HighestSingleFPTS > out[j].HighestSingleFPTS
		}
		if out[i].TotalProjectedFPTS != out[j].TotalProjectedFPTS {
			return out[i].TotalProjectedFPTS > out[j].TotalProjectedFPTS
		}
		return strings.ToLower(out[i].PlayerName) < strings.ToLower(out[j].PlayerName)
	})
	return out
}

func (s *Service) buildRecommendations(cands []pickups.CandidateProjection, roster []pitchers.PitcherProjection, weakRoster []pitchers.RosterInput, topN int, opts pickups.RecommendOptions) pickups.RecommendResult {
	_ = roster
	_ = weakRoster

	items := []pickups.RecommendationItem{}
	appendRanked := func(itemType pickups.ItemType, rows []pickups.RecommendationItem) {
		for i := range rows {
			rank := i + 1
			rows[i].ResultRank = &rank
			rows[i].ItemType = itemType
			items = append(items, rows[i])
		}
	}

	topCandidates := []pickups.RecommendationItem{}
	streamers := []pickups.RecommendationItem{}
	risky := []pickups.RecommendationItem{}
	unmatched := []pickups.RecommendationItem{}
	upgrades := []pickups.RecommendationItem{}

	for _, c := range cands {
		base := toRecommendationItem(c)
		if c.Unmatched {
			base.ItemType = pickups.ItemTypeUnmatched
			unmatched = append(unmatched, base)
			continue
		}
		if isRiskyCandidate(c) {
			base.ItemType = pickups.ItemTypeRiskyMonitor
			if c.HighestSingleFPTS >= s.cfg.RiskyMonitorMinTotalFPTS {
				risky = append(risky, base)
			}
		}
		if !isBlockedCandidate(c) {
			topCandidates = append(topCandidates, base)
		}
		minStreamer := s.cfg.MinStreamerTotalFPTS
		if opts.MinTotalFPTS != nil {
			minStreamer = *opts.MinTotalFPTS
		}
		if c.HighestSingleFPTS >= minStreamer && !containsFlag(c.Flags, "tbd") && !isBlockedCandidate(c) {
			streamers = append(streamers, base)
		}

	}

	trim := func(rows []pickups.RecommendationItem) []pickups.RecommendationItem {
		if len(rows) > topN {
			return rows[:topN]
		}
		return rows
	}
	topCandidates = trim(topCandidates)
	streamers = trim(streamers)
	upgrades = trim(upgrades)
	risky = trim(risky)
	unmatched = trim(unmatched)

	appendRanked(pickups.ItemTypeTopCandidate, topCandidates)
	appendRanked(pickups.ItemTypeStreamer, streamers)
	appendRanked(pickups.ItemTypeRiskyMonitor, risky)
	appendRanked(pickups.ItemTypeUnmatched, unmatched)

	return pickups.RecommendResult{
		TopCandidates: topCandidates,
		TopStreamers:  streamers,
		Upgrades:      upgrades,
		RiskyMonitor:  risky,
		Unmatched:     unmatched,
		Items:         items,
	}
}

func toRecommendationItem(c pickups.CandidateProjection) pickups.RecommendationItem {
	item := pickups.RecommendationItem{
		PlayerName:          c.PlayerName,
		MLBTeam:             c.MLBTeam,
		ESPNPlayerID:        c.ESPNPlayerID,
		MatchedPitcherName:  c.MatchedPitcherName,
		ProjectedStartCount: c.ProjectedStartCount,
		Flags:               append([]string{}, c.Flags...),
		Notes:               append([]string{}, c.Notes...),
		Details: map[string]interface{}{
			"starts":                    c.Starts,
			"average_projected_fpts":    c.AverageProjectedFPTS,
			"highest_single_start_fpts": c.HighestSingleFPTS,
		},
		CreatedAt: time.Now().UTC(),
	}
	total := c.TotalProjectedFPTS
	item.TotalProjectedFPTS = &total
	return item
}

func containsFlag(flags []string, target string) bool {
	for _, f := range flags {
		if strings.EqualFold(strings.TrimSpace(f), target) {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func isBlockedCandidate(c pickups.CandidateProjection) bool {
	return containsFlag(c.Flags, "status_blocked")
}

func isRiskyCandidate(c pickups.CandidateProjection) bool {
	return containsFlag(c.Flags, "status_blocked") ||
		containsFlag(c.Flags, "tbd") ||
		containsFlag(c.Flags, "missing_projection") ||
		containsFlag(c.Flags, "status_risk")
}

func isHardBlockedStatus(status string) bool {
	s := strings.ToUpper(strings.TrimSpace(status))
	switch {
	case s == "OUT":
		return true
	case strings.Contains(s, "IL"):
		return true
	case strings.Contains(s, "DL"):
		return true
	case strings.Contains(s, "INJURED_RESERVE"):
		return true
	default:
		return false
	}
}

func isSoftRiskStatus(status string) bool {
	s := strings.ToUpper(strings.TrimSpace(status))
	switch {
	case s == "DAY_TO_DAY":
		return true
	case s == "QUESTIONABLE":
		return true
	default:
		return false
	}
}
