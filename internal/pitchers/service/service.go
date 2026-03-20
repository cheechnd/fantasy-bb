package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"fantasy-baseball/internal/forecaster"
	"fantasy-baseball/internal/pitchers"
	pinput "fantasy-baseball/internal/pitchers/input"
	"fantasy-baseball/internal/pitchers/matching"
	"fantasy-baseball/internal/pitchers/repository"
)

type Service struct {
	foreRepo  *forecaster.Repository
	pitchRepo *repository.Repository
}

func New(foreRepo *forecaster.Repository, pitchRepo *repository.Repository) *Service {
	return &Service{foreRepo: foreRepo, pitchRepo: pitchRepo}
}

type playerInput struct {
	Name     string
	Team     string
	Locked   bool
	MustHold bool
	Notes    string
}

func (s *Service) AnalyzeWeek(ctx context.Context, opts pitchers.AnalysisOptions) (pitchers.AnalysisReport, error) {
	roster, err := s.loadRosterInputs(opts)
	if err != nil {
		return pitchers.AnalysisReport{}, err
	}
	players := make([]playerInput, 0, len(roster))
	for _, p := range roster {
		players = append(players, playerInput{Name: p.PlayerName, Team: p.MLBTeam, Locked: p.Locked, MustHold: p.MustHold, Notes: p.Notes})
	}
	return s.analyzePlayers(ctx, players, opts, pitchers.AnalysisTypeWeekly, "rostered_pitcher", false)
}

func (s *Service) TwoStart(ctx context.Context, opts pitchers.AnalysisOptions) (pitchers.AnalysisReport, error) {
	roster, err := s.loadRosterInputs(opts)
	if err != nil {
		return pitchers.AnalysisReport{}, err
	}
	players := make([]playerInput, 0, len(roster))
	for _, p := range roster {
		players = append(players, playerInput{Name: p.PlayerName, Team: p.MLBTeam, Locked: p.Locked, MustHold: p.MustHold, Notes: p.Notes})
	}
	return s.analyzePlayers(ctx, players, opts, pitchers.AnalysisTypeTwoStart, "two_start_pitcher", true)
}

func (s *Service) Streamers(ctx context.Context, opts pitchers.AnalysisOptions) (pitchers.AnalysisReport, error) {
	roster, err := s.loadRosterInputs(opts)
	if err != nil {
		return pitchers.AnalysisReport{}, err
	}
	pool, err := pinput.LoadFreeAgents(opts.PoolPath)
	if err != nil {
		return pitchers.AnalysisReport{}, err
	}
	rosterSet := map[string]struct{}{}
	for _, r := range roster {
		rosterSet[matching.NormalizeName(r.PlayerName)] = struct{}{}
	}
	players := make([]playerInput, 0, len(pool))
	for _, p := range pool {
		if _, ok := rosterSet[matching.NormalizeName(p.PlayerName)]; ok {
			continue
		}
		players = append(players, playerInput{Name: p.PlayerName, Team: p.MLBTeam, Notes: p.Notes})
	}

	report, err := s.analyzePlayers(ctx, players, opts, pitchers.AnalysisTypeStreamers, "streamer", false)
	if err != nil {
		return pitchers.AnalysisReport{}, err
	}
	if opts.MinTotalFPTS != nil {
		filtered := make([]pitchers.PitcherProjection, 0, len(report.RankedPitchers))
		for _, p := range report.RankedPitchers {
			if p.TotalProjectedFPTS >= *opts.MinTotalFPTS {
				filtered = append(filtered, p)
			}
		}
		report.RankedPitchers = filtered
	}
	if opts.TopN > 0 && len(report.RankedPitchers) > opts.TopN {
		report.RankedPitchers = report.RankedPitchers[:opts.TopN]
	}
	return report, nil
}

func (s *Service) Report(ctx context.Context, opts pitchers.AnalysisOptions) (pitchers.AnalysisReport, error) {
	rosterReport, err := s.AnalyzeWeek(ctx, opts)
	if err != nil {
		return pitchers.AnalysisReport{}, err
	}
	rosterReport.AnalysisType = pitchers.AnalysisTypeReport
	return rosterReport, nil
}

func (s *Service) ExplainMatches(ctx context.Context, opts pitchers.AnalysisOptions) ([]pitchers.MatchResult, error) {
	roster, err := s.loadRosterInputs(opts)
	if err != nil {
		return nil, err
	}
	starts, _, err := s.windowedStarts(ctx, opts)
	if err != nil {
		return nil, err
	}
	candidates := matching.BuildCandidates(starts)
	results := make([]pitchers.MatchResult, 0, len(roster))
	for _, p := range roster {
		results = append(results, matching.Match(p.PlayerName, p.MLBTeam, candidates))
	}
	return results, nil
}

func (s *Service) loadRosterInputs(opts pitchers.AnalysisOptions) ([]pitchers.RosterInput, error) {
	if len(opts.RosterInputs) > 0 {
		return opts.RosterInputs, nil
	}
	roster, err := pinput.LoadRoster(opts.RosterPath)
	if err != nil {
		return nil, err
	}
	out := make([]pitchers.RosterInput, 0, len(roster))
	for _, p := range roster {
		out = append(out, pitchers.RosterInput{
			PlayerName: p.PlayerName,
			MLBTeam:    p.MLBTeam,
			Role:       p.Role,
			Status:     p.Status,
			Locked:     p.Locked,
			MustHold:   p.MustHold,
			Notes:      p.Notes,
		})
	}
	return out, nil
}

func (s *Service) LastReport(ctx context.Context) (*repository.AnalysisRunRow, []repository.AnalysisResultRow, error) {
	run, err := s.pitchRepo.LatestRun(ctx)
	if err != nil {
		return nil, nil, err
	}
	if run == nil {
		return nil, nil, nil
	}
	results, err := s.pitchRepo.ResultsByRun(ctx, run.ID)
	if err != nil {
		return nil, nil, err
	}
	return run, results, nil
}

func (s *Service) analyzePlayers(ctx context.Context, players []playerInput, opts pitchers.AnalysisOptions, analysisType pitchers.AnalysisType, matchedResultType string, onlyTwoStart bool) (pitchers.AnalysisReport, error) {
	starts, importRunID, err := s.windowedStarts(ctx, opts)
	if err != nil {
		return pitchers.AnalysisReport{}, err
	}
	candidates := matching.BuildCandidates(starts)

	matches := make([]pitchers.MatchResult, 0, len(players))
	ranked := make([]pitchers.PitcherProjection, 0)
	unmatched := make([]pitchers.MatchResult, 0)
	ambiguous := make([]pitchers.MatchResult, 0)

	for _, p := range players {
		m := matching.Match(p.Name, p.Team, candidates)
		matches = append(matches, m)
		switch m.MatchStatus {
		case pitchers.MatchStatusUnmatched:
			unmatched = append(unmatched, m)
			continue
		case pitchers.MatchStatusAmbiguous:
			ambiguous = append(ambiguous, m)
			continue
		}

		proj := aggregateProjectionForMatch(m, starts)
		if p.Locked {
			proj.Flags = append(proj.Flags, "locked")
		}
		if p.MustHold {
			proj.Flags = append(proj.Flags, "must_hold")
		}
		if p.Notes != "" {
			proj.Flags = append(proj.Flags, "has_notes")
		}
		proj.PlayerName = p.Name
		proj.MLBTeam = p.Team
		if onlyTwoStart && proj.StartCount < 2 {
			continue
		}
		ranked = append(ranked, proj)
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].TotalProjectedFPTS != ranked[j].TotalProjectedFPTS {
			return ranked[i].TotalProjectedFPTS > ranked[j].TotalProjectedFPTS
		}
		if ranked[i].StartCount != ranked[j].StartCount {
			return ranked[i].StartCount > ranked[j].StartCount
		}
		if ranked[i].HighestSingleFPTS != ranked[j].HighestSingleFPTS {
			return ranked[i].HighestSingleFPTS > ranked[j].HighestSingleFPTS
		}
		return ranked[i].PlayerName < ranked[j].PlayerName
	})

	twoStart := make([]pitchers.PitcherProjection, 0)
	for _, r := range ranked {
		if r.StartCount >= 2 {
			twoStart = append(twoStart, r)
		}
	}

	report := pitchers.AnalysisReport{
		AnalysisType:     analysisType,
		ImportRunID:      importRunID,
		WindowStart:      opts.From.Format("2006-01-02"),
		WindowEnd:        opts.To.Format("2006-01-02"),
		RankedPitchers:   ranked,
		TwoStartPitchers: twoStart,
		UnmatchedPlayers: unmatched,
		AmbiguousPlayers: ambiguous,
		MatchResults:     matches,
	}

	runID, err := s.persistReport(ctx, report, opts, matchedResultType)
	if err != nil {
		return pitchers.AnalysisReport{}, err
	}
	report.AnalysisRunID = runID
	return report, nil
}

func (s *Service) persistReport(ctx context.Context, report pitchers.AnalysisReport, opts pitchers.AnalysisOptions, matchedResultType string) (int64, error) {
	results := make([]repository.ResultInput, 0)
	for i, p := range report.RankedPitchers {
		rank := i + 1
		details := map[string]any{"starts": p.Starts, "avg_projected_fpts": p.AverageProjectedFPTS, "highest_single_fpts": p.HighestSingleFPTS}
		results = append(results, repository.ResultInput{
			ResultType:          matchedResultType,
			PlayerName:          p.PlayerName,
			MLBTeam:             p.MLBTeam,
			MatchedPitcherName:  p.MatchedPitcherName,
			ProjectedStartCount: p.StartCount,
			TotalProjectedFPTS:  &p.TotalProjectedFPTS,
			ResultRank:          &rank,
			Flags:               p.Flags,
			Details:             details,
		})
	}
	for _, u := range report.UnmatchedPlayers {
		results = append(results, repository.ResultInput{ResultType: "unmatched", PlayerName: u.InputPlayerName, MLBTeam: u.InputMLBTeam, Flags: []string{"unmatched"}, Details: map[string]any{"explanation": u.Explanation}})
	}
	for _, a := range report.AmbiguousPlayers {
		results = append(results, repository.ResultInput{ResultType: "warning", PlayerName: a.InputPlayerName, MLBTeam: a.InputMLBTeam, Flags: []string{"ambiguous_match"}, Details: map[string]any{"candidates": a.CandidateDisplayList, "explanation": a.Explanation}})
	}

	summary := map[string]any{
		"ranked_count":    len(report.RankedPitchers),
		"two_start_count": len(report.TwoStartPitchers),
		"unmatched_count": len(report.UnmatchedPlayers),
		"ambiguous_count": len(report.AmbiguousPlayers),
	}
	return s.pitchRepo.SaveRun(ctx, repository.CreateRunInput{
		AnalysisType: string(report.AnalysisType),
		ImportRunID:  report.ImportRunID,
		RosterPath:   firstNonEmpty(opts.RosterSource, opts.RosterPath),
		PoolPath:     opts.PoolPath,
		WindowStart:  report.WindowStart,
		WindowEnd:    report.WindowEnd,
		Status:       "success",
		Summary:      summary,
	}, report.MatchResults, results)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func (s *Service) windowedStarts(ctx context.Context, opts pitchers.AnalysisOptions) ([]forecaster.ProbableStart, *int64, error) {
	importRunID := opts.ImportRunID
	if importRunID == nil {
		latest, err := s.foreRepo.LatestImportRun(ctx)
		if err != nil {
			return nil, nil, err
		}
		if latest == nil {
			return nil, nil, fmt.Errorf("no forecaster import found; run `fb forecaster import` first")
		}
		importRunID = &latest.ID
	}
	starts, err := s.foreRepo.ListProbableStarts(ctx, forecaster.ListFilter{From: &opts.From, To: &opts.To, IncludeTBD: true, ImportRun: importRunID})
	if err != nil {
		return nil, nil, err
	}
	return starts, importRunID, nil
}

func aggregateProjectionForMatch(m pitchers.MatchResult, starts []forecaster.ProbableStart) pitchers.PitcherProjection {
	key := matching.NormalizeName(m.MatchedPitcherName)
	proj := pitchers.PitcherProjection{MatchedPitcherName: m.MatchedPitcherName}
	total := 0.0
	highest := 0.0
	countWithProjection := 0
	for _, s := range starts {
		if matching.NormalizeName(s.PitcherName) != key {
			continue
		}
		date := ""
		if s.GameDate != nil {
			date = s.GameDate.Format("2006-01-02")
		}
		proj.Starts = append(proj.Starts, pitchers.PitcherStart{Date: date, Opponent: s.Opponent, ProjectedFPTS: s.ProjectedFPTS, Status: string(s.Status)})
		proj.StartCount++
		if s.ProjectedFPTS == nil {
			if !containsFlag(proj.Flags, "missing_projection") {
				proj.Flags = append(proj.Flags, "missing_projection")
			}
		} else {
			total += *s.ProjectedFPTS
			countWithProjection++
			if *s.ProjectedFPTS > highest {
				highest = *s.ProjectedFPTS
			}
		}
		if s.Status == forecaster.StatusTBD && !containsFlag(proj.Flags, "tbd") {
			proj.Flags = append(proj.Flags, "tbd")
		}
	}
	if proj.StartCount >= 2 {
		proj.Flags = append(proj.Flags, "two_start_week")
	}
	proj.TotalProjectedFPTS = total
	if countWithProjection > 0 {
		proj.AverageProjectedFPTS = total / float64(countWithProjection)
	}
	proj.HighestSingleFPTS = highest
	sort.Slice(proj.Starts, func(i, j int) bool {
		return proj.Starts[i].Date < proj.Starts[j].Date
	})
	return proj
}

func containsFlag(flags []string, target string) bool {
	for _, f := range flags {
		if strings.EqualFold(f, target) {
			return true
		}
	}
	return false
}
