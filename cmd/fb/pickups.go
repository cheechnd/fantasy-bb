package main

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	esrepo "fantasy-baseball/internal/espn/repository"
	"fantasy-baseball/internal/forecaster"
	"fantasy-baseball/internal/pickups"
	pickrepo "fantasy-baseball/internal/pickups/repository"
	picksvc "fantasy-baseball/internal/pickups/service"
	pitchrepo "fantasy-baseball/internal/pitchers/repository"
	pitchsvc "fantasy-baseball/internal/pitchers/service"
	"fantasy-baseball/internal/store/sqlite"

	"github.com/spf13/cobra"
)

func newPickupsCmd(opts *cliOptions) *cobra.Command {
	cmd := &cobra.Command{Use: "pickups", Short: "Read-only pickup and streamer recommendations"}
	cmd.AddGroup(
		&cobra.Group{ID: "generate", Title: "Generate"},
		&cobra.Group{ID: "inspect", Title: "Inspection"},
	)
	recommendCmd := newPickupsRecommendCmd(opts)
	recommendCmd.GroupID = "generate"
	lastCmd := newPickupsLastCmd(opts)
	lastCmd.GroupID = "inspect"
	cmd.AddCommand(recommendCmd, lastCmd)
	return cmd
}

func newPickupsRecommendCmd(opts *cliOptions) *cobra.Command {
	var fromRaw, toRaw string
	var topN int
	var view string
	var minTotal float64
	var syncRunID, importRunID, candidateRunID int64
	cmd := &cobra.Command{
		Use:   "recommend",
		Short: "Generate pickup recommendation report (full/top-streamers)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			view = strings.ToLower(strings.TrimSpace(view))
			if view == "" {
				view = "full"
			}
			if view != "full" && view != "top-streamers" {
				return fmt.Errorf("invalid --view value %q (expected full|top-streamers)", view)
			}
			v, err := withPickupsService(cmd.Context(), opts, func(ctx context.Context, svc *picksvc.Service) (any, error) {
				opts2, err := buildPickupOptions(cmd, fromRaw, toRaw, topN, &syncRunID, &importRunID, &candidateRunID, &minTotal)
				if err != nil {
					return nil, err
				}
				if view == "top-streamers" {
					return svc.TopStreamers(ctx, opts2)
				}
				return svc.Recommend(ctx, opts2)
			})
			if err != nil {
				return err
			}
			result := v.(pickups.RecommendResult)
			if opts.OutputJSON {
				return writeJSON(cmd, result)
			}
			if view == "top-streamers" {
				printPickupItemsTable(cmd, result.TopStreamers)
			} else {
				printPickupRecommendation(cmd, result)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&fromRaw, "from", "", "Window start date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&toRaw, "to", "", "Window end date (YYYY-MM-DD)")
	cmd.Flags().Int64Var(&syncRunID, "sync-run", 0, "ESPN sync run ID (defaults to latest)")
	cmd.Flags().Int64Var(&importRunID, "import-run", 0, "Forecaster import run ID (defaults to latest)")
	cmd.Flags().Int64Var(&candidateRunID, "candidate-run", 0, "Candidate run ID (defaults to latest)")
	cmd.Flags().IntVar(&topN, "top", 10, "Top recommendations per section")
	cmd.Flags().StringVar(&view, "view", "full", "View mode: full|top-streamers")
	cmd.Flags().Float64Var(&minTotal, "min-total-fpts", 0, "Minimum total projected FPTS (used by top-streamers view)")
	return cmd
}

func newPickupsLastCmd(opts *cliOptions) *cobra.Command {
	var recommendationID int64
	cmd := &cobra.Command{
		Use:   "last",
		Short: "Show saved pickup recommendation (latest by default)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := withPickupsService(cmd.Context(), opts, func(ctx context.Context, svc *picksvc.Service) (any, error) {
				if recommendationID > 0 {
					run, items, err := svc.Show(ctx, recommendationID)
					if err != nil {
						return nil, err
					}
					return map[string]any{"run": run, "items": items}, nil
				}
				run, items, err := svc.Last(ctx)
				if err != nil {
					return nil, err
				}
				return map[string]any{"run": run, "items": items}, nil
			})
			if err != nil {
				return err
			}
			payload := v.(map[string]any)
			if opts.OutputJSON {
				return writeJSON(cmd, payload)
			}
			run, _ := payload["run"].(*pickups.RecommendationRun)
			if run == nil {
				if recommendationID > 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "Recommendation %d not found.\n", recommendationID)
					return nil
				}
				fmt.Fprintln(cmd.OutOrStdout(), "No pickup recommendations found.")
				return nil
			}
			printPickupRun(cmd, run)
			printPickupItemsTable(cmd, payload["items"].([]pickups.RecommendationItem))
			return nil
		},
	}
	cmd.Flags().Int64Var(&recommendationID, "recommendation-id", 0, "Recommendation run ID (defaults to latest)")
	return cmd
}

func withPickupsService(ctx context.Context, opts *cliOptions, fn func(context.Context, *picksvc.Service) (any, error)) (any, error) {
	cfg, _, err := loadConfigWithOverrides(opts)
	if err != nil {
		return nil, err
	}
	store, err := sqlite.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	if _, err := store.Migrate(ctx); err != nil {
		return nil, err
	}
	foreRepo := forecaster.NewRepository(store.DB())
	espnRepo := esrepo.New(store.DB())
	pickRepo := pickrepo.New(store.DB())
	pitchRepo := pitchrepo.New(store.DB())
	pitchService := pitchsvc.New(foreRepo, pitchRepo)
	svc := picksvc.New(foreRepo, espnRepo, pickRepo, pitchService, picksvc.Config{
		MinStreamerTotalFPTS:     cfg.Pickups.Pitchers.MinStreamerTotalFPTS,
		StrongUpgradeDeltaFPTS:   cfg.Pickups.Pitchers.StrongUpgradeDeltaFPTS,
		MarginalUpgradeDeltaFPTS: cfg.Pickups.Pitchers.MarginalUpgradeDeltaFPTS,
		RiskyMonitorMinTotalFPTS: cfg.Pickups.Pitchers.RiskyMonitorMinTotalFPTS,
	})
	return fn(ctx, svc)
}

func buildPickupOptions(cmd *cobra.Command, fromRaw, toRaw string, topN int, syncRunID, importRunID, candidateRunID *int64, minTotal *float64) (pickups.RecommendOptions, error) {
	from, to, err := parseWindow(fromRaw, toRaw)
	if err != nil {
		return pickups.RecommendOptions{}, err
	}
	out := pickups.RecommendOptions{From: from, To: to, TopN: topN}
	if cmd.Flags().Changed("sync-run") {
		v := *syncRunID
		out.SyncRunID = &v
	}
	if cmd.Flags().Changed("import-run") {
		v := *importRunID
		out.ImportRunID = &v
	}
	if cmd.Flags().Changed("candidate-run") {
		v := *candidateRunID
		out.CandidateRunID = &v
	}
	if minTotal != nil && cmd.Flags().Changed("min-total-fpts") {
		v := *minTotal
		out.MinTotalFPTS = &v
	}
	return out, nil
}

func printPickupRecommendation(cmd *cobra.Command, r pickups.RecommendResult) {
	fmt.Fprintf(cmd.OutOrStdout(), "Recommendation Run: %d\n", r.RecommendationRunID)
	fmt.Fprintf(cmd.OutOrStdout(), "Window: %s to %s\n\n", r.WindowStart, r.WindowEnd)
	fmt.Fprintln(cmd.OutOrStdout(), "Top overall candidates")
	printPickupItemsTable(cmd, r.TopCandidates)
	fmt.Fprintln(cmd.OutOrStdout())
	fmt.Fprintln(cmd.OutOrStdout(), "Best streamers")
	printPickupItemsTable(cmd, r.TopStreamers)
	fmt.Fprintln(cmd.OutOrStdout())
	fmt.Fprintln(cmd.OutOrStdout(), "Risky / monitor")
	printPickupItemsTable(cmd, r.RiskyMonitor)
	fmt.Fprintln(cmd.OutOrStdout())
	fmt.Fprintln(cmd.OutOrStdout(), "Unmatched / insufficient data")
	printPickupItemsTable(cmd, r.Unmatched)
}

func printPickupRun(cmd *cobra.Command, run *pickups.RecommendationRun) {
	fmt.Fprintf(cmd.OutOrStdout(), "Recommendation Run: %d\n", run.ID)
	fmt.Fprintf(cmd.OutOrStdout(), "Window: %s to %s\n", run.WindowStart, run.WindowEnd)
	fmt.Fprintf(cmd.OutOrStdout(), "Created: %s\n", run.CreatedAt.Format(time.RFC3339))
	fmt.Fprintf(cmd.OutOrStdout(), "Status: %s\n\n", run.Status)
}

func printPickupItemsTable(cmd *cobra.Command, rows []pickups.RecommendationItem) {
	if len(rows) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "(none)")
		return
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TYPE\tPLAYER\tTEAM\tSTARTS\tSCHEDULE\tTOTAL_FPTS\tTARGET\tDELTA\tFLAGS\tNOTES")
	for _, row := range rows {
		total := "-"
		if row.TotalProjectedFPTS != nil {
			total = fmt.Sprintf("%.1f", *row.TotalProjectedFPTS)
		}
		delta := "-"
		if row.ComparisonDeltaFPTS != nil {
			delta = fmt.Sprintf("%+.1f", *row.ComparisonDeltaFPTS)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\t%s\t%s\t%s\t%s\n", row.ItemType, row.PlayerName, firstNonEmpty(row.MLBTeam, "-"), row.ProjectedStartCount, formatItemSchedule(row), total, firstNonEmpty(row.ComparisonTargetName, "-"), delta, strings.Join(row.Flags, ","), strings.Join(row.Notes, "; "))
	}
	w.Flush()
}

func formatItemSchedule(item pickups.RecommendationItem) string {
	rawStarts, ok := item.Details["starts"]
	if !ok || rawStarts == nil {
		return "-"
	}
	parts := []string{}
	switch starts := rawStarts.(type) {
	case []pickups.Start:
		for _, st := range starts {
			if st.Date == "" {
				continue
			}
			parts = append(parts, fmt.Sprintf("%s %s", st.Date, firstNonEmpty(st.Opponent, "-")))
		}
	case []any:
		for _, v := range starts {
			m, ok := v.(map[string]any)
			if !ok {
				continue
			}
			date, _ := m["date"].(string)
			opp, _ := m["opponent"].(string)
			if strings.TrimSpace(date) == "" {
				continue
			}
			parts = append(parts, fmt.Sprintf("%s %s", date, firstNonEmpty(opp, "-")))
		}
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}
