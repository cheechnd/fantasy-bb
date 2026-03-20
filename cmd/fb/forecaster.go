package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"fantasy-baseball/internal/config"
	"fantasy-baseball/internal/forecaster"
	"fantasy-baseball/internal/forecaster/service"
	"fantasy-baseball/internal/store/sqlite"

	"github.com/spf13/cobra"
)

const defaultForecasterURL = "https://www.espn.com/fantasy/baseball/story/_/id/31165100/fantasy-baseball-forecaster-probable-starting-pitcher-projections-matchups-daily-weekly-leagues"

func newForecasterCmd(opts *cliOptions) *cobra.Command {
	fc := &cobra.Command{Use: "forecaster", Short: "Forecaster probable-start import and analysis"}
	fc.AddCommand(newForecasterImportCmd(opts))
	fc.AddCommand(newForecasterListCmd(opts))
	fc.AddCommand(newForecasterShowWeekCmd(opts))
	fc.AddCommand(newForecasterTopCmd(opts))
	fc.AddCommand(newForecasterClearCmd(opts))
	fc.AddCommand(newForecasterSourceStatusCmd(opts))
	fc.AddCommand(newForecasterWarningsCmd(opts))
	return fc
}

func newForecasterImportCmd(opts *cliOptions) *cobra.Command {
	var filePath string
	var sourceURL string

	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import a forecaster HTML source",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cmd.Flags().Changed("url") && strings.TrimSpace(sourceURL) == "" {
				sourceURL = defaultForecasterURL
			}
			if (filePath == "" && sourceURL == "") || (filePath != "" && sourceURL != "") {
				return fmt.Errorf("provide exactly one of --file or --url")
			}

			ctx := cmd.Context()
			cfg, _, err := loadConfigWithOverrides(opts)
			if err != nil {
				return err
			}

			store, err := sqlite.Open(cfg.DBPath)
			if err != nil {
				return err
			}
			defer store.Close()
			if _, err := store.Migrate(ctx); err != nil {
				return err
			}

			repo := forecaster.NewRepository(store.DB())
			svc := service.New(repo)

			var summary service.ImportSummary
			if filePath != "" {
				summary, err = svc.ImportFromFile(ctx, filePath)
			} else {
				summary, err = svc.ImportFromURL(ctx, sourceURL)
			}
			if err != nil {
				return err
			}

			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{
					"ok":      true,
					"command": "forecaster import",
					"summary": summary,
				})
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Source: %s (%s)\n", summary.SourceIdentifier, summary.SourceType)
			fmt.Fprintf(cmd.OutOrStdout(), "Imported at: %s\n", summary.ImportRun.ImportedAt.Format(time.RFC3339))
			fmt.Fprintf(cmd.OutOrStdout(), "Raw team rows: %d\n", summary.RawRows)
			fmt.Fprintf(cmd.OutOrStdout(), "Probable starts created: %d\n", summary.ProbableStartCount)
			fmt.Fprintf(cmd.OutOrStdout(), "Warnings: %d\n", summary.WarningCount)
			if summary.WarningCount > 0 {
				limit := 5
				if len(summary.Warnings) < limit {
					limit = len(summary.Warnings)
				}
				fmt.Fprintln(cmd.OutOrStdout(), "Warning sample:")
				for i := 0; i < limit; i++ {
					fmt.Fprintf(cmd.OutOrStdout(), "- [%s] %s\n", summary.Warnings[i].WarningType, summary.Warnings[i].Message)
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&filePath, "file", "", "Path to local HTML file")
	cmd.Flags().StringVar(&sourceURL, "url", "", "Source URL for forecaster HTML (optional value; defaults to ESPN forecaster URL when --url is provided without a value)")
	cmd.Flags().Lookup("url").NoOptDefVal = defaultForecasterURL
	return cmd
}

func newForecasterListCmd(opts *cliOptions) *cobra.Command {
	var from string
	var to string
	var team string
	var pitcher string
	var throws string
	var minFPTS float64
	var hasMinFPTS bool
	var includeTBD bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List normalized probable starts",
		RunE: func(cmd *cobra.Command, _ []string) error {
			filter := forecaster.ListFilter{Team: team, Pitcher: pitcher, ThrowsHand: strings.ToUpper(throws), IncludeTBD: includeTBD}
			fromDate, err := parseDateFlag(from)
			if err != nil {
				return err
			}
			toDate, err := parseDateFlag(to)
			if err != nil {
				return err
			}
			filter.From = fromDate
			filter.To = toDate
			if hasMinFPTS {
				filter.MinFPTS = &minFPTS
			}

			starts, err := withForecasterService(cmd.Context(), opts, func(ctx context.Context, svc *service.Service, _ appExecution) (any, error) {
				return svc.List(ctx, filter)
			})
			if err != nil {
				return err
			}
			rows := starts.([]forecaster.ProbableStart)

			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"ok": true, "count": len(rows), "rows": rows})
			}
			printProbableStartsTable(cmd, rows)
			return nil
		},
	}

	cmd.Flags().StringVar(&from, "from", "", "Filter from date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&to, "to", "", "Filter to date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&team, "team", "", "Team code filter (e.g., NYY)")
	cmd.Flags().StringVar(&pitcher, "pitcher", "", "Pitcher name contains filter")
	cmd.Flags().StringVar(&throws, "throws", "", "Throws hand filter (L|R)")
	cmd.Flags().Float64Var(&minFPTS, "min-fpts", 0, "Minimum projected FPTS")
	cmd.Flags().BoolVar(&includeTBD, "include-tbd", false, "Include TBD rows")
	cmd.Flags().BoolVar(&hasMinFPTS, "use-min-fpts", false, "Enable min-fpts filter")
	_ = cmd.Flags().MarkHidden("use-min-fpts")
	cmd.PreRun = func(cmd *cobra.Command, _ []string) {
		hasMinFPTS = cmd.Flags().Changed("min-fpts")
	}
	return cmd
}

func newForecasterShowWeekCmd(opts *cliOptions) *cobra.Command {
	var from string
	var includeTBD bool
	cmd := &cobra.Command{
		Use:   "show-week",
		Short: "Show next 7 days of probable starts grouped by date",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := withForecasterService(cmd.Context(), opts, func(ctx context.Context, svc *service.Service, _ appExecution) (any, error) {
				return svc.ShowWeek(ctx, from, includeTBD)
			})
			if err != nil {
				return err
			}
			rows := v.([]forecaster.ProbableStart)
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"ok": true, "count": len(rows), "rows": rows})
			}
			printGroupedByDate(cmd, rows)
			return nil
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "Week start date (YYYY-MM-DD); defaults to today")
	cmd.Flags().BoolVar(&includeTBD, "include-tbd", true, "Include TBD rows")
	return cmd
}

func newForecasterTopCmd(opts *cliOptions) *cobra.Command {
	var from string
	var to string
	var top int
	var minFPTS float64
	var team string
	var hasMinFPTS bool
	cmd := &cobra.Command{
		Use:   "top",
		Short: "Show top projected probable starts",
		RunE: func(cmd *cobra.Command, _ []string) error {
			fromDate, err := parseDateFlag(from)
			if err != nil {
				return err
			}
			toDate, err := parseDateFlag(to)
			if err != nil {
				return err
			}
			filter := forecaster.TopFilter{From: fromDate, To: toDate, TopN: top, Team: team}
			if hasMinFPTS {
				filter.MinFPTS = &minFPTS
			}

			v, err := withForecasterService(cmd.Context(), opts, func(ctx context.Context, svc *service.Service, _ appExecution) (any, error) {
				return svc.Top(ctx, filter)
			})
			if err != nil {
				return err
			}
			rows := v.([]forecaster.ProbableStart)
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"ok": true, "count": len(rows), "rows": rows})
			}
			printProbableStartsTable(cmd, rows)
			return nil
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "From date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&to, "to", "", "To date (YYYY-MM-DD)")
	cmd.Flags().IntVar(&top, "top", 10, "Maximum rows to return")
	cmd.Flags().Float64Var(&minFPTS, "min-fpts", 0, "Minimum projected FPTS")
	cmd.Flags().StringVar(&team, "team", "", "Team code filter")
	cmd.PreRun = func(cmd *cobra.Command, _ []string) {
		hasMinFPTS = cmd.Flags().Changed("min-fpts")
	}
	return cmd
}

func newForecasterClearCmd(opts *cliOptions) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "clear",
		Short: "Delete imported forecaster data",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := withForecasterService(cmd.Context(), opts, func(ctx context.Context, svc *service.Service, exec appExecution) (any, error) {
				if exec.RequireConfirmation && !yes {
					return nil, fmt.Errorf("confirmation required: rerun with --yes to clear forecaster data")
				}
				if exec.DryRun {
					runs, err := svc.SourceStatus(ctx, 1)
					if err != nil {
						return nil, err
					}
					count := 0
					if len(runs) > 0 {
						count = runs[0].ProbableStartCount
					}
					return map[string]any{"dry_run": true, "latest_probable_start_count": count}, nil
				}
				return svc.Clear(ctx)
			})
			if err != nil {
				return err
			}
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"ok": true, "result": v})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Cleared forecaster data: %+v\n", v)
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Confirm data deletion")
	return cmd
}

func newForecasterSourceStatusCmd(opts *cliOptions) *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:     "status",
		Aliases: []string{"source-status"},
		Short:   "Show import history and latest status",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := withForecasterService(cmd.Context(), opts, func(ctx context.Context, svc *service.Service, _ appExecution) (any, error) {
				runs, err := svc.SourceStatus(ctx, limit)
				if err != nil {
					return nil, err
				}
				latest, err := svc.LatestImport(ctx)
				if err != nil {
					return nil, err
				}
				return map[string]any{"runs": runs, "latest": latest}, nil
			})
			if err != nil {
				return err
			}
			payload := v.(map[string]any)
			runs := payload["runs"].([]forecaster.ImportRun)
			latest := payload["latest"]
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"ok": true, "runs": runs, "latest": latest})
			}
			printSourceStatus(cmd, runs)
			if latest != nil {
				lr := latest.(*forecaster.ImportRun)
				fmt.Fprintf(cmd.OutOrStdout(), "\nLatest import: #%d %s %s (%s)\n", lr.ID, lr.SourceType, lr.SourceIdentifier, lr.ImportedAt.Format(time.RFC3339))
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "Maximum runs to show")
	return cmd
}

func newForecasterWarningsCmd(opts *cliOptions) *cobra.Command {
	var limit int
	var importRunID int64
	cmd := &cobra.Command{
		Use:   "warnings",
		Short: "Show parse warnings for the latest or specified import run",
		RunE: func(cmd *cobra.Command, _ []string) error {
			var runIDPtr *int64
			if cmd.Flags().Changed("import-run") {
				runIDPtr = &importRunID
			}
			v, err := withForecasterService(cmd.Context(), opts, func(ctx context.Context, svc *service.Service, _ appExecution) (any, error) {
				return svc.Warnings(ctx, runIDPtr, limit)
			})
			if err != nil {
				return err
			}
			rows := v.([]forecaster.ParseWarning)
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"ok": true, "count": len(rows), "warnings": rows})
			}
			printWarnings(cmd, rows)
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 50, "Maximum warnings to show")
	cmd.Flags().Int64Var(&importRunID, "import-run", 0, "Import run ID (defaults to latest)")
	return cmd
}

type appExecution struct {
	DryRun              bool
	RequireConfirmation bool
}

func withForecasterService(ctx context.Context, opts *cliOptions, fn func(context.Context, *service.Service, appExecution) (any, error)) (any, error) {
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

	repo := forecaster.NewRepository(s.DB())
	svc := service.New(repo)
	exec := appExecution{DryRun: cfg.Execution.DryRun, RequireConfirmation: cfg.Execution.RequireConfirmation}
	if opts.DryRun {
		exec.DryRun = true
	}
	if !opts.RequireConfirmation {
		exec.RequireConfirmation = false
	}
	return fn(ctx, svc, exec)
}

func loadConfigWithOverrides(opts *cliOptions) (config.Config, config.Paths, error) {
	cfg, paths, err := config.Load(toOverrides(opts))
	return cfg, paths, err
}

func parseDateFlag(raw string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	tm, err := time.ParseInLocation("2006-01-02", raw, time.Local)
	if err != nil {
		return nil, fmt.Errorf("invalid date %q, expected YYYY-MM-DD", raw)
	}
	tm = time.Date(tm.Year(), tm.Month(), tm.Day(), 0, 0, 0, 0, time.Local)
	return &tm, nil
}

func printProbableStartsTable(cmd *cobra.Command, rows []forecaster.ProbableStart) {
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "DATE\tTEAM\tOPP\tPITCHER\tTHR\tFPTS\tSTATUS")
	for _, r := range rows {
		date := "n/a"
		if r.GameDate != nil {
			date = r.GameDate.Format("2006-01-02")
		}
		fpts := "-"
		if r.ProjectedFPTS != nil {
			fpts = fmt.Sprintf("%.1f", *r.ProjectedFPTS)
		}
		pitcher := r.PitcherName
		if pitcher == "" {
			pitcher = "-"
		}
		throws := r.ThrowsHand
		if throws == "" {
			throws = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", date, r.Team, r.Opponent, pitcher, throws, fpts, r.Status)
	}
	w.Flush()
}

func printGroupedByDate(cmd *cobra.Command, rows []forecaster.ProbableStart) {
	if len(rows) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No rows found.")
		return
	}
	groups := map[string][]forecaster.ProbableStart{}
	keys := []string{}
	for _, r := range rows {
		key := "unknown-date"
		if r.GameDate != nil {
			key = r.GameDate.Format("2006-01-02")
		}
		if _, ok := groups[key]; !ok {
			keys = append(keys, key)
		}
		groups[key] = append(groups[key], r)
	}
	sort.Strings(keys)
	for _, date := range keys {
		fmt.Fprintf(cmd.OutOrStdout(), "%s\n", date)
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "TEAM\tOPP\tPITCHER\tTHR\tFPTS\tSTATUS")
		for _, r := range groups[date] {
			pitcher := r.PitcherName
			if pitcher == "" {
				pitcher = "-"
			}
			throws := r.ThrowsHand
			if throws == "" {
				throws = "-"
			}
			fpts := "-"
			if r.ProjectedFPTS != nil {
				fpts = fmt.Sprintf("%.1f", *r.ProjectedFPTS)
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", r.Team, r.Opponent, pitcher, throws, fpts, r.Status)
		}
		w.Flush()
		fmt.Fprintln(cmd.OutOrStdout())
	}
}

func printSourceStatus(cmd *cobra.Command, runs []forecaster.ImportRun) {
	if len(runs) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No import runs found.")
		return
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSOURCE\tIDENTIFIER\tIMPORTED_AT\tRAW_ROWS\tSTARTS\tWARNINGS\tSTATUS")
	for _, r := range runs {
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%d\t%d\t%d\t%s\n", r.ID, r.SourceType, abbreviate(r.SourceIdentifier, 40), r.ImportedAt.Format(time.RFC3339), r.RawRowCount, r.ProbableStartCount, r.WarningCount, r.Status)
	}
	w.Flush()
}

func printWarnings(cmd *cobra.Command, rows []forecaster.ParseWarning) {
	if len(rows) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No parse warnings found.")
		return
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tRUN_ID\tTYPE\tMESSAGE\tCREATED_AT")
	for _, r := range rows {
		fmt.Fprintf(w, "%d\t%d\t%s\t%s\t%s\n", r.ID, r.ImportRunID, r.WarningType, abbreviate(r.Message, 80), r.CreatedAt.Format(time.RFC3339))
	}
	w.Flush()
}

func abbreviate(v string, max int) string {
	if len(v) <= max {
		return v
	}
	if max < 4 {
		return v[:max]
	}
	return v[:max-3] + "..."
}

func writeJSON(cmd *cobra.Command, payload any) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}
