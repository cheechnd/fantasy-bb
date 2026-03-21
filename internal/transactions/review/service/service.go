package service

import (
	"context"
	"fmt"

	"fantasy-baseball/internal/transactions"
	reviewrepo "fantasy-baseball/internal/transactions/review/repository"
)

type Service struct {
	repo *reviewrepo.Repository
}

func New(repo *reviewrepo.Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) ReviewPlan(ctx context.Context, planID int64) (*transactions.PlanReview, error) {
	return s.repo.ReviewByPlanID(ctx, planID)
}

func (s *Service) Approve(ctx context.Context, planID, itemID int64, note string) (*transactions.ReviewDecision, error) {
	return s.transition(ctx, planID, itemID, transactions.ReviewStateApproved, note)
}

func (s *Service) Reject(ctx context.Context, planID, itemID int64, note string) (*transactions.ReviewDecision, error) {
	return s.transition(ctx, planID, itemID, transactions.ReviewStateRejected, note)
}

func (s *Service) Defer(ctx context.Context, planID, itemID int64, note string) (*transactions.ReviewDecision, error) {
	return s.transition(ctx, planID, itemID, transactions.ReviewStateDeferred, note)
}

func (s *Service) Reset(ctx context.Context, planID int64, itemID *int64) (any, error) {
	if itemID != nil {
		return s.transition(ctx, planID, *itemID, transactions.ReviewStatePending, "")
	}
	changed, err := s.repo.ResetPlan(ctx, planID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"plan_id": planID, "changed_count": changed, "state": transactions.ReviewStatePending}, nil
}

func (s *Service) Queue(ctx context.Context, limit int) ([]transactions.ApprovalQueueItem, error) {
	return s.repo.Queue(ctx, limit)
}

func (s *Service) Approvals(ctx context.Context, limit int, state *transactions.ReviewState) ([]transactions.ApprovalStateRow, error) {
	return s.repo.Approvals(ctx, limit, state)
}

func (s *Service) transition(ctx context.Context, planID, itemID int64, target transactions.ReviewState, note string) (*transactions.ReviewDecision, error) {
	if planID <= 0 {
		return nil, fmt.Errorf("plan-id must be > 0")
	}
	if itemID <= 0 {
		return nil, fmt.Errorf("item id must be > 0")
	}
	review, err := s.repo.ReviewByPlanID(ctx, planID)
	if err != nil {
		return nil, err
	}

	var current *transactions.ReviewedPlanItem
	for i := range review.Items {
		if review.Items[i].ID == itemID {
			current = &review.Items[i]
			break
		}
	}
	if current == nil {
		return nil, reviewrepo.ErrPlanItemPlanMismatch
	}
	if current.ReviewState == target {
		return nil, fmt.Errorf("item %d is already in state %s", itemID, target)
	}
	if !validTransition(current.ReviewState, target) {
		return nil, fmt.Errorf("invalid review transition: %s -> %s", current.ReviewState, target)
	}
	return s.repo.TransitionState(ctx, planID, itemID, target, note)
}

func validTransition(current, target transactions.ReviewState) bool {
	switch current {
	case transactions.ReviewStatePending:
		return target == transactions.ReviewStateApproved || target == transactions.ReviewStateRejected || target == transactions.ReviewStateDeferred
	case transactions.ReviewStateApproved:
		return target == transactions.ReviewStateRejected || target == transactions.ReviewStateDeferred || target == transactions.ReviewStatePending
	case transactions.ReviewStateRejected:
		return target == transactions.ReviewStatePending
	case transactions.ReviewStateDeferred:
		return target == transactions.ReviewStateApproved || target == transactions.ReviewStateRejected || target == transactions.ReviewStatePending
	default:
		return false
	}
}
