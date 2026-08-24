package pickups

import "time"

type ItemType string

const (
	ItemTypeTopCandidate ItemType = "top_candidate"
	ItemTypeUnmatched    ItemType = "unmatched"
)

type CandidateProjection struct {
	PlayerName            string    `json:"player_name"`
	MLBTeam               string    `json:"mlb_team,omitempty"`
	ESPNPlayerID          *int64    `json:"espn_player_id,omitempty"`
	AcquisitionStatus     string    `json:"acquisition_status,omitempty"`
	WaiverProcessDatetime *string   `json:"waiver_process_datetime"`
	MatchedPitcherName    string    `json:"matched_pitcher_name,omitempty"`
	ProjectedStartCount   int       `json:"projected_start_count"`
	TotalProjectedFPTS    float64   `json:"total_projected_fpts"`
	AverageProjectedFPTS  float64   `json:"average_projected_fpts"`
	HighestSingleFPTS     float64   `json:"highest_single_start_fpts"`
	Starts                []Start   `json:"starts,omitempty"`
	Flags                 []string  `json:"flags,omitempty"`
	Notes                 []string  `json:"notes,omitempty"`
	Unmatched             bool      `json:"unmatched,omitempty"`
	CreatedAt             time.Time `json:"created_at,omitempty"`
}

type Start struct {
	Date          string   `json:"date"`
	Opponent      string   `json:"opponent"`
	ProjectedFPTS *float64 `json:"projected_fpts,omitempty"`
	Status        string   `json:"status"`
}

type RecommendationItem struct {
	ID                   int64                  `json:"id,omitempty"`
	RecommendationRunID  int64                  `json:"recommendation_run_id,omitempty"`
	ItemType             ItemType               `json:"item_type"`
	PlayerName           string                 `json:"player_name"`
	MLBTeam              string                 `json:"mlb_team,omitempty"`
	ESPNPlayerID         *int64                 `json:"espn_player_id,omitempty"`
	MatchedPitcherName   string                 `json:"matched_pitcher_name,omitempty"`
	ProjectedStartCount  int                    `json:"projected_start_count"`
	TotalProjectedFPTS   *float64               `json:"total_projected_fpts,omitempty"`
	ComparisonTargetName string                 `json:"comparison_target_name,omitempty"`
	ComparisonDeltaFPTS  *float64               `json:"comparison_delta_fpts,omitempty"`
	ResultRank           *int                   `json:"result_rank,omitempty"`
	Flags                []string               `json:"flags,omitempty"`
	Notes                []string               `json:"notes,omitempty"`
	Details              map[string]interface{} `json:"details,omitempty"`
	CreatedAt            time.Time              `json:"created_at"`
}

type RecommendationRun struct {
	ID             int64                `json:"id"`
	SyncRunID      *int64               `json:"sync_run_id,omitempty"`
	ImportRunID    *int64               `json:"import_run_id,omitempty"`
	CandidateRunID *int64               `json:"candidate_run_id,omitempty"`
	WindowStart    string               `json:"window_start"`
	WindowEnd      string               `json:"window_end"`
	CreatedAt      time.Time            `json:"created_at"`
	Status         string               `json:"status"`
	SummaryJSON    string               `json:"summary_json,omitempty"`
	Items          []RecommendationItem `json:"items,omitempty"`
}

type RecommendResult struct {
	RecommendationRunID int64                `json:"recommendation_run_id"`
	SyncRunID           *int64               `json:"sync_run_id,omitempty"`
	ImportRunID         *int64               `json:"import_run_id,omitempty"`
	CandidateRunID      *int64               `json:"candidate_run_id,omitempty"`
	WindowStart         string               `json:"window_start"`
	WindowEnd           string               `json:"window_end"`
	TopCandidates       []RecommendationItem `json:"top_candidates"`
	Unmatched           []RecommendationItem `json:"unmatched"`
	Items               []RecommendationItem `json:"items"`
}

type RecommendOptions struct {
	From           time.Time
	To             time.Time
	SyncRunID      *int64
	ImportRunID    *int64
	CandidateRunID *int64
	TopN           int
	MinTotalFPTS   *float64
}
