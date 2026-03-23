package main

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"

	"fantasy-baseball/internal/config"
	"fantasy-baseball/internal/monitor"
	"fantasy-baseball/internal/store/sqlite"

	"github.com/spf13/cobra"
)

func newMonitorCmd(opts *cliOptions) *cobra.Command {
	cmd := &cobra.Command{Use: "monitor", Short: "Evaluate freshness and actionability of saved artifacts"}
	cmd.AddGroup(
		&cobra.Group{ID: "overview", Title: "Overview"},
		&cobra.Group{ID: "artifacts", Title: "Artifact Checks"},
	)

	summaryCmd := newMonitorSummaryCmd(opts)
	summaryCmd.GroupID = "overview"
	plansCmd := newMonitorPlansCmd(opts)
	plansCmd.GroupID = "artifacts"
	lineupCmd := newMonitorLineupCmd(opts)
	lineupCmd.GroupID = "artifacts"
	pickupsCmd := newMonitorPickupsCmd(opts)
	pickupsCmd.GroupID = "artifacts"
	approvalsCmd := newMonitorApprovalsCmd(opts)
	approvalsCmd.GroupID = "artifacts"
	adhocCmd := newMonitorAdHocCmd(opts)
	adhocCmd.GroupID = "artifacts"
	executionCmd := newMonitorExecutionCmd(opts)
	executionCmd.GroupID = "artifacts"
	cmd.AddCommand(summaryCmd, plansCmd, lineupCmd, pickupsCmd, approvalsCmd, adhocCmd, executionCmd)
	return cmd
}

func newMonitorSummaryCmd(opts *cliOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "summary",
		Short: "Show high-level stale/blocked/invalidated overview across workflows",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := withMonitorService(cmd.Context(), opts, func(ctx context.Context, svc *monitor.Service) (any, error) {
				return svc.Summary(ctx)
			})
			if err != nil {
				return err
			}
			run := v.(*monitor.Run)
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"run": run})
			}
			printMonitorRunSummary(cmd, run)
			return nil
		},
	}
}

func newMonitorPlansCmd(opts *cliOptions) *cobra.Command {
	var limit int
	var artifactID int64
	var latestOnly bool
	cmd := &cobra.Command{
		Use:   "plans",
		Short: "Evaluate pitcher plan freshness against current roster/forecaster context",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := withMonitorService(cmd.Context(), opts, func(ctx context.Context, svc *monitor.Service) (any, error) {
				if artifactID > 0 {
					return svc.Show(ctx, "plan", artifactID)
				}
				return svc.Plans(ctx, monitor.EvaluateOptions{Limit: limit, LatestOnly: latestOnly})
			})
			if err != nil {
				return err
			}
			run := v.(*monitor.Run)
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"run": run})
			}
			printMonitorRunTable(cmd, run)
			return nil
		},
	}
	cmd.Flags().Int64Var(&artifactID, "id", 0, "Specific plan artifact ID")
	cmd.Flags().IntVar(&limit, "limit", 20, "Maximum artifacts to evaluate")
	cmd.Flags().BoolVar(&latestOnly, "latest-only", false, "Evaluate latest artifact only")
	return cmd
}

func newMonitorLineupCmd(opts *cliOptions) *cobra.Command {
	var limit int
	var artifactID int64
	var artifactType string
	var latestOnly bool
	cmd := &cobra.Command{
		Use:   "lineup",
		Short: "Evaluate lineup plans and approved lineup actions against live roster slots",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := withMonitorService(cmd.Context(), opts, func(ctx context.Context, svc *monitor.Service) (any, error) {
				if artifactID > 0 {
					t := strings.ToLower(strings.TrimSpace(artifactType))
					if t == "" {
						t = "lineup_plan"
					}
					if t != "lineup_plan" && t != "lineup_approval" {
						return nil, fmt.Errorf("invalid --type value %q (expected lineup_plan|lineup_approval)", t)
					}
					return svc.Show(ctx, t, artifactID)
				}
				return svc.Lineup(ctx, monitor.EvaluateOptions{Limit: limit, LatestOnly: latestOnly})
			})
			if err != nil {
				return err
			}
			run := v.(*monitor.Run)
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"run": run})
			}
			printMonitorRunTable(cmd, run)
			return nil
		},
	}
	cmd.Flags().Int64Var(&artifactID, "id", 0, "Specific lineup artifact ID")
	cmd.Flags().StringVar(&artifactType, "type", "lineup_plan", "Artifact type when --id is set: lineup_plan|lineup_approval")
	cmd.Flags().IntVar(&limit, "limit", 20, "Maximum artifacts to evaluate")
	cmd.Flags().BoolVar(&latestOnly, "latest-only", false, "Evaluate latest artifact only")
	return cmd
}

func newMonitorPickupsCmd(opts *cliOptions) *cobra.Command {
	var limit int
	var artifactID int64
	var latestOnly bool
	cmd := &cobra.Command{
		Use:   "pickups",
		Short: "Evaluate pickup recommendations and candidate pool freshness",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := withMonitorService(cmd.Context(), opts, func(ctx context.Context, svc *monitor.Service) (any, error) {
				if artifactID > 0 {
					return svc.Show(ctx, "pickup", artifactID)
				}
				return svc.Pickups(ctx, monitor.EvaluateOptions{Limit: limit, LatestOnly: latestOnly})
			})
			if err != nil {
				return err
			}
			run := v.(*monitor.Run)
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"run": run})
			}
			printMonitorRunTable(cmd, run)
			return nil
		},
	}
	cmd.Flags().Int64Var(&artifactID, "id", 0, "Specific pickup artifact ID")
	cmd.Flags().IntVar(&limit, "limit", 20, "Maximum artifacts to evaluate")
	cmd.Flags().BoolVar(&latestOnly, "latest-only", false, "Evaluate latest artifact only")
	return cmd
}

func newMonitorApprovalsCmd(opts *cliOptions) *cobra.Command {
	var limit int
	var artifactID int64
	var artifactType string
	cmd := &cobra.Command{
		Use:   "approvals",
		Short: "Evaluate approved transaction/lineup items for current actionability",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := withMonitorService(cmd.Context(), opts, func(ctx context.Context, svc *monitor.Service) (any, error) {
				if artifactID > 0 {
					t := strings.ToLower(strings.TrimSpace(artifactType))
					if t == "" {
						t = "approval"
					}
					if t != "approval" && t != "lineup_approval" {
						return nil, fmt.Errorf("invalid --type value %q (expected approval|lineup_approval)", t)
					}
					return svc.Show(ctx, t, artifactID)
				}
				return svc.Approvals(ctx, monitor.EvaluateOptions{Limit: limit})
			})
			if err != nil {
				return err
			}
			run := v.(*monitor.Run)
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"run": run})
			}
			printMonitorRunTable(cmd, run)
			return nil
		},
	}
	cmd.Flags().Int64Var(&artifactID, "id", 0, "Specific approval artifact ID")
	cmd.Flags().StringVar(&artifactType, "type", "approval", "Artifact type when --id is set: approval|lineup_approval")
	cmd.Flags().IntVar(&limit, "limit", 50, "Maximum approved artifacts to evaluate")
	return cmd
}

func newMonitorAdHocCmd(opts *cliOptions) *cobra.Command {
	var limit int
	var artifactID int64
	cmd := &cobra.Command{
		Use:   "ad-hoc",
		Short: "Evaluate ad hoc add/drop requests for current validity",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := withMonitorService(cmd.Context(), opts, func(ctx context.Context, svc *monitor.Service) (any, error) {
				if artifactID > 0 {
					return svc.Show(ctx, "ad_hoc", artifactID)
				}
				return svc.AdHoc(ctx, monitor.EvaluateOptions{Limit: limit})
			})
			if err != nil {
				return err
			}
			run := v.(*monitor.Run)
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"run": run})
			}
			printMonitorRunTable(cmd, run)
			return nil
		},
	}
	cmd.Flags().Int64Var(&artifactID, "id", 0, "Specific ad hoc request artifact ID")
	cmd.Flags().IntVar(&limit, "limit", 50, "Maximum requests to evaluate")
	return cmd
}

func newMonitorExecutionCmd(opts *cliOptions) *cobra.Command {
	var limit int
	var artifactID int64
	cmd := &cobra.Command{
		Use:   "execution",
		Short: "Show unresolved or follow-up-needed execution attempts",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := withMonitorService(cmd.Context(), opts, func(ctx context.Context, svc *monitor.Service) (any, error) {
				if artifactID > 0 {
					return svc.Show(ctx, "execution", artifactID)
				}
				return svc.Execution(ctx, monitor.EvaluateOptions{Limit: limit})
			})
			if err != nil {
				return err
			}
			run := v.(*monitor.Run)
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"run": run})
			}
			printMonitorRunTable(cmd, run)
			return nil
		},
	}
	cmd.Flags().Int64Var(&artifactID, "id", 0, "Specific execution artifact ID")
	cmd.Flags().IntVar(&limit, "limit", 50, "Maximum execution attempts to evaluate")
	return cmd
}

func withMonitorService(ctx context.Context, opts *cliOptions, fn func(context.Context, *monitor.Service) (any, error)) (any, error) {
	cfg, _, err := config.Load(toOverrides(opts))
	if err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	store, err := sqlite.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	if err := store.Ping(ctx); err != nil {
		return nil, err
	}
	migrationStatus, err := store.MigrationStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("inspect migration status: %w", err)
	}
	pending := 0
	for _, s := range migrationStatus {
		if !s.Applied {
			pending++
		}
	}
	if pending > 0 {
		return nil, fmt.Errorf("monitoring requires up-to-date schema: %d migration(s) pending (run `fb db migrate`)", pending)
	}
	repo := monitor.NewRepository(store.DB())
	svc := monitor.NewService(repo, monitor.Config{
		PlansStaleHours:               cfg.Monitoring.PlansStaleHours,
		LineupStaleHours:              cfg.Monitoring.LineupStaleHours,
		PickupsStaleHours:             cfg.Monitoring.PickupsStaleHours,
		CandidatePoolStaleHours:       cfg.Monitoring.CandidatePoolStaleHours,
		ApprovalStaleHours:            cfg.Monitoring.ApprovalStaleHours,
		ExecutionFollowupHours:        cfg.Monitoring.ExecutionFollowupHours,
		RequireLiveRecheckForApproved: cfg.Monitoring.RequireLiveRecheckForApproved,
	})
	return fn(ctx, svc)
}

func printMonitorRunSummary(cmd *cobra.Command, run *monitor.Run) {
	if run == nil {
		fmt.Fprintln(cmd.OutOrStdout(), "No monitoring data found.")
		return
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Monitor Run: %d\n", run.ID)
	fmt.Fprintf(cmd.OutOrStdout(), "Type: %s\n", run.RunType)
	fmt.Fprintf(cmd.OutOrStdout(), "Created: %s\n\n", run.CreatedAt.Format("2006-01-02T15:04:05Z07:00"))
	printMonitorCounts(cmd, run.Items)
	if len(run.Items) > 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "\nItems needing attention")
		filtered := make([]monitor.Item, 0, len(run.Items))
		for _, it := range run.Items {
			if it.MonitorStatus == monitor.StatusFresh {
				continue
			}
			filtered = append(filtered, it)
		}
		printMonitorItems(cmd, filtered)
	}
}

func printMonitorRunTable(cmd *cobra.Command, run *monitor.Run) {
	if run == nil || len(run.Items) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No artifacts found.")
		return
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Monitor Run: %d\n", run.ID)
	fmt.Fprintf(cmd.OutOrStdout(), "Type: %s\n", run.RunType)
	fmt.Fprintf(cmd.OutOrStdout(), "Created: %s\n\n", run.CreatedAt.Format("2006-01-02T15:04:05Z07:00"))
	printMonitorItems(cmd, run.Items)
}

func printMonitorItems(cmd *cobra.Command, items []monitor.Item) {
	if len(items) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "(none)")
		return
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TYPE\tID\tSTATUS\tRECOMMENDED\tREASONS")
	for _, it := range items {
		reason := ""
		if len(it.Reasons) > 0 {
			reason = it.Reasons[0].Message
		}
		fmt.Fprintf(w, "%s\t%d\t%s\t%s\t%s\n", it.ArtifactType, it.ArtifactID, it.MonitorStatus, it.RecommendedAction, reason)
	}
	w.Flush()
}

func printMonitorCounts(cmd *cobra.Command, items []monitor.Item) {
	if len(items) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No artifacts found.")
		return
	}
	total := map[monitor.Status]int{}
	for _, it := range items {
		total[it.MonitorStatus]++
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "STATUS\tCOUNT")
	for _, st := range []monitor.Status{monitor.StatusFresh, monitor.StatusStale, monitor.StatusBlocked, monitor.StatusInvalidated, monitor.StatusUnknown} {
		fmt.Fprintf(w, "%s\t%d\n", st, total[st])
	}
	w.Flush()
}
