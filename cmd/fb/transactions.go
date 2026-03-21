package main

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"

	esrepo "fantasy-baseball/internal/espn/repository"
	"fantasy-baseball/internal/forecaster"
	pickrepo "fantasy-baseball/internal/pickups/repository"
	pitchplan "fantasy-baseball/internal/pitchers/planner"
	"fantasy-baseball/internal/store/sqlite"
	"fantasy-baseball/internal/transactions"
	tranrepo "fantasy-baseball/internal/transactions/repository"
	transvc "fantasy-baseball/internal/transactions/service"

	"github.com/spf13/cobra"
)

func newTransactionsCmd(opts *cliOptions) *cobra.Command {
	cmd := &cobra.Command{Use: "transactions", Short: "Read-only add/drop transaction planning"}
	cmd.AddGroup(
		&cobra.Group{ID: "generate", Title: "Generate"},
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
	showCmd := newTransactionsShowCmd(opts)
	showCmd.GroupID = "inspect"
	explainCmd := newTransactionsExplainCmd(opts)
	explainCmd.GroupID = "explain"
	cmd.AddCommand(planCmd, topCmd, compareCmd, lastCmd, showCmd, explainCmd)
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

func newTransactionsShowCmd(opts *cliOptions) *cobra.Command {
	var planID int64
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show a specific saved transaction plan",
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
			printTransactionPlan(cmd, plan)
			return nil
		},
	}
	cmd.Flags().Int64Var(&planID, "plan-id", 0, "Transaction plan ID")
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
	fmt.Fprintln(w, "BUCKET\tADD\tDROP\tDELTA_FPTS\tADD_FPTS\tDROP_FPTS\tADD_STARTS\tDROP_STARTS\tFLAGS")
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
		fmt.Fprintf(w, "%s\t%s (%s)\t%s (%s)\t%s\t%s\t%s\t%d\t%d\t%s\n", row.Bucket, row.AddPlayerName, firstNonEmpty(row.AddPlayerTeam, "-"), row.DropPlayerName, firstNonEmpty(row.DropPlayerTeam, "-"), delta, addTotal, dropTotal, row.AddProjectedStartCount, row.DropProjectedStartCount, strings.Join(row.Flags, ","))
	}
	w.Flush()
}

func printTransactionCompare(cmd *cobra.Command, rows []transactions.PlanItem) {
	if len(rows) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "(none)")
		return
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ADD\tDROP\tBUCKET\tDELTA\tNOTES")
	for _, row := range rows {
		delta := "-"
		if row.DeltaFPTS != nil {
			delta = fmt.Sprintf("%+.1f", *row.DeltaFPTS)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", row.AddPlayerName, row.DropPlayerName, row.Bucket, delta, strings.Join(row.Notes, "; "))
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
		fmt.Fprintf(cmd.OutOrStdout(), "- %s: add %s (%s) drop %s (%s), delta %s\n", row.Bucket, row.AddPlayerName, firstNonEmpty(row.AddPlayerTeam, "-"), row.DropPlayerName, firstNonEmpty(row.DropPlayerTeam, "-"), delta)
		if len(row.Notes) > 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "  notes: %s\n", strings.Join(row.Notes, "; "))
		}
		if len(row.Flags) > 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "  flags: %s\n", strings.Join(row.Flags, ","))
		}
	}
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
