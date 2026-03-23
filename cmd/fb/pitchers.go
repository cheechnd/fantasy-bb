package main

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"fantasy-baseball/internal/config"
	esrepo "fantasy-baseball/internal/espn/repository"
	essvc "fantasy-baseball/internal/espn/service"
	"fantasy-baseball/internal/forecaster"
	pitchers "fantasy-baseball/internal/pitchers"
	"fantasy-baseball/internal/pitchers/planner"
	pitchrepo "fantasy-baseball/internal/pitchers/repository"
	pitchsvc "fantasy-baseball/internal/pitchers/service"
	"fantasy-baseball/internal/store/sqlite"

	"github.com/spf13/cobra"
)

func newPitchersCmd(opts *cliOptions) *cobra.Command {
	cmd := &cobra.Command{Use: "pitchers", Short: "ESPN roster-aware pitcher analysis"}
	cmd.AddGroup(
		&cobra.Group{ID: "analysis", Title: "Analysis"},
		&cobra.Group{ID: "planning", Title: "Planning"},
		&cobra.Group{ID: "inspect", Title: "Inspection"},
	)
	analyzeCmd := newPitchersAnalyzeWeekCmd(opts)
	analyzeCmd.GroupID = "analysis"
	reportCmd := newPitchersReportCmd(opts)
	reportCmd.GroupID = "analysis"
	explainMatchesCmd := newPitchersExplainMatchesCmd(opts)
	explainMatchesCmd.GroupID = "analysis"
	lastReportCmd := newPitchersLastReportCmd(opts)
	lastReportCmd.GroupID = "inspect"
	planCmd := newPitchersPlanCmd(opts)
	planCmd.GroupID = "planning"
	planLastCmd := newPitchersPlanLastCmd(opts)
	planLastCmd.GroupID = "inspect"
	cmd.AddCommand(analyzeCmd, reportCmd, explainMatchesCmd, lastReportCmd, planCmd, planLastCmd)
	return cmd
}

func newPitchersAnalyzeWeekCmd(opts *cliOptions) *cobra.Command {
	var fromRaw, toRaw string
	var syncRunID int64
	var importRunID int64
	cmd := &cobra.Command{
		Use:   "analyze-week",
		Short: "Analyze weekly projected starts for ESPN rostered pitchers",
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts2, err := buildAnalysisOptions(cmd, fromRaw, toRaw, &importRunID)
			if err != nil {
				return err
			}
			r, err := withPitchersServices(cmd.Context(), opts, func(_ context.Context, svc *pitchsvc.Service, es *essvc.Service) (any, error) {
				src, err := resolveESPNPitcherSource(cmd.Context(), cmd, es, syncRunID)
				if err != nil {
					return nil, err
				}
				opts2.RosterInputs = src.Inputs
				opts2.RosterSource = src.Source
				return svc.AnalyzeWeek(cmd.Context(), opts2)
			})
			if err != nil {
				return err
			}
			report := r.(pitchers.AnalysisReport)
			if opts.OutputJSON {
				return writeJSON(cmd, report)
			}
			printPitcherReport(cmd, report)
			return nil
		},
	}
	cmd.Flags().Int64Var(&syncRunID, "sync-run", 0, "ESPN sync run ID (defaults to latest)")
	cmd.Flags().StringVar(&fromRaw, "from", "", "Window start date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&toRaw, "to", "", "Window end date (YYYY-MM-DD)")
	cmd.Flags().Int64Var(&importRunID, "import-run", 0, "Forecaster import run ID (defaults to latest)")
	return cmd
}

func newPitchersReportCmd(opts *cliOptions) *cobra.Command {
	var fromRaw, toRaw string
	var syncRunID int64
	var importRunID int64
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Produce combined weekly pitcher report for ESPN roster",
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts2, err := buildAnalysisOptions(cmd, fromRaw, toRaw, &importRunID)
			if err != nil {
				return err
			}
			r, err := withPitchersServices(cmd.Context(), opts, func(_ context.Context, svc *pitchsvc.Service, es *essvc.Service) (any, error) {
				src, err := resolveESPNPitcherSource(cmd.Context(), cmd, es, syncRunID)
				if err != nil {
					return nil, err
				}
				opts2.RosterInputs = src.Inputs
				opts2.RosterSource = src.Source
				return svc.Report(cmd.Context(), opts2)
			})
			if err != nil {
				return err
			}
			report := r.(pitchers.AnalysisReport)
			if opts.OutputJSON {
				return writeJSON(cmd, report)
			}
			printPitcherReport(cmd, report)
			return nil
		},
	}
	cmd.Flags().Int64Var(&syncRunID, "sync-run", 0, "ESPN sync run ID (defaults to latest)")
	cmd.Flags().StringVar(&fromRaw, "from", "", "Window start date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&toRaw, "to", "", "Window end date (YYYY-MM-DD)")
	cmd.Flags().Int64Var(&importRunID, "import-run", 0, "Forecaster import run ID (defaults to latest)")
	return cmd
}

func newPitchersLastReportCmd(opts *cliOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "last-report",
		Short: "Show latest saved pitcher analysis run summary",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := withPitchersService(cmd.Context(), opts, func(_ context.Context, svc *pitchsvc.Service) (any, error) {
				run, rows, err := svc.LastReport(cmd.Context())
				if err != nil {
					return nil, err
				}
				return map[string]any{"run": run, "results": rows}, nil
			})
			if err != nil {
				return err
			}
			payload := v.(map[string]any)
			if opts.OutputJSON {
				return writeJSON(cmd, payload)
			}
			run, _ := payload["run"].(*pitchrepo.AnalysisRunRow)
			if run == nil {
				fmt.Fprintln(cmd.OutOrStdout(), "No saved analysis runs found.")
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Latest analysis run #%d\n", run.ID)
			fmt.Fprintf(cmd.OutOrStdout(), "Type: %s\nWindow: %s to %s\nCreated: %s\n", run.AnalysisType, run.WindowStart, run.WindowEnd, run.CreatedAt.Format(time.RFC3339))
			rows := payload["results"].([]pitchrepo.AnalysisResultRow)
			printLastReportTable(cmd, rows)
			return nil
		},
	}
	return cmd
}

func newPitchersExplainMatchesCmd(opts *cliOptions) *cobra.Command {
	var fromRaw, toRaw string
	var syncRunID int64
	var importRunID int64
	cmd := &cobra.Command{
		Use:   "explain-matches",
		Short: "Explain ESPN roster player matching against probable starts",
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts2, err := buildAnalysisOptions(cmd, fromRaw, toRaw, &importRunID)
			if err != nil {
				return err
			}
			v, err := withPitchersServices(cmd.Context(), opts, func(_ context.Context, svc *pitchsvc.Service, es *essvc.Service) (any, error) {
				src, err := resolveESPNPitcherSource(cmd.Context(), cmd, es, syncRunID)
				if err != nil {
					return nil, err
				}
				opts2.RosterInputs = src.Inputs
				opts2.RosterSource = src.Source
				return svc.ExplainMatches(cmd.Context(), opts2)
			})
			if err != nil {
				return err
			}
			matches := v.([]pitchers.MatchResult)
			if opts.OutputJSON {
				return writeJSON(cmd, matches)
			}
			printMatchTable(cmd, matches)
			return nil
		},
	}
	cmd.Flags().Int64Var(&syncRunID, "sync-run", 0, "ESPN sync run ID (defaults to latest)")
	cmd.Flags().StringVar(&fromRaw, "from", "", "Window start date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&toRaw, "to", "", "Window end date (YYYY-MM-DD)")
	cmd.Flags().Int64Var(&importRunID, "import-run", 0, "Forecaster import run ID (defaults to latest)")
	return cmd
}

func newPitchersPlanCmd(opts *cliOptions) *cobra.Command {
	var fromRaw, toRaw string
	var syncRunID int64
	var importRunID int64
	var view string
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Build and save weekly pitcher plan (full or start-sit view)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			view = strings.ToLower(strings.TrimSpace(view))
			if view == "" {
				view = "full"
			}
			if view != "full" && view != "start-sit" {
				return fmt.Errorf("invalid --view value %q (expected full|start-sit)", view)
			}
			analysisOpts, err := buildAnalysisOptions(cmd, fromRaw, toRaw, &importRunID)
			if err != nil {
				return err
			}
			v, err := withPitchersPlannerServices(cmd.Context(), opts, func(ctx context.Context, cfg config.Config, svc *pitchsvc.Service, es *essvc.Service, ps *planner.Service) (any, error) {
				src, err := resolveESPNPitcherSource(ctx, cmd, es, syncRunID)
				if err != nil {
					return nil, err
				}
				analysisOpts.RosterInputs = src.Inputs
				analysisOpts.RosterSource = src.Source
				report, err := svc.Report(ctx, analysisOpts)
				if err != nil {
					return nil, err
				}
				syncRun := src.SyncRunID
				plan, err := ps.GenerateAndSave(ctx, planner.GenerateInput{
					SyncRunID:       &syncRun,
					ImportRunID:     report.ImportRunID,
					AnalysisRunID:   &report.AnalysisRunID,
					WindowStart:     report.WindowStart,
					WindowEnd:       report.WindowEnd,
					Rules:           planningRulesFromConfig(cfg),
					Analysis:        report,
					RosterSnapshots: src.Snapshots,
				})
				if err != nil {
					return nil, err
				}
				return map[string]any{"plan": plan, "source": src, "analysis_run_id": report.AnalysisRunID}, nil
			})
			if err != nil {
				return err
			}
			payload := v.(map[string]any)
			plan := payload["plan"].(*planner.Plan)
			if opts.OutputJSON {
				return writeJSON(cmd, payload)
			}
			printPitcherPlan(cmd, plan, view == "start-sit")
			return nil
		},
	}
	cmd.Flags().Int64Var(&syncRunID, "sync-run", 0, "ESPN sync run ID (defaults to latest)")
	cmd.Flags().StringVar(&fromRaw, "from", "", "Window start date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&toRaw, "to", "", "Window end date (YYYY-MM-DD)")
	cmd.Flags().Int64Var(&importRunID, "import-run", 0, "Forecaster import run ID (defaults to latest)")
	cmd.Flags().StringVar(&view, "view", "full", "View mode: full|start-sit")
	return cmd
}

func newPitchersPlanLastCmd(opts *cliOptions) *cobra.Command {
	var planID int64
	var view string
	cmd := &cobra.Command{
		Use:   "plan-last",
		Short: "Show saved pitcher plan (latest by default)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			view = strings.ToLower(strings.TrimSpace(view))
			if view == "" {
				view = "full"
			}
			if view != "full" && view != "start-sit" {
				return fmt.Errorf("invalid --view value %q (expected full|start-sit)", view)
			}
			v, err := withPitchersPlannerServices(cmd.Context(), opts, func(ctx context.Context, _ config.Config, _ *pitchsvc.Service, _ *essvc.Service, ps *planner.Service) (any, error) {
				if planID > 0 {
					return ps.ByID(ctx, planID)
				}
				return ps.Latest(ctx)
			})
			if err != nil {
				return err
			}
			plan, _ := v.(*planner.Plan)
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"plan": plan})
			}
			if plan == nil {
				if planID > 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "Pitcher plan %d not found.\n", planID)
					return nil
				}
				fmt.Fprintln(cmd.OutOrStdout(), "No saved pitcher plans found.")
				return nil
			}
			printPitcherPlan(cmd, plan, view == "start-sit")
			return nil
		},
	}
	cmd.Flags().Int64Var(&planID, "plan-id", 0, "Pitcher plan ID")
	cmd.Flags().StringVar(&view, "view", "full", "View mode: full|start-sit")
	return cmd
}

func withPitchersServices(ctx context.Context, opts *cliOptions, fn func(context.Context, *pitchsvc.Service, *essvc.Service) (any, error)) (any, error) {
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
	foreRepo := forecaster.NewRepository(s.DB())
	pitchRepo := pitchrepo.New(s.DB())
	espnRepo := esrepo.New(s.DB())
	service := pitchsvc.New(foreRepo, pitchRepo)
	espnService := essvc.New(espnRepo)
	return fn(ctx, service, espnService)
}

func withPitchersPlannerServices(ctx context.Context, opts *cliOptions, fn func(context.Context, config.Config, *pitchsvc.Service, *essvc.Service, *planner.Service) (any, error)) (any, error) {
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
	foreRepo := forecaster.NewRepository(s.DB())
	pitchRepo := pitchrepo.New(s.DB())
	espnRepo := esrepo.New(s.DB())
	planRepo := planner.NewRepository(s.DB())
	pitchService := pitchsvc.New(foreRepo, pitchRepo)
	espnService := essvc.New(espnRepo)
	planService := planner.NewService(planRepo)
	return fn(ctx, cfg, pitchService, espnService, planService)
}

func withPitchersService(ctx context.Context, opts *cliOptions, fn func(context.Context, *pitchsvc.Service) (any, error)) (any, error) {
	return withPitchersServices(ctx, opts, func(c context.Context, svc *pitchsvc.Service, _ *essvc.Service) (any, error) {
		return fn(c, svc)
	})
}

func resolveESPNPitcherSource(ctx context.Context, cmd *cobra.Command, es *essvc.Service, syncRunID int64) (essvc.PitcherRosterSource, error) {
	var runIDPtr *int64
	if cmd.Flags().Changed("sync-run") {
		runIDPtr = &syncRunID
	}
	return es.PitcherRosterSource(ctx, runIDPtr)
}

func buildAnalysisOptions(cmd *cobra.Command, fromRaw string, toRaw string, importRunID *int64) (pitchers.AnalysisOptions, error) {
	from, to, err := parseWindow(fromRaw, toRaw)
	if err != nil {
		return pitchers.AnalysisOptions{}, err
	}
	var runID *int64
	if cmd.Flags().Changed("import-run") && importRunID != nil {
		v := *importRunID
		runID = &v
	}
	return pitchers.AnalysisOptions{From: from, To: to, ImportRunID: runID}, nil
}

func parseWindow(fromRaw, toRaw string) (time.Time, time.Time, error) {
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	end := start.AddDate(0, 0, 6)
	if strings.TrimSpace(fromRaw) != "" {
		tm, err := time.ParseInLocation("2006-01-02", fromRaw, time.Local)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid --from date %q", fromRaw)
		}
		start = tm
	}
	if strings.TrimSpace(toRaw) != "" {
		tm, err := time.ParseInLocation("2006-01-02", toRaw, time.Local)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid --to date %q", toRaw)
		}
		end = tm
	} else if strings.TrimSpace(fromRaw) != "" {
		end = start.AddDate(0, 0, 6)
	}
	if end.Before(start) {
		return time.Time{}, time.Time{}, fmt.Errorf("--to cannot be before --from")
	}
	return start, end, nil
}

func printPitcherReport(cmd *cobra.Command, report pitchers.AnalysisReport) {
	fmt.Fprintf(cmd.OutOrStdout(), "Analysis Run: %d\n", report.AnalysisRunID)
	fmt.Fprintf(cmd.OutOrStdout(), "Analysis Type: %s\n", report.AnalysisType)
	fmt.Fprintf(cmd.OutOrStdout(), "Window: %s to %s\n\n", report.WindowStart, report.WindowEnd)

	fmt.Fprintln(cmd.OutOrStdout(), "Ranked Rostered Pitchers")
	printProjectionTable(cmd, report.RankedPitchers)
	fmt.Fprintln(cmd.OutOrStdout())
	fmt.Fprintln(cmd.OutOrStdout(), "Two-Start Pitchers")
	printProjectionTable(cmd, report.TwoStartPitchers)
	fmt.Fprintln(cmd.OutOrStdout())
	fmt.Fprintln(cmd.OutOrStdout(), "Unmatched Players")
	printMatchTable(cmd, report.UnmatchedPlayers)
	if len(report.AmbiguousPlayers) > 0 {
		fmt.Fprintln(cmd.OutOrStdout())
		fmt.Fprintln(cmd.OutOrStdout(), "Ambiguous Matches")
		printMatchTable(cmd, report.AmbiguousPlayers)
	}
}

func printProjectionTable(cmd *cobra.Command, rows []pitchers.PitcherProjection) {
	if len(rows) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "(none)")
		return
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PLAYER\tTEAM\tMATCHED\tSTARTS\tTOTAL_FPTS\tAVG_FPTS\tFLAGS")
	for _, r := range rows {
		flags := strings.Join(r.Flags, ",")
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%.1f\t%.1f\t%s\n", r.PlayerName, r.MLBTeam, r.MatchedPitcherName, r.StartCount, r.TotalProjectedFPTS, r.AverageProjectedFPTS, flags)
	}
	w.Flush()
}

func printMatchTable(cmd *cobra.Command, rows []pitchers.MatchResult) {
	if len(rows) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "(none)")
		return
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "INPUT\tTEAM\tSTATUS\tMATCHED\tEXPLANATION")
	for _, r := range rows {
		matched := r.MatchedPitcherName
		if matched == "" && len(r.CandidateDisplayList) > 0 {
			matched = strings.Join(r.CandidateDisplayList, " | ")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", r.InputPlayerName, r.InputMLBTeam, r.MatchStatus, matched, r.Explanation)
	}
	w.Flush()
}

func printLastReportTable(cmd *cobra.Command, rows []pitchrepo.AnalysisResultRow) {
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TYPE\tPLAYER\tTEAM\tMATCHED\tSTARTS\tTOTAL_FPTS\tRANK")
	for _, r := range rows {
		total := "-"
		if r.TotalProjectedFPTS != nil {
			total = fmt.Sprintf("%.1f", *r.TotalProjectedFPTS)
		}
		rank := "-"
		if r.ResultRank != nil {
			rank = fmt.Sprintf("%d", *r.ResultRank)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n", r.ResultType, r.PlayerName, r.MLBTeam, r.MatchedPitcherName, r.ProjectedStartCount, total, rank)
	}
	w.Flush()
}

func planningRulesFromConfig(cfg config.Config) planner.RuleConfig {
	return planner.RuleConfig{
		AutoStartMinTotalFPTS:    cfg.Planning.Pitchers.AutoStartMinTotalFPTS,
		LikelyStartMinTotalFPTS:  cfg.Planning.Pitchers.LikelyStartMinTotalFPTS,
		MonitorMinTotalFPTS:      cfg.Planning.Pitchers.MonitorMinTotalFPTS,
		TBDPenalty:               cfg.Planning.Pitchers.TBDPenalty,
		MissingProjectionPenalty: cfg.Planning.Pitchers.MissingProjectionPenalty,
		AmbiguousMatchPenalty:    cfg.Planning.Pitchers.AmbiguousMatchPenalty,
	}
}

func printPitcherPlan(cmd *cobra.Command, plan *planner.Plan, startSitOnly bool) {
	if plan == nil {
		fmt.Fprintln(cmd.OutOrStdout(), "(no plan)")
		return
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Pitcher Plan: %d\n", plan.ID)
	fmt.Fprintf(cmd.OutOrStdout(), "Window: %s to %s\n", plan.WindowStart, plan.WindowEnd)
	if plan.SyncRunID != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "ESPN sync run: %d\n", *plan.SyncRunID)
	}
	if plan.ImportRunID != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Forecaster import run: %d\n", *plan.ImportRunID)
	}
	if plan.AnalysisRunID != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Analysis run: %d\n", *plan.AnalysisRunID)
	}
	fmt.Fprintln(cmd.OutOrStdout())

	groups := bucketGroups(plan.Items)
	if startSitOnly {
		fmt.Fprintln(cmd.OutOrStdout(), "Start Candidates")
		printPlanItemsTable(cmd, append(groups[planner.BucketAutoStart], groups[planner.BucketLikelyStart]...))
		fmt.Fprintln(cmd.OutOrStdout())
		fmt.Fprintln(cmd.OutOrStdout(), "Monitor")
		printPlanItemsTable(cmd, groups[planner.BucketMonitor])
		fmt.Fprintln(cmd.OutOrStdout())
		fmt.Fprintln(cmd.OutOrStdout(), "Sit Candidates")
		printPlanItemsTable(cmd, append(groups[planner.BucketBench], groups[planner.BucketNoStartScheduled]...))
		fmt.Fprintln(cmd.OutOrStdout())
	} else {
		order := []struct {
			Name   string
			Bucket planner.Bucket
		}{
			{Name: "Projection: Auto-Start", Bucket: planner.BucketAutoStart},
			{Name: "Projection: Likely Start", Bucket: planner.BucketLikelyStart},
			{Name: "Projection: Monitor", Bucket: planner.BucketMonitor},
			{Name: "Projection: Low Priority", Bucket: planner.BucketBench},
			{Name: "Projection: No Starts", Bucket: planner.BucketNoStartScheduled},
		}
		for i, entry := range order {
			fmt.Fprintln(cmd.OutOrStdout(), entry.Name)
			printPlanItemsTable(cmd, groups[entry.Bucket])
			if i < len(order)-1 {
				fmt.Fprintln(cmd.OutOrStdout())
			}
		}
	}

	fmt.Fprintln(cmd.OutOrStdout())
	fmt.Fprintln(cmd.OutOrStdout(), "Summary")
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "BUCKET\tCOUNT")
	for _, bucket := range []planner.Bucket{planner.BucketAutoStart, planner.BucketLikelyStart, planner.BucketMonitor, planner.BucketBench, planner.BucketNoStartScheduled} {
		fmt.Fprintf(w, "%s\t%d\n", bucket, plan.Summary.Counts[bucket])
	}
	w.Flush()
}

func bucketGroups(items []planner.PlanItem) map[planner.Bucket][]planner.PlanItem {
	out := map[planner.Bucket][]planner.PlanItem{}
	for _, item := range items {
		out[item.Bucket] = append(out[item.Bucket], item)
	}
	return out
}

func printPlanItemsTable(cmd *cobra.Command, rows []planner.PlanItem) {
	if len(rows) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "(none)")
		return
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PLAYER\tTEAM\tMATCHED\tSTARTS\tTOTAL_FPTS\tFLAGS\tNOTES")
	for _, r := range rows {
		total := "-"
		if r.TotalProjectedFPTS != nil {
			total = fmt.Sprintf("%.1f", *r.TotalProjectedFPTS)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\t%s\n", r.PlayerName, r.MLBTeam, r.MatchedPitcherName, r.ProjectedStartCount, total, strings.Join(r.Flags, ","), strings.Join(r.Notes, "; "))
	}
	w.Flush()
}
