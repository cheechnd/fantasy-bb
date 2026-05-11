package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"fantasy-baseball/internal/config"
	"fantasy-baseball/internal/execute"
	realrepo "fantasy-baseball/internal/execute/real/repository"
	pfsvc "fantasy-baseball/internal/execute/service"
	"fantasy-baseball/internal/transactions"
	tranrepo "fantasy-baseball/internal/transactions/repository"
	reviewrepo "fantasy-baseball/internal/transactions/review/repository"
)

type Writer interface {
	ExecuteAddDrop(ctx context.Context, cfg config.Config, req WriteRequest) (WriteResult, error)
}

type Verifier interface {
	Verify(ctx context.Context, cfg config.Config, req WriteRequest, writeRes WriteResult) (execute.VerificationStatus, map[string]any, error)
}

type WriteRequest struct {
	ApprovedItemID   int64
	SourcePlanID     int64
	AddPlayerName    string
	DropPlayerName   string
	AddESPNPlayerID  *int64
	DropESPNPlayerID *int64
	AddOnly          bool
	ScoringPeriodID  *int64
	EffectiveNextDay bool
}

type WriteResult struct {
	OK              bool
	Endpoint        string
	ResponseStatus  int
	ResponseMessage string
	ResponseJSON    map[string]any
}

type Service struct {
	preflightService *pfsvc.Service
	reviewRepo       *reviewrepo.Repository
	tranRepo         *tranrepo.Repository
	realRepo         *realrepo.Repository
	writer           Writer
	verifier         Verifier
}

var errMissingRequestPlayerIDs = errors.New("missing request player ids for verification")

func New(preflightService *pfsvc.Service, reviewRepo *reviewrepo.Repository, tranRepo *tranrepo.Repository, realRepo *realrepo.Repository, writer Writer, verifier Verifier) *Service {
	return &Service{
		preflightService: preflightService,
		reviewRepo:       reviewRepo,
		tranRepo:         tranRepo,
		realRepo:         realRepo,
		writer:           writer,
		verifier:         verifier,
	}
}

func (s *Service) ExecuteOne(ctx context.Context, cfg config.Config, opts execute.RealExecutionOptions) (*execute.RealExecutionResult, error) {
	if opts.ItemID <= 0 {
		return nil, fmt.Errorf("item id must be > 0")
	}
	if opts.ScoringPeriodID != nil && opts.EffectiveNextDay {
		return nil, fmt.Errorf("use only one of scoring period override or next-day scheduling")
	}
	if opts.ScoringPeriodID != nil && *opts.ScoringPeriodID <= 0 {
		return nil, fmt.Errorf("scoring period id override must be > 0")
	}

	approved, err := s.findApprovedItem(ctx, opts.ItemID)
	if err != nil {
		return nil, err
	}

	preflightRun, err := s.preflightService.Preflight(ctx, execute.Options{
		ItemID:           &opts.ItemID,
		Limit:            1,
		ScoringPeriodID:  intPtrFromInt64(opts.ScoringPeriodID),
		EffectiveNextDay: opts.EffectiveNextDay,
	})
	if err != nil {
		return nil, err
	}
	var preflightItem *execute.RunItem
	if preflightRun != nil && len(preflightRun.Items) > 0 {
		preflightItem = &preflightRun.Items[0]
	}
	if preflightItem == nil {
		return nil, fmt.Errorf("preflight returned no item for approved item %d", opts.ItemID)
	}

	result := &execute.RealExecutionResult{
		PreflightRun:  preflightRun,
		PreflightItem: preflightItem,
		WillWrite:     false,
		Message:       "confirmation required; rerun with --confirm",
	}
	if !opts.Confirm {
		return result, nil
	}

	if !cfg.Execution.Real.Enabled {
		return nil, fmt.Errorf("real execution is disabled by config (execution.real.enabled=false)")
	}
	if cfg.Execution.Real.RequireConfirmation && !opts.Confirm {
		return nil, fmt.Errorf("real execution requires explicit confirmation")
	}
	if preflightItem.ValidationStatus != execute.StatusExecutable {
		attempt, _ := s.createAbortedAttempt(ctx, approved, preflightRun, fmt.Sprintf("immediate preflight returned %s", preflightItem.ValidationStatus), preflightItem)
		return &execute.RealExecutionResult{
			Attempt:       attempt,
			PreflightRun:  preflightRun,
			PreflightItem: preflightItem,
			WillWrite:     false,
			Message:       fmt.Sprintf("execution aborted: immediate preflight returned %s", preflightItem.ValidationStatus),
		}, nil
	}

	if !cfg.Execution.Real.AllowRepeatExecution {
		done, err := s.realRepo.HasSuccessfulAttempt(ctx, opts.ItemID)
		if err != nil {
			return nil, err
		}
		if done && cfg.Execution.Hardening.BlockOnPriorSuccess {
			return nil, fmt.Errorf("execution blocked: approved item %d already has a successful execution attempt", opts.ItemID)
		}
	}
	if cfg.Execution.Hardening.BlockOnPriorSuccess || cfg.Execution.Hardening.BlockOnAmbiguousPriorAttempt {
		last, _, err := s.realRepo.LatestAttemptByApprovedItem(ctx, opts.ItemID)
		if err != nil {
			return nil, err
		}
		if last != nil {
			if cfg.Execution.Hardening.BlockOnPriorSuccess && (last.ExecutionStatus == execute.ExecutionStatusSucceeded || last.VerificationStatus == execute.VerificationStatusVerified) {
				return nil, fmt.Errorf("execution blocked: approved item %d already has a verified successful execution attempt", opts.ItemID)
			}
			if cfg.Execution.Hardening.BlockOnAmbiguousPriorAttempt &&
				(last.ExecutionStatus == execute.ExecutionStatusAmbiguous || last.ExecutionStatus == execute.ExecutionStatusSubmitted || last.VerificationStatus == execute.VerificationStatusPending) {
				return nil, fmt.Errorf("execution blocked: approved item %d has unresolved prior attempt (%s/%s); run `fb execute result --execution-id %d` and `fb execute verify --execution-id %d`", opts.ItemID, last.ExecutionStatus, last.VerificationStatus, last.ID, last.ID)
			}
		}
	}

	planItem, err := s.loadPlanItem(ctx, approved)
	if err != nil {
		return nil, err
	}
	req := WriteRequest{
		ApprovedItemID:   opts.ItemID,
		SourcePlanID:     approved.PlanID,
		AddPlayerName:    approved.AddPlayerName,
		DropPlayerName:   approved.DropPlayerName,
		AddESPNPlayerID:  planItem.AddESPNPlayerID,
		DropESPNPlayerID: planItem.DropESPNPlayerID,
		AddOnly:          strings.TrimSpace(approved.DropPlayerName) == "",
		ScoringPeriodID:  opts.ScoringPeriodID,
		EffectiveNextDay: opts.EffectiveNextDay,
	}
	if req.AddESPNPlayerID == nil || (!req.AddOnly && req.DropESPNPlayerID == nil) {
		attempt, _ := s.createAbortedAttempt(ctx, approved, preflightRun, "missing ESPN player ids for add/drop action", preflightItem)
		return &execute.RealExecutionResult{
			Attempt:       attempt,
			PreflightRun:  preflightRun,
			PreflightItem: preflightItem,
			WillWrite:     false,
			Message:       "execution aborted: missing ESPN player IDs for add/drop",
		}, nil
	}

	preflightRunID := preflightRun.ID
	attemptID, err := s.realRepo.CreateAttempt(ctx, realrepo.CreateAttemptInput{
		ApprovedItemID:     opts.ItemID,
		SourcePlanID:       approved.PlanID,
		PreflightRunID:     &preflightRunID,
		ExecutionStatus:    execute.ExecutionStatusStarted,
		VerificationStatus: execute.VerificationStatusUnknown,
		AddPlayerName:      approved.AddPlayerName,
		DropPlayerName:     approved.DropPlayerName,
		RequestSummary: map[string]any{
			"action_type":        actionType(req.AddOnly),
			"add_player_name":    req.AddPlayerName,
			"drop_player_name":   req.DropPlayerName,
			"add_player_id":      req.AddESPNPlayerID,
			"drop_player_id":     req.DropESPNPlayerID,
			"add_only":           req.AddOnly,
			"scoring_period_id":  req.ScoringPeriodID,
			"effective_next_day": req.EffectiveNextDay,
		},
		Details: map[string]any{
			"approved_note": approved.Note,
		},
	})
	if err != nil {
		return nil, err
	}
	_ = s.realRepo.AddEvent(ctx, attemptID, "preflight_passed", map[string]any{
		"status": preflightItem.ValidationStatus,
	})
	_ = s.realRepo.AddEvent(ctx, attemptID, "write_started", map[string]any{"item_id": opts.ItemID})

	submittedAt := time.Now().UTC()
	_ = s.realRepo.AddEvent(ctx, attemptID, "write_submitted", map[string]any{"submitted_at": submittedAt.Format(time.RFC3339)})
	writeRes, writeErr := s.writer.ExecuteAddDrop(ctx, cfg, req)
	if writeErr != nil {
		_ = s.realRepo.AddEvent(ctx, attemptID, "write_failed", map[string]any{"error": writeErr.Error()})
		_ = s.realRepo.CompleteAttempt(ctx, attemptID, realrepo.CompleteInput{
			ExecutionStatus:    execute.ExecutionStatusFailed,
			VerificationStatus: execute.VerificationStatusUnknown,
			ResponseSummary: map[string]any{
				"ok":              false,
				"response_status": writeRes.ResponseStatus,
				"endpoint":        writeRes.Endpoint,
			},
			ErrorMessage: writeErr.Error(),
		})
		attempt, _, _ := s.realRepo.AttemptByID(ctx, attemptID)
		return &execute.RealExecutionResult{
			Attempt:       attempt,
			PreflightRun:  preflightRun,
			PreflightItem: preflightItem,
			WillWrite:     true,
			Message:       fmt.Sprintf("execution failed: %s", writeErr.Error()),
		}, nil
	}
	_ = s.realRepo.AddEvent(ctx, attemptID, "write_succeeded", map[string]any{
		"response_status": writeRes.ResponseStatus,
		"endpoint":        writeRes.Endpoint,
	})

	_ = s.realRepo.AddEvent(ctx, attemptID, "verification_started", nil)
	verStatus, verDetails, verErr := s.verifier.Verify(ctx, cfg, req, writeRes)
	if verErr != nil {
		verStatus = execute.VerificationStatusVerificationFailed
		verDetails = map[string]any{"error": verErr.Error()}
		_ = s.realRepo.AddEvent(ctx, attemptID, "verification_failed", verDetails)
	} else {
		if verStatus == execute.VerificationStatusVerified {
			_ = s.realRepo.AddEvent(ctx, attemptID, "verification_succeeded", verDetails)
		} else if verStatus == execute.VerificationStatusPending {
			_ = s.realRepo.AddEvent(ctx, attemptID, "verification_pending", verDetails)
		} else {
			_ = s.realRepo.AddEvent(ctx, attemptID, "verification_inconclusive", verDetails)
		}
	}
	execStatus, ambiguousReason := deriveExecutionStatusFromVerification(verStatus)
	finalOutcome := map[string]any{
		"execution_status":    execStatus,
		"verification_status": verStatus,
	}
	if inferred, ok := verDetails["inference"]; ok {
		finalOutcome["inference"] = inferred
	}
	if ambiguousReason != "" {
		finalOutcome["ambiguous_reason"] = ambiguousReason
	}
	lastVerifiedAt := time.Now().UTC()

	_ = s.realRepo.CompleteAttempt(ctx, attemptID, realrepo.CompleteInput{
		ExecutionStatus:    execStatus,
		VerificationStatus: verStatus,
		SubmittedAt:        &submittedAt,
		LastVerifiedAt:     &lastVerifiedAt,
		AmbiguousReason:    ambiguousReason,
		ResponseSummary: map[string]any{
			"ok":              true,
			"response_status": writeRes.ResponseStatus,
			"endpoint":        writeRes.Endpoint,
			"message":         writeRes.ResponseMessage,
			"response_json":   writeRes.ResponseJSON,
		},
		Details:      verDetails,
		FinalOutcome: finalOutcome,
	})

	attempt, events, _ := s.realRepo.AttemptByID(ctx, attemptID)
	if attempt != nil {
		attempt.Events = events
	}
	return &execute.RealExecutionResult{
		Attempt:       attempt,
		PreflightRun:  preflightRun,
		PreflightItem: preflightItem,
		WillWrite:     true,
		Message:       describeExecutionMessage(execStatus, verStatus),
	}, nil
}

func (s *Service) Last(ctx context.Context) (*execute.Attempt, error) {
	a, ev, err := s.realRepo.LatestAttempt(ctx)
	if err != nil {
		return nil, err
	}
	if a != nil {
		a.Events = ev
	}
	return a, nil
}

func (s *Service) ByID(ctx context.Context, attemptID int64) (*execute.Attempt, error) {
	a, ev, err := s.realRepo.AttemptByID(ctx, attemptID)
	if err != nil {
		return nil, err
	}
	if a != nil {
		a.Events = ev
	}
	return a, nil
}

func (s *Service) History(ctx context.Context, limit int) ([]execute.Attempt, error) {
	return s.realRepo.ListAttempts(ctx, limit)
}

func (s *Service) Pending(ctx context.Context, limit int) ([]execute.Attempt, error) {
	return s.realRepo.ListPendingAttempts(ctx, limit)
}

func (s *Service) VerifyAttempt(ctx context.Context, cfg config.Config, attemptID int64) (*execute.VerifyResult, error) {
	attempt, _, err := s.realRepo.AttemptByID(ctx, attemptID)
	if err != nil {
		return nil, err
	}
	if attempt == nil {
		return nil, fmt.Errorf("execution attempt %d not found", attemptID)
	}
	recheckCount, err := s.realRepo.CountVerificationRechecks(ctx, attemptID)
	if err != nil {
		return nil, err
	}
	if cfg.Execution.Hardening.VerificationRecheckLimit > 0 && recheckCount >= cfg.Execution.Hardening.VerificationRecheckLimit {
		return nil, fmt.Errorf("verification recheck limit reached for execution %d (%d)", attemptID, cfg.Execution.Hardening.VerificationRecheckLimit)
	}
	req, err := requestFromAttempt(attempt)
	if err != nil {
		if errors.Is(err, errMissingRequestPlayerIDs) && (attempt.ExecutionStatus == execute.ExecutionStatusAborted || attempt.ExecutionStatus == execute.ExecutionStatusFailed) {
			_ = s.realRepo.AddEvent(ctx, attemptID, "verification_skipped", map[string]any{
				"reason": "missing request player ids",
			})
			return &execute.VerifyResult{
				Attempt:   attempt,
				Inference: "not_applicable",
				Message:   "verification skipped: execution attempt has no request player ids",
			}, nil
		}
		return nil, err
	}
	_ = s.realRepo.AddEvent(ctx, attemptID, "verification_started", map[string]any{"mode": "recheck"})
	verStatus, verDetails, verErr := s.verifier.Verify(ctx, cfg, req, WriteResult{})
	now := time.Now().UTC()
	if verErr != nil {
		verStatus = execute.VerificationStatusVerificationFailed
		verDetails = map[string]any{"error": verErr.Error()}
		_ = s.realRepo.AddEvent(ctx, attemptID, "verification_failed", verDetails)
	} else {
		switch verStatus {
		case execute.VerificationStatusVerified:
			_ = s.realRepo.AddEvent(ctx, attemptID, "verification_succeeded", verDetails)
		case execute.VerificationStatusPending:
			_ = s.realRepo.AddEvent(ctx, attemptID, "verification_pending", verDetails)
		default:
			_ = s.realRepo.AddEvent(ctx, attemptID, "verification_inconclusive", verDetails)
		}
	}
	execStatus, ambiguousReason := deriveExecutionStatusFromVerification(verStatus)
	if attempt.ExecutionStatus == execute.ExecutionStatusFailed || attempt.ExecutionStatus == execute.ExecutionStatusAborted {
		execStatus = attempt.ExecutionStatus
	}
	if err := s.realRepo.UpdateVerification(ctx, attemptID, verStatus, verDetails, now, execStatus, ambiguousReason); err != nil {
		return nil, err
	}
	updated, _, err := s.realRepo.AttemptByID(ctx, attemptID)
	if err != nil {
		return nil, err
	}
	return &execute.VerifyResult{
		Attempt:   updated,
		Inference: asString(verDetails["inference"]),
		Message:   describeExecutionMessage(execStatus, verStatus),
	}, nil
}

func (s *Service) ReconcileAttempt(ctx context.Context, cfg config.Config, attemptID int64) (*execute.VerifyResult, error) {
	attempt, _, err := s.realRepo.AttemptByID(ctx, attemptID)
	if err != nil {
		return nil, err
	}
	if attempt == nil {
		return nil, fmt.Errorf("execution attempt %d not found", attemptID)
	}
	req, err := requestFromAttempt(attempt)
	if err != nil {
		if errors.Is(err, errMissingRequestPlayerIDs) && (attempt.ExecutionStatus == execute.ExecutionStatusAborted || attempt.ExecutionStatus == execute.ExecutionStatusFailed) {
			_ = s.realRepo.AddEvent(ctx, attemptID, "reconciliation_run", map[string]any{
				"inference": "not_applicable",
				"reason":    "missing request player ids",
			})
			return &execute.VerifyResult{
				Attempt:   attempt,
				Inference: "not_applicable",
				Message:   "reconciliation skipped: execution attempt has no request player ids",
			}, nil
		}
		return nil, err
	}
	verStatus, verDetails, verErr := s.verifier.Verify(ctx, cfg, req, WriteResult{})
	if verErr != nil {
		verStatus = execute.VerificationStatusVerificationFailed
		verDetails = map[string]any{"error": verErr.Error()}
	}
	inference := asString(verDetails["inference"])
	_ = s.realRepo.AddEvent(ctx, attemptID, "reconciliation_run", map[string]any{
		"inference":           inference,
		"verification_status": verStatus,
	})
	return &execute.VerifyResult{
		Attempt:   attempt,
		Inference: inference,
		Message:   fmt.Sprintf("reconciliation: %s", firstNonEmpty(inference, "inconclusive")),
	}, nil
}

func (s *Service) createAbortedAttempt(ctx context.Context, approved *transactions.ApprovalQueueItem, preflightRun *execute.Run, reason string, preflightItem *execute.RunItem) (*execute.Attempt, error) {
	var preflightRunID *int64
	if preflightRun != nil {
		id := preflightRun.ID
		preflightRunID = &id
	}
	id, err := s.realRepo.CreateAttempt(ctx, realrepo.CreateAttemptInput{
		ApprovedItemID:     approved.TransactionPlanItemID,
		SourcePlanID:       approved.PlanID,
		PreflightRunID:     preflightRunID,
		ExecutionStatus:    execute.ExecutionStatusAborted,
		VerificationStatus: execute.VerificationStatusUnknown,
		AddPlayerName:      approved.AddPlayerName,
		DropPlayerName:     approved.DropPlayerName,
		Details: map[string]any{
			"reason":           reason,
			"preflight_status": preflightItem.ValidationStatus,
		},
	})
	if err != nil {
		return nil, err
	}
	_ = s.realRepo.AddEvent(ctx, id, "preflight_failed", map[string]any{"reason": reason})
	_ = s.realRepo.AddEvent(ctx, id, "execution_aborted", map[string]any{"reason": reason})
	_ = s.realRepo.CompleteAttempt(ctx, id, realrepo.CompleteInput{
		ExecutionStatus:    execute.ExecutionStatusAborted,
		VerificationStatus: execute.VerificationStatusUnknown,
		ErrorMessage:       reason,
		Details: map[string]any{
			"preflight_status": preflightItem.ValidationStatus,
		},
	})
	a, ev, _ := s.realRepo.AttemptByID(ctx, id)
	if a != nil {
		a.Events = ev
	}
	return a, nil
}

func deriveExecutionStatusFromVerification(ver execute.VerificationStatus) (execute.ExecutionStatus, string) {
	switch ver {
	case execute.VerificationStatusVerified:
		return execute.ExecutionStatusSucceeded, ""
	case execute.VerificationStatusPending:
		return execute.ExecutionStatusSubmitted, "verification pending"
	case execute.VerificationStatusUnverified:
		return execute.ExecutionStatusAmbiguous, "verification inconclusive"
	case execute.VerificationStatusVerificationFailed:
		return execute.ExecutionStatusAmbiguous, "verification failed"
	default:
		return execute.ExecutionStatusAmbiguous, "verification unknown"
	}
}

func describeExecutionMessage(execStatus execute.ExecutionStatus, verStatus execute.VerificationStatus) string {
	switch execStatus {
	case execute.ExecutionStatusSucceeded:
		return "execution succeeded and verified"
	case execute.ExecutionStatusSubmitted:
		return fmt.Sprintf("execution submitted; verification status %s", verStatus)
	case execute.ExecutionStatusAmbiguous:
		return fmt.Sprintf("execution result is ambiguous; verification status %s", verStatus)
	default:
		return "execution attempted"
	}
}

func requestFromAttempt(attempt *execute.Attempt) (WriteRequest, error) {
	addOnly, _ := attempt.RequestSummary["add_only"].(bool)
	effectiveNextDay, _ := attempt.RequestSummary["effective_next_day"].(bool)
	req := WriteRequest{
		ApprovedItemID:   attempt.ApprovedItemID,
		SourcePlanID:     attempt.SourcePlanID,
		AddPlayerName:    attempt.AddPlayerName,
		DropPlayerName:   attempt.DropPlayerName,
		AddOnly:          addOnly || strings.TrimSpace(attempt.DropPlayerName) == "",
		EffectiveNextDay: effectiveNextDay,
	}
	addID := asInt64(attempt.RequestSummary["add_player_id"])
	dropID := asInt64(attempt.RequestSummary["drop_player_id"])
	if addID <= 0 || (!req.AddOnly && dropID <= 0) {
		return WriteRequest{}, fmt.Errorf("%w: execution attempt %d", errMissingRequestPlayerIDs, attempt.ID)
	}
	req.AddESPNPlayerID = &addID
	if dropID > 0 {
		req.DropESPNPlayerID = &dropID
	}
	return req, nil
}

func asInt64(v any) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case int:
		return int64(t)
	case float64:
		return int64(t)
	case float32:
		return int64(t)
	}
	return 0
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func actionType(addOnly bool) string {
	if addOnly {
		return "add_pitcher"
	}
	return "add_drop_pitcher"
}

func intPtrFromInt64(v *int64) *int {
	if v == nil {
		return nil
	}
	out := int(*v)
	return &out
}

func (s *Service) findApprovedItem(ctx context.Context, itemID int64) (*transactions.ApprovalQueueItem, error) {
	rows, err := s.reviewRepo.Queue(ctx, 500)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.TransactionPlanItemID == itemID {
			cp := row
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("approved item %d not found", itemID)
}

func (s *Service) loadPlanItem(ctx context.Context, approved *transactions.ApprovalQueueItem) (*transactions.PlanItem, error) {
	items, err := s.tranRepo.PlanItems(ctx, approved.PlanID)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.ID == approved.TransactionPlanItemID {
			cp := item
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("approved item %d not found in source plan %d", approved.TransactionPlanItemID, approved.PlanID)
}

func ConfirmationRequired(cfg config.Config) bool {
	return cfg.Execution.Real.RequireConfirmation
}

func IsRealExecutionEnabled(cfg config.Config) bool {
	return cfg.Execution.Real.Enabled
}
