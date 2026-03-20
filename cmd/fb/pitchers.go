package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	esrepo "fantasy-baseball/internal/espn/repository"
	essvc "fantasy-baseball/internal/espn/service"
	"fantasy-baseball/internal/forecaster"
	pitchers "fantasy-baseball/internal/pitchers"
	pitchrepo "fantasy-baseball/internal/pitchers/repository"
	pitchsvc "fantasy-baseball/internal/pitchers/service"
	"fantasy-baseball/internal/store/sqlite"

	"github.com/spf13/cobra"
)

func newPitchersCmd(opts *cliOptions) *cobra.Command {
	cmd := &cobra.Command{Use: "pitchers", Short: "Roster-aware pitcher analysis"}
	cmd.AddCommand(newPitchersAnalyzeWeekCmd(opts))
	cmd.AddCommand(newPitchersTwoStartCmd(opts))
	cmd.AddCommand(newPitchersStreamersCmd(opts))
	cmd.AddCommand(newPitchersReportCmd(opts))
	cmd.AddCommand(newPitchersLastReportCmd(opts))
	cmd.AddCommand(newPitchersExplainMatchesCmd(opts))
	return cmd
}

func newPitchersAnalyzeWeekCmd(opts *cliOptions) *cobra.Command {
	var rosterPath, fromRaw, toRaw string
	var useESPN bool
	var syncRunID int64
	var importRunID int64
	cmd := &cobra.Command{
		Use:   "analyze-week (--roster <path> | --espn)",
		Short: "Analyze weekly projected starts for rostered pitchers",
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts2, err := buildAnalysisOptions(cmd, rosterPath, "", fromRaw, toRaw, &importRunID)
			if err != nil {
				return err
			}
			r, err := withPitchersServices(cmd.Context(), opts, func(_ context.Context, svc *pitchsvc.Service, es *essvc.Service) (any, error) {
				if err := resolveRosterSource(cmd.Context(), cmd, es, useESPN, syncRunID, &opts2); err != nil {
					return nil, err
				}
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
	cmd.Flags().StringVar(&rosterPath, "roster", "", "Path to roster JSON")
	cmd.Flags().BoolVar(&useESPN, "espn", false, "Use latest ESPN roster snapshot instead of --roster")
	cmd.Flags().Int64Var(&syncRunID, "sync-run", 0, "ESPN sync run ID (defaults to latest when --espn)")
	cmd.Flags().StringVar(&fromRaw, "from", "", "Window start date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&toRaw, "to", "", "Window end date (YYYY-MM-DD)")
	cmd.Flags().Int64Var(&importRunID, "import-run", 0, "Forecaster import run ID (defaults to latest)")
	return cmd
}

func newPitchersTwoStartCmd(opts *cliOptions) *cobra.Command {
	var rosterPath, fromRaw, toRaw string
	var useESPN bool
	var syncRunID int64
	var importRunID int64
	cmd := &cobra.Command{
		Use:   "two-start (--roster <path> | --espn)",
		Short: "Show rostered pitchers with 2+ projected starts",
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts2, err := buildAnalysisOptions(cmd, rosterPath, "", fromRaw, toRaw, &importRunID)
			if err != nil {
				return err
			}
			r, err := withPitchersServices(cmd.Context(), opts, func(_ context.Context, svc *pitchsvc.Service, es *essvc.Service) (any, error) {
				if err := resolveRosterSource(cmd.Context(), cmd, es, useESPN, syncRunID, &opts2); err != nil {
					return nil, err
				}
				return svc.TwoStart(cmd.Context(), opts2)
			})
			if err != nil {
				return err
			}
			report := r.(pitchers.AnalysisReport)
			if opts.OutputJSON {
				return writeJSON(cmd, report.TwoStartPitchers)
			}
			printProjectionTable(cmd, report.TwoStartPitchers)
			return nil
		},
	}
	cmd.Flags().StringVar(&rosterPath, "roster", "", "Path to roster JSON")
	cmd.Flags().BoolVar(&useESPN, "espn", false, "Use latest ESPN roster snapshot instead of --roster")
	cmd.Flags().Int64Var(&syncRunID, "sync-run", 0, "ESPN sync run ID (defaults to latest when --espn)")
	cmd.Flags().StringVar(&fromRaw, "from", "", "Window start date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&toRaw, "to", "", "Window end date (YYYY-MM-DD)")
	cmd.Flags().Int64Var(&importRunID, "import-run", 0, "Forecaster import run ID (defaults to latest)")
	return cmd
}

func newPitchersStreamersCmd(opts *cliOptions) *cobra.Command {
	var rosterPath, poolPath, fromRaw, toRaw string
	var importRunID int64
	var topN int
	var minTotal float64
	cmd := &cobra.Command{
		Use:   "streamers --roster <path> --pool <path>",
		Short: "Rank streamer candidates from a free-agent pool",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(rosterPath) == "" || strings.TrimSpace(poolPath) == "" {
				return fmt.Errorf("--roster and --pool are required")
			}
			opts2, err := buildAnalysisOptions(cmd, rosterPath, poolPath, fromRaw, toRaw, &importRunID)
			if err != nil {
				return err
			}
			opts2.TopN = topN
			if cmd.Flags().Changed("min-total-fpts") {
				opts2.MinTotalFPTS = &minTotal
			}
			r, err := withPitchersService(cmd.Context(), opts, func(_ context.Context, svc *pitchsvc.Service) (any, error) {
				return svc.Streamers(cmd.Context(), opts2)
			})
			if err != nil {
				return err
			}
			report := r.(pitchers.AnalysisReport)
			if opts.OutputJSON {
				return writeJSON(cmd, report)
			}
			printProjectionTable(cmd, report.RankedPitchers)
			return nil
		},
	}
	cmd.Flags().StringVar(&rosterPath, "roster", "", "Path to roster JSON")
	cmd.Flags().StringVar(&poolPath, "pool", "", "Path to free_agents JSON")
	cmd.Flags().StringVar(&fromRaw, "from", "", "Window start date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&toRaw, "to", "", "Window end date (YYYY-MM-DD)")
	cmd.Flags().Int64Var(&importRunID, "import-run", 0, "Forecaster import run ID (defaults to latest)")
	cmd.Flags().IntVar(&topN, "top", 10, "Top streamer rows")
	cmd.Flags().Float64Var(&minTotal, "min-total-fpts", 0, "Minimum total projected FPTS")
	return cmd
}

func newPitchersReportCmd(opts *cliOptions) *cobra.Command {
	var rosterPath, fromRaw, toRaw string
	var useESPN bool
	var syncRunID int64
	var importRunID int64
	cmd := &cobra.Command{
		Use:   "report (--roster <path> | --espn)",
		Short: "Produce combined weekly pitcher report",
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts2, err := buildAnalysisOptions(cmd, rosterPath, "", fromRaw, toRaw, &importRunID)
			if err != nil {
				return err
			}
			r, err := withPitchersServices(cmd.Context(), opts, func(_ context.Context, svc *pitchsvc.Service, es *essvc.Service) (any, error) {
				if err := resolveRosterSource(cmd.Context(), cmd, es, useESPN, syncRunID, &opts2); err != nil {
					return nil, err
				}
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
	cmd.Flags().StringVar(&rosterPath, "roster", "", "Path to roster JSON")
	cmd.Flags().BoolVar(&useESPN, "espn", false, "Use latest ESPN roster snapshot instead of --roster")
	cmd.Flags().Int64Var(&syncRunID, "sync-run", 0, "ESPN sync run ID (defaults to latest when --espn)")
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
	var rosterPath, fromRaw, toRaw string
	var useESPN bool
	var syncRunID int64
	var importRunID int64
	cmd := &cobra.Command{
		Use:   "explain-matches (--roster <path> | --espn)",
		Short: "Explain roster player matching against probable starts",
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts2, err := buildAnalysisOptions(cmd, rosterPath, "", fromRaw, toRaw, &importRunID)
			if err != nil {
				return err
			}
			v, err := withPitchersServices(cmd.Context(), opts, func(_ context.Context, svc *pitchsvc.Service, es *essvc.Service) (any, error) {
				if err := resolveRosterSource(cmd.Context(), cmd, es, useESPN, syncRunID, &opts2); err != nil {
					return nil, err
				}
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
	cmd.Flags().StringVar(&rosterPath, "roster", "", "Path to roster JSON")
	cmd.Flags().BoolVar(&useESPN, "espn", false, "Use latest ESPN roster snapshot instead of --roster")
	cmd.Flags().Int64Var(&syncRunID, "sync-run", 0, "ESPN sync run ID (defaults to latest when --espn)")
	cmd.Flags().StringVar(&fromRaw, "from", "", "Window start date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&toRaw, "to", "", "Window end date (YYYY-MM-DD)")
	cmd.Flags().Int64Var(&importRunID, "import-run", 0, "Forecaster import run ID (defaults to latest)")
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

func withPitchersService(ctx context.Context, opts *cliOptions, fn func(context.Context, *pitchsvc.Service) (any, error)) (any, error) {
	return withPitchersServices(ctx, opts, func(c context.Context, svc *pitchsvc.Service, _ *essvc.Service) (any, error) {
		return fn(c, svc)
	})
}

func buildAnalysisOptions(cmd *cobra.Command, rosterPath string, poolPath string, fromRaw string, toRaw string, importRunID *int64) (pitchers.AnalysisOptions, error) {
	from, to, err := parseWindow(fromRaw, toRaw)
	if err != nil {
		return pitchers.AnalysisOptions{}, err
	}
	var runID *int64
	if cmd.Flags().Changed("import-run") && importRunID != nil {
		v := *importRunID
		runID = &v
	}
	return pitchers.AnalysisOptions{From: from, To: to, ImportRunID: runID, RosterPath: rosterPath, PoolPath: poolPath}, nil
}

func resolveRosterSource(ctx context.Context, cmd *cobra.Command, es *essvc.Service, useESPN bool, syncRunID int64, opts *pitchers.AnalysisOptions) error {
	if useESPN && strings.TrimSpace(opts.RosterPath) != "" {
		return fmt.Errorf("use either --roster or --espn, not both")
	}
	if !useESPN && strings.TrimSpace(opts.RosterPath) == "" {
		return fmt.Errorf("either --roster or --espn is required")
	}
	if !useESPN {
		return nil
	}
	var runIDPtr *int64
	if cmd.Flags().Changed("sync-run") {
		runIDPtr = &syncRunID
	}
	inputs, source, err := es.RosterInputsForPitchers(ctx, runIDPtr)
	if err != nil {
		return err
	}
	opts.RosterInputs = inputs
	opts.RosterSource = source
	return nil
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

func marshalAny(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
