package pitchers

import "time"

type AnalysisType string

const (
	AnalysisTypeWeekly    AnalysisType = "weekly_pitchers"
	AnalysisTypeTwoStart  AnalysisType = "two_start"
	AnalysisTypeStreamers AnalysisType = "streamers"
	AnalysisTypeReport    AnalysisType = "report"
)

type MatchStatus string

const (
	MatchStatusMatched   MatchStatus = "matched"
	MatchStatusUnmatched MatchStatus = "unmatched"
	MatchStatusAmbiguous MatchStatus = "ambiguous_match"
)

type MatchResult struct {
	InputPlayerName      string      `json:"input_player_name"`
	InputMLBTeam         string      `json:"input_mlb_team,omitempty"`
	NormalizedLookupKey  string      `json:"normalized_lookup_key"`
	MatchStatus          MatchStatus `json:"match_status"`
	MatchedPitcherName   string      `json:"matched_pitcher_name,omitempty"`
	MatchedPitcherTeam   string      `json:"matched_pitcher_team,omitempty"`
	CandidateDisplayList []string    `json:"candidate_display_list,omitempty"`
	Explanation          string      `json:"explanation,omitempty"`
}

type PitcherStart struct {
	Date          string   `json:"date"`
	Opponent      string   `json:"opponent"`
	ProjectedFPTS *float64 `json:"projected_fpts,omitempty"`
	Status        string   `json:"status"`
}

type PitcherProjection struct {
	PlayerName           string         `json:"player_name"`
	MLBTeam              string         `json:"mlb_team,omitempty"`
	MatchedPitcherName   string         `json:"matched_pitcher_name"`
	StartCount           int            `json:"start_count"`
	Starts               []PitcherStart `json:"starts"`
	TotalProjectedFPTS   float64        `json:"total_projected_fpts"`
	AverageProjectedFPTS float64        `json:"average_projected_fpts"`
	HighestSingleFPTS    float64        `json:"highest_single_fpts"`
	Flags                []string       `json:"flags,omitempty"`
}

type AnalysisReport struct {
	AnalysisRunID    int64               `json:"analysis_run_id"`
	AnalysisType     AnalysisType        `json:"analysis_type"`
	ImportRunID      *int64              `json:"import_run_id,omitempty"`
	WindowStart      string              `json:"window_start"`
	WindowEnd        string              `json:"window_end"`
	RankedPitchers   []PitcherProjection `json:"ranked_pitchers"`
	TwoStartPitchers []PitcherProjection `json:"two_start_pitchers"`
	UnmatchedPlayers []MatchResult       `json:"unmatched_players"`
	AmbiguousPlayers []MatchResult       `json:"ambiguous_players"`
	MatchResults     []MatchResult       `json:"match_results"`
	Warnings         []string            `json:"warnings,omitempty"`
	CreatedAt        *time.Time          `json:"created_at,omitempty"`
}

type AnalysisOptions struct {
	From         time.Time
	To           time.Time
	ImportRunID  *int64
	RosterPath   string
	RosterInputs []RosterInput
	RosterSource string
	PoolPath     string
	TopN         int
	MinTotalFPTS *float64
}

type RosterInput struct {
	PlayerName string `json:"player_name"`
	MLBTeam    string `json:"mlb_team,omitempty"`
	Role       string `json:"role,omitempty"`
	Status     string `json:"status,omitempty"`
	Locked     bool   `json:"locked,omitempty"`
	MustHold   bool   `json:"must_hold,omitempty"`
	Notes      string `json:"notes,omitempty"`
}
