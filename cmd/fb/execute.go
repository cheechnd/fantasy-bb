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
	"fantasy-baseball/internal/store/sqlite"
	tranrepo "fantasy-baseball/internal/transactions/repository"
	reviewrepo "fantasy-baseball/internal/transactions/review/repository"

	"github.com/spf13/cobra"
)

func newExecuteCmd(opts *cliOptions) *cobra.Command {
	cmd := &cobra.Command{Use: "execute", Short: "Execution readiness and single-item real transaction execution"}
	cmd.AddGroup(
		&cobra.Group{ID: "run", Title: "Run"},
		&cobra.Group{ID: "write", Title: "Real Write"},
		&cobra.Group{ID: "inspect", Title: "Inspection"},
	)
	preflightCmd := newExecutePreflightCmd(opts)
	preflightCmd.GroupID = "run"
	dryRunCmd := newExecuteDryRunCmd(opts)
	dryRunCmd.GroupID = "run"
	transactionCmd := newExecuteTransactionCmd(opts)
	transactionCmd.GroupID = "write"
	confirmCmd := newExecuteConfirmCmd(opts)
	confirmCmd.GroupID = "write"
	queueCmd := newExecuteQueueCmd(opts)
	queueCmd.GroupID = "inspect"
	lastCmd := newExecuteLastCmd(opts)
	lastCmd.GroupID = "inspect"
	showCmd := newExecuteShowCmd(opts)
	showCmd.GroupID = "inspect"
	historyCmd := newExecuteHistoryCmd(opts)
	historyCmd.GroupID = "inspect"
	resultCmd := newExecuteResultCmd(opts)
	resultCmd.GroupID = "inspect"
	cmd.AddCommand(preflightCmd, dryRunCmd, transactionCmd, confirmCmd, queueCmd, lastCmd, showCmd, historyCmd, resultCmd)
	return cmd
}

func newExecutePreflightCmd(opts *cliOptions) *cobra.Command {
	var itemID int64
	var limit int
	cmd := &cobra.Command{
		Use:   "preflight",
		Short: "Validate approved transaction items against current live state",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := withExecuteService(cmd.Context(), opts, func(ctx context.Context, svc *exesvc.Service) (any, error) {
				return svc.Preflight(ctx, execute.Options{
					ItemID: optionalInt64(cmd, "item", itemID),
					Limit:  limit,
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
	return cmd
}

func newExecuteDryRunCmd(opts *cliOptions) *cobra.Command {
	var itemID int64
	var limit int
	cmd := &cobra.Command{
		Use:   "dry-run",
		Short: "Generate dry-run execution previews for approved items",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := withExecuteService(cmd.Context(), opts, func(ctx context.Context, svc *exesvc.Service) (any, error) {
				return svc.DryRun(ctx, execute.Options{
					ItemID: optionalInt64(cmd, "item", itemID),
					Limit:  limit,
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
	return cmd
}

func newExecuteQueueCmd(opts *cliOptions) *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "queue",
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
	cmd := &cobra.Command{
		Use:   "last",
		Short: "Show latest preflight/dry-run execution run",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := withExecuteService(cmd.Context(), opts, func(ctx context.Context, svc *exesvc.Service) (any, error) {
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
				fmt.Fprintln(cmd.OutOrStdout(), "No execution runs found.")
				return nil
			}
			printExecutionRun(cmd, run)
			return nil
		},
	}
	return cmd
}

func newExecuteShowCmd(opts *cliOptions) *cobra.Command {
	var runID int64
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show a specific execution run",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if runID <= 0 {
				return fmt.Errorf("--run-id must be > 0")
			}
			v, err := withExecuteService(cmd.Context(), opts, func(ctx context.Context, svc *exesvc.Service) (any, error) {
				return svc.Show(ctx, runID)
			})
			if err != nil {
				return err
			}
			run := v.(*execute.Run)
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"run": run})
			}
			if run == nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Execution run %d not found.\n", runID)
				return nil
			}
			printExecutionRun(cmd, run)
			return nil
		},
	}
	cmd.Flags().Int64Var(&runID, "run-id", 0, "Execution run ID")
	return cmd
}

func newExecuteTransactionCmd(opts *cliOptions) *cobra.Command {
	var itemID int64
	var confirm bool
	cmd := &cobra.Command{
		Use:   "transaction",
		Short: "Prepare or execute one approved add/drop transaction",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if itemID <= 0 {
				return fmt.Errorf("--item must be > 0")
			}
			v, err := withRealExecuteService(cmd.Context(), opts, func(ctx context.Context, cfg config.Config, svc *exerealsvc.Service) (any, error) {
				return svc.ExecuteOne(ctx, cfg, execute.RealExecutionOptions{
					ItemID:  itemID,
					Confirm: confirm,
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
	return cmd
}

func newExecuteConfirmCmd(opts *cliOptions) *cobra.Command {
	var itemID int64
	cmd := &cobra.Command{
		Use:   "confirm",
		Short: "Execute one approved add/drop transaction with explicit confirmation",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if itemID <= 0 {
				return fmt.Errorf("--item must be > 0")
			}
			v, err := withRealExecuteService(cmd.Context(), opts, func(ctx context.Context, cfg config.Config, svc *exerealsvc.Service) (any, error) {
				return svc.ExecuteOne(ctx, cfg, execute.RealExecutionOptions{
					ItemID:  itemID,
					Confirm: true,
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
	return cmd
}

func newExecuteHistoryCmd(opts *cliOptions) *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "history",
		Short: "Show real execution attempt history",
		RunE: func(cmd *cobra.Command, _ []string) error {
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
	return cmd
}

func newExecuteResultCmd(opts *cliOptions) *cobra.Command {
	var attemptID int64
	cmd := &cobra.Command{
		Use:   "result",
		Short: "Show details for a specific real execution attempt",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if attemptID <= 0 {
				return fmt.Errorf("--execution-id must be > 0")
			}
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

func optionalInt64(cmd *cobra.Command, name string, value int64) *int64 {
	if cmd.Flags().Changed(name) {
		v := value
		return &v
	}
	return nil
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
		fmt.Fprintf(cmd.OutOrStdout(), "Action: add %s / drop %s\n", res.PreflightItem.AddPlayerName, res.PreflightItem.DropPlayerName)
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
}

func printExecutionHistory(cmd *cobra.Command, rows []execute.Attempt) {
	if len(rows) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No real execution attempts found.")
		return
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "EXECUTION_ID\tITEM\tPLAN\tADD\tDROP\tSTARTED_AT\tSTATUS\tVERIFY")
	for _, row := range rows {
		fmt.Fprintf(
			w,
			"%d\t%d\t%d\t%s\t%s\t%s\t%s\t%s\n",
			row.ID,
			row.ApprovedItemID,
			row.SourcePlanID,
			row.AddPlayerName,
			row.DropPlayerName,
			row.StartedAt.Format(time.RFC3339),
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
	fmt.Fprintf(cmd.OutOrStdout(), "Action: add %s / drop %s\n", attempt.AddPlayerName, attempt.DropPlayerName)
	fmt.Fprintf(cmd.OutOrStdout(), "Started: %s\n", attempt.StartedAt.Format(time.RFC3339))
	if attempt.CompletedAt != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Completed: %s\n", attempt.CompletedAt.Format(time.RFC3339))
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Execution status: %s\n", attempt.ExecutionStatus)
	fmt.Fprintf(cmd.OutOrStdout(), "Verification status: %s\n", attempt.VerificationStatus)
	if msg := strings.TrimSpace(attempt.ErrorMessage); msg != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Error: %s\n", msg)
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
