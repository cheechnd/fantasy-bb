package transactions

import "time"

type Bucket string

const (
	BucketStrongMove   Bucket = "strong_move"
	BucketMarginalMove Bucket = "marginal_move"
	BucketRiskyMove    Bucket = "risky_move"
	BucketWatchOnly    Bucket = "watch_only"
)

type ReviewState string

const (
	ReviewStatePending  ReviewState = "pending"
	ReviewStateApproved ReviewState = "approved"
	ReviewStateRejected ReviewState = "rejected"
	ReviewStateDeferred ReviewState = "deferred"
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
	AddStartDate            string                 `json:"add_start_date,omitempty"`
	AddStartOpponent        string                 `json:"add_start_opponent,omitempty"`
	DropPlayerName          string                 `json:"drop_player_name"`
	DropPlayerTeam          string                 `json:"drop_player_team,omitempty"`
	DropESPNPlayerID        *int64                 `json:"drop_espn_player_id,omitempty"`
	DropBestStartDate       string                 `json:"drop_best_start_date,omitempty"`
	DropBestStartOpponent   string                 `json:"drop_best_start_opponent,omitempty"`
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

type ReviewedPlanItem struct {
	PlanItem
	ReviewState   ReviewState `json:"review_state"`
	ReviewNote    string      `json:"review_note,omitempty"`
	ReviewUpdated time.Time   `json:"review_updated_at"`
}

type PlanReview struct {
	Plan        *Plan                 `json:"plan"`
	Items       []ReviewedPlanItem    `json:"items"`
	StateCounts map[ReviewState]int64 `json:"state_counts"`
}

type ReviewDecision struct {
	TransactionPlanItemID int64       `json:"transaction_plan_item_id"`
	PlanID                int64       `json:"plan_id"`
	PreviousState         ReviewState `json:"previous_state"`
	NewState              ReviewState `json:"new_state"`
	Note                  string      `json:"note,omitempty"`
	ChangedAt             time.Time   `json:"changed_at"`
}

type ApprovalQueueItem struct {
	TransactionPlanItemID int64       `json:"transaction_plan_item_id"`
	PlanID                int64       `json:"plan_id"`
	Bucket                Bucket      `json:"bucket"`
	AddPlayerName         string      `json:"add_player_name"`
	AddPlayerTeam         string      `json:"add_player_team,omitempty"`
	DropPlayerName        string      `json:"drop_player_name"`
	DropPlayerTeam        string      `json:"drop_player_team,omitempty"`
	DeltaFPTS             *float64    `json:"delta_fpts,omitempty"`
	Note                  string      `json:"note,omitempty"`
	ApprovedAt            time.Time   `json:"approved_at"`
	State                 ReviewState `json:"state"`
}

type ApprovalStateRow struct {
	TransactionPlanItemID int64       `json:"transaction_plan_item_id"`
	PlanID                int64       `json:"plan_id"`
	Bucket                Bucket      `json:"bucket"`
	AddPlayerName         string      `json:"add_player_name"`
	DropPlayerName        string      `json:"drop_player_name"`
	DeltaFPTS             *float64    `json:"delta_fpts,omitempty"`
	CurrentState          ReviewState `json:"current_state"`
	Note                  string      `json:"note,omitempty"`
	UpdatedAt             time.Time   `json:"updated_at"`
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
	WontDropMinPercentOwned        float64
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

type AdHocRequestState string

const (
	AdHocStateCreated    AdHocRequestState = "created"
	AdHocStateResolved   AdHocRequestState = "resolved"
	AdHocStateUnresolved AdHocRequestState = "unresolved"
	AdHocStatePreflight  AdHocRequestState = "preflighted"
	AdHocStateExecuted   AdHocRequestState = "executed"
	AdHocStateFailed     AdHocRequestState = "failed"
)

type AdHocResolutionStatus string

const (
	AdHocResolutionResolved    AdHocResolutionStatus = "resolved"
	AdHocResolutionAmbiguous   AdHocResolutionStatus = "ambiguous"
	AdHocResolutionUnresolved  AdHocResolutionStatus = "unresolved"
	AdHocResolutionInvalidType AdHocResolutionStatus = "invalid_target_type"
)

type AdHocRequest struct {
	ID                       int64                 `json:"id"`
	RequestedAddPlayerName   string                `json:"requested_add_player_name"`
	RequestedDropPlayerName  string                `json:"requested_drop_player_name"`
	NormalizedAddLookup      string                `json:"normalized_add_lookup"`
	NormalizedDropLookup     string                `json:"normalized_drop_lookup"`
	ResolvedAddPlayerName    string                `json:"resolved_add_player_name,omitempty"`
	ResolvedAddESPNPlayerID  *int64                `json:"resolved_add_espn_player_id,omitempty"`
	ResolvedDropPlayerName   string                `json:"resolved_drop_player_name,omitempty"`
	ResolvedDropESPNPlayerID *int64                `json:"resolved_drop_espn_player_id,omitempty"`
	RequestState             AdHocRequestState     `json:"request_state"`
	ResolutionStatus         AdHocResolutionStatus `json:"resolution_status"`
	ResolutionNotes          map[string]any        `json:"resolution_notes,omitempty"`
	LinkedPlanID             *int64                `json:"linked_plan_id,omitempty"`
	LinkedPlanItemID         *int64                `json:"linked_plan_item_id,omitempty"`
	LinkedExecutionAttemptID *int64                `json:"linked_execution_attempt_id,omitempty"`
	CreatedAt                time.Time             `json:"created_at"`
	UpdatedAt                time.Time             `json:"updated_at"`
}

type AdHocRequestEvent struct {
	ID        int64          `json:"id"`
	RequestID int64          `json:"request_id"`
	EventType string         `json:"event_type"`
	EventData map[string]any `json:"event_data,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}
