package planner

import "time"

type Bucket string

const (
	BucketAutoStart        Bucket = "auto_start"
	BucketLikelyStart      Bucket = "likely_start"
	BucketMonitor          Bucket = "monitor"
	BucketBench            Bucket = "bench"
	BucketNoStartScheduled Bucket = "no_start_scheduled"
)

type Plan struct {
	ID            int64       `json:"id"`
	SyncRunID     *int64      `json:"sync_run_id,omitempty"`
	ImportRunID   *int64      `json:"import_run_id,omitempty"`
	AnalysisRunID *int64      `json:"analysis_run_id,omitempty"`
	WindowStart   string      `json:"window_start"`
	WindowEnd     string      `json:"window_end"`
	CreatedAt     time.Time   `json:"created_at"`
	Status        string      `json:"status"`
	SummaryJSON   string      `json:"summary_json,omitempty"`
	Summary       PlanSummary `json:"summary"`
	Items         []PlanItem  `json:"items"`
}

type PlanSummary struct {
	Counts map[Bucket]int `json:"counts"`
}

type PlanItem struct {
	ID                  int64                  `json:"id,omitempty"`
	PlanID              int64                  `json:"plan_id,omitempty"`
	Bucket              Bucket                 `json:"bucket"`
	PlayerName          string                 `json:"player_name"`
	MLBTeam             string                 `json:"mlb_team,omitempty"`
	ESPNPlayerID        *int64                 `json:"espn_player_id,omitempty"`
	MatchedPitcherName  string                 `json:"matched_pitcher_name,omitempty"`
	ProjectedStartCount int                    `json:"projected_start_count"`
	TotalProjectedFPTS  *float64               `json:"total_projected_fpts,omitempty"`
	ResultRank          *int                   `json:"result_rank,omitempty"`
	Flags               []string               `json:"flags,omitempty"`
	Notes               []string               `json:"notes,omitempty"`
	Details             map[string]interface{} `json:"details,omitempty"`
	CreatedAt           time.Time              `json:"created_at"`
}

type RuleConfig struct {
	AutoStartMinTotalFPTS    float64
	LikelyStartMinTotalFPTS  float64
	MonitorMinTotalFPTS      float64
	TwoStartAutoStartBonus   float64
	TBDPenalty               float64
	MissingProjectionPenalty float64
	AmbiguousMatchPenalty    float64
}

type CreateInput struct {
	SyncRunID     *int64
	ImportRunID   *int64
	AnalysisRunID *int64
	WindowStart   string
	WindowEnd     string
	Status        string
	Summary       map[string]interface{}
	Items         []PlanItem
}
