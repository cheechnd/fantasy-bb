package main

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"fantasy-baseball/internal/config"
	esrepo "fantasy-baseball/internal/espn/repository"
	"fantasy-baseball/internal/execute"
	exerealrepo "fantasy-baseball/internal/execute/real/repository"
	exerealsvc "fantasy-baseball/internal/execute/real/service"
	exerepo "fantasy-baseball/internal/execute/repository"
	exesvc "fantasy-baseball/internal/execute/service"
	lp "fantasy-baseball/internal/lineup/pitchers"
	pitchplan "fantasy-baseball/internal/pitchers/planner"
	"fantasy-baseball/internal/store/sqlite"
	adhocrepo "fantasy-baseball/internal/transactions/adhoc/repository"
	adhocsvc "fantasy-baseball/internal/transactions/adhoc/service"
	tranrepo "fantasy-baseball/internal/transactions/repository"
	reviewrepo "fantasy-baseball/internal/transactions/review/repository"

	"github.com/spf13/cobra"
)

func newExecuteCmd(opts *cliOptions) *cobra.Command {
	cmd := &cobra.Command{Use: "execute", Short: "Execute transaction and lineup operations"}
	cmd.AddGroup(
		&cobra.Group{ID: "run", Title: "Run"},
		&cobra.Group{ID: "followup", Title: "Follow-up"},
	)
	txCmd := newExecuteTransactionDirectCmd(opts)
	txCmd.GroupID = "run"
	lineupCmd := newExecuteLineupDirectCmd(opts)
	lineupCmd.GroupID = "run"
	preflightCmd := newExecutePreflightCmd(opts)
	preflightCmd.GroupID = "run"
	dryRunCmd := newExecuteDryRunCmd(opts)
	dryRunCmd.GroupID = "run"
	historyCmd := newExecuteHistoryCmd(opts)
	historyCmd.GroupID = "followup"
	verifyCmd := newExecuteVerifyCmd(opts)
	verifyCmd.GroupID = "followup"
	resolveCmd := newExecuteResolveCmd(opts)
	resolveCmd.GroupID = "followup"
	reconcileCmd := newExecuteReconcileCmd(opts)
	reconcileCmd.GroupID = "followup"
	pendingCmd := newExecutePendingCmd(opts)
	pendingCmd.GroupID = "followup"
	cmd.AddCommand(txCmd, lineupCmd, preflightCmd, dryRunCmd, historyCmd, verifyCmd, resolveCmd, reconcileCmd, pendingCmd)
	return cmd
}

func newExecuteTransactionDirectCmd(opts *cliOptions) *cobra.Command {
	var addName, dropName string
	var confirm bool
	var scoringPeriodID int64
	var nextDay bool
	cmd := &cobra.Command{
		Use:   "transaction",
		Short: "Prepare or execute one transaction directly by player names (WAIVERS blocked)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(addName) == "" {
				return fmt.Errorf("--add is required")
			}
			if cmd.Flags().Changed("scoring-period-id") && nextDay {
				return fmt.Errorf("use only one of --scoring-period-id or --next-day")
			}
			v, err := withAdHocAndRealExecuteService(cmd.Context(), opts, func(ctx context.Context, cfg config.Config, adhoc *adhocsvc.Service, real *exerealsvc.Service) (any, error) {
				req, err := adhoc.CreateAndResolveWithOptions(ctx, addName, dropName, adhocsvc.ResolveOptions{
					ScoringPeriodID:  intPtrFromInt64(optionalInt64(cmd, "scoring-period-id", scoringPeriodID)),
					EffectiveNextDay: nextDay,
				})
				if err != nil {
					return nil, err
				}
				updated, itemID, err := adhoc.EnsureExecutionCandidate(ctx, req.ID)
				if err != nil {
					return nil, err
				}
				res, err := real.ExecuteOne(ctx, cfg, execute.RealExecutionOptions{
					ItemID:           itemID,
					Confirm:          confirm,
					ScoringPeriodID:  optionalInt64(cmd, "scoring-period-id", scoringPeriodID),
					EffectiveNextDay: nextDay,
				})
				if err != nil {
					return nil, err
				}
				if res.Attempt != nil {
					_ = adhoc.LinkExecutionResult(ctx, updated.ID, res.Attempt.ID, res.Attempt.ExecutionStatus == execute.ExecutionStatusSucceeded)
				}
				return map[string]any{
					"request_id": updated.ID,
					"result":     res,
				}, nil
			})
			if err != nil {
				return err
			}
			payload := v.(map[string]any)
			res := payload["result"].(*execute.RealExecutionResult)
			if opts.OutputJSON {
				return writeJSON(cmd, payload)
			}
			printRealExecutionResult(cmd, res)
			return nil
		},
	}
	cmd.Flags().StringVar(&addName, "add", "", "Add player name")
	cmd.Flags().StringVar(&dropName, "drop", "", "Optional drop player name")
	cmd.Flags().BoolVar(&confirm, "confirm", false, "Actually perform the real write attempt")
	cmd.Flags().Int64Var(&scoringPeriodID, "scoring-period-id", 0, "Override ESPN scoring period id for execution")
	cmd.Flags().BoolVar(&nextDay, "next-day", false, "Execute effective next scoring period")
	return cmd
}

func newExecuteLineupDirectCmd(opts *cliOptions) *cobra.Command {
	var playerName string
	var toSlot string
	var syncRunID int64
	var scoringPeriodID int
	var nextDay bool
	var confirm bool
	cmd := &cobra.Command{
		Use:   "lineup",
		Short: "Prepare or execute one lineup move directly by player and slot",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cmd.Flags().Changed("scoring-period-id") && nextDay {
				return fmt.Errorf("use only one of --scoring-period-id or --next-day")
			}
			if strings.TrimSpace(playerName) == "" {
				return fmt.Errorf("--player is required")
			}
			if strings.TrimSpace(toSlot) == "" {
				return fmt.Errorf("--to-slot is required")
			}
			v, err := withLineupService(cmd.Context(), opts, func(ctx context.Context, cfg config.Config, svc *lp.Service) (any, error) {
				lineupOpts := lineupContextOptions(cmd, cfg, syncRunID, scoringPeriodID, nextDay)
				plan, err := svc.CreateAdHocPlanWithOptions(ctx, playerName, toSlot, lineupOpts)
				if err != nil {
					return nil, err
				}
				if len(plan.Items) == 0 {
					return nil, fmt.Errorf("no actionable lineup items generated")
				}
				item := plan.Items[0]
				if _, err := svc.Transition(ctx, plan.ID, item.ID, lp.ReviewStateApproved, "direct_execute"); err != nil {
					return nil, err
				}
				a, p, willWrite, msg, err := svc.ExecuteWithOptions(ctx, cfg, item.ID, confirm, lineupOpts)
				if err != nil {
					return nil, err
				}
				return map[string]any{
					"plan_id":    plan.ID,
					"item_id":    item.ID,
					"context":    targetLineupContextPayload(lineupOpts),
					"attempt":    a,
					"preflight":  p,
					"will_write": willWrite,
					"message":    msg,
				}, nil
			})
			if err != nil {
				return err
			}
			payload := v.(map[string]any)
			if opts.OutputJSON {
				return writeJSON(cmd, payload)
			}
			itemID, _ := payload["item_id"].(int64)
			printLineupExecutionPreview(cmd, itemID, payload)
			return nil
		},
	}
	cmd.Flags().StringVar(&playerName, "player", "", "Pitcher name on your roster")
	cmd.Flags().StringVar(&toSlot, "to-slot", "", "Target slot: P|SP|RP|BE")
	cmd.Flags().Int64Var(&syncRunID, "sync-run", 0, "ESPN sync run ID (defaults to latest)")
	cmd.Flags().IntVar(&scoringPeriodID, "scoring-period-id", 0, "Target ESPN scoring period ID")
	cmd.Flags().BoolVar(&nextDay, "next-day", false, "Execute lineup move in the next ESPN scoring period")
	cmd.Flags().BoolVar(&confirm, "confirm", false, "Actually perform the real write attempt")
	return cmd
}

func newExecutePreflightCmd(opts *cliOptions) *cobra.Command {
	var itemID int64
	var limit int
	var scoringPeriodID int
	var nextDay bool
	cmd := &cobra.Command{
		Use:   "preflight",
		Short: "Validate approved transaction items against current live state",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cmd.Flags().Changed("scoring-period-id") && nextDay {
				return fmt.Errorf("use only one of --scoring-period-id or --next-day")
			}
			v, err := withExecuteService(cmd.Context(), opts, func(ctx context.Context, svc *exesvc.Service) (any, error) {
				return svc.Preflight(ctx, execute.Options{
					ItemID:           optionalInt64(cmd, "item", itemID),
					Limit:            limit,
					ScoringPeriodID:  optionalInt(cmd, "scoring-period-id", scoringPeriodID),
					EffectiveNextDay: nextDay,
				})
			})
			if err != nil {
				return err
			}
			run := v.(*execute.Run)
			if opts.OutputJSON {
				return writeJSON(cmd, run)
			}
			printExecutionRun(cmd, run)
			return nil
		},
	}
	cmd.Flags().Int64Var(&itemID, "item", 0, "Optional approved item ID")
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum approved queue items to validate")
	cmd.Flags().IntVar(&scoringPeriodID, "scoring-period-id", 0, "Validate against a specific ESPN scoring period roster sync")
	cmd.Flags().BoolVar(&nextDay, "next-day", false, "Validate against the next-day ESPN roster sync")
	return cmd
}

func newExecuteDryRunCmd(opts *cliOptions) *cobra.Command {
	var itemID int64
	var limit int
	var scoringPeriodID int
	var nextDay bool
	cmd := &cobra.Command{
		Use:   "dry-run",
		Short: "Generate dry-run execution previews for approved items",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cmd.Flags().Changed("scoring-period-id") && nextDay {
				return fmt.Errorf("use only one of --scoring-period-id or --next-day")
			}
			v, err := withExecuteService(cmd.Context(), opts, func(ctx context.Context, svc *exesvc.Service) (any, error) {
				return svc.DryRun(ctx, execute.Options{
					ItemID:           optionalInt64(cmd, "item", itemID),
					Limit:            limit,
					ScoringPeriodID:  optionalInt(cmd, "scoring-period-id", scoringPeriodID),
					EffectiveNextDay: nextDay,
				})
			})
			if err != nil {
				return err
			}
			run := v.(*execute.Run)
			if opts.OutputJSON {
				return writeJSON(cmd, run)
			}
			printExecutionRun(cmd, run)
			return nil
		},
	}
	cmd.Flags().Int64Var(&itemID, "item", 0, "Optional approved item ID")
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum approved queue items to validate")
	cmd.Flags().IntVar(&scoringPeriodID, "scoring-period-id", 0, "Preview against a specific ESPN scoring period roster sync")
	cmd.Flags().BoolVar(&nextDay, "next-day", false, "Preview against the next-day ESPN roster sync")
	return cmd
}

func newExecuteQueueCmd(opts *cliOptions) *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "readiness",
		Short: "Show approved items with latest execution-readiness status",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := withExecuteService(cmd.Context(), opts, func(ctx context.Context, svc *exesvc.Service) (any, error) {
				return svc.Queue(ctx, limit)
			})
			if err != nil {
				return err
			}
			rows := v.([]execute.QueueRow)
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"ok": true, "count": len(rows), "rows": rows})
			}
			printExecuteQueue(cmd, rows)
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "Maximum rows to show")
	return cmd
}

func newExecuteLastCmd(opts *cliOptions) *cobra.Command {
	var runID int64
	cmd := &cobra.Command{
		Use:   "last-run",
		Short: "Show execution run (latest by default)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := withExecuteService(cmd.Context(), opts, func(ctx context.Context, svc *exesvc.Service) (any, error) {
				if runID > 0 {
					return svc.Show(ctx, runID)
				}
				return svc.Last(ctx)
			})
			if err != nil {
				return err
			}
			run := v.(*execute.Run)
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"run": run})
			}
			if run == nil {
				if runID > 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "Execution run %d not found.\n", runID)
					return nil
				}
				fmt.Fprintln(cmd.OutOrStdout(), "No execution runs found.")
				return nil
			}
			printExecutionRun(cmd, run)
			return nil
		},
	}
	cmd.Flags().Int64Var(&runID, "run-id", 0, "Execution run ID (defaults to latest)")
	return cmd
}

func newExecuteTransactionCmd(opts *cliOptions) *cobra.Command {
	var itemID int64
	var confirm bool
	var scoringPeriodID int64
	var nextDay bool
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Prepare or execute one approved add/drop transaction",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if itemID <= 0 {
				return fmt.Errorf("--item must be > 0")
			}
			if cmd.Flags().Changed("scoring-period-id") && nextDay {
				return fmt.Errorf("use only one of --scoring-period-id or --next-day")
			}
			v, err := withRealExecuteService(cmd.Context(), opts, func(ctx context.Context, cfg config.Config, svc *exerealsvc.Service) (any, error) {
				return svc.ExecuteOne(ctx, cfg, execute.RealExecutionOptions{
					ItemID:           itemID,
					Confirm:          confirm,
					ScoringPeriodID:  optionalInt64(cmd, "scoring-period-id", scoringPeriodID),
					EffectiveNextDay: nextDay,
				})
			})
			if err != nil {
				return err
			}
			res := v.(*execute.RealExecutionResult)
			if opts.OutputJSON {
				return writeJSON(cmd, res)
			}
			printRealExecutionResult(cmd, res)
			return nil
		},
	}
	cmd.Flags().Int64Var(&itemID, "item", 0, "Approved queue item ID to execute")
	cmd.Flags().BoolVar(&confirm, "confirm", false, "Actually perform the real write attempt")
	cmd.Flags().Int64Var(&scoringPeriodID, "scoring-period-id", 0, "Override ESPN scoring period id for execution")
	cmd.Flags().BoolVar(&nextDay, "next-day", false, "Execute effective next scoring period")
	return cmd
}

func newExecuteAdHocCmd(opts *cliOptions) *cobra.Command {
	var requestID int64
	var confirm bool
	var scoringPeriodID int64
	var nextDay bool
	cmd := &cobra.Command{
		Use:   "run-ad-hoc",
		Short: "Prepare or execute one resolved ad hoc add/drop request",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if requestID <= 0 {
				return fmt.Errorf("--request-id must be > 0")
			}
			if cmd.Flags().Changed("scoring-period-id") && nextDay {
				return fmt.Errorf("use only one of --scoring-period-id or --next-day")
			}
			v, err := withAdHocAndRealExecuteService(cmd.Context(), opts, func(ctx context.Context, cfg config.Config, adhoc *adhocsvc.Service, real *exerealsvc.Service) (any, error) {
				req, itemID, err := adhoc.EnsureExecutionCandidate(ctx, requestID)
				if err != nil {
					return nil, err
				}
				res, err := real.ExecuteOne(ctx, cfg, execute.RealExecutionOptions{
					ItemID:           itemID,
					Confirm:          confirm,
					ScoringPeriodID:  optionalInt64(cmd, "scoring-period-id", scoringPeriodID),
					EffectiveNextDay: nextDay,
				})
				if err != nil {
					return nil, err
				}
				if res.Attempt != nil {
					_ = adhoc.LinkExecutionResult(ctx, req.ID, res.Attempt.ID, res.Attempt.ExecutionStatus == execute.ExecutionStatusSucceeded)
				}
				return res, nil
			})
			if err != nil {
				return err
			}
			res := v.(*execute.RealExecutionResult)
			if opts.OutputJSON {
				return writeJSON(cmd, res)
			}
			printRealExecutionResult(cmd, res)
			return nil
		},
	}
	cmd.Flags().Int64Var(&requestID, "request-id", 0, "Ad hoc request ID")
	cmd.Flags().BoolVar(&confirm, "confirm", false, "Actually perform the real write attempt")
	cmd.Flags().Int64Var(&scoringPeriodID, "scoring-period-id", 0, "Override ESPN scoring period id for execution")
	cmd.Flags().BoolVar(&nextDay, "next-day", false, "Execute effective next scoring period")
	return cmd
}

func newExecuteHistoryCmd(opts *cliOptions) *cobra.Command {
	var limit int
	var attemptID int64
	cmd := &cobra.Command{
		Use:   "history",
		Short: "Show execution attempts (list or one by --execution-id)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if attemptID > 0 {
				v, err := withRealExecuteService(cmd.Context(), opts, func(ctx context.Context, _ config.Config, svc *exerealsvc.Service) (any, error) {
					return svc.ByID(ctx, attemptID)
				})
				if err != nil {
					return err
				}
				attempt := v.(*execute.Attempt)
				if opts.OutputJSON {
					return writeJSON(cmd, map[string]any{"attempt": attempt})
				}
				if attempt == nil {
					fmt.Fprintf(cmd.OutOrStdout(), "Execution attempt %d not found.\n", attemptID)
					return nil
				}
				printExecutionAttempt(cmd, attempt)
				return nil
			}
			v, err := withRealExecuteService(cmd.Context(), opts, func(ctx context.Context, _ config.Config, svc *exerealsvc.Service) (any, error) {
				return svc.History(ctx, limit)
			})
			if err != nil {
				return err
			}
			rows := v.([]execute.Attempt)
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"count": len(rows), "attempts": rows})
			}
			printExecutionHistory(cmd, rows)
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "Maximum attempts to show")
	cmd.Flags().Int64Var(&attemptID, "execution-id", 0, "Execution attempt ID (shows one attempt)")
	return cmd
}

func newExecuteVerifyCmd(opts *cliOptions) *cobra.Command {
	var attemptID int64
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Re-run verification checks for a prior execution attempt",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if attemptID <= 0 {
				return fmt.Errorf("--execution-id must be > 0")
			}
			v, err := withRealExecuteService(cmd.Context(), opts, func(ctx context.Context, cfg config.Config, svc *exerealsvc.Service) (any, error) {
				return svc.VerifyAttempt(ctx, cfg, attemptID)
			})
			if err != nil {
				return err
			}
			res := v.(*execute.VerifyResult)
			if opts.OutputJSON {
				return writeJSON(cmd, res)
			}
			printVerifyResult(cmd, res)
			return nil
		},
	}
	cmd.Flags().Int64Var(&attemptID, "execution-id", 0, "Execution attempt ID")
	return cmd
}

func newExecutePendingCmd(opts *cliOptions) *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "pending",
		Short: "Show unresolved execution attempts (ambiguous/pending/unverified)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := withRealExecuteService(cmd.Context(), opts, func(ctx context.Context, _ config.Config, svc *exerealsvc.Service) (any, error) {
				return svc.Pending(ctx, limit)
			})
			if err != nil {
				return err
			}
			rows := v.([]execute.Attempt)
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"count": len(rows), "attempts": rows})
			}
			printExecutionPending(cmd, rows)
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "Maximum rows to show")
	return cmd
}

func newExecuteResolveCmd(opts *cliOptions) *cobra.Command {
	var attemptID int64
	cmd := &cobra.Command{
		Use:   "resolve",
		Short: "Run verify then reconcile (if needed) for one unresolved execution attempt",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if attemptID <= 0 {
				return fmt.Errorf("--execution-id must be > 0")
			}
			v, err := withRealExecuteService(cmd.Context(), opts, func(ctx context.Context, cfg config.Config, svc *exerealsvc.Service) (any, error) {
				verifyRes, err := svc.VerifyAttempt(ctx, cfg, attemptID)
				if err != nil {
					return nil, err
				}
				payload := map[string]any{
					"verify": verifyRes,
					"final":  verifyRes,
				}
				if verifyRes != nil && verifyRes.Attempt != nil && attemptNeedsFollowup(verifyRes.Attempt) {
					reconcileRes, recErr := svc.ReconcileAttempt(ctx, cfg, attemptID)
					if recErr != nil {
						payload["reconcile_error"] = recErr.Error()
						return payload, nil
					}
					payload["reconcile"] = reconcileRes
					payload["final"] = reconcileRes
				}
				return payload, nil
			})
			if err != nil {
				return err
			}
			payload := v.(map[string]any)
			if opts.OutputJSON {
				return writeJSON(cmd, payload)
			}
			finalRes, _ := payload["final"].(*execute.VerifyResult)
			if finalRes != nil {
				printVerifyResult(cmd, finalRes)
			}
			if recErr, ok := payload["reconcile_error"].(string); ok && strings.TrimSpace(recErr) != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Reconcile warning: %s\n", recErr)
			}
			return nil
		},
	}
	cmd.Flags().Int64Var(&attemptID, "execution-id", 0, "Execution attempt ID")
	return cmd
}

func newExecuteReconcileCmd(opts *cliOptions) *cobra.Command {
	var attemptID int64
	cmd := &cobra.Command{
		Use:   "reconcile",
		Short: "Reconcile a prior unresolved execution attempt against live roster state",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if attemptID <= 0 {
				return fmt.Errorf("--execution-id must be > 0")
			}
			v, err := withRealExecuteService(cmd.Context(), opts, func(ctx context.Context, cfg config.Config, svc *exerealsvc.Service) (any, error) {
				return svc.ReconcileAttempt(ctx, cfg, attemptID)
			})
			if err != nil {
				return err
			}
			res := v.(*execute.VerifyResult)
			if opts.OutputJSON {
				return writeJSON(cmd, res)
			}
			printVerifyResult(cmd, res)
			return nil
		},
	}
	cmd.Flags().Int64Var(&attemptID, "execution-id", 0, "Execution attempt ID")
	return cmd
}

func withExecuteService(ctx context.Context, opts *cliOptions, fn func(context.Context, *exesvc.Service) (any, error)) (any, error) {
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

	service := exesvc.New(
		exerepo.New(s.DB()),
		reviewrepo.New(s.DB()),
		esrepo.New(s.DB()),
		tranrepo.New(s.DB()),
		execute.ServiceConfig{
			DefaultLimit:                 cfg.Execution.Preflight.DefaultLimit,
			MaxLimit:                     cfg.Execution.Preflight.MaxLimit,
			CandidateRefreshLimit:        cfg.Execution.Preflight.CandidateRefreshLimit,
			StaleHoursThreshold:          cfg.Execution.Preflight.StaleHoursThreshold,
			RequireLiveRosterCheck:       cfg.Execution.Preflight.RequireLiveRosterCheck,
			RequireLiveAvailabilityCheck: cfg.Execution.Preflight.RequireLiveAvailabilityCheck,
		},
	)
	return fn(ctx, service)
}

func withRealExecuteService(ctx context.Context, opts *cliOptions, fn func(context.Context, config.Config, *exerealsvc.Service) (any, error)) (any, error) {
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

	preflightSvc := exesvc.New(
		exerepo.New(s.DB()),
		reviewrepo.New(s.DB()),
		esrepo.New(s.DB()),
		tranrepo.New(s.DB()),
		execute.ServiceConfig{
			DefaultLimit:                 cfg.Execution.Preflight.DefaultLimit,
			MaxLimit:                     cfg.Execution.Preflight.MaxLimit,
			CandidateRefreshLimit:        cfg.Execution.Preflight.CandidateRefreshLimit,
			StaleHoursThreshold:          cfg.Execution.Preflight.StaleHoursThreshold,
			RequireLiveRosterCheck:       cfg.Execution.Preflight.RequireLiveRosterCheck,
			RequireLiveAvailabilityCheck: cfg.Execution.Preflight.RequireLiveAvailabilityCheck,
		},
	)
	realSvc := exerealsvc.New(
		preflightSvc,
		reviewrepo.New(s.DB()),
		tranrepo.New(s.DB()),
		exerealrepo.New(s.DB()),
		exerealsvc.NewESPNWriter(time.Duration(cfg.ESPN.TimeoutSeconds)*time.Second),
		exerealsvc.NewESPNVerifier(time.Duration(cfg.Execution.Real.VerificationTimeoutSeconds)*time.Second),
	)
	return fn(ctx, cfg, realSvc)
}

func withAdHocAndRealExecuteService(ctx context.Context, opts *cliOptions, fn func(context.Context, config.Config, *adhocsvc.Service, *exerealsvc.Service) (any, error)) (any, error) {
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

	preflightSvc := exesvc.New(
		exerepo.New(s.DB()),
		reviewrepo.New(s.DB()),
		esrepo.New(s.DB()),
		tranrepo.New(s.DB()),
		execute.ServiceConfig{
			DefaultLimit:                 cfg.Execution.Preflight.DefaultLimit,
			MaxLimit:                     cfg.Execution.Preflight.MaxLimit,
			CandidateRefreshLimit:        cfg.Execution.Preflight.CandidateRefreshLimit,
			StaleHoursThreshold:          cfg.Execution.Preflight.StaleHoursThreshold,
			RequireLiveRosterCheck:       cfg.Execution.Preflight.RequireLiveRosterCheck,
			RequireLiveAvailabilityCheck: cfg.Execution.Preflight.RequireLiveAvailabilityCheck,
		},
	)
	realSvc := exerealsvc.New(
		preflightSvc,
		reviewrepo.New(s.DB()),
		tranrepo.New(s.DB()),
		exerealrepo.New(s.DB()),
		exerealsvc.NewESPNWriter(time.Duration(cfg.ESPN.TimeoutSeconds)*time.Second),
		exerealsvc.NewESPNVerifier(time.Duration(cfg.Execution.Real.VerificationTimeoutSeconds)*time.Second),
	)
	adHocSvc := adhocsvc.New(
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
	return fn(ctx, cfg, adHocSvc, realSvc)
}

func withLineupService(ctx context.Context, opts *cliOptions, fn func(context.Context, config.Config, *lp.Service) (any, error)) (any, error) {
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

	repo := lp.NewRepository(s.DB())
	pitcherPlanRepo := pitchplan.NewRepository(s.DB())
	espnRepo := esrepo.New(s.DB())
	svc := lp.NewService(
		repo,
		pitcherPlanRepo,
		espnRepo,
		lp.NewESPNWriter(lineupTimeoutDuration(cfg.ESPN.TimeoutSeconds)),
		lp.NewESPNVerifier(lineupTimeoutDuration(cfg.ESPN.TimeoutSeconds)),
	)
	return fn(ctx, cfg, svc)
}

func lineupTimeoutDuration(seconds int) time.Duration {
	if seconds <= 0 {
		seconds = 20
	}
	return time.Duration(seconds) * time.Second
}

func optionalInt64(cmd *cobra.Command, name string, value int64) *int64 {
	if cmd.Flags().Changed(name) {
		v := value
		return &v
	}
	return nil
}

func optionalInt(cmd *cobra.Command, name string, value int) *int {
	if cmd.Flags().Changed(name) {
		v := value
		return &v
	}
	return nil
}

func intPtrFromInt64(v *int64) *int {
	if v == nil {
		return nil
	}
	out := int(*v)
	return &out
}

func lineupContextOptions(cmd *cobra.Command, cfg config.Config, syncRunID int64, scoringPeriodID int, nextDay bool) lp.ContextOptions {
	opts := lp.ContextOptions{
		SyncRunID:        optionalInt64(cmd, "sync-run", syncRunID),
		ScoringPeriodID:  optionalInt(cmd, "scoring-period-id", scoringPeriodID),
		EffectiveNextDay: nextDay,
	}
	if nextDay {
		opts.ScoringPeriodDate = nextDayDate(cfg.League.Timezone)
	}
	return opts
}

func nextDayDate(timezone string) *string {
	loc, err := time.LoadLocation(strings.TrimSpace(timezone))
	if err != nil {
		loc = time.Local
	}
	v := time.Now().In(loc).AddDate(0, 0, 1).Format("2006-01-02")
	return &v
}

func targetLineupContextPayload(opts lp.ContextOptions) map[string]any {
	return map[string]any{
		"sync_run_id":                opts.SyncRunID,
		"target_scoring_period_id":   opts.ScoringPeriodID,
		"target_scoring_period_date": opts.ScoringPeriodDate,
		"effective_next_day":         opts.EffectiveNextDay,
	}
}

func printExecutionRun(cmd *cobra.Command, run *execute.Run) {
	if run == nil {
		fmt.Fprintln(cmd.OutOrStdout(), "No execution run found.")
		return
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Execution Run: %d\n", run.ID)
	fmt.Fprintf(cmd.OutOrStdout(), "Type: %s\n", run.RunType)
	fmt.Fprintf(cmd.OutOrStdout(), "Created: %s\n\n", run.CreatedAt.Format(time.RFC3339))

	order := []struct {
		title  string
		status execute.ValidationStatus
	}{
		{"Executable", execute.StatusExecutable},
		{"Blocked", execute.StatusBlocked},
		{"Stale", execute.StatusStale},
		{"Conflict", execute.StatusConflict},
		{"Unknown", execute.StatusUnknown},
	}
	grouped := map[execute.ValidationStatus][]execute.RunItem{}
	for _, item := range run.Items {
		grouped[item.ValidationStatus] = append(grouped[item.ValidationStatus], item)
	}
	for i, g := range order {
		fmt.Fprintln(cmd.OutOrStdout(), g.title)
		printExecutionItemsTable(cmd, grouped[g.status])
		if i < len(order)-1 {
			fmt.Fprintln(cmd.OutOrStdout())
		}
	}
}

func printExecutionItemsTable(cmd *cobra.Command, rows []execute.RunItem) {
	if len(rows) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "(none)")
		return
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "APPROVED_ITEM\tPLAN\tADD\tDROP\tSTATUS\tREASONS")
	for _, row := range rows {
		reasons := make([]string, 0, len(row.ValidationReasons))
		for _, r := range row.ValidationReasons {
			reasons = append(reasons, r.Code)
		}
		fmt.Fprintf(w, "%d\t%d\t%s\t%s\t%s\t%s\n", row.ApprovedItemID, row.SourcePlanID, row.AddPlayerName, row.DropPlayerName, row.ValidationStatus, strings.Join(reasons, ","))
	}
	w.Flush()
}

func printExecuteQueue(cmd *cobra.Command, rows []execute.QueueRow) {
	if len(rows) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "(queue empty)")
		return
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "APPROVED_ITEM\tPLAN\tADD\tDROP\tAPPROVED_AT\tLAST_STATUS\tLAST_RUN\tLAST_CHECKED\tNOTE")
	for _, row := range rows {
		lastStatus := "-"
		if row.LastValidation != nil {
			lastStatus = string(*row.LastValidation)
		}
		lastRun := "-"
		if row.LastExecutionRunID != nil {
			lastRun = fmt.Sprintf("%d", *row.LastExecutionRunID)
		}
		lastChecked := "-"
		if row.LastCheckedAt != nil {
			lastChecked = row.LastCheckedAt.Format(time.RFC3339)
		}
		fmt.Fprintf(w, "%d\t%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", row.ApprovedItemID, row.SourcePlanID, row.AddPlayerName, row.DropPlayerName, row.ApprovedAt.Format(time.RFC3339), lastStatus, lastRun, lastChecked, firstNonEmpty(row.ApprovalNote, "-"))
	}
	w.Flush()
}

func printRealExecutionResult(cmd *cobra.Command, res *execute.RealExecutionResult) {
	if res == nil {
		fmt.Fprintln(cmd.OutOrStdout(), "No result.")
		return
	}
	if res.PreflightItem != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Approved Item: %d\n", res.PreflightItem.ApprovedItemID)
		fmt.Fprintf(cmd.OutOrStdout(), "Action: %s\n", formatExecutionAction(res.PreflightItem.AddPlayerName, res.PreflightItem.DropPlayerName))
		fmt.Fprintf(cmd.OutOrStdout(), "Immediate preflight: %s\n", res.PreflightItem.ValidationStatus)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Will write: %t\n", res.WillWrite)
	if res.Attempt != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Execution ID: %d\n", res.Attempt.ID)
		fmt.Fprintf(cmd.OutOrStdout(), "Execution status: %s\n", res.Attempt.ExecutionStatus)
		fmt.Fprintf(cmd.OutOrStdout(), "Verification status: %s\n", res.Attempt.VerificationStatus)
		if res.Attempt.ExecutionStatus == execute.ExecutionStatusSucceeded {
			fmt.Fprintln(cmd.OutOrStdout(), "REAL WRITE SUCCESS")
		}
	}
	if strings.TrimSpace(res.Message) != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Message: %s\n", res.Message)
	}
	if res.Attempt != nil {
		if next := nextActionForAttempt(*res.Attempt); next != "-" {
			fmt.Fprintf(cmd.OutOrStdout(), "Next: %s\n", next)
		}
	}
}

func printVerifyResult(cmd *cobra.Command, res *execute.VerifyResult) {
	if res == nil {
		fmt.Fprintln(cmd.OutOrStdout(), "No verification result.")
		return
	}
	if res.Attempt != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Execution ID: %d\n", res.Attempt.ID)
		fmt.Fprintf(cmd.OutOrStdout(), "Action: %s\n", formatExecutionAction(res.Attempt.AddPlayerName, res.Attempt.DropPlayerName))
		fmt.Fprintf(cmd.OutOrStdout(), "Execution status: %s\n", res.Attempt.ExecutionStatus)
		fmt.Fprintf(cmd.OutOrStdout(), "Verification status: %s\n", res.Attempt.VerificationStatus)
	}
	if strings.TrimSpace(res.Inference) != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Inference: %s\n", res.Inference)
	}
	if strings.TrimSpace(res.Message) != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Message: %s\n", res.Message)
	}
	if res.Attempt != nil {
		if next := nextActionForAttempt(*res.Attempt); next != "-" {
			fmt.Fprintf(cmd.OutOrStdout(), "Next: %s\n", next)
		}
	}
}

func printExecutionPending(cmd *cobra.Command, rows []execute.Attempt) {
	if len(rows) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No unresolved execution attempts.")
		return
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "EXECUTION_ID\tITEM\tPLAN\tADD\tDROP\tSTATUS\tVERIFY\tSTARTED\tNEXT_ACTION")
	for _, row := range rows {
		fmt.Fprintf(
			w,
			"%d\t%d\t%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
			row.ID, row.ApprovedItemID, row.SourcePlanID, row.AddPlayerName, row.DropPlayerName,
			row.ExecutionStatus, row.VerificationStatus, row.StartedAt.Format(time.RFC3339), nextActionForAttempt(row),
		)
	}
	w.Flush()
}

func printExecutionHistory(cmd *cobra.Command, rows []execute.Attempt) {
	if len(rows) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No real execution attempts found.")
		return
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "EXECUTION_ID\tITEM\tPLAN\tADD\tDROP\tSTARTED_AT\tSUBMITTED_AT\tSTATUS\tVERIFY")
	for _, row := range rows {
		submitted := "-"
		if row.SubmittedAt != nil {
			submitted = row.SubmittedAt.Format(time.RFC3339)
		}
		fmt.Fprintf(
			w,
			"%d\t%d\t%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
			row.ID,
			row.ApprovedItemID,
			row.SourcePlanID,
			row.AddPlayerName,
			row.DropPlayerName,
			row.StartedAt.Format(time.RFC3339),
			submitted,
			row.ExecutionStatus,
			row.VerificationStatus,
		)
	}
	w.Flush()
}

func printExecutionAttempt(cmd *cobra.Command, attempt *execute.Attempt) {
	if attempt == nil {
		fmt.Fprintln(cmd.OutOrStdout(), "Execution attempt not found.")
		return
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Execution ID: %d\n", attempt.ID)
	fmt.Fprintf(cmd.OutOrStdout(), "Approved item: %d\n", attempt.ApprovedItemID)
	fmt.Fprintf(cmd.OutOrStdout(), "Plan: %d\n", attempt.SourcePlanID)
	fmt.Fprintf(cmd.OutOrStdout(), "Action: %s\n", formatExecutionAction(attempt.AddPlayerName, attempt.DropPlayerName))
	fmt.Fprintf(cmd.OutOrStdout(), "Started: %s\n", attempt.StartedAt.Format(time.RFC3339))
	if attempt.CompletedAt != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Completed: %s\n", attempt.CompletedAt.Format(time.RFC3339))
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Execution status: %s\n", attempt.ExecutionStatus)
	fmt.Fprintf(cmd.OutOrStdout(), "Verification status: %s\n", attempt.VerificationStatus)
	if attempt.SubmittedAt != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Submitted: %s\n", attempt.SubmittedAt.Format(time.RFC3339))
	}
	if attempt.LastVerifiedAt != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Last verified: %s\n", attempt.LastVerifiedAt.Format(time.RFC3339))
	}
	if note, ok := attempt.Details["approved_note"].(string); ok && strings.HasPrefix(note, "ad_hoc_request:") {
		fmt.Fprintf(cmd.OutOrStdout(), "Source: ad_hoc (%s)\n", strings.TrimPrefix(note, "ad_hoc_request:"))
	} else {
		fmt.Fprintln(cmd.OutOrStdout(), "Source: approved_plan_item")
	}
	if msg := strings.TrimSpace(attempt.AmbiguousReason); msg != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Ambiguous reason: %s\n", msg)
	}
	if msg := strings.TrimSpace(attempt.ErrorMessage); msg != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Error: %s\n", msg)
	}
	if next := nextActionForAttempt(*attempt); next != "-" {
		fmt.Fprintf(cmd.OutOrStdout(), "Next: %s\n", next)
	}
	fmt.Fprintln(cmd.OutOrStdout())
	fmt.Fprintln(cmd.OutOrStdout(), "Events")
	if len(attempt.Events) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "(none)")
		return
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "EVENT\tAT")
	for _, ev := range attempt.Events {
		fmt.Fprintf(w, "%s\t%s\n", ev.EventType, ev.CreatedAt.Format(time.RFC3339))
	}
	w.Flush()
}

func attemptNeedsFollowup(a *execute.Attempt) bool {
	if a == nil {
		return false
	}
	return a.ExecutionStatus == execute.ExecutionStatusAmbiguous ||
		a.ExecutionStatus == execute.ExecutionStatusSubmitted ||
		a.VerificationStatus == execute.VerificationStatusPending ||
		a.VerificationStatus == execute.VerificationStatusUnverified ||
		a.VerificationStatus == execute.VerificationStatusUnknown ||
		a.VerificationStatus == execute.VerificationStatusVerificationFailed
}

func nextActionForAttempt(a execute.Attempt) string {
	switch {
	case a.ExecutionStatus == execute.ExecutionStatusAmbiguous ||
		a.ExecutionStatus == execute.ExecutionStatusSubmitted ||
		a.VerificationStatus == execute.VerificationStatusPending ||
		a.VerificationStatus == execute.VerificationStatusUnverified:
		return fmt.Sprintf("fb execute verify --execution-id %d", a.ID)
	case a.VerificationStatus == execute.VerificationStatusUnknown ||
		a.VerificationStatus == execute.VerificationStatusVerificationFailed:
		return fmt.Sprintf("fb execute resolve --execution-id %d", a.ID)
	default:
		return "-"
	}
}

func formatExecutionAction(addName, dropName string) string {
	if strings.TrimSpace(dropName) == "" {
		return fmt.Sprintf("add %s", addName)
	}
	return fmt.Sprintf("add %s / drop %s", addName, dropName)
}

func printLineupExecutionPreview(cmd *cobra.Command, itemID int64, payload map[string]any) {
	pre, _ := payload["preflight"].(*lp.PreflightItem)
	attempt, _ := payload["attempt"].(*lp.ExecutionAttempt)
	willWrite, _ := payload["will_write"].(bool)
	msg, _ := payload["message"].(string)

	fmt.Fprintf(cmd.OutOrStdout(), "Approved Lineup Item: %d\n", itemID)
	if pre != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Action: %s %s (%s -> %s)\n", pre.ActionType, pre.PlayerName, firstNonEmpty(pre.CurrentSlot, "-"), firstNonEmpty(pre.TargetSlot, "-"))
		if pre.TargetScoringPeriodID != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Target scoring period: %d\n", *pre.TargetScoringPeriodID)
		}
		if pre.TargetScoringPeriodDate != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Target date: %s\n", *pre.TargetScoringPeriodDate)
		}
		if pre.EffectiveNextDay {
			fmt.Fprintln(cmd.OutOrStdout(), "Effective: next-day")
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Immediate preflight: %s\n", pre.ValidationStatus)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Will write: %t\n", willWrite)
	if attempt != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Execution ID: %d\n", attempt.ID)
		fmt.Fprintf(cmd.OutOrStdout(), "Execution status: %s\n", attempt.ExecutionStatus)
		fmt.Fprintf(cmd.OutOrStdout(), "Verification status: %s\n", attempt.VerificationStatus)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Message: %s\n", msg)
}
