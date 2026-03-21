package transactions

import "time"

type Bucket string

const (
	BucketStrongMove   Bucket = "strong_move"
	BucketMarginalMove Bucket = "marginal_move"
	BucketRiskyMove    Bucket = "risky_move"
	BucketWatchOnly    Bucket = "watch_only"
)

type Plan struct {
	ID                        int64       `json:"id"`
	SyncRunID                 *int64      `json:"sync_run_id,omitempty"`
	ImportRunID               *int64      `json:"import_run_id,omitempty"`
	PitcherPlanID             *int64      `json:"pitcher_plan_id,omitempty"`
	PickupRecommendationRunID *int64      `json:"pickup_recommendation_run_id,omitempty"`
	WindowStart               string      `json:"window_start"`
	WindowEnd                 string      `json:"window_end"`
	CreatedAt                 time.Time   `json:"created_at"`
	Status                    string      `json:"status"`
	SummaryJSON               string      `json:"summary_json,omitempty"`
	Summary                   PlanSummary `json:"summary"`
	Items                     []PlanItem  `json:"items"`
}

type PlanSummary struct {
	Counts map[Bucket]int `json:"counts"`
}

type PlanItem struct {
	ID                      int64                  `json:"id,omitempty"`
	TransactionPlanID       int64                  `json:"transaction_plan_id,omitempty"`
	Bucket                  Bucket                 `json:"bucket"`
	AddPlayerName           string                 `json:"add_player_name"`
	AddPlayerTeam           string                 `json:"add_player_team,omitempty"`
	AddESPNPlayerID         *int64                 `json:"add_espn_player_id,omitempty"`
	DropPlayerName          string                 `json:"drop_player_name"`
	DropPlayerTeam          string                 `json:"drop_player_team,omitempty"`
	DropESPNPlayerID        *int64                 `json:"drop_espn_player_id,omitempty"`
	AddProjectedStartCount  int                    `json:"add_projected_start_count"`
	AddTotalProjectedFPTS   *float64               `json:"add_total_projected_fpts,omitempty"`
	DropProjectedStartCount int                    `json:"drop_projected_start_count"`
	DropTotalProjectedFPTS  *float64               `json:"drop_total_projected_fpts,omitempty"`
	DeltaFPTS               *float64               `json:"delta_fpts,omitempty"`
	ResultRank              *int                   `json:"result_rank,omitempty"`
	Flags                   []string               `json:"flags,omitempty"`
	Notes                   []string               `json:"notes,omitempty"`
	Details                 map[string]interface{} `json:"details,omitempty"`
	CreatedAt               time.Time              `json:"created_at"`
}

type Options struct {
	From          time.Time
	To            time.Time
	SyncRunID     *int64
	ImportRunID   *int64
	PitcherPlanID *int64
	PickupRunID   *int64
	TopN          int
}

type ServiceConfig struct {
	TopMoveLimit                   int
	MaxPairings                    int
	StrongMoveDeltaFPTS            float64
	MarginalMoveDeltaFPTS          float64
	RiskyMoveMinDeltaFPTS          float64
	UncertaintyPenaltyTBD          float64
	UncertaintyPenaltyMissingProj  float64
	UncertaintyPenaltyAmbiguous    float64
	AllowCompareAgainstLikelyStart bool
}

type CreatePlanInput struct {
	SyncRunID                 *int64
	ImportRunID               *int64
	PitcherPlanID             *int64
	PickupRecommendationRunID *int64
	WindowStart               string
	WindowEnd                 string
	Status                    string
	Summary                   map[string]interface{}
	Items                     []PlanItem
}
