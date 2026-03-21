package monitor

import "time"

type Status string

const (
	StatusFresh       Status = "fresh"
	StatusStale       Status = "stale"
	StatusBlocked     Status = "blocked"
	StatusInvalidated Status = "invalidated"
	StatusUnknown     Status = "unknown"
)

type RecommendedAction string

const (
	ActionNoAction             RecommendedAction = "no_action"
	ActionRegeneratePlan       RecommendedAction = "regenerate_plan"
	ActionRegenerateLineup     RecommendedAction = "regenerate_lineup"
	ActionRefreshCandidatePool RecommendedAction = "refresh_candidate_pool"
	ActionRerunPickups         RecommendedAction = "rerun_pickups"
	ActionRePreflight          RecommendedAction = "re_preflight"
	ActionInspectExecution     RecommendedAction = "inspect_execution"
	ActionReconcileExecution   RecommendedAction = "reconcile_execution"
	ActionDiscardArtifact      RecommendedAction = "discard_artifact"
)

type Reason struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Item struct {
	ID                int64             `json:"id,omitempty"`
	MonitorRunID      int64             `json:"monitor_run_id,omitempty"`
	ArtifactType      string            `json:"artifact_type"`
	ArtifactID        int64             `json:"artifact_id"`
	MonitorStatus     Status            `json:"monitor_status"`
	Reasons           []Reason          `json:"reasons"`
	RecommendedAction RecommendedAction `json:"recommended_action"`
	Details           map[string]any    `json:"details,omitempty"`
	CreatedAt         time.Time         `json:"created_at"`
}

type Run struct {
	ID        int64          `json:"id"`
	RunType   string         `json:"run_type"`
	CreatedAt time.Time      `json:"created_at"`
	Status    string         `json:"status"`
	ItemCount int            `json:"item_count"`
	Summary   map[string]any `json:"summary"`
	Items     []Item         `json:"items,omitempty"`
}

type Summary struct {
	Counts map[string]map[Status]int `json:"counts"`
	Items  []Item                    `json:"items"`
}

type Config struct {
	PlansStaleHours               int
	LineupStaleHours              int
	PickupsStaleHours             int
	CandidatePoolStaleHours       int
	ApprovalStaleHours            int
	ExecutionFollowupHours        int
	RequireLiveRecheckForApproved bool
}

type EvaluateOptions struct {
	Limit      int
	LatestOnly bool
	Type       string
	ID         int64
}
