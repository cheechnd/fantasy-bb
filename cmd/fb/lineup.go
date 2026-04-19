package main

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"fantasy-baseball/internal/config"
	esrepo "fantasy-baseball/internal/espn/repository"
	lp "fantasy-baseball/internal/lineup/pitchers"
	pitchplan "fantasy-baseball/internal/pitchers/planner"
	"fantasy-baseball/internal/store/sqlite"

	"github.com/spf13/cobra"
)

func newLineupCmd(opts *cliOptions) *cobra.Command {
	cmd := &cobra.Command{Use: "lineup", Short: "Pitcher lineup planning"}
	cmd.AddGroup(
		&cobra.Group{ID: "plan", Title: "Plan"},
		&cobra.Group{ID: "inspect", Title: "Inspection"},
	)
	planCmd := newLineupPlanCmd(opts)
	planCmd.GroupID = "plan"
	lastCmd := newLineupLastCmd(opts)
	lastCmd.GroupID = "inspect"
	cmd.AddCommand(planCmd, lastCmd)
	return cmd
}

func newLineupPlanCmd(opts *cliOptions) *cobra.Command {
	var pitcherPlanID, syncRunID int64
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Generate factual pitcher lineup actions from pitcher plan + live roster",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := withLineupService(cmd.Context(), opts, func(ctx context.Context, _ config.Config, svc *lp.Service) (any, error) {
				return svc.GeneratePlan(ctx, optionalInt64(cmd, "pitcher-plan-id", pitcherPlanID), optionalInt64(cmd, "sync-run", syncRunID))
			})
			if err != nil {
				return err
			}
			plan := v.(*lp.Plan)
			if opts.OutputJSON {
				return writeJSON(cmd, plan)
			}
			printLineupPlan(cmd, plan)
			return nil
		},
	}
	cmd.Flags().Int64Var(&pitcherPlanID, "pitcher-plan-id", 0, "Pitcher plan ID (defaults to latest)")
	cmd.Flags().Int64Var(&syncRunID, "sync-run", 0, "ESPN sync run ID (defaults to pitcher plan sync or latest)")
	return cmd
}

func newLineupLastCmd(opts *cliOptions) *cobra.Command {
	var planID int64
	cmd := &cobra.Command{
		Use:   "last",
		Short: "Show saved lineup plan (latest by default)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := withLineupService(cmd.Context(), opts, func(ctx context.Context, _ config.Config, svc *lp.Service) (any, error) {
				if planID > 0 {
					return svc.PlanByID(ctx, planID)
				}
				return svc.LatestPlan(ctx)
			})
			if err != nil {
				return err
			}
			plan, _ := v.(*lp.Plan)
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"plan": plan})
			}
			if plan == nil {
				if planID > 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "Lineup plan %d not found.\n", planID)
					return nil
				}
				fmt.Fprintln(cmd.OutOrStdout(), "No lineup plans found.")
				return nil
			}
			printLineupPlan(cmd, plan)
			return nil
		},
	}
	cmd.Flags().Int64Var(&planID, "plan-id", 0, "Lineup plan ID (defaults to latest)")
	return cmd
}

func newLineupPitchersAdHocCmd(opts *cliOptions) *cobra.Command {
	var playerName string
	var toSlot string
	var syncRunID int64
	cmd := &cobra.Command{
		Use:   "ad-hoc",
		Short: "Create a single ad hoc lineup action plan for one pitcher",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := withLineupService(cmd.Context(), opts, func(ctx context.Context, _ config.Config, svc *lp.Service) (any, error) {
				return svc.CreateAdHocPlan(ctx, playerName, toSlot, optionalInt64(cmd, "sync-run", syncRunID))
			})
			if err != nil {
				return err
			}
			plan := v.(*lp.Plan)
			if opts.OutputJSON {
				return writeJSON(cmd, plan)
			}
			printLineupPlan(cmd, plan)
			return nil
		},
	}
	cmd.Flags().StringVar(&playerName, "player", "", "Pitcher name on your roster")
	cmd.Flags().StringVar(&toSlot, "to-slot", "", "Target slot: P|SP|RP|BE")
	cmd.Flags().Int64Var(&syncRunID, "sync-run", 0, "ESPN sync run ID (defaults to latest)")
	return cmd
}

func newLineupPitchersPlanCmd(opts *cliOptions) *cobra.Command {
	var pitcherPlanID, syncRunID int64
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Generate factual pitcher lineup actions from pitcher plan + live roster",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := withLineupService(cmd.Context(), opts, func(ctx context.Context, _ config.Config, svc *lp.Service) (any, error) {
				return svc.GeneratePlan(ctx, optionalInt64(cmd, "pitcher-plan-id", pitcherPlanID), optionalInt64(cmd, "sync-run", syncRunID))
			})
			if err != nil {
				return err
			}
			plan := v.(*lp.Plan)
			if opts.OutputJSON {
				return writeJSON(cmd, plan)
			}
			printLineupPlan(cmd, plan)
			return nil
		},
	}
	cmd.Flags().Int64Var(&pitcherPlanID, "pitcher-plan-id", 0, "Pitcher plan ID (defaults to latest)")
	cmd.Flags().Int64Var(&syncRunID, "sync-run", 0, "ESPN sync run ID (defaults to pitcher plan sync or latest)")
	return cmd
}

func newLineupPitchersReviewCmd(opts *cliOptions) *cobra.Command {
	var planID int64
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Show lineup plan items with review state",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if planID <= 0 {
				return fmt.Errorf("--plan-id must be > 0")
			}
			v, err := withLineupService(cmd.Context(), opts, func(ctx context.Context, _ config.Config, svc *lp.Service) (any, error) {
				return svc.Review(ctx, planID)
			})
			if err != nil {
				return err
			}
			items := v.([]lp.ReviewedPlanItem)
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"plan_id": planID, "items": items})
			}
			printLineupReview(cmd, planID, items)
			return nil
		},
	}
	cmd.Flags().Int64Var(&planID, "plan-id", 0, "Lineup plan ID")
	return cmd
}

func newLineupPitchersStateCmd(opts *cliOptions, use, short string, target lp.ReviewState) *cobra.Command {
	var planID, itemID int64
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
			v, err := withLineupService(cmd.Context(), opts, func(ctx context.Context, _ config.Config, svc *lp.Service) (any, error) {
				return svc.Transition(ctx, planID, itemID, target, note)
			})
			if err != nil {
				return err
			}
			d := v.(*lp.ReviewDecision)
			if opts.OutputJSON {
				return writeJSON(cmd, d)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Lineup item %d (plan %d): %s -> %s\n", d.LineupPlanItemID, d.PlanID, d.PreviousState, d.NewState)
			if strings.TrimSpace(d.Note) != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Note: %s\n", d.Note)
			}
			return nil
		},
	}
	cmd.Flags().Int64Var(&planID, "plan-id", 0, "Lineup plan ID")
	cmd.Flags().Int64Var(&itemID, "item", 0, "Lineup plan item ID")
	cmd.Flags().StringVar(&note, "note", "", "Optional review note")
	return cmd
}

func newLineupPitchersQueueCmd(opts *cliOptions) *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "queue",
		Short: "Show approved lineup actions ready for preflight/execution",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := withLineupService(cmd.Context(), opts, func(ctx context.Context, _ config.Config, svc *lp.Service) (any, error) {
				return svc.Queue(ctx, limit)
			})
			if err != nil {
				return err
			}
			rows := v.([]lp.QueueItem)
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"count": len(rows), "items": rows})
			}
			printLineupQueue(cmd, rows)
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "Maximum queue rows")
	return cmd
}

func newLineupPitchersPreflightCmd(opts *cliOptions) *cobra.Command {
	var itemID int64
	var limit int
	cmd := &cobra.Command{
		Use:   "preflight",
		Short: "Preflight approved lineup actions against current live roster/slot state",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := withLineupService(cmd.Context(), opts, func(ctx context.Context, _ config.Config, svc *lp.Service) (any, error) {
				return svc.Preflight(ctx, optionalInt64(cmd, "item", itemID), limit)
			})
			if err != nil {
				return err
			}
			res := v.(*lp.PreflightResult)
			if opts.OutputJSON {
				return writeJSON(cmd, res)
			}
			printLineupPreflight(cmd, res)
			return nil
		},
	}
	cmd.Flags().Int64Var(&itemID, "item", 0, "Optional approved lineup item ID")
	cmd.Flags().IntVar(&limit, "limit", 20, "Maximum queue items to preflight")
	return cmd
}

func newLineupPitchersRunCmd(opts *cliOptions) *cobra.Command {
	var itemID int64
	var confirm bool
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Prepare or run one approved pitcher lineup action",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if itemID <= 0 {
				return fmt.Errorf("--item must be > 0")
			}
			v, err := withLineupService(cmd.Context(), opts, func(ctx context.Context, cfg config.Config, svc *lp.Service) (any, error) {
				a, p, willWrite, msg, err := svc.Execute(ctx, cfg, itemID, confirm)
				if err != nil {
					return nil, err
				}
				return map[string]any{"attempt": a, "preflight": p, "will_write": willWrite, "message": msg}, nil
			})
			if err != nil {
				return err
			}
			payload := v.(map[string]any)
			if opts.OutputJSON {
				return writeJSON(cmd, payload)
			}
			printLineupExecutionPreview(cmd, itemID, payload)
			return nil
		},
	}
	cmd.Flags().Int64Var(&itemID, "item", 0, "Approved lineup item ID to execute")
	cmd.Flags().BoolVar(&confirm, "confirm", false, "Actually perform the real write attempt")
	return cmd
}

func newLineupPitchersHistoryCmd(opts *cliOptions) *cobra.Command {
	var limit int
	var executionID int64
	cmd := &cobra.Command{
		Use:   "history",
		Short: "Show lineup execution attempts (list or one by --execution-id)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if executionID > 0 {
				v, err := withLineupService(cmd.Context(), opts, func(ctx context.Context, _ config.Config, svc *lp.Service) (any, error) {
					return svc.ExecutionResult(ctx, executionID)
				})
				if err != nil {
					return err
				}
				a, _ := v.(*lp.ExecutionAttempt)
				if opts.OutputJSON {
					return writeJSON(cmd, map[string]any{"attempt": a})
				}
				if a == nil {
					fmt.Fprintf(cmd.OutOrStdout(), "Lineup execution %d not found.\n", executionID)
					return nil
				}
				printLineupResult(cmd, a)
				return nil
			}
			v, err := withLineupService(cmd.Context(), opts, func(ctx context.Context, _ config.Config, svc *lp.Service) (any, error) {
				return svc.ExecutionHistory(ctx, limit)
			})
			if err != nil {
				return err
			}
			rows := v.([]lp.ExecutionAttempt)
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"count": len(rows), "rows": rows})
			}
			printLineupHistory(cmd, rows)
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "Maximum rows")
	cmd.Flags().Int64Var(&executionID, "execution-id", 0, "Lineup execution attempt ID (shows one attempt)")
	return cmd
}

func withLineupService(ctx context.Context, opts *cliOptions, fn func(context.Context, config.Config, *lp.Service) (any, error)) (any, error) {
	cfg, _, err := config.Load(toOverrides(opts))
	if err != nil {
		return nil, err
	}
	s, err := sqlite.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	repo := lp.NewRepository(s.DB())
	pitcherPlanRepo := pitchplan.NewRepository(s.DB())
	espnRepo := esrepo.New(s.DB())
	svc := lp.NewService(
		repo,
		pitcherPlanRepo,
		espnRepo,
		lp.NewESPNWriter(timeDurationSeconds(cfg.ESPN.TimeoutSeconds)),
		lp.NewESPNVerifier(timeDurationSeconds(cfg.ESPN.TimeoutSeconds)),
	)
	return fn(ctx, cfg, svc)
}

func timeDurationSeconds(sec int) time.Duration {
	if sec <= 0 {
		sec = 20
	}
	return time.Duration(sec) * time.Second
}

func printLineupPlan(cmd *cobra.Command, plan *lp.Plan) {
	fmt.Fprintf(cmd.OutOrStdout(), "Lineup Plan: %d\n", plan.ID)
	if plan.PitcherPlanID != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Pitcher Plan: %d\n", *plan.PitcherPlanID)
	}
	if plan.SyncRunID != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "ESPN Sync Run: %d\n", *plan.SyncRunID)
	}
	fmt.Fprintln(cmd.OutOrStdout())
	if len(plan.Items) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No actionable lineup items.")
		return
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ITEM\tACTION\tPLAYER\tCURRENT\tTARGET\tFLAGS")
	for _, it := range plan.Items {
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\n", it.ID, it.ActionType, it.PlayerName, firstNonEmpty(it.CurrentSlot, "-"), firstNonEmpty(it.TargetSlot, "-"), strings.Join(it.Flags, ","))
	}
	w.Flush()
}

func printLineupReview(cmd *cobra.Command, planID int64, items []lp.ReviewedPlanItem) {
	fmt.Fprintf(cmd.OutOrStdout(), "Lineup Review: plan %d\n", planID)
	if len(items) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "(none)")
		return
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ITEM\tSTATE\tACTION\tPLAYER\tCURRENT\tTARGET\tNOTE")
	for _, it := range items {
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n", it.ID, it.ReviewState, it.ActionType, it.PlayerName, firstNonEmpty(it.CurrentSlot, "-"), firstNonEmpty(it.TargetSlot, "-"), firstNonEmpty(it.ReviewNote, "-"))
	}
	w.Flush()
}

func printLineupQueue(cmd *cobra.Command, rows []lp.QueueItem) {
	if len(rows) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "(none)")
		return
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ITEM\tPLAN\tSTATE\tACTION\tPLAYER\tCURRENT\tTARGET\tAPPROVED_AT\tNOTE")
	for _, r := range rows {
		fmt.Fprintf(w, "%d\t%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.LineupPlanItemID, r.PlanID, r.State, r.ActionType, r.PlayerName, firstNonEmpty(r.CurrentSlot, "-"), firstNonEmpty(r.TargetSlot, "-"), r.ApprovedAt.Format(time.RFC3339), firstNonEmpty(r.Note, "-"))
	}
	w.Flush()
}

func printLineupPreflight(cmd *cobra.Command, res *lp.PreflightResult) {
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ITEM\tPLAN\tACTION\tPLAYER\tSTATUS\tCURRENT\tTARGET\tREASONS")
	for _, it := range res.Items {
		reasonTexts := make([]string, 0, len(it.Reasons))
		for _, r := range it.Reasons {
			reasonTexts = append(reasonTexts, r.Code)
		}
		fmt.Fprintf(w, "%d\t%d\t%s\t%s\t%s\t%s\t%s\t%s\n", it.LineupPlanItemID, it.PlanID, it.ActionType, it.PlayerName, it.ValidationStatus, firstNonEmpty(it.CurrentSlot, "-"), firstNonEmpty(it.TargetSlot, "-"), strings.Join(reasonTexts, ","))
	}
	w.Flush()
}

func printLineupExecutionPreview(cmd *cobra.Command, itemID int64, payload map[string]any) {
	pre, _ := payload["preflight"].(*lp.PreflightItem)
	attempt, _ := payload["attempt"].(*lp.ExecutionAttempt)
	willWrite, _ := payload["will_write"].(bool)
	msg, _ := payload["message"].(string)
	fmt.Fprintf(cmd.OutOrStdout(), "Approved Lineup Item: %d\n", itemID)
	if pre != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Action: %s %s (%s -> %s)\n", pre.ActionType, pre.PlayerName, firstNonEmpty(pre.CurrentSlot, "-"), firstNonEmpty(pre.TargetSlot, "-"))
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

func printLineupHistory(cmd *cobra.Command, rows []lp.ExecutionAttempt) {
	if len(rows) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "(none)")
		return
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "EXEC_ID\tITEM\tPLAN\tSTARTED\tSTATUS\tVERIFICATION\tERROR")
	for _, r := range rows {
		fmt.Fprintf(w, "%d\t%d\t%d\t%s\t%s\t%s\t%s\n", r.ID, r.ApprovedLineupItemID, r.LineupPlanID, r.StartedAt.Format(time.RFC3339), r.ExecutionStatus, r.VerificationStatus, firstNonEmpty(r.ErrorMessage, "-"))
	}
	w.Flush()
}

func printLineupResult(cmd *cobra.Command, a *lp.ExecutionAttempt) {
	fmt.Fprintf(cmd.OutOrStdout(), "Lineup Execution: %d\n", a.ID)
	fmt.Fprintf(cmd.OutOrStdout(), "Approved item: %d\n", a.ApprovedLineupItemID)
	fmt.Fprintf(cmd.OutOrStdout(), "Plan: %d\n", a.LineupPlanID)
	fmt.Fprintf(cmd.OutOrStdout(), "Execution status: %s\n", a.ExecutionStatus)
	fmt.Fprintf(cmd.OutOrStdout(), "Verification status: %s\n", a.VerificationStatus)
	if strings.TrimSpace(a.ErrorMessage) != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Error: %s\n", a.ErrorMessage)
	}
}
