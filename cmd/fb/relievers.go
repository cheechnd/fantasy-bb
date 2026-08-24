package main

import (
	"context"
	"fmt"
	"text/tabwriter"
	"time"

	espnrepo "fantasy-baseball/internal/espn/repository"
	"fantasy-baseball/internal/relievers"
	"fantasy-baseball/internal/store/sqlite"

	"github.com/spf13/cobra"
)

func newRelieversCmd(opts *cliOptions) *cobra.Command {
	cmd := &cobra.Command{Use: "relievers", Short: "Read-only ESPN reliever depth chart facts"}
	cmd.AddGroup(
		&cobra.Group{ID: "sync", Title: "Sync"},
		&cobra.Group{ID: "show", Title: "Show"},
	)
	syncCmd := newRelieversSyncCmd(opts)
	syncCmd.GroupID = "sync"
	showCmd := newRelieversShowCmd(opts)
	showCmd.GroupID = "show"
	statusCmd := newRelieversStatusCmd(opts)
	statusCmd.GroupID = "show"
	cmd.AddCommand(syncCmd, showCmd, statusCmd)
	return cmd
}

func newRelieversSyncCmd(opts *cliOptions) *cobra.Command {
	var sourceURL string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Fetch and persist ESPN reliever depth chart facts",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := withRelieverService(cmd.Context(), opts, func(ctx context.Context, svc *relievers.Service) (any, error) {
				return svc.Sync(ctx, relievers.SyncOptions{SourceURL: sourceURL, DryRun: dryRun})
			})
			if err != nil {
				if v == nil {
					return err
				}
			}
			summary := v.(relievers.SyncSummary)
			if opts.OutputJSON {
				out := map[string]any{"ok": err == nil, "summary": summary}
				if err != nil {
					out["error"] = err.Error()
				}
				return writeJSON(cmd, out)
			}
			if summary.RunID != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Reliever run: %d\n", *summary.RunID)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Source: %s\n", summary.SourceURL)
			if summary.SourceDate != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Source date: %s\n", summary.SourceDate)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Fetched at: %s\n", summary.FetchedAt)
			fmt.Fprintf(cmd.OutOrStdout(), "Status: %s\n", summary.Status)
			fmt.Fprintf(cmd.OutOrStdout(), "Teams parsed: %d\n", summary.TeamCount)
			fmt.Fprintf(cmd.OutOrStdout(), "Rows parsed: %d\n", summary.RowCount)
			fmt.Fprintf(cmd.OutOrStdout(), "Matched: %d\n", summary.MatchedCount)
			fmt.Fprintf(cmd.OutOrStdout(), "Unmatched: %d\n", summary.UnmatchedCount)
			fmt.Fprintf(cmd.OutOrStdout(), "Ambiguous: %d\n", summary.AmbiguousCount)
			fmt.Fprintf(cmd.OutOrStdout(), "Conflicts: %d\n", summary.ConflictCount)
			if len(summary.Warnings) > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "Warnings: %d\n", len(summary.Warnings))
			}
			if summary.DryRun {
				fmt.Fprintln(cmd.OutOrStdout(), "Mode: DRY-RUN (no snapshot persisted)")
			}
			return err
		},
	}
	cmd.Flags().StringVar(&sourceURL, "url", relievers.DefaultSourceURL, "ESPN reliever depth chart URL")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Fetch and parse but do not persist")
	return cmd
}

func newRelieversShowCmd(opts *cliOptions) *cobra.Command {
	var runID int64
	var limit int
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show latest or selected ESPN reliever depth chart facts",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := withRelieverService(cmd.Context(), opts, func(ctx context.Context, svc *relievers.Service) (any, error) {
				var id *int64
				if cmd.Flags().Changed("run") {
					id = &runID
				}
				run, rows, err := svc.Show(ctx, id, limit)
				return map[string]any{"run": run, "rows": rows}, err
			})
			if err != nil {
				return err
			}
			payload := v.(map[string]any)
			if opts.OutputJSON {
				return writeJSON(cmd, payload)
			}
			run, _ := payload["run"].(*relievers.DepthChartRun)
			if run == nil {
				fmt.Fprintln(cmd.OutOrStdout(), "No reliever depth chart runs found. Run `fb relievers sync` first.")
				return nil
			}
			rows := payload["rows"].([]relievers.DepthChartEntry)
			fmt.Fprintf(cmd.OutOrStdout(), "Reliever run: %d\n", run.ID)
			fmt.Fprintf(cmd.OutOrStdout(), "Source date: %s\n", firstNonEmpty(run.SourceDate, "-"))
			fmt.Fprintf(cmd.OutOrStdout(), "Fetched at: %s\n", run.FetchedAt.Format(time.RFC3339))
			fmt.Fprintf(cmd.OutOrStdout(), "Status: %s\n", run.Status)
			fmt.Fprintf(cmd.OutOrStdout(), "Rows: %d\n\n", run.RowCount)
			printRelieversTable(cmd, rows)
			return nil
		},
	}
	cmd.Flags().Int64Var(&runID, "run", 0, "Specific reliever run ID (defaults to latest)")
	cmd.Flags().IntVar(&limit, "limit", 200, "Maximum rows to show")
	return cmd
}

func newRelieversStatusCmd(opts *cliOptions) *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show reliever depth chart sync history and freshness",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := withRelieverService(cmd.Context(), opts, func(ctx context.Context, svc *relievers.Service) (any, error) {
				return svc.Status(ctx, limit)
			})
			if err != nil {
				return err
			}
			runs := v.([]relievers.DepthChartRun)
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"ok": true, "runs": runs})
			}
			if len(runs) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No reliever depth chart runs found.")
				return nil
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tSTATUS\tSOURCE_DATE\tFETCHED_AT\tTEAMS\tROWS\tMATCHED\tUNMATCHED\tAMBIG\tCONFLICT")
			for _, run := range runs {
				fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%d\t%d\t%d\t%d\t%d\t%d\n", run.ID, run.Status, firstNonEmpty(run.SourceDate, "-"), run.FetchedAt.Format(time.RFC3339), run.TeamCount, run.RowCount, run.MatchedCount, run.UnmatchedCount, run.AmbiguousCount, run.ConflictCount)
			}
			w.Flush()
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "Maximum runs to show")
	return cmd
}

func withRelieverService(ctx context.Context, opts *cliOptions, fn func(context.Context, *relievers.Service) (any, error)) (any, error) {
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
	espnRepo := espnrepo.New(store.DB())
	repo := relievers.NewRepository(store.DB())
	svc := relievers.NewService(repo, espnRepo)
	return fn(ctx, svc)
}

func printRelieversTable(cmd *cobra.Command, rows []relievers.DepthChartEntry) {
	if len(rows) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No reliever depth chart rows found.")
		return
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "RUN\tTEAM\tPLAYER\tRELIEF_ROLE\tESPN_ID\tROSTER%\tMATCH\tCONFLICT")
	for _, row := range rows {
		playerID := "-"
		if row.ESPNPlayerID != nil {
			playerID = fmt.Sprintf("%d", *row.ESPNPlayerID)
		}
		pct := "-"
		if row.RosterPercent != nil {
			pct = fmt.Sprintf("%.1f", *row.RosterPercent)
		}
		conflict := "no"
		if row.ConflictFlag {
			conflict = firstNonEmpty(row.ConflictReason, "yes")
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", row.RunID, row.MLBTeam, row.PlayerName, row.ReliefRole, playerID, pct, row.MatchStatus, conflict)
	}
	w.Flush()
}
