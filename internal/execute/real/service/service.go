package service

import (
	"context"
	"fmt"

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

	approved, err := s.findApprovedItem(ctx, opts.ItemID)
	if err != nil {
		return nil, err
	}

	preflightRun, err := s.preflightService.Preflight(ctx, execute.Options{ItemID: &opts.ItemID, Limit: 1})
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
		if done {
			return nil, fmt.Errorf("approved item %d already has a successful execution attempt", opts.ItemID)
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
	}
	if req.AddESPNPlayerID == nil || req.DropESPNPlayerID == nil {
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
			"action_type":      "add_drop_pitcher",
			"add_player_name":  req.AddPlayerName,
			"drop_player_name": req.DropPlayerName,
			"add_player_id":    req.AddESPNPlayerID,
			"drop_player_id":   req.DropESPNPlayerID,
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
		} else {
			_ = s.realRepo.AddEvent(ctx, attemptID, "verification_failed", verDetails)
		}
	}

	_ = s.realRepo.CompleteAttempt(ctx, attemptID, realrepo.CompleteInput{
		ExecutionStatus:    execute.ExecutionStatusSucceeded,
		VerificationStatus: verStatus,
		ResponseSummary: map[string]any{
			"ok":              true,
			"response_status": writeRes.ResponseStatus,
			"endpoint":        writeRes.Endpoint,
			"message":         writeRes.ResponseMessage,
			"response_json":   writeRes.ResponseJSON,
		},
		Details: verDetails,
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
		Message:       "execution attempted",
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
