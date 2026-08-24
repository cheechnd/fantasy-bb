package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"fantasy-baseball/internal/config"
	"fantasy-baseball/internal/espn"
	"fantasy-baseball/internal/espn/client"
	"fantasy-baseball/internal/espn/repository"
	"fantasy-baseball/internal/pitchers"
	"fantasy-baseball/internal/pitchers/matching"
)

type Service struct {
	repo *repository.Repository
}

func New(repo *repository.Repository) *Service {
	return &Service{repo: repo}
}

type SyncOptions struct {
	DryRun           bool
	ScoringPeriodID  *int
	EffectiveNextDay bool
}

type FreeAgentOptions struct {
	Limit  int
	Search string
	Team   string
}

type ShowRosterFilter struct {
	SyncRunID        *int64
	ScoringPeriodID  *int
	EffectiveNextDay bool
	PitchersOnly     bool
}

type MatchupOptions struct {
	MatchupPeriodID *int
	ScoringPeriodID *int
}

type PitcherRosterSource struct {
	SyncRunID int64
	Source    string
	Inputs    []pitchers.RosterInput
	Snapshots []espn.RosterSnapshot
}

func (s *Service) Validate(ctx context.Context, cfg config.Config) (espn.ValidateReport, error) {
	report := espn.ValidateReport{Checks: []map[string]any{}}

	addCheck := func(name string, err error) {
		item := map[string]any{"name": name, "ok": err == nil}
		if err != nil {
			item["error"] = err.Error()
		}
		report.Checks = append(report.Checks, item)
	}

	err := cfg.ValidateESPNUsage()
	addCheck("config.espn_settings", err)
	if err != nil {
		report.OK = false
		return report, nil
	}

	creds, err := cfg.LoadESPNCredentialsFromEnv()
	addCheck("auth.env_credentials", err)
	if err != nil {
		report.OK = false
		return report, nil
	}

	client := client.New(time.Duration(cfg.ESPN.TimeoutSeconds)*time.Second, "")
	fetchResult, err := client.FetchLeague(ctx, cfg, creds)
	addCheck("espn.connectivity", err)
	if err != nil {
		report.OK = false
		return report, nil
	}

	parsed, err := parseLeaguePayload(fetchResult.Payload, cfg.League.TeamID)
	addCheck("espn.payload_parse", err)
	if err != nil {
		report.OK = false
		return report, nil
	}

	report.OK = true
	report.Summary = map[string]string{
		"league_name": parsed.League.LeagueName,
		"team_name":   parsed.League.TeamName,
		"endpoint":    fetchResult.Endpoint,
	}
	return report, nil
}

func (s *Service) SyncRoster(ctx context.Context, cfg config.Config, opts SyncOptions) (espn.SyncSummary, error) {
	if err := cfg.ValidateESPNUsage(); err != nil {
		return espn.SyncSummary{}, err
	}
	creds, err := cfg.LoadESPNCredentialsFromEnv()
	if err != nil {
		return espn.SyncSummary{}, err
	}

	cli := client.New(time.Duration(cfg.ESPN.TimeoutSeconds)*time.Second, "")
	scoringPeriodID := opts.ScoringPeriodID
	effectiveNextDay := opts.EffectiveNextDay
	fetchResult, err := cli.FetchLeagueWithOptions(ctx, cfg, creds, client.LeagueFetchOptions{ScoringPeriodID: scoringPeriodID})
	if err != nil {
		return espn.SyncSummary{}, err
	}
	if effectiveNextDay && scoringPeriodID == nil {
		currentSP, ok := detectScoringPeriodID(fetchResult.Payload)
		if !ok {
			return espn.SyncSummary{}, fmt.Errorf("could not resolve current ESPN scoring period for --next-day roster sync")
		}
		nextSP := currentSP + 1
		scoringPeriodID = &nextSP
		fetchResult, err = cli.FetchLeagueWithOptions(ctx, cfg, creds, client.LeagueFetchOptions{ScoringPeriodID: scoringPeriodID})
		if err != nil {
			return espn.SyncSummary{}, err
		}
	}

	parsed, err := parseLeaguePayload(fetchResult.Payload, cfg.League.TeamID)
	if err != nil {
		return espn.SyncSummary{}, err
	}
	parsed.League.LeagueID = cfg.League.LeagueID
	parsed.League.TeamID = cfg.League.TeamID
	parsed.League.Season = cfg.League.Season

	warningCount := len(parsed.Warnings)
	now := time.Now().UTC()
	summary := espn.SyncSummary{
		LeagueID:           cfg.League.LeagueID,
		TeamID:             cfg.League.TeamID,
		Season:             cfg.League.Season,
		SyncedAt:           now.Format(time.RFC3339),
		RosteredPlayers:    len(parsed.Roster),
		PitcherCount:       countPitchers(parsed.Roster),
		WarningCount:       warningCount,
		SourceEndpoint:     fetchResult.Endpoint,
		ResponseStatusCode: fetchResult.ResponseStatus,
		ScoringPeriodID:    scoringPeriodID,
		EffectiveNextDay:   effectiveNextDay,
		DryRun:             opts.DryRun,
	}

	if opts.DryRun {
		return summary, nil
	}

	summaryJSON := map[string]any{
		"rostered_players":   len(parsed.Roster),
		"pitcher_count":      summary.PitcherCount,
		"warnings":           warningMessages(parsed.Warnings),
		"source_endpoint":    fetchResult.Endpoint,
		"scoring_period_id":  scoringPeriodID,
		"effective_next_day": effectiveNextDay,
	}

	runID, err := s.repo.PersistSync(ctx, repository.PersistSyncInput{
		SyncType:         "roster",
		LeagueID:         cfg.League.LeagueID,
		TeamID:           cfg.League.TeamID,
		Season:           cfg.League.Season,
		Status:           "success",
		WarningCount:     warningCount,
		ScoringPeriodID:  scoringPeriodID,
		EffectiveNextDay: effectiveNextDay,
		Summary:          summaryJSON,
		Payloads: []espn.RawPayload{{
			PayloadType:    "league_roster",
			SourceEndpoint: fetchResult.Endpoint,
			ResponseStatus: fetchResult.ResponseStatus,
			PayloadJSON:    string(fetchResult.Payload),
		}},
		League:   parsed.League,
		Roster:   parsed.Roster,
		Warnings: parsed.Warnings,
	})
	if err != nil {
		return espn.SyncSummary{}, err
	}
	summary.SyncRunID = &runID
	return summary, nil
}

func (s *Service) ShowRoster(ctx context.Context, filter ShowRosterFilter) ([]espn.RosterSnapshot, error) {
	return s.repo.LatestRosterForContext(ctx, filter.SyncRunID, filter.ScoringPeriodID, filter.EffectiveNextDay, filter.PitchersOnly)
}

func (s *Service) ShowLeague(ctx context.Context, syncRunID *int64) (*espn.LeagueSnapshot, error) {
	return s.repo.LatestLeague(ctx, syncRunID)
}

func (s *Service) SourceStatus(ctx context.Context, limit int) ([]espn.SyncRun, error) {
	return s.repo.ListSyncRuns(ctx, limit)
}

func (s *Service) Warnings(ctx context.Context, syncRunID *int64, limit int) ([]espn.ParseWarning, error) {
	return s.repo.ListWarnings(ctx, syncRunID, limit)
}

func (s *Service) LatestSync(ctx context.Context) (*espn.SyncRun, error) {
	return s.repo.LatestSyncRun(ctx)
}

func (s *Service) SyncRunByID(ctx context.Context, syncRunID int64) (*espn.SyncRun, error) {
	return s.repo.SyncRunByID(ctx, syncRunID)
}

func (s *Service) MatchupSummary(ctx context.Context, cfg config.Config, opts MatchupOptions) (espn.MatchupSummary, error) {
	if err := cfg.ValidateESPNUsage(); err != nil {
		return espn.MatchupSummary{}, err
	}
	creds, err := cfg.LoadESPNCredentialsFromEnv()
	if err != nil {
		return espn.MatchupSummary{}, err
	}
	cli := client.New(time.Duration(cfg.ESPN.TimeoutSeconds)*time.Second, "")
	fetchResult, err := cli.FetchMatchupScore(ctx, cfg, creds, client.MatchupFetchOptions{
		MatchupPeriodID: opts.MatchupPeriodID,
		ScoringPeriodID: opts.ScoringPeriodID,
	})
	if err != nil {
		return espn.MatchupSummary{}, err
	}
	var seasonSchedulePayload []byte
	if seasonFetch, scheduleErr := cli.FetchSeasonProTeamSchedules(ctx, cfg, creds); scheduleErr == nil {
		seasonSchedulePayload = seasonFetch.Payload
	}
	return parseMatchupSummary(fetchResult.Payload, seasonSchedulePayload, cfg, fetchResult.Endpoint, fetchResult.ResponseStatus, opts)
}

func (s *Service) SyncFreeAgentPitchers(ctx context.Context, cfg config.Config, opts FreeAgentOptions) (espn.CandidateSummary, error) {
	if err := cfg.ValidateESPNUsage(); err != nil {
		return espn.CandidateSummary{}, err
	}
	creds, err := cfg.LoadESPNCredentialsFromEnv()
	if err != nil {
		return espn.CandidateSummary{}, err
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = cfg.Pickups.Pitchers.DefaultCandidateLimit
	}
	maxLimit := cfg.Pickups.Pitchers.MaxCandidateLimit
	if maxLimit <= 0 {
		maxLimit = 50
	}
	if limit > maxLimit {
		limit = maxLimit
	}

	cli := client.New(time.Duration(cfg.ESPN.TimeoutSeconds)*time.Second, "")
	attempts := []client.FreeAgentFetchOptions{
		{Limit: limit, UseSlotFilter: true, SlotIDs: []int{14, 15, 13}},
		{Limit: limit, UseSlotFilter: true, SlotIDs: []int{13}},
		{Limit: limit, UseSlotFilter: false},
	}
	var fetchResult client.FetchResult
	var candidates []espn.FreeAgentCandidate
	warnings := []espn.ParseWarningInput{}
	var fetchErr error
	for i, fetchOpts := range attempts {
		fetchResult, fetchErr = cli.FetchFreeAgentPitchers(ctx, cfg, creds, fetchOpts)
		if fetchErr != nil {
			return espn.CandidateSummary{}, fetchErr
		}
		candidates, warnings = parseFreeAgentCandidatesPayload(fetchResult.Payload, opts.Search, opts.Team, limit, cfg.League.Timezone)
		if len(candidates) > 0 {
			break
		}
		warnings = append(warnings, espn.ParseWarningInput{
			WarningType: "candidate_fetch_empty",
			Message:     fmt.Sprintf("free-agent fetch attempt %d returned zero pitcher candidates", i+1),
			RowContext: map[string]interface{}{
				"use_slot_filter": fetchOpts.UseSlotFilter,
				"slot_ids":        fetchOpts.SlotIDs,
				"limit":           fetchOpts.Limit,
			},
		})
	}
	runFilters := map[string]any{
		"limit":  limit,
		"search": strings.TrimSpace(opts.Search),
		"team":   strings.ToUpper(strings.TrimSpace(opts.Team)),
	}
	latestSync, err := s.repo.LatestSyncRun(ctx)
	if err != nil {
		return espn.CandidateSummary{}, err
	}
	var syncRunID *int64
	if latestSync != nil {
		syncRunID = &latestSync.ID
	}
	summaryJSON := map[string]any{
		"candidate_count": len(candidates),
		"warnings":        warningMessages(warnings),
		"effective_limit": limit,
		"source_endpoint": fetchResult.Endpoint,
	}
	runID, err := s.repo.PersistCandidates(ctx, repository.PersistCandidateInput{
		SyncRunID:    syncRunID,
		QueryType:    "pitchers",
		QueryText:    strings.TrimSpace(opts.Search),
		Filters:      runFilters,
		Status:       "success",
		WarningCount: len(warnings),
		Summary:      summaryJSON,
		Payload: espn.RawPayload{
			PayloadType:    "free_agents_pitchers",
			SourceEndpoint: fetchResult.Endpoint,
			ResponseStatus: fetchResult.ResponseStatus,
			PayloadJSON:    string(fetchResult.Payload),
		},
		Candidates: candidates,
	})
	if err != nil {
		return espn.CandidateSummary{}, err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	return espn.CandidateSummary{
		CandidateRunID: &runID,
		SyncedAt:       now,
		CandidateCount: len(candidates),
		WarningCount:   len(warnings),
		QueryType:      "pitchers",
		QueryText:      strings.TrimSpace(opts.Search),
		EffectiveLimit: limit,
		SourceEndpoint: fetchResult.Endpoint,
		ResponseStatus: fetchResult.ResponseStatus,
	}, nil
}

func (s *Service) LatestCandidateRun(ctx context.Context) (*espn.CandidateRun, error) {
	return s.repo.LatestCandidateRun(ctx)
}

func (s *Service) CandidateRunByID(ctx context.Context, candidateRunID int64) (*espn.CandidateRun, error) {
	return s.repo.CandidateRunByID(ctx, candidateRunID)
}

func (s *Service) Candidates(ctx context.Context, candidateRunID *int64, limit int) ([]espn.FreeAgentCandidate, error) {
	return s.repo.ListCandidates(ctx, candidateRunID, limit)
}

func (s *Service) RosterInputsForPitchers(ctx context.Context, syncRunID *int64) ([]pitchers.RosterInput, string, error) {
	src, err := s.PitcherRosterSource(ctx, syncRunID)
	if err != nil {
		return nil, "", err
	}
	return src.Inputs, src.Source, nil
}

func (s *Service) PitcherRosterSource(ctx context.Context, syncRunID *int64) (PitcherRosterSource, error) {
	rows, err := s.repo.LatestRoster(ctx, syncRunID, true)
	if err != nil {
		return PitcherRosterSource{}, err
	}
	if len(rows) == 0 {
		return PitcherRosterSource{}, fmt.Errorf("no ESPN roster snapshot found; run `fb espn sync roster` first")
	}
	inputs := make([]pitchers.RosterInput, 0, len(rows))
	var resolvedRunID int64
	if syncRunID != nil {
		resolvedRunID = *syncRunID
	} else {
		resolvedRunID = rows[0].SyncRunID
	}
	for _, row := range rows {
		if strings.EqualFold(strings.TrimSpace(row.Role), "RP") {
			continue
		}
		inputs = append(inputs, pitchers.RosterInput{
			PlayerName: row.PlayerName,
			MLBTeam:    row.MLBTeam,
			Role:       row.Role,
			Status:     strings.ToLower(strings.TrimSpace(row.StatusTag)),
		})
	}
	return PitcherRosterSource{
		SyncRunID: resolvedRunID,
		Source:    fmt.Sprintf("espn:sync_run:%d", resolvedRunID),
		Inputs:    inputs,
		Snapshots: rows,
	}, nil
}

type parsedPayload struct {
	League   espn.LeagueSnapshot
	Roster   []espn.RosterSnapshot
	Warnings []espn.ParseWarningInput
}

func detectScoringPeriodID(payload []byte) (int, bool) {
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return 0, false
	}
	if v := toInt(raw["scoringPeriodId"]); v > 0 {
		return v, true
	}
	if v := toInt(mapPath(raw, "status", "currentScoringPeriod")); v > 0 {
		return v, true
	}
	if v := toInt(mapPath(raw, "status", "currentScoringPeriodId")); v > 0 {
		return v, true
	}
	return 0, false
}

type leagueResponse struct {
	Settings struct {
		Name            string `json:"name"`
		ScoringSettings struct {
			ScoringType string `json:"scoringType"`
		} `json:"scoringSettings"`
		RosterSettings map[string]any `json:"rosterSettings"`
	} `json:"settings"`
	Teams []teamResponse `json:"teams"`
}

type teamResponse struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Location string `json:"location"`
	Nickname string `json:"nickname"`
	Roster   struct {
		Entries []rosterEntry `json:"entries"`
	} `json:"roster"`
}

type rosterEntry struct {
	LineupSlotID    int `json:"lineupSlotId"`
	PlayerPoolEntry struct {
		Player playerResponse `json:"player"`
	} `json:"playerPoolEntry"`
}

type playerResponse struct {
	ID                int64  `json:"id"`
	FullName          string `json:"fullName"`
	ProTeamAbbrev     string `json:"proTeamAbbrev"`
	ProTeamID         int    `json:"proTeamId"`
	DefaultPositionID int    `json:"defaultPositionId"`
	EligibleSlots     []int  `json:"eligibleSlots"`
	InjuryStatus      string `json:"injuryStatus"`
	Ownership         struct {
		PercentOwned float64 `json:"percentOwned"`
	} `json:"ownership"`
}

func parseLeaguePayload(payload []byte, teamIDRaw string) (parsedPayload, error) {
	var body leagueResponse
	if err := json.Unmarshal(payload, &body); err != nil {
		return parsedPayload{}, fmt.Errorf("parse ESPN league payload: %w", err)
	}
	teamIDRaw = strings.TrimSpace(teamIDRaw)
	teamIDValue, err := strconv.ParseInt(teamIDRaw, 10, 64)
	if err != nil {
		return parsedPayload{}, fmt.Errorf("league.team_id must be numeric for ESPN endpoint: %q", teamIDRaw)
	}

	out := parsedPayload{Warnings: []espn.ParseWarningInput{}}
	team, ok := findTeam(body.Teams, teamIDValue)
	if !ok {
		return parsedPayload{}, fmt.Errorf("team_id %d not found in ESPN response", teamIDValue)
	}

	settingsJSON, _ := json.Marshal(body.Settings.RosterSettings)
	out.League = espn.LeagueSnapshot{
		LeagueName:   strings.TrimSpace(body.Settings.Name),
		TeamName:     teamDisplayName(team),
		ScoringType:  strings.TrimSpace(body.Settings.ScoringSettings.ScoringType),
		SettingsJSON: string(settingsJSON),
		CreatedAt:    time.Now().UTC(),
	}

	for _, entry := range team.Roster.Entries {
		row, warning := normalizeRosterEntry(entry)
		if warning != nil {
			out.Warnings = append(out.Warnings, *warning)
		}
		if strings.TrimSpace(row.PlayerName) == "" {
			continue
		}
		out.Roster = append(out.Roster, row)
	}
	if len(out.Roster) == 0 {
		return parsedPayload{}, fmt.Errorf("ESPN payload contained no roster players for team %d", teamIDValue)
	}
	return out, nil
}

func normalizeRosterEntry(entry rosterEntry) (espn.RosterSnapshot, *espn.ParseWarningInput) {
	player := entry.PlayerPoolEntry.Player
	row := espn.RosterSnapshot{
		PlayerName:     strings.TrimSpace(player.FullName),
		NormalizedName: matching.NormalizeName(player.FullName),
		MLBTeam:        resolveMLBTeam(player.ProTeamAbbrev, player.ProTeamID),
		RosterSlot:     lineupSlotLabel(entry.LineupSlotID),
		StatusTag:      strings.TrimSpace(player.InjuryStatus),
		CreatedAt:      time.Now().UTC(),
	}
	if player.ID > 0 {
		v := player.ID
		row.ESPNPlayerID = &v
	}
	row.Role, row.IsPitcher = inferRole(player.DefaultPositionID, player.EligibleSlots)
	if row.Role == "UNKNOWN" {
		return row, &espn.ParseWarningInput{
			WarningType: "role_uncertain",
			Message:     fmt.Sprintf("unable to confidently classify role for player %q", row.PlayerName),
			RowContext: map[string]interface{}{
				"player_name":           row.PlayerName,
				"default_position_id":   player.DefaultPositionID,
				"eligible_slots":        player.EligibleSlots,
				"lineup_slot_id":        entry.LineupSlotID,
				"pro_team_id":           player.ProTeamID,
				"pro_team_abbreviation": player.ProTeamAbbrev,
			},
		}
	}
	rawJSON, _ := json.Marshal(player)
	row.RawPlayerJSON = string(rawJSON)
	return row, nil
}

func resolveMLBTeam(proTeamAbbrev string, proTeamID int) string {
	abbr := strings.ToUpper(strings.TrimSpace(proTeamAbbrev))
	if abbr != "" {
		if abbr == "ATH" {
			return "OAK"
		}
		return abbr
	}
	if code, ok := espnProTeamIDToCode[proTeamID]; ok {
		return code
	}
	return ""
}

var espnProTeamIDToCode = map[int]string{
	1:  "BAL",
	2:  "BOS",
	3:  "LAA",
	4:  "CHW",
	5:  "CLE",
	6:  "DET",
	7:  "KC",
	8:  "MIL",
	9:  "MIN",
	10: "NYY",
	11: "OAK",
	12: "SEA",
	13: "TEX",
	14: "TOR",
	15: "ATL",
	16: "CHC",
	17: "CIN",
	18: "HOU",
	19: "LAD",
	20: "WSH",
	21: "NYM",
	22: "PHI",
	23: "PIT",
	24: "STL",
	25: "SD",
	26: "SF",
	27: "COL",
	28: "MIA",
	29: "ARI",
	30: "TB",
}

func inferRole(defaultPositionID int, eligibleSlots []int) (string, bool) {
	hasSP := false
	hasRP := false
	for _, slot := range eligibleSlots {
		if slot == 14 {
			hasSP = true
		}
		if slot == 15 {
			hasRP = true
		}
	}
	switch {
	case hasSP && !hasRP:
		return "SP", true
	case hasRP && !hasSP:
		return "RP", true
	case hasSP && hasRP:
		return "P", true
	}

	if defaultPositionID == 14 {
		return "SP", true
	}
	if defaultPositionID == 15 {
		return "RP", true
	}
	if defaultPositionID == 13 {
		return "P", true
	}
	// ESPN free-agent payloads often use position id 1 for pitchers.
	if defaultPositionID == 1 {
		return "P", true
	}
	for _, slot := range eligibleSlots {
		if isPitcherSlot(slot) {
			return roleFromSlot(slot), true
		}
	}
	return "UNKNOWN", false
}

func roleFromSlot(slot int) string {
	switch slot {
	case 14:
		return "SP"
	case 15:
		return "RP"
	case 13:
		return "P"
	default:
		return "P"
	}
}

func isPitcherSlot(slot int) bool {
	switch slot {
	case 13, 14, 15:
		return true
	default:
		return false
	}
}

func lineupSlotLabel(slot int) string {
	switch slot {
	case 13:
		return "P"
	case 14:
		return "SP"
	case 15:
		return "RP"
	case 16:
		return "BE"
	case 17:
		return "IL"
	default:
		return fmt.Sprintf("slot_%d", slot)
	}
}

func findTeam(teams []teamResponse, teamID int64) (teamResponse, bool) {
	for _, t := range teams {
		if t.ID == teamID {
			return t, true
		}
	}
	return teamResponse{}, false
}

func teamDisplayName(team teamResponse) string {
	if strings.TrimSpace(team.Name) != "" {
		return strings.TrimSpace(team.Name)
	}
	joined := strings.TrimSpace(strings.TrimSpace(team.Location) + " " + strings.TrimSpace(team.Nickname))
	if joined == "" {
		return fmt.Sprintf("Team %d", team.ID)
	}
	return joined
}

func countPitchers(rows []espn.RosterSnapshot) int {
	count := 0
	for _, row := range rows {
		if row.IsPitcher {
			count++
		}
	}
	return count
}

func warningMessages(rows []espn.ParseWarningInput) []string {
	out := make([]string, 0, len(rows))
	for _, w := range rows {
		out = append(out, w.Message)
	}
	return out
}

func parseMatchupSummary(payload []byte, seasonSchedulePayload []byte, cfg config.Config, endpoint string, responseStatus int, opts MatchupOptions) (espn.MatchupSummary, error) {
	var root map[string]any
	if err := json.Unmarshal(payload, &root); err != nil {
		return espn.MatchupSummary{}, fmt.Errorf("parse ESPN matchup payload: %w", err)
	}

	teamID, err := strconv.ParseInt(strings.TrimSpace(cfg.League.TeamID), 10, 64)
	if err != nil {
		return espn.MatchupSummary{}, fmt.Errorf("league.team_id must be numeric for ESPN endpoint: %q", cfg.League.TeamID)
	}

	matchupPeriod := toInt(mapPath(root, "status", "currentMatchupPeriod"))
	if opts.MatchupPeriodID != nil && *opts.MatchupPeriodID > 0 {
		matchupPeriod = *opts.MatchupPeriodID
	}
	if matchupPeriod <= 0 {
		return espn.MatchupSummary{}, fmt.Errorf("unable to determine matchup period")
	}

	var teamNameByID = map[int64]string{}
	if teams, ok := root["teams"].([]any); ok {
		for _, raw := range teams {
			m, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			id := int64(toInt(m["id"]))
			if id <= 0 {
				continue
			}
			name := strings.TrimSpace(toString(m["name"]))
			if name == "" {
				loc := strings.TrimSpace(toString(m["location"]))
				nn := strings.TrimSpace(toString(m["nickname"]))
				name = strings.TrimSpace(strings.TrimSpace(loc) + " " + strings.TrimSpace(nn))
			}
			if name == "" {
				name = fmt.Sprintf("Team %d", id)
			}
			teamNameByID[id] = name
		}
	}

	var sched map[string]any
	if schedule, ok := root["schedule"].([]any); ok {
		for _, raw := range schedule {
			m, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if toInt(m["matchupPeriodId"]) != matchupPeriod {
				continue
			}
			home, _ := m["home"].(map[string]any)
			away, _ := m["away"].(map[string]any)
			if toInt64(mapPath(home, "teamId")) == teamID || toInt64(mapPath(away, "teamId")) == teamID {
				sched = m
				break
			}
		}
	}
	if sched == nil {
		return espn.MatchupSummary{}, fmt.Errorf("no matchup found for team_id=%d in matchup_period=%d", teamID, matchupPeriod)
	}

	home, _ := sched["home"].(map[string]any)
	away, _ := sched["away"].(map[string]any)
	homeID := toInt64(mapPath(home, "teamId"))
	isHome := homeID == teamID
	self := away
	opp := home
	if isHome {
		self = home
		opp = away
	}

	selfStarts := parseStartsUsed(self)
	selfPoints := pickLiveOrTotalPoints(self)
	oppPoints := pickLiveOrTotalPoints(opp)

	scoringPeriodID := toInt(sched["scoringPeriodId"])
	if scoringPeriodID <= 0 {
		scoringPeriodID = toInt(root["scoringPeriodId"])
	}

	span := deriveMatchupScoringPeriodSpan(root, seasonSchedulePayload, self, opp, matchupPeriod, scoringPeriodID)
	periodCount := span.Count
	if periodCount <= 0 {
		periodCount = 7
	}

	var startsMax *int
	var startsRemaining *int
	lineupLimit := parseStartsLimitPerScoringPeriod(root)
	if lineupLimit > 0 {
		max := int(math.Round(lineupLimit * float64(periodCount)))
		startsMax = &max
		remain := max - selfStarts
		startsRemaining = &remain
	}

	out := espn.MatchupSummary{
		LeagueID:                  cfg.League.LeagueID,
		Season:                    cfg.League.Season,
		TeamID:                    cfg.League.TeamID,
		TeamName:                  teamNameByID[teamID],
		MatchupPeriod:             matchupPeriod,
		OpponentTeamID:            int64Ptr(toInt64(mapPath(opp, "teamId"))),
		OpponentName:              teamNameByID[toInt64(mapPath(opp, "teamId"))],
		IsHome:                    isHome,
		TeamPoints:                selfPoints,
		OpponentPoints:            oppPoints,
		MatchupScoringPeriodStart: intPtrOrNil(span.Start),
		MatchupScoringPeriodEnd:   intPtrOrNil(span.End),
		MatchupScoringPeriodCount: span.Count,
		ConfiguredMatchupWeeks:    span.ConfiguredWeeks,
		MultiWeekScoringMatchup:   span.Count > 7 || span.ConfiguredWeeks > 1,
		PitchingStartsUsed:        selfStarts,
		PitchingStartsMax:         startsMax,
		PitchingStartsRemaining:   startsRemaining,
		StartsLimitExceeded:       toBool(mapPath(self, "cumulativeScore", "statBySlot", "22", "limitExceeded")),
		ScoringPeriodID:           intPtrOrNil(scoringPeriodID),
		SourceEndpoint:            endpoint,
		ResponseStatus:            responseStatus,
		CheckedAt:                 time.Now().UTC().Format(time.RFC3339),
	}
	if out.TeamName == "" {
		out.TeamName = fmt.Sprintf("Team %d", teamID)
	}
	if out.OpponentName == "" && out.OpponentTeamID != nil {
		out.OpponentName = fmt.Sprintf("Team %d", *out.OpponentTeamID)
	}
	return out, nil
}

func parseStartsUsed(side map[string]any) int {
	return int(math.Round(toFloat64(mapPath(side, "cumulativeScore", "statBySlot", "22", "value"))))
}

func parseStartsLimitPerScoringPeriod(root map[string]any) float64 {
	return toFloat64(mapPath(root, "settings", "rosterSettings", "lineupSlotStatLimits", "22", "limitValue"))
}

type matchupScoringPeriodSpan struct {
	Start           int
	End             int
	Count           int
	ConfiguredWeeks int
}

func deriveMatchupScoringPeriodSpan(root map[string]any, seasonSchedulePayload []byte, self map[string]any, opp map[string]any, matchupPeriod int, scoringPeriodID int) matchupScoringPeriodSpan {
	start := matchupStartScoringPeriod(self, opp, scoringPeriodID)
	if start <= 0 {
		return matchupScoringPeriodSpan{}
	}
	configuredWeeks := matchupConfiguredWeekCount(root, matchupPeriod)
	baseDays := configuredWeeks * 7
	if configuredWeeks <= 0 || baseDays <= 0 {
		configuredWeeks = 1
		baseDays = 7
	}
	end := start + baseDays - 1
	gamePeriods := seasonGameScoringPeriods(seasonSchedulePayload)
	if len(gamePeriods) == 0 {
		return matchupScoringPeriodSpan{Start: start, End: end, Count: baseDays, ConfiguredWeeks: configuredWeeks}
	}

	// ESPN extends the All-Star matchup through the no-game break and the first
	// post-break scoring period. The game schedule endpoint exposes those empty
	// scoring periods by omission, so extend a normal week only across that gap.
	probe := end + 1
	gapDays := 0
	for gapDays < 14 && !gamePeriods[probe] {
		gapDays++
		probe++
	}
	if gapDays > 0 && gamePeriods[probe] {
		end = probe
	}
	return matchupScoringPeriodSpan{Start: start, End: end, Count: end - start + 1, ConfiguredWeeks: configuredWeeks}
}

func matchupConfiguredWeekCount(root map[string]any, matchupPeriod int) int {
	if matchupPeriod <= 0 {
		return 0
	}
	raw, ok := mapPath(root, "settings", "scheduleSettings", "matchupPeriods").(map[string]any)
	if !ok {
		return 0
	}
	periods, ok := raw[strconv.Itoa(matchupPeriod)].([]any)
	if !ok || len(periods) == 0 {
		return 0
	}
	return len(periods)
}

func matchupStartScoringPeriod(self map[string]any, opp map[string]any, scoringPeriodID int) int {
	start := 0
	for _, side := range []map[string]any{self, opp} {
		raw, ok := side["pointsByScoringPeriod"].(map[string]any)
		if !ok {
			continue
		}
		for k := range raw {
			sp, err := strconv.Atoi(k)
			if err != nil || sp <= 0 {
				continue
			}
			if start == 0 || sp < start {
				start = sp
			}
		}
	}
	if start <= 0 && scoringPeriodID > 0 {
		start = scoringPeriodID
	}
	return start
}

func seasonGameScoringPeriods(payload []byte) map[int]bool {
	out := map[int]bool{}
	if len(payload) == 0 {
		return out
	}
	var root map[string]any
	if err := json.Unmarshal(payload, &root); err != nil {
		return out
	}
	proTeams, ok := mapPath(root, "settings", "proTeams").([]any)
	if !ok {
		return out
	}
	for _, rawTeam := range proTeams {
		team, ok := rawTeam.(map[string]any)
		if !ok {
			continue
		}
		byPeriod, ok := team["proGamesByScoringPeriod"].(map[string]any)
		if !ok {
			continue
		}
		for key, rawGames := range byPeriod {
			games, ok := rawGames.([]any)
			if !ok || len(games) == 0 {
				continue
			}
			sp, err := strconv.Atoi(key)
			if err != nil || sp <= 0 {
				continue
			}
			out[sp] = true
		}
	}
	return out
}

func pickLiveOrTotalPoints(side map[string]any) float64 {
	live := toFloat64(side["totalPointsLive"])
	if live != 0 {
		return live
	}
	return toFloat64(side["totalPoints"])
}

func parseFreeAgentCandidatesPayload(payload []byte, searchRaw string, teamRaw string, limit int, timezoneRaw ...string) ([]espn.FreeAgentCandidate, []espn.ParseWarningInput) {
	search := strings.ToLower(strings.TrimSpace(searchRaw))
	teamFilter := strings.ToUpper(strings.TrimSpace(teamRaw))
	tz := fantasyTimezone(firstString(timezoneRaw, "America/New_York"))
	seen := map[string]struct{}{}
	out := []espn.FreeAgentCandidate{}
	warnings := []espn.ParseWarningInput{}

	var root any
	if err := json.Unmarshal(payload, &root); err != nil {
		return nil, []espn.ParseWarningInput{{
			WarningType: "candidate_payload_parse",
			Message:     fmt.Sprintf("failed to parse free-agent payload: %v", err),
		}}
	}

	appendCandidate := func(playerMap map[string]any, acquisitionStatusRaw string, waiverProcessRaw any) {
		name, _ := pickString(playerMap, "fullName", "playerName", "name")
		if strings.TrimSpace(name) == "" {
			return
		}
		if search != "" && !strings.Contains(strings.ToLower(name), search) {
			return
		}
		playerID := pickInt64(playerMap, "id", "playerId")
		key := matching.NormalizeName(name)
		if playerID != nil {
			key = fmt.Sprintf("%s:%d", key, *playerID)
		}
		if _, ok := seen[key]; ok {
			return
		}

		abbrev, _ := pickString(playerMap, "proTeamAbbrev")
		proTeamID := pickInt(playerMap, "proTeamId")
		mlbTeam := resolveMLBTeam(abbrev, proTeamID)
		if teamFilter != "" && teamFilter != strings.ToUpper(mlbTeam) {
			return
		}

		defaultPos := pickInt(playerMap, "defaultPositionId")
		eligibleSlots := pickIntSlice(playerMap, "eligibleSlots")
		role, isPitcher := inferRole(defaultPos, eligibleSlots)
		if !isPitcher {
			return
		}
		statusTag, _ := pickString(playerMap, "injuryStatus")
		acquisitionStatus := strings.TrimSpace(acquisitionStatusRaw)
		if acquisitionStatus == "" {
			acquisitionStatus, _ = pickString(playerMap, "status")
		}
		acquisitionStatus = espn.NormalizeAcquisitionStatus(acquisitionStatus)
		if acquisitionStatus == "" {
			acquisitionStatus = espn.AcquisitionStatusFreeAgent
		}
		if waiverProcessRaw == nil {
			waiverProcessRaw = playerMap["waiverProcessDate"]
		}
		waiverProcessDatetime := formatWaiverProcessDatetime(waiverProcessRaw, tz)
		rawJSON, _ := json.Marshal(playerMap)
		row := espn.FreeAgentCandidate{
			ESPNPlayerID:          playerID,
			PlayerName:            strings.TrimSpace(name),
			NormalizedName:        matching.NormalizeName(name),
			MLBTeam:               mlbTeam,
			IsPitcher:             true,
			Role:                  role,
			AcquisitionStatus:     acquisitionStatus,
			WaiverProcessDatetime: waiverProcessDatetime,
			StatusTag:             strings.TrimSpace(statusTag),
			RawPlayerJSON:         string(rawJSON),
			CreatedAt:             time.Now().UTC(),
		}
		seen[key] = struct{}{}
		out = append(out, row)
		if role == "UNKNOWN" {
			warnings = append(warnings, espn.ParseWarningInput{
				WarningType: "candidate_role_uncertain",
				Message:     fmt.Sprintf("role uncertain for candidate %q", row.PlayerName),
			})
		}
	}

	rootMap, _ := root.(map[string]any)
	if rootMap != nil {
		if players, ok := rootMap["players"].([]any); ok {
			for _, raw := range players {
				entry, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				playerMap := entry
				if nested, ok := entry["player"].(map[string]any); ok {
					playerMap = nested
				}
				entryStatus, _ := pickString(entry, "status")
				appendCandidate(playerMap, entryStatus, entry["waiverProcessDate"])
			}
		}
	}

	// Fallback for payload variants that do not expose players[].
	if len(out) == 0 {
		visitAny(root, func(playerMap map[string]any) {
			appendCandidate(playerMap, "", nil)
		})
	}

	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i].PlayerName) < strings.ToLower(out[j].PlayerName)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, warnings
}

func visitAny(v any, onPlayer func(map[string]any)) {
	switch t := v.(type) {
	case map[string]any:
		if looksLikePlayerMap(t) {
			onPlayer(t)
		}
		for _, v2 := range t {
			visitAny(v2, onPlayer)
		}
	case []any:
		for _, item := range t {
			visitAny(item, onPlayer)
		}
	}
}

func looksLikePlayerMap(m map[string]any) bool {
	_, hasName := m["fullName"]
	_, hasTeam := m["proTeamAbbrev"]
	_, hasPos := m["defaultPositionId"]
	return hasName && (hasTeam || hasPos)
}

func pickString(m map[string]any, keys ...string) (string, bool) {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok {
				return s, true
			}
		}
	}
	return "", false
}

func pickInt(m map[string]any, key string) int {
	if v, ok := m[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		}
	}
	return 0
}

func pickInt64(m map[string]any, keys ...string) *int64 {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			if iv, ok := anyInt64(v); ok {
				return &iv
			}
		}
	}
	return nil
}

func anyInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	case json.Number:
		iv, err := n.Int64()
		return iv, err == nil
	default:
		return 0, false
	}
}

func firstString(values []string, fallback string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return fallback
}

func fantasyTimezone(raw string) *time.Location {
	loc, err := time.LoadLocation(strings.TrimSpace(raw))
	if err != nil {
		loc, _ = time.LoadLocation("America/New_York")
	}
	if loc == nil {
		return time.Local
	}
	return loc
}

func formatWaiverProcessDatetime(raw any, loc *time.Location) *string {
	ms, ok := anyInt64(raw)
	if !ok || ms <= 0 {
		return nil
	}
	if loc == nil {
		loc = time.Local
	}
	formatted := time.UnixMilli(ms).In(loc).Format(time.RFC3339)
	return &formatted
}

func pickIntSlice(m map[string]any, key string) []int {
	v, ok := m[key]
	if !ok {
		return nil
	}
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]int, 0, len(items))
	for _, item := range items {
		switch n := item.(type) {
		case float64:
			out = append(out, int(n))
		case int:
			out = append(out, n)
		}
	}
	return out
}

func mapPath(v any, keys ...string) any {
	cur := v
	for _, key := range keys {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		next, ok := m[key]
		if !ok {
			return nil
		}
		cur = next
	}
	return cur
}

func toString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	default:
		return fmt.Sprintf("%v", v)
	}
}

func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case float32:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	default:
		return 0
	}
}

func toInt64(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case float32:
		return int64(n)
	case int:
		return int64(n)
	case int64:
		return n
	case json.Number:
		i, _ := n.Int64()
		return i
	default:
		return 0
	}
}

func toFloat64(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	default:
		return 0
	}
}

func toBool(v any) bool {
	switch b := v.(type) {
	case bool:
		return b
	default:
		return false
	}
}

func int64Ptr(v int64) *int64 {
	if v <= 0 {
		return nil
	}
	out := v
	return &out
}

func intPtrOrNil(v int) *int {
	if v <= 0 {
		return nil
	}
	out := v
	return &out
}
