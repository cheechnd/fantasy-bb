package main

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	esrepo "fantasy-baseball/internal/espn/repository"
	"fantasy-baseball/internal/execute"
	exerepo "fantasy-baseball/internal/execute/repository"
	exesvc "fantasy-baseball/internal/execute/service"
	"fantasy-baseball/internal/store/sqlite"
	tranrepo "fantasy-baseball/internal/transactions/repository"
	reviewrepo "fantasy-baseball/internal/transactions/review/repository"

	"github.com/spf13/cobra"
)

func newExecuteCmd(opts *cliOptions) *cobra.Command {
	cmd := &cobra.Command{Use: "execute", Short: "Dry-run execution planning and preflight validation"}
	cmd.AddGroup(
		&cobra.Group{ID: "run", Title: "Run"},
		&cobra.Group{ID: "inspect", Title: "Inspection"},
	)
	preflightCmd := newExecutePreflightCmd(opts)
	preflightCmd.GroupID = "run"
	dryRunCmd := newExecuteDryRunCmd(opts)
	dryRunCmd.GroupID = "run"
	queueCmd := newExecuteQueueCmd(opts)
	queueCmd.GroupID = "inspect"
	lastCmd := newExecuteLastCmd(opts)
	lastCmd.GroupID = "inspect"
	showCmd := newExecuteShowCmd(opts)
	showCmd.GroupID = "inspect"
	cmd.AddCommand(preflightCmd, dryRunCmd, queueCmd, lastCmd, showCmd)
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
