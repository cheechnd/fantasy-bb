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

type ExecutionStatus string

const (
	ExecutionStatusStarted   ExecutionStatus = "started"
	ExecutionStatusSubmitted ExecutionStatus = "submitted"
	ExecutionStatusSucceeded ExecutionStatus = "succeeded"
	ExecutionStatusFailed    ExecutionStatus = "failed"
	ExecutionStatusAborted   ExecutionStatus = "aborted"
	ExecutionStatusAmbiguous ExecutionStatus = "ambiguous"
)

type VerificationStatus string

const (
	VerificationStatusVerified           VerificationStatus = "verified"
	VerificationStatusPending            VerificationStatus = "verification_pending"
	VerificationStatusUnverified         VerificationStatus = "unverified"
	VerificationStatusVerificationFailed VerificationStatus = "verification_failed"
	VerificationStatusUnknown            VerificationStatus = "unknown"
)

type Attempt struct {
	ID                 int64              `json:"id"`
	ApprovedItemID     int64              `json:"approved_item_id"`
	SourcePlanID       int64              `json:"source_plan_id"`
	PreflightRunID     *int64             `json:"preflight_run_id,omitempty"`
	StartedAt          time.Time          `json:"started_at"`
	SubmittedAt        *time.Time         `json:"submitted_at,omitempty"`
	CompletedAt        *time.Time         `json:"completed_at,omitempty"`
	ExecutionStatus    ExecutionStatus    `json:"execution_status"`
	VerificationStatus VerificationStatus `json:"verification_status"`
	LastVerifiedAt     *time.Time         `json:"last_verified_at,omitempty"`
	AmbiguousReason    string             `json:"ambiguous_reason,omitempty"`
	AddPlayerName      string             `json:"add_player_name"`
	DropPlayerName     string             `json:"drop_player_name"`
	RequestSummary     map[string]any     `json:"request_summary,omitempty"`
	ResponseSummary    map[string]any     `json:"response_summary,omitempty"`
	FinalOutcome       map[string]any     `json:"final_outcome,omitempty"`
	ErrorMessage       string             `json:"error_message,omitempty"`
	Details            map[string]any     `json:"details,omitempty"`
	Events             []AttemptEvent     `json:"events,omitempty"`
}

type AttemptEvent struct {
	ID                 int64              `json:"id"`
	ExecutionAttemptID int64              `json:"execution_attempt_id"`
	EventType          string             `json:"event_type"`
	EventData          map[string]any     `json:"event_data,omitempty"`
	CreatedAt          time.Time          `json:"created_at"`
}

type RealExecutionOptions struct {
	ItemID  int64
	Confirm bool
}

type RealExecutionResult struct {
	Attempt       *Attempt    `json:"attempt,omitempty"`
	PreflightRun  *Run        `json:"preflight_run,omitempty"`
	PreflightItem *RunItem    `json:"preflight_item,omitempty"`
	WillWrite     bool        `json:"will_write"`
	Message       string      `json:"message"`
}

type VerifyResult struct {
	Attempt   *Attempt  `json:"attempt,omitempty"`
	Inference string    `json:"inference,omitempty"`
	Message   string    `json:"message"`
}
