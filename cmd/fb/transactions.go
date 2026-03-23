package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	esrepo "fantasy-baseball/internal/espn/repository"
	"fantasy-baseball/internal/forecaster"
	pickrepo "fantasy-baseball/internal/pickups/repository"
	pitchplan "fantasy-baseball/internal/pitchers/planner"
	"fantasy-baseball/internal/store/sqlite"
	"fantasy-baseball/internal/transactions"
	adhocrepo "fantasy-baseball/internal/transactions/adhoc/repository"
	adhocsvc "fantasy-baseball/internal/transactions/adhoc/service"
	tranrepo "fantasy-baseball/internal/transactions/repository"
	reviewrepo "fantasy-baseball/internal/transactions/review/repository"
	reviewsvc "fantasy-baseball/internal/transactions/review/service"
	transvc "fantasy-baseball/internal/transactions/service"

	"github.com/spf13/cobra"
)

func newTransactionsCmd(opts *cliOptions) *cobra.Command {
	cmd := &cobra.Command{Use: "transactions", Short: "Add/drop transaction planning, review, and execution"}
	cmd.AddGroup(
		&cobra.Group{ID: "generate", Title: "Generate"},
		&cobra.Group{ID: "adhoc", Title: "Ad Hoc"},
		&cobra.Group{ID: "review", Title: "Review Workflow"},
		&cobra.Group{ID: "execute", Title: "Execution"},
		&cobra.Group{ID: "inspect", Title: "Inspection"},
		&cobra.Group{ID: "explain", Title: "Explain"},
	)
	planCmd := newTransactionsPlanCmd(opts)
	planCmd.GroupID = "generate"
	topCmd := newTransactionsTopCmd(opts)
	topCmd.GroupID = "generate"
	compareCmd := newTransactionsCompareCmd(opts)
	compareCmd.GroupID = "generate"
	lastCmd := newTransactionsLastCmd(opts)
	lastCmd.GroupID = "inspect"
	explainCmd := newTransactionsExplainCmd(opts)
	explainCmd.GroupID = "explain"
	reviewCmd := newTransactionsReviewCmd(opts)
	reviewCmd.GroupID = "review"
	approveCmd := newTransactionsApproveCmd(opts)
	approveCmd.GroupID = "review"
	rejectCmd := newTransactionsRejectCmd(opts)
	rejectCmd.GroupID = "review"
	deferCmd := newTransactionsDeferCmd(opts)
	deferCmd.GroupID = "review"
	queueCmd := newTransactionsQueueCmd(opts)
	queueCmd.GroupID = "review"
	approvalsCmd := newTransactionsApprovalsCmd(opts)
	approvalsCmd.GroupID = "review"
	resetReviewCmd := newTransactionsResetReviewCmd(opts)
	resetReviewCmd.GroupID = "review"
	adHocCmd := newTransactionsAdHocCmd(opts)
	adHocCmd.GroupID = "adhoc"
	adHocShowCmd := newTransactionsAdHocShowCmd(opts)
	adHocShowCmd.GroupID = "adhoc"
	adHocListCmd := newTransactionsAdHocListCmd(opts)
	adHocListCmd.GroupID = "adhoc"
	executeCmd := newExecuteCmd(opts)
	executeCmd.GroupID = "execute"
	cmd.AddCommand(
		planCmd, topCmd, compareCmd,
		adHocCmd, adHocShowCmd, adHocListCmd,
		executeCmd,
		reviewCmd, approveCmd, rejectCmd, deferCmd, queueCmd, approvalsCmd, resetReviewCmd,
		lastCmd, explainCmd,
	)
	return cmd
}

func newTransactionsAdHocCmd(opts *cliOptions) *cobra.Command {
	var addName, dropName string
	cmd := &cobra.Command{
		Use:   "ad-hoc",
		Short: "Create and resolve a manual ad hoc pitcher add/drop request",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := withAdHocService(cmd.Context(), opts, func(ctx context.Context, svc *adhocsvc.Service) (any, error) {
				return svc.CreateAndResolve(ctx, addName, dropName)
			})
			if err != nil {
				return err
			}
			req := v.(*transactions.AdHocRequest)
			if opts.OutputJSON {
				return writeJSON(cmd, req)
			}
			printAdHocRequest(cmd, req)
			return nil
		},
	}
	cmd.Flags().StringVar(&addName, "add", "", "Add player name")
	cmd.Flags().StringVar(&dropName, "drop", "", "Drop player name")
	return cmd
}

func newTransactionsAdHocShowCmd(opts *cliOptions) *cobra.Command {
	var requestID int64
	cmd := &cobra.Command{
		Use:   "ad-hoc-show",
		Short: "Show a saved ad hoc transaction request",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if requestID <= 0 {
				return fmt.Errorf("--request-id must be > 0")
			}
			v, err := withAdHocService(cmd.Context(), opts, func(ctx context.Context, svc *adhocsvc.Service) (any, error) {
				return svc.ByID(ctx, requestID)
			})
			if err != nil {
				return err
			}
			req := v.(*transactions.AdHocRequest)
			if req == nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Ad hoc request %d not found.\n", requestID)
				return nil
			}
			if opts.OutputJSON {
				return writeJSON(cmd, req)
			}
			printAdHocRequest(cmd, req)
			return nil
		},
	}
	cmd.Flags().Int64Var(&requestID, "request-id", 0, "Ad hoc request ID")
	return cmd
}

func newTransactionsAdHocListCmd(opts *cliOptions) *cobra.Command {
	var limit int
	var stateRaw string
	cmd := &cobra.Command{
		Use:   "ad-hoc-list",
		Short: "List recent ad hoc transaction requests",
		RunE: func(cmd *cobra.Command, _ []string) error {
			var state *transactions.AdHocRequestState
			if strings.TrimSpace(stateRaw) != "" {
				s := transactions.AdHocRequestState(strings.TrimSpace(stateRaw))
				state = &s
			}
			v, err := withAdHocService(cmd.Context(), opts, func(ctx context.Context, svc *adhocsvc.Service) (any, error) {
				return svc.List(ctx, limit, state)
			})
			if err != nil {
				return err
			}
			rows := v.([]transactions.AdHocRequest)
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"count": len(rows), "requests": rows})
			}
			printAdHocRequests(cmd, rows)
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 25, "Maximum requests to show")
	cmd.Flags().StringVar(&stateRaw, "state", "", "Optional state filter")
	return cmd
}

func newTransactionsPlanCmd(opts *cliOptions) *cobra.Command {
	var fromRaw, toRaw string
	var syncRunID, importRunID, pitcherPlanID, pickupRunID int64
	var topN int
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Generate and save a full read-only add/drop transaction plan",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := withTransactionsService(cmd.Context(), opts, func(ctx context.Context, svc *transvc.Service) (any, error) {
				opts2, err := buildTransactionOptions(cmd, fromRaw, toRaw, topN, &syncRunID, &importRunID, &pitcherPlanID, &pickupRunID)
				if err != nil {
					return nil, err
				}
				return svc.GenerateAndSave(ctx, opts2)
			})
			if err != nil {
				return err
			}
			plan := v.(*transactions.Plan)
			if opts.OutputJSON {
				return writeJSON(cmd, plan)
			}
			printTransactionPlan(cmd, plan)
			return nil
		},
	}
	cmd.Flags().StringVar(&fromRaw, "from", "", "Window start date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&toRaw, "to", "", "Window end date (YYYY-MM-DD)")
	cmd.Flags().Int64Var(&syncRunID, "sync-run", 0, "ESPN sync run ID (defaults to latest artifact chain)")
	cmd.Flags().Int64Var(&importRunID, "import-run", 0, "Forecaster import run ID (defaults to latest artifact chain)")
	cmd.Flags().Int64Var(&pitcherPlanID, "pitcher-plan-id", 0, "Pitcher plan ID (defaults to latest)")
	cmd.Flags().Int64Var(&pickupRunID, "pickup-run", 0, "Pickup recommendation run ID (defaults to latest)")
	cmd.Flags().IntVar(&topN, "top", 10, "Top move rows to keep in saved plan")
	return cmd
}

func newTransactionsTopCmd(opts *cliOptions) *cobra.Command {
	var fromRaw, toRaw string
	var syncRunID, importRunID, pitcherPlanID, pickupRunID int64
	var topN int
	cmd := &cobra.Command{
		Use:   "top",
		Short: "Show top ranked add/drop proposals",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := withTransactionsService(cmd.Context(), opts, func(ctx context.Context, svc *transvc.Service) (any, error) {
				opts2, err := buildTransactionOptions(cmd, fromRaw, toRaw, topN, &syncRunID, &importRunID, &pitcherPlanID, &pickupRunID)
				if err != nil {
					return nil, err
				}
				return svc.GenerateAndSave(ctx, opts2)
			})
			if err != nil {
				return err
			}
			plan := v.(*transactions.Plan)
			rows := filterTopTransactionRows(plan.Items)
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"plan": plan, "top": rows})
			}
			printTransactionRowsTable(cmd, rows)
			return nil
		},
	}
	cmd.Flags().StringVar(&fromRaw, "from", "", "Window start date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&toRaw, "to", "", "Window end date (YYYY-MM-DD)")
	cmd.Flags().Int64Var(&syncRunID, "sync-run", 0, "ESPN sync run ID (defaults to latest artifact chain)")
	cmd.Flags().Int64Var(&importRunID, "import-run", 0, "Forecaster import run ID (defaults to latest artifact chain)")
	cmd.Flags().Int64Var(&pitcherPlanID, "pitcher-plan-id", 0, "Pitcher plan ID (defaults to latest)")
	cmd.Flags().Int64Var(&pickupRunID, "pickup-run", 0, "Pickup recommendation run ID (defaults to latest)")
	cmd.Flags().IntVar(&topN, "top", 10, "Top move rows")
	return cmd
}

func newTransactionsCompareCmd(opts *cliOptions) *cobra.Command {
	var fromRaw, toRaw string
	var syncRunID, importRunID, pitcherPlanID, pickupRunID int64
	var topN int
	cmd := &cobra.Command{
		Use:   "compare",
		Short: "Show deterministic comparison reasoning for proposed add/drop moves",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := withTransactionsService(cmd.Context(), opts, func(ctx context.Context, svc *transvc.Service) (any, error) {
				opts2, err := buildTransactionOptions(cmd, fromRaw, toRaw, topN, &syncRunID, &importRunID, &pitcherPlanID, &pickupRunID)
				if err != nil {
					return nil, err
				}
				return svc.GenerateAndSave(ctx, opts2)
			})
			if err != nil {
				return err
			}
			plan := v.(*transactions.Plan)
			if opts.OutputJSON {
				return writeJSON(cmd, plan)
			}
			printTransactionCompare(cmd, plan.Items)
			return nil
		},
	}
	cmd.Flags().StringVar(&fromRaw, "from", "", "Window start date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&toRaw, "to", "", "Window end date (YYYY-MM-DD)")
	cmd.Flags().Int64Var(&syncRunID, "sync-run", 0, "ESPN sync run ID (defaults to latest artifact chain)")
	cmd.Flags().Int64Var(&importRunID, "import-run", 0, "Forecaster import run ID (defaults to latest artifact chain)")
	cmd.Flags().Int64Var(&pitcherPlanID, "pitcher-plan-id", 0, "Pitcher plan ID (defaults to latest)")
	cmd.Flags().Int64Var(&pickupRunID, "pickup-run", 0, "Pickup recommendation run ID (defaults to latest)")
	cmd.Flags().IntVar(&topN, "top", 10, "Top move rows")
	return cmd
}

func newTransactionsLastCmd(opts *cliOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "last",
		Short: "Show latest saved transaction plan",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := withTransactionsService(cmd.Context(), opts, func(ctx context.Context, svc *transvc.Service) (any, error) {
				return svc.Latest(ctx)
			})
			if err != nil {
				return err
			}
			plan, _ := v.(*transactions.Plan)
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"plan": plan})
			}
			if plan == nil {
				fmt.Fprintln(cmd.OutOrStdout(), "No transaction plans found.")
				return nil
			}
			printTransactionPlan(cmd, plan)
			return nil
		},
	}
	return cmd
}

func newTransactionsExplainCmd(opts *cliOptions) *cobra.Command {
	var planID int64
	cmd := &cobra.Command{
		Use:   "explain",
		Short: "Explain move-by-move deterministic logic for a saved plan",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if planID <= 0 {
				return fmt.Errorf("--plan-id must be > 0")
			}
			v, err := withTransactionsService(cmd.Context(), opts, func(ctx context.Context, svc *transvc.Service) (any, error) {
				return svc.ByID(ctx, planID)
			})
			if err != nil {
				return err
			}
			plan, _ := v.(*transactions.Plan)
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"plan": plan})
			}
			if plan == nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Transaction plan %d not found.\n", planID)
				return nil
			}
			printTransactionExplain(cmd, plan)
			return nil
		},
	}
	cmd.Flags().Int64Var(&planID, "plan-id", 0, "Transaction plan ID")
	return cmd
}

func newTransactionsReviewCmd(opts *cliOptions) *cobra.Command {
	var planID int64
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Show a transaction plan with current review state per item",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if planID <= 0 {
				return fmt.Errorf("--plan-id must be > 0")
			}
			v, err := withTransactionsReviewService(cmd.Context(), opts, func(ctx context.Context, svc *reviewsvc.Service) (any, error) {
				return svc.ReviewPlan(ctx, planID)
			})
			if err != nil {
				return err
			}
			review := v.(*transactions.PlanReview)
			if opts.OutputJSON {
				return writeJSON(cmd, review)
			}
			printTransactionPlanReview(cmd, review)
			return nil
		},
	}
	cmd.Flags().Int64Var(&planID, "plan-id", 0, "Transaction plan ID")
	return cmd
}

func newTransactionsApproveCmd(opts *cliOptions) *cobra.Command {
	return newTransactionsStateChangeCmd(opts, "approve", "Mark a transaction plan item as approved", transactions.ReviewStateApproved)
}

func newTransactionsRejectCmd(opts *cliOptions) *cobra.Command {
	return newTransactionsStateChangeCmd(opts, "reject", "Mark a transaction plan item as rejected", transactions.ReviewStateRejected)
}

func newTransactionsDeferCmd(opts *cliOptions) *cobra.Command {
	return newTransactionsStateChangeCmd(opts, "defer", "Mark a transaction plan item as deferred", transactions.ReviewStateDeferred)
}

func newTransactionsStateChangeCmd(opts *cliOptions, use, short string, target transactions.ReviewState) *cobra.Command {
	var planID int64
	var itemID int64
	var note string
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if planID <= 0 {
				return fmt.Errorf("--plan-id must be > 0")
			}
			if itemID <= 0 {
				return fmt.Errorf("--item must be > 0")
			}
			v, err := withTransactionsReviewService(cmd.Context(), opts, func(ctx context.Context, svc *reviewsvc.Service) (any, error) {
				switch target {
				case transactions.ReviewStateApproved:
					return svc.Approve(ctx, planID, itemID, note)
				case transactions.ReviewStateRejected:
					return svc.Reject(ctx, planID, itemID, note)
				case transactions.ReviewStateDeferred:
					return svc.Defer(ctx, planID, itemID, note)
				default:
					return nil, fmt.Errorf("unsupported review target state %q", target)
				}
			})
			if err != nil {
				return err
			}
			decision := v.(*transactions.ReviewDecision)
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"ok": true, "decision": decision})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Plan %d item %d: %s -> %s\n", decision.PlanID, decision.TransactionPlanItemID, decision.PreviousState, decision.NewState)
			if decision.Note != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Note: %s\n", decision.Note)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Updated: %s\n", decision.ChangedAt.Format(time.RFC3339))
			return nil
		},
	}
	cmd.Flags().Int64Var(&planID, "plan-id", 0, "Transaction plan ID")
	cmd.Flags().Int64Var(&itemID, "item", 0, "Transaction plan item ID")
	cmd.Flags().StringVar(&note, "note", "", "Optional decision note")
	return cmd
}

func newTransactionsQueueCmd(opts *cliOptions) *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "queue",
		Short: "Show approved transaction items queued for future execution",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := withTransactionsReviewService(cmd.Context(), opts, func(ctx context.Context, svc *reviewsvc.Service) (any, error) {
				return svc.Queue(ctx, limit)
			})
			if err != nil {
				return err
			}
			rows := v.([]transactions.ApprovalQueueItem)
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"ok": true, "count": len(rows), "items": rows})
			}
			printTransactionQueue(cmd, rows)
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "Maximum queued items to show")
	return cmd
}

func newTransactionsApprovalsCmd(opts *cliOptions) *cobra.Command {
	var limit int
	var stateRaw string
	cmd := &cobra.Command{
		Use:   "approvals",
		Short: "Show transaction review states across plans",
		RunE: func(cmd *cobra.Command, _ []string) error {
			var state *transactions.ReviewState
			if cmd.Flags().Changed("state") {
				s, err := parseReviewState(stateRaw)
				if err != nil {
					return err
				}
				state = &s
			}
			v, err := withTransactionsReviewService(cmd.Context(), opts, func(ctx context.Context, svc *reviewsvc.Service) (any, error) {
				return svc.Approvals(ctx, limit, state)
			})
			if err != nil {
				return err
			}
			rows := v.([]transactions.ApprovalStateRow)
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"ok": true, "count": len(rows), "rows": rows})
			}
			printTransactionApprovals(cmd, rows)
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 50, "Maximum approval rows to show")
	cmd.Flags().StringVar(&stateRaw, "state", "", "Filter by review state (pending|approved|rejected|deferred)")
	return cmd
}

func newTransactionsResetReviewCmd(opts *cliOptions) *cobra.Command {
	var planID int64
	var itemID int64
	cmd := &cobra.Command{
		Use:   "reset-review",
		Short: "Reset review state to pending for a plan or a single item",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if planID <= 0 {
				return fmt.Errorf("--plan-id must be > 0")
			}
			var itemPtr *int64
			if cmd.Flags().Changed("item") {
				if itemID <= 0 {
					return fmt.Errorf("--item must be > 0")
				}
				itemPtr = &itemID
			}
			v, err := withTransactionsReviewService(cmd.Context(), opts, func(ctx context.Context, svc *reviewsvc.Service) (any, error) {
				return svc.Reset(ctx, planID, itemPtr)
			})
			if err != nil {
				return err
			}
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"ok": true, "result": v})
			}
			switch x := v.(type) {
			case *transactions.ReviewDecision:
				fmt.Fprintf(cmd.OutOrStdout(), "Plan %d item %d reset: %s -> %s\n", x.PlanID, x.TransactionPlanItemID, x.PreviousState, x.NewState)
			case map[string]any:
				fmt.Fprintf(cmd.OutOrStdout(), "Plan %d reset to pending (changed items: %v)\n", planID, x["changed_count"])
			default:
				fmt.Fprintf(cmd.OutOrStdout(), "Plan %d review reset completed.\n", planID)
			}
			return nil
		},
	}
	cmd.Flags().Int64Var(&planID, "plan-id", 0, "Transaction plan ID")
	cmd.Flags().Int64Var(&itemID, "item", 0, "Optional transaction plan item ID")
	return cmd
}

func withTransactionsService(ctx context.Context, opts *cliOptions, fn func(context.Context, *transvc.Service) (any, error)) (any, error) {
	cfg, _, err := loadConfigWithOverrides(opts)
	if err != nil {
		return nil, err
	}
	s, err := sqlite.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	if _, err := s.Migrate(ctx); err != nil {
		return nil, err
	}

	service := transvc.New(
		forecaster.NewRepository(s.DB()),
		esrepo.New(s.DB()),
		pitchplan.NewRepository(s.DB()),
		pickrepo.New(s.DB()),
		tranrepo.New(s.DB()),
		transactions.ServiceConfig{
			TopMoveLimit:                   cfg.Transactions.Pitchers.TopMoveLimit,
			MaxPairings:                    cfg.Transactions.Pitchers.MaxPairings,
			StrongMoveDeltaFPTS:            cfg.Transactions.Pitchers.StrongMoveDeltaFPTS,
			MarginalMoveDeltaFPTS:          cfg.Transactions.Pitchers.MarginalMoveDeltaFPTS,
			RiskyMoveMinDeltaFPTS:          cfg.Transactions.Pitchers.RiskyMoveMinDeltaFPTS,
			UncertaintyPenaltyTBD:          cfg.Transactions.Pitchers.UncertaintyPenaltyTBD,
			UncertaintyPenaltyMissingProj:  cfg.Transactions.Pitchers.UncertaintyPenaltyMissingProj,
			UncertaintyPenaltyAmbiguous:    cfg.Transactions.Pitchers.UncertaintyPenaltyAmbiguous,
			AllowCompareAgainstLikelyStart: cfg.Transactions.Pitchers.AllowCompareAgainstLikelyStart,
		},
	)
	return fn(ctx, service)
}

func withTransactionsReviewService(ctx context.Context, opts *cliOptions, fn func(context.Context, *reviewsvc.Service) (any, error)) (any, error) {
	cfg, _, err := loadConfigWithOverrides(opts)
	if err != nil {
		return nil, err
	}
	s, err := sqlite.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	if _, err := s.Migrate(ctx); err != nil {
		return nil, err
	}

	service := reviewsvc.New(reviewrepo.New(s.DB()))
	return fn(ctx, service)
}

func withAdHocService(ctx context.Context, opts *cliOptions, fn func(context.Context, *adhocsvc.Service) (any, error)) (any, error) {
	cfg, _, err := loadConfigWithOverrides(opts)
	if err != nil {
		return nil, err
	}
	s, err := sqlite.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	if _, err := s.Migrate(ctx); err != nil {
		return nil, err
	}
	service := adhocsvc.New(
		adhocrepo.New(s.DB()),
		esrepo.New(s.DB()),
		tranrepo.New(s.DB()),
		reviewrepo.New(s.DB()),
		adhocsvc.Config{
			Enabled:                    cfg.Transactions.AdHoc.Enabled,
			MaxRecentRequests:          cfg.Transactions.AdHoc.MaxRecentRequests,
			RequirePitchersOnly:        cfg.Transactions.AdHoc.RequirePitchersOnly,
			ReuseBoundedCandidateLimit: cfg.Transactions.AdHoc.ReuseBoundedCandidateLimit,
		},
	)
	return fn(ctx, service)
}

func buildTransactionOptions(cmd *cobra.Command, fromRaw, toRaw string, topN int, syncRunID, importRunID, pitcherPlanID, pickupRunID *int64) (transactions.Options, error) {
	from, to, err := parseWindow(fromRaw, toRaw)
	if err != nil {
		return transactions.Options{}, err
	}
	out := transactions.Options{From: from, To: to, TopN: topN}
	if cmd.Flags().Changed("sync-run") {
		v := *syncRunID
		out.SyncRunID = &v
	}
	if cmd.Flags().Changed("import-run") {
		v := *importRunID
		out.ImportRunID = &v
	}
	if cmd.Flags().Changed("pitcher-plan-id") {
		v := *pitcherPlanID
		out.PitcherPlanID = &v
	}
	if cmd.Flags().Changed("pickup-run") {
		v := *pickupRunID
		out.PickupRunID = &v
	}
	return out, nil
}

func parseReviewState(raw string) (transactions.ReviewState, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(transactions.ReviewStatePending):
		return transactions.ReviewStatePending, nil
	case string(transactions.ReviewStateApproved):
		return transactions.ReviewStateApproved, nil
	case string(transactions.ReviewStateRejected):
		return transactions.ReviewStateRejected, nil
	case string(transactions.ReviewStateDeferred):
		return transactions.ReviewStateDeferred, nil
	default:
		return "", fmt.Errorf("invalid --state value %q (expected pending|approved|rejected|deferred)", raw)
	}
}

func printTransactionPlan(cmd *cobra.Command, plan *transactions.Plan) {
	fmt.Fprintf(cmd.OutOrStdout(), "Transaction Plan: %d\n", plan.ID)
	fmt.Fprintf(cmd.OutOrStdout(), "Window: %s to %s\n", plan.WindowStart, plan.WindowEnd)
	if plan.SyncRunID != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "ESPN sync run: %d\n", *plan.SyncRunID)
	}
	if plan.ImportRunID != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Forecaster import run: %d\n", *plan.ImportRunID)
	}
	if plan.PitcherPlanID != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Pitcher plan: %d\n", *plan.PitcherPlanID)
	}
	if plan.PickupRecommendationRunID != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Pickup recommendation run: %d\n", *plan.PickupRecommendationRunID)
	}
	fmt.Fprintln(cmd.OutOrStdout())

	order := []struct {
		name   string
		bucket transactions.Bucket
	}{
		{name: "Strong moves", bucket: transactions.BucketStrongMove},
		{name: "Marginal moves", bucket: transactions.BucketMarginalMove},
		{name: "Risky moves", bucket: transactions.BucketRiskyMove},
		{name: "Watch only", bucket: transactions.BucketWatchOnly},
	}
	grouped := groupMoves(plan.Items)
	for i, entry := range order {
		fmt.Fprintln(cmd.OutOrStdout(), entry.name)
		printTransactionRowsTable(cmd, grouped[entry.bucket])
		if i < len(order)-1 {
			fmt.Fprintln(cmd.OutOrStdout())
		}
	}

	fmt.Fprintln(cmd.OutOrStdout())
	fmt.Fprintln(cmd.OutOrStdout(), "Summary")
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "BUCKET\tCOUNT")
	for _, entry := range order {
		fmt.Fprintf(w, "%s\t%d\n", entry.bucket, plan.Summary.Counts[entry.bucket])
	}
	w.Flush()
}

func printTransactionRowsTable(cmd *cobra.Command, rows []transactions.PlanItem) {
	if len(rows) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "(none)")
		return
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "BUCKET\tADD\tADD_DATE\tADD_OPP\tDROP\tDROP_BEST_DATE\tDROP_BEST_OPP\tDELTA_START\tADD_FPTS\tDROP_FPTS\tADD_STARTS\tDROP_STARTS\tFLAGS")
	for _, row := range rows {
		addTotal := "-"
		if row.AddTotalProjectedFPTS != nil {
			addTotal = fmt.Sprintf("%.1f", *row.AddTotalProjectedFPTS)
		}
		dropTotal := "-"
		if row.DropTotalProjectedFPTS != nil {
			dropTotal = fmt.Sprintf("%.1f", *row.DropTotalProjectedFPTS)
		}
		delta := "-"
		if row.DeltaFPTS != nil {
			delta = fmt.Sprintf("%+.1f", *row.DeltaFPTS)
		}
		fmt.Fprintf(
			w,
			"%s\t%s (%s)\t%s\t%s\t%s (%s)\t%s\t%s\t%s\t%s\t%s\t%d\t%d\t%s\n",
			row.Bucket,
			row.AddPlayerName,
			firstNonEmpty(row.AddPlayerTeam, "-"),
			firstNonEmpty(row.AddStartDate, "-"),
			firstNonEmpty(row.AddStartOpponent, "-"),
			row.DropPlayerName,
			firstNonEmpty(row.DropPlayerTeam, "-"),
			firstNonEmpty(row.DropBestStartDate, "-"),
			firstNonEmpty(row.DropBestStartOpponent, "-"),
			delta,
			addTotal,
			dropTotal,
			row.AddProjectedStartCount,
			row.DropProjectedStartCount,
			strings.Join(row.Flags, ","),
		)
	}
	w.Flush()
}

func printTransactionCompare(cmd *cobra.Command, rows []transactions.PlanItem) {
	if len(rows) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "(none)")
		return
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ADD\tADD_DATE\tADD_OPP\tDROP\tDROP_BEST_DATE\tDROP_BEST_OPP\tBUCKET\tDELTA_START\tNOTES")
	for _, row := range rows {
		delta := "-"
		if row.DeltaFPTS != nil {
			delta = fmt.Sprintf("%+.1f", *row.DeltaFPTS)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", row.AddPlayerName, firstNonEmpty(row.AddStartDate, "-"), firstNonEmpty(row.AddStartOpponent, "-"), row.DropPlayerName, firstNonEmpty(row.DropBestStartDate, "-"), firstNonEmpty(row.DropBestStartOpponent, "-"), row.Bucket, delta, strings.Join(row.Notes, "; "))
	}
	w.Flush()
}

func printTransactionExplain(cmd *cobra.Command, plan *transactions.Plan) {
	fmt.Fprintf(cmd.OutOrStdout(), "Transaction Plan: %d\n", plan.ID)
	fmt.Fprintf(cmd.OutOrStdout(), "Window: %s to %s\n\n", plan.WindowStart, plan.WindowEnd)
	for _, row := range plan.Items {
		delta := "-"
		if row.DeltaFPTS != nil {
			delta = fmt.Sprintf("%+.1f", *row.DeltaFPTS)
		}
		fmt.Fprintf(
			cmd.OutOrStdout(),
			"- %s: add %s (%s) on %s vs %s, drop %s (%s) best %s vs %s, delta %s\n",
			row.Bucket,
			row.AddPlayerName,
			firstNonEmpty(row.AddPlayerTeam, "-"),
			firstNonEmpty(row.AddStartDate, "-"),
			firstNonEmpty(row.AddStartOpponent, "-"),
			row.DropPlayerName,
			firstNonEmpty(row.DropPlayerTeam, "-"),
			firstNonEmpty(row.DropBestStartDate, "-"),
			firstNonEmpty(row.DropBestStartOpponent, "-"),
			delta,
		)
		if len(row.Notes) > 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "  notes: %s\n", strings.Join(row.Notes, "; "))
		}
		if len(row.Flags) > 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "  flags: %s\n", strings.Join(row.Flags, ","))
		}
	}
}

func printTransactionPlanReview(cmd *cobra.Command, review *transactions.PlanReview) {
	if review == nil || review.Plan == nil {
		fmt.Fprintln(cmd.OutOrStdout(), "No review data found.")
		return
	}
	plan := review.Plan
	fmt.Fprintf(cmd.OutOrStdout(), "Transaction Plan Review: %d\n", plan.ID)
	fmt.Fprintf(cmd.OutOrStdout(), "Window: %s to %s\n\n", plan.WindowStart, plan.WindowEnd)

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ITEM\tSTATE\tBUCKET\tADD\tDROP\tDELTA\tNOTE\tUPDATED")
	for _, row := range review.Items {
		delta := "-"
		if row.DeltaFPTS != nil {
			delta = fmt.Sprintf("%+.1f", *row.DeltaFPTS)
		}
		updated := "-"
		if !row.ReviewUpdated.IsZero() {
			updated = row.ReviewUpdated.Format(time.RFC3339)
		}
		fmt.Fprintf(
			w,
			"%d\t%s\t%s\t%s (%s)\t%s (%s)\t%s\t%s\t%s\n",
			row.ID,
			row.ReviewState,
			row.Bucket,
			row.AddPlayerName,
			firstNonEmpty(row.AddPlayerTeam, "-"),
			row.DropPlayerName,
			firstNonEmpty(row.DropPlayerTeam, "-"),
			delta,
			firstNonEmpty(row.ReviewNote, "-"),
			updated,
		)
	}
	w.Flush()

	fmt.Fprintln(cmd.OutOrStdout())
	fmt.Fprintln(cmd.OutOrStdout(), "State Summary")
	sw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(sw, "STATE\tCOUNT")
	for _, state := range []transactions.ReviewState{
		transactions.ReviewStatePending,
		transactions.ReviewStateApproved,
		transactions.ReviewStateRejected,
		transactions.ReviewStateDeferred,
	} {
		fmt.Fprintf(sw, "%s\t%d\n", state, review.StateCounts[state])
	}
	sw.Flush()
}

func printTransactionQueue(cmd *cobra.Command, rows []transactions.ApprovalQueueItem) {
	if len(rows) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "(queue empty)")
		return
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ITEM\tPLAN\tSTATE\tADD\tDROP\tDELTA\tAPPROVED_AT\tNOTE")
	for _, row := range rows {
		delta := "-"
		if row.DeltaFPTS != nil {
			delta = fmt.Sprintf("%+.1f", *row.DeltaFPTS)
		}
		fmt.Fprintf(
			w,
			"%d\t%d\t%s\t%s (%s)\t%s (%s)\t%s\t%s\t%s\n",
			row.TransactionPlanItemID,
			row.PlanID,
			row.State,
			row.AddPlayerName,
			firstNonEmpty(row.AddPlayerTeam, "-"),
			row.DropPlayerName,
			firstNonEmpty(row.DropPlayerTeam, "-"),
			delta,
			row.ApprovedAt.Format(time.RFC3339),
			firstNonEmpty(row.Note, "-"),
		)
	}
	w.Flush()
}

func printTransactionApprovals(cmd *cobra.Command, rows []transactions.ApprovalStateRow) {
	if len(rows) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "(no review state rows found)")
		return
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ITEM\tPLAN\tSTATE\tBUCKET\tADD\tDROP\tDELTA\tUPDATED\tNOTE")
	for _, row := range rows {
		delta := "-"
		if row.DeltaFPTS != nil {
			delta = fmt.Sprintf("%+.1f", *row.DeltaFPTS)
		}
		fmt.Fprintf(
			w,
			"%d\t%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			row.TransactionPlanItemID,
			row.PlanID,
			row.CurrentState,
			row.Bucket,
			row.AddPlayerName,
			row.DropPlayerName,
			delta,
			row.UpdatedAt.Format(time.RFC3339),
			firstNonEmpty(row.Note, "-"),
		)
	}
	w.Flush()
}

func printAdHocRequest(cmd *cobra.Command, req *transactions.AdHocRequest) {
	if req == nil {
		fmt.Fprintln(cmd.OutOrStdout(), "Ad hoc request not found.")
		return
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Ad Hoc Request: %d\n", req.ID)
	fmt.Fprintf(cmd.OutOrStdout(), "Requested: add %s / drop %s\n", req.RequestedAddPlayerName, req.RequestedDropPlayerName)
	fmt.Fprintf(cmd.OutOrStdout(), "State: %s\n", req.RequestState)
	fmt.Fprintf(cmd.OutOrStdout(), "Resolution: %s\n", req.ResolutionStatus)
	if req.ResolvedAddPlayerName != "" || req.ResolvedDropPlayerName != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Resolved: add %s / drop %s\n", firstNonEmpty(req.ResolvedAddPlayerName, "-"), firstNonEmpty(req.ResolvedDropPlayerName, "-"))
	}
	if len(req.ResolutionNotes) > 0 {
		if b, err := json.MarshalIndent(req.ResolutionNotes, "", "  "); err == nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Notes: %s\n", string(b))
		}
	}
	if req.LinkedPlanItemID != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Linked plan item: %d\n", *req.LinkedPlanItemID)
	}
	if req.LinkedExecutionAttemptID != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Linked execution attempt: %d\n", *req.LinkedExecutionAttemptID)
	}
}

func printAdHocRequests(cmd *cobra.Command, rows []transactions.AdHocRequest) {
	if len(rows) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "(no ad hoc requests found)")
		return
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "REQUEST\tADD\tDROP\tSTATE\tRESOLUTION\tUPDATED")
	for _, r := range rows {
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\n", r.ID, r.RequestedAddPlayerName, r.RequestedDropPlayerName, r.RequestState, r.ResolutionStatus, r.UpdatedAt.Format(time.RFC3339))
	}
	w.Flush()
}

func groupMoves(items []transactions.PlanItem) map[transactions.Bucket][]transactions.PlanItem {
	out := map[transactions.Bucket][]transactions.PlanItem{}
	for _, item := range items {
		out[item.Bucket] = append(out[item.Bucket], item)
	}
	return out
}

func filterTopTransactionRows(items []transactions.PlanItem) []transactions.PlanItem {
	out := []transactions.PlanItem{}
	for _, row := range items {
		if row.Bucket == transactions.BucketWatchOnly {
			continue
		}
		out = append(out, row)
	}
	return out
}
