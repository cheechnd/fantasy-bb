package execute

import "time"

type RunType string

const (
	RunTypePreflight RunType = "preflight"
	RunTypeDryRun    RunType = "dry_run"
)

type ValidationStatus string

const (
	StatusExecutable ValidationStatus = "executable"
	StatusBlocked    ValidationStatus = "blocked"
	StatusStale      ValidationStatus = "stale"
	StatusConflict   ValidationStatus = "conflict"
	StatusUnknown    ValidationStatus = "unknown"
)

type Reason struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ActionPreview struct {
	ActionType              string `json:"action_type"`
	ApprovedItemID          int64  `json:"approved_item_id"`
	SourcePlanID            int64  `json:"source_plan_id"`
	AddPlayerName           string `json:"add_player_name"`
	DropPlayerName          string `json:"drop_player_name"`
	RosterSyncRunID         *int64 `json:"roster_sync_run_id,omitempty"`
	CandidateRunID          *int64 `json:"candidate_run_id,omitempty"`
	RosterCheckPassed       bool   `json:"roster_check_passed"`
	AvailabilityCheckPassed bool   `json:"availability_check_passed"`
	AddAlreadyRostered      bool   `json:"add_already_rostered"`
	ExecutionReadiness      string `json:"execution_readiness"`
	CheckedAt               string `json:"checked_at"`
}

type RunItem struct {
	ID                int64            `json:"id"`
	ExecutionRunID    int64            `json:"execution_run_id"`
	ApprovedItemID    int64            `json:"approved_item_id"`
	SourcePlanID      int64            `json:"source_plan_id"`
	AddPlayerName     string           `json:"add_player_name"`
	DropPlayerName    string           `json:"drop_player_name"`
	ValidationStatus  ValidationStatus `json:"validation_status"`
	ReadinessRank     *int             `json:"readiness_rank,omitempty"`
	ValidationReasons []Reason         `json:"validation_reasons"`
	ActionPreview     ActionPreview    `json:"action_preview"`
	Details           map[string]any   `json:"details,omitempty"`
	CreatedAt         time.Time        `json:"created_at"`
}

type Run struct {
	ID        int64          `json:"id"`
	RunType   RunType        `json:"run_type"`
	CreatedAt time.Time      `json:"created_at"`
	Status    string         `json:"status"`
	ItemCount int            `json:"item_count"`
	Summary   map[string]any `json:"summary"`
	Items     []RunItem      `json:"items,omitempty"`
}

type Options struct {
	ItemID *int64
	Limit  int
}

type QueueRow struct {
	ApprovedItemID     int64             `json:"approved_item_id"`
	SourcePlanID       int64             `json:"source_plan_id"`
	AddPlayerName      string            `json:"add_player_name"`
	DropPlayerName     string            `json:"drop_player_name"`
	ApprovedAt         time.Time         `json:"approved_at"`
	ApprovalNote       string            `json:"approval_note,omitempty"`
	LastValidation     *ValidationStatus `json:"last_validation_status,omitempty"`
	LastExecutionRunID *int64            `json:"last_execution_run_id,omitempty"`
	LastCheckedAt      *time.Time        `json:"last_checked_at,omitempty"`
}

type ServiceConfig struct {
	DefaultLimit                 int
	MaxLimit                     int
	CandidateRefreshLimit        int
	StaleHoursThreshold          int
	RequireLiveRosterCheck       bool
	RequireLiveAvailabilityCheck bool
}
