package pitchers

import (
	"time"

	"fantasy-baseball/internal/execute"
)

type ActionType string

const (
	ActionActivatePitcher    ActionType = "activate_pitcher"
	ActionBenchPitcher       ActionType = "bench_pitcher"
	ActionNoActionNeeded     ActionType = "no_action_needed"
	ActionAmbiguousOrBlocked ActionType = "ambiguous_or_blocked"
)

type ReviewState string

const (
	ReviewStatePending  ReviewState = "pending"
	ReviewStateApproved ReviewState = "approved"
	ReviewStateRejected ReviewState = "rejected"
	ReviewStateDeferred ReviewState = "deferred"
)

type Plan struct {
	ID            int64          `json:"id"`
	PitcherPlanID *int64         `json:"pitcher_plan_id,omitempty"`
	SyncRunID     *int64         `json:"sync_run_id,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	Status        string         `json:"status"`
	Summary       map[string]any `json:"summary"`
	Items         []PlanItem     `json:"items,omitempty"`
}

type PlanItem struct {
	ID           int64          `json:"id"`
	LineupPlanID int64          `json:"lineup_plan_id"`
	ActionType   ActionType     `json:"action_type"`
	PlayerName   string         `json:"player_name"`
	ESPNPlayerID *int64         `json:"espn_player_id,omitempty"`
	CurrentSlot  string         `json:"current_slot,omitempty"`
	TargetSlot   string         `json:"target_slot,omitempty"`
	Rationale    map[string]any `json:"rationale,omitempty"`
	Flags        []string       `json:"flags,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
}

type ReviewedPlanItem struct {
	PlanItem
	ReviewState   ReviewState `json:"review_state"`
	ReviewNote    string      `json:"review_note,omitempty"`
	ReviewUpdated time.Time   `json:"review_updated_at"`
}

type ReviewDecision struct {
	LineupPlanItemID int64       `json:"lineup_plan_item_id"`
	PlanID           int64       `json:"plan_id"`
	PreviousState    ReviewState `json:"previous_state"`
	NewState         ReviewState `json:"new_state"`
	Note             string      `json:"note,omitempty"`
	ChangedAt        time.Time   `json:"changed_at"`
}

type QueueItem struct {
	LineupPlanItemID int64       `json:"lineup_plan_item_id"`
	PlanID           int64       `json:"plan_id"`
	ActionType       ActionType  `json:"action_type"`
	PlayerName       string      `json:"player_name"`
	CurrentSlot      string      `json:"current_slot,omitempty"`
	TargetSlot       string      `json:"target_slot,omitempty"`
	Note             string      `json:"note,omitempty"`
	ApprovedAt       time.Time   `json:"approved_at"`
	State            ReviewState `json:"state"`
}

type ContextOptions struct {
	SyncRunID         *int64  `json:"sync_run_id,omitempty"`
	ScoringPeriodID   *int    `json:"scoring_period_id,omitempty"`
	ScoringPeriodDate *string `json:"scoring_period_date"`
	EffectiveNextDay  bool    `json:"effective_next_day"`
}

type PreflightItem struct {
	LineupPlanItemID        int64                    `json:"lineup_plan_item_id"`
	PlanID                  int64                    `json:"plan_id"`
	PlayerName              string                   `json:"player_name"`
	ActionType              ActionType               `json:"action_type"`
	ValidationStatus        execute.ValidationStatus `json:"validation_status"`
	Reasons                 []execute.Reason         `json:"reasons"`
	CurrentSlot             string                   `json:"current_slot,omitempty"`
	TargetSlot              string                   `json:"target_slot,omitempty"`
	TargetScoringPeriodID   *int                     `json:"target_scoring_period_id,omitempty"`
	TargetScoringPeriodDate *string                  `json:"target_scoring_period_date"`
	EffectiveNextDay        bool                     `json:"effective_next_day"`
	CheckedAt               time.Time                `json:"checked_at"`
}

type PreflightResult struct {
	Items []PreflightItem `json:"items"`
}

type ExecutionAttempt struct {
	ID                   int64                      `json:"id"`
	ApprovedLineupItemID int64                      `json:"approved_lineup_item_id"`
	LineupPlanID         int64                      `json:"lineup_plan_id"`
	StartedAt            time.Time                  `json:"started_at"`
	CompletedAt          *time.Time                 `json:"completed_at,omitempty"`
	ExecutionStatus      execute.ExecutionStatus    `json:"execution_status"`
	VerificationStatus   execute.VerificationStatus `json:"verification_status"`
	RequestSummary       map[string]any             `json:"request_summary,omitempty"`
	ResponseSummary      map[string]any             `json:"response_summary,omitempty"`
	ErrorMessage         string                     `json:"error_message,omitempty"`
	Details              map[string]any             `json:"details,omitempty"`
	Events               []ExecutionEvent           `json:"events,omitempty"`
}

type ExecutionEvent struct {
	ID        int64          `json:"id"`
	AttemptID int64          `json:"lineup_execution_attempt_id"`
	EventType string         `json:"event_type"`
	EventData map[string]any `json:"event_data,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}
