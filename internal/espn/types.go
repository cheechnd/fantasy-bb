package espn

import "time"

type SyncRun struct {
	ID           int64     `json:"id"`
	SyncType     string    `json:"sync_type"`
	LeagueID     string    `json:"league_id"`
	TeamID       string    `json:"team_id"`
	Season       int       `json:"season"`
	StartedAt    time.Time `json:"started_at"`
	CompletedAt  time.Time `json:"completed_at"`
	Status       string    `json:"status"`
	WarningCount int       `json:"warning_count"`
	SummaryJSON  string    `json:"summary_json,omitempty"`
}

type RawPayload struct {
	PayloadType    string
	SourceEndpoint string
	ResponseStatus int
	PayloadJSON    string
}

type LeagueSnapshot struct {
	SyncRunID    int64     `json:"sync_run_id"`
	LeagueID     string    `json:"league_id"`
	Season       int       `json:"season"`
	LeagueName   string    `json:"league_name"`
	TeamID       string    `json:"team_id"`
	TeamName     string    `json:"team_name"`
	ScoringType  string    `json:"scoring_type,omitempty"`
	SettingsJSON string    `json:"settings_json,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type RosterSnapshot struct {
	ID             int64     `json:"id,omitempty"`
	SyncRunID      int64     `json:"sync_run_id"`
	ESPNPlayerID   *int64    `json:"espn_player_id,omitempty"`
	PlayerName     string    `json:"player_name"`
	NormalizedName string    `json:"normalized_name"`
	MLBTeam        string    `json:"mlb_team,omitempty"`
	RosterSlot     string    `json:"roster_slot,omitempty"`
	IsPitcher      bool      `json:"is_pitcher"`
	Role           string    `json:"role,omitempty"`
	StatusTag      string    `json:"status_tag,omitempty"`
	RawPlayerJSON  string    `json:"raw_player_json,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

type SyncSummary struct {
	SyncRunID          *int64 `json:"sync_run_id,omitempty"`
	LeagueID           string `json:"league_id"`
	TeamID             string `json:"team_id"`
	Season             int    `json:"season"`
	SyncedAt           string `json:"synced_at"`
	RosteredPlayers    int    `json:"rostered_players"`
	PitcherCount       int    `json:"pitcher_count"`
	WarningCount       int    `json:"warning_count"`
	SourceEndpoint     string `json:"source_endpoint"`
	ResponseStatusCode int    `json:"response_status_code"`
	DryRun             bool   `json:"dry_run"`
}

type ParseWarning struct {
	ID             int64     `json:"id"`
	SyncRunID      int64     `json:"sync_run_id"`
	WarningType    string    `json:"warning_type"`
	Message        string    `json:"message"`
	RowContextJSON string    `json:"row_context_json,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

type ParseWarningInput struct {
	WarningType string                 `json:"warning_type"`
	Message     string                 `json:"message"`
	RowContext  map[string]interface{} `json:"row_context,omitempty"`
}

type ValidateReport struct {
	OK      bool              `json:"ok"`
	Checks  []map[string]any  `json:"checks"`
	Summary map[string]string `json:"summary,omitempty"`
}
