package relievers

import "time"

const (
	SourceESPNRelieverDepthChart = "espn_reliever_depth_chart"
	DefaultSourceURL             = "https://www.espn.com/fantasy/baseball/flb/story?page=REcloserorgchart"
)

type DepthChartRun struct {
	ID             int64     `json:"id"`
	Source         string    `json:"source"`
	SourceURL      string    `json:"source_url"`
	SourceDate     string    `json:"source_date,omitempty"`
	FetchedAt      time.Time `json:"fetched_at"`
	Status         string    `json:"status"`
	TeamCount      int       `json:"team_count"`
	RowCount       int       `json:"row_count"`
	MatchedCount   int       `json:"matched_count"`
	UnmatchedCount int       `json:"unmatched_count"`
	AmbiguousCount int       `json:"ambiguous_count"`
	ConflictCount  int       `json:"conflict_count"`
	WarningsJSON   string    `json:"warnings_json,omitempty"`
	SummaryJSON    string    `json:"summary_json,omitempty"`
}

type DepthChartEntry struct {
	ID              int64     `json:"id,omitempty"`
	RunID           int64     `json:"run_id"`
	ESPNPlayerID    *int64    `json:"espn_player_id,omitempty"`
	PlayerName      string    `json:"player_name"`
	NormalizedName  string    `json:"normalized_name"`
	MLBTeam         string    `json:"mlb_team"`
	ReliefRole      string    `json:"relief_role"`
	SourceRoleLabel string    `json:"source_role_label"`
	RosterPercent   *float64  `json:"roster_percent,omitempty"`
	MatchStatus     string    `json:"match_status"`
	MatchReason     string    `json:"match_reason,omitempty"`
	ConflictFlag    bool      `json:"conflict_flag"`
	ConflictReason  string    `json:"conflict_reason,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type SyncSummary struct {
	RunID          *int64   `json:"run_id,omitempty"`
	Source         string   `json:"source"`
	SourceURL      string   `json:"source_url"`
	SourceDate     string   `json:"source_date,omitempty"`
	FetchedAt      string   `json:"fetched_at"`
	Status         string   `json:"status"`
	TeamCount      int      `json:"team_count"`
	RowCount       int      `json:"row_count"`
	MatchedCount   int      `json:"matched_count"`
	UnmatchedCount int      `json:"unmatched_count"`
	AmbiguousCount int      `json:"ambiguous_count"`
	ConflictCount  int      `json:"conflict_count"`
	Warnings       []string `json:"warnings,omitempty"`
	DryRun         bool     `json:"dry_run"`
}

type Enrichment struct {
	ReliefRole       string `json:"relief_role,omitempty"`
	ReliefRoleTeam   string `json:"relief_role_team,omitempty"`
	ReliefRoleSource string `json:"relief_role_source,omitempty"`
	ReliefRoleAsOf   string `json:"relief_role_as_of,omitempty"`
	MatchStatus      string `json:"relief_role_match_status,omitempty"`
	ConflictFlag     bool   `json:"relief_role_conflict_flag,omitempty"`
	ConflictReason   string `json:"relief_role_conflict_reason,omitempty"`
}
