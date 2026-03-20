package service

import (
	"context"
	"encoding/json"
	"fmt"
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
	DryRun bool
}

type ShowRosterFilter struct {
	SyncRunID    *int64
	PitchersOnly bool
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

	client := client.New(time.Duration(cfg.ESPN.TimeoutSeconds)*time.Second, "")
	fetchResult, err := client.FetchLeague(ctx, cfg, creds)
	if err != nil {
		return espn.SyncSummary{}, err
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
		DryRun:             opts.DryRun,
	}

	if opts.DryRun {
		return summary, nil
	}

	summaryJSON := map[string]any{
		"rostered_players": len(parsed.Roster),
		"pitcher_count":    summary.PitcherCount,
		"warnings":         warningMessages(parsed.Warnings),
		"source_endpoint":  fetchResult.Endpoint,
	}

	runID, err := s.repo.PersistSync(ctx, repository.PersistSyncInput{
		SyncType:     "roster",
		LeagueID:     cfg.League.LeagueID,
		TeamID:       cfg.League.TeamID,
		Season:       cfg.League.Season,
		Status:       "success",
		WarningCount: warningCount,
		Summary:      summaryJSON,
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
	return s.repo.LatestRoster(ctx, filter.SyncRunID, filter.PitchersOnly)
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
			Notes:      "source=espn",
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
