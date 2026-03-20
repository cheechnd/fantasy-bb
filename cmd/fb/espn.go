package main

import (
	"context"
	"fmt"
	"text/tabwriter"
	"time"

	"fantasy-baseball/internal/config"
	"fantasy-baseball/internal/espn"
	espnrepo "fantasy-baseball/internal/espn/repository"
	espnsvc "fantasy-baseball/internal/espn/service"
	"fantasy-baseball/internal/store/sqlite"

	"github.com/spf13/cobra"
)

func newESPNCmd(opts *cliOptions) *cobra.Command {
	cmd := &cobra.Command{Use: "espn", Short: "Read-only ESPN ingestion and inspection"}
	cmd.AddCommand(newESPNValidateCmd(opts))
	cmd.AddCommand(newESPNSyncCmd(opts))
	cmd.AddCommand(newESPNShowCmd(opts))
	cmd.AddCommand(newESPNSourceStatusCmd(opts))
	cmd.AddCommand(newESPNWarningsCmd(opts))
	return cmd
}

func newESPNValidateCmd(opts *cliOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate ESPN config, env credentials, and connectivity",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			v, err := withESPNService(ctx, opts, func(_ context.Context, svc *espnsvc.Service, cfg loadedConfig) (any, error) {
				return svc.Validate(ctx, cfg.Config)
			})
			if err != nil {
				return err
			}
			report := v.(espn.ValidateReport)
			if opts.OutputJSON {
				return writeJSON(cmd, report)
			}
			for _, c := range report.Checks {
				name, _ := c["name"].(string)
				ok, _ := c["ok"].(bool)
				if ok {
					fmt.Fprintf(cmd.OutOrStdout(), "[OK] %s\n", name)
					continue
				}
				errMsg, _ := c["error"].(string)
				fmt.Fprintf(cmd.OutOrStdout(), "[FAIL] %s: %s\n", name, errMsg)
			}
			if report.OK {
				fmt.Fprintln(cmd.OutOrStdout(), "ESPN validate: PASS")
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "ESPN validate: FAIL")
			}
			return nil
		},
	}
}

func newESPNSyncCmd(opts *cliOptions) *cobra.Command {
	cmd := &cobra.Command{Use: "sync", Short: "Sync read-only ESPN snapshots"}
	cmd.AddCommand(newESPNSyncRosterCmd(opts))
	return cmd
}

func newESPNSyncRosterCmd(opts *cliOptions) *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "roster",
		Short: "Fetch and persist ESPN league/roster snapshots",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := withESPNService(cmd.Context(), opts, func(_ context.Context, svc *espnsvc.Service, cfg loadedConfig) (any, error) {
				execOpts := executionOptionsFromConfigAndFlags(cmd, cfg.Config, opts)
				if cmd.Flags().Changed("dry-run") {
					execOpts.DryRun = dryRun
				}
				return svc.SyncRoster(cmd.Context(), cfg.Config, espnsvc.SyncOptions{DryRun: execOpts.DryRun})
			})
			if err != nil {
				return err
			}
			summary := v.(espn.SyncSummary)
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"ok": true, "summary": summary})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "League: %s\n", summary.LeagueID)
			fmt.Fprintf(cmd.OutOrStdout(), "Team: %s\n", summary.TeamID)
			fmt.Fprintf(cmd.OutOrStdout(), "Season: %d\n", summary.Season)
			fmt.Fprintf(cmd.OutOrStdout(), "Synced at: %s\n", summary.SyncedAt)
			if summary.SyncRunID != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Sync run: %d\n", *summary.SyncRunID)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Rostered players: %d\n", summary.RosteredPlayers)
			fmt.Fprintf(cmd.OutOrStdout(), "Pitchers identified: %d\n", summary.PitcherCount)
			fmt.Fprintf(cmd.OutOrStdout(), "Warnings: %d\n", summary.WarningCount)
			if summary.DryRun {
				fmt.Fprintln(cmd.OutOrStdout(), "Mode: DRY-RUN (no snapshot persisted)")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Fetch and parse but do not persist")
	return cmd
}

func newESPNShowCmd(opts *cliOptions) *cobra.Command {
	cmd := &cobra.Command{Use: "show", Short: "Show stored ESPN snapshots"}
	cmd.AddCommand(newESPNShowRosterCmd(opts))
	cmd.AddCommand(newESPNShowLeagueCmd(opts))
	return cmd
}

func newESPNShowRosterCmd(opts *cliOptions) *cobra.Command {
	var syncRunID int64
	var pitchersOnly bool
	cmd := &cobra.Command{
		Use:   "roster",
		Short: "Show latest or selected ESPN roster snapshot",
		RunE: func(cmd *cobra.Command, _ []string) error {
			var runID *int64
			if cmd.Flags().Changed("sync-run") {
				runID = &syncRunID
			}
			v, err := withESPNService(cmd.Context(), opts, func(_ context.Context, svc *espnsvc.Service, _ loadedConfig) (any, error) {
				return svc.ShowRoster(cmd.Context(), espnsvc.ShowRosterFilter{SyncRunID: runID, PitchersOnly: pitchersOnly})
			})
			if err != nil {
				return err
			}
			rows := v.([]espn.RosterSnapshot)
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"ok": true, "count": len(rows), "rows": rows})
			}
			printESPNRosterTable(cmd, rows)
			return nil
		},
	}
	cmd.Flags().Int64Var(&syncRunID, "sync-run", 0, "Specific ESPN sync run ID (defaults to latest)")
	cmd.Flags().BoolVar(&pitchersOnly, "pitchers-only", false, "Show only pitchers")
	return cmd
}

func newESPNShowLeagueCmd(opts *cliOptions) *cobra.Command {
	var syncRunID int64
	cmd := &cobra.Command{
		Use:   "league",
		Short: "Show latest or selected ESPN league metadata snapshot",
		RunE: func(cmd *cobra.Command, _ []string) error {
			var runID *int64
			if cmd.Flags().Changed("sync-run") {
				runID = &syncRunID
			}
			v, err := withESPNService(cmd.Context(), opts, func(_ context.Context, svc *espnsvc.Service, _ loadedConfig) (any, error) {
				return svc.ShowLeague(cmd.Context(), runID)
			})
			if err != nil {
				return err
			}
			row := v.(*espn.LeagueSnapshot)
			if row == nil {
				if opts.OutputJSON {
					return writeJSON(cmd, map[string]any{"ok": true, "league": nil})
				}
				fmt.Fprintln(cmd.OutOrStdout(), "No ESPN league snapshot found.")
				return nil
			}
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"ok": true, "league": row})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "League: %s\n", row.LeagueName)
			fmt.Fprintf(cmd.OutOrStdout(), "League ID: %s\n", row.LeagueID)
			fmt.Fprintf(cmd.OutOrStdout(), "Season: %d\n", row.Season)
			fmt.Fprintf(cmd.OutOrStdout(), "Team: %s (%s)\n", row.TeamName, row.TeamID)
			fmt.Fprintf(cmd.OutOrStdout(), "Scoring: %s\n", firstNonEmpty(row.ScoringType, "unknown"))
			fmt.Fprintf(cmd.OutOrStdout(), "Captured: %s\n", row.CreatedAt.Format(time.RFC3339))
			return nil
		},
	}
	cmd.Flags().Int64Var(&syncRunID, "sync-run", 0, "Specific ESPN sync run ID (defaults to latest)")
	return cmd
}

func newESPNSourceStatusCmd(opts *cliOptions) *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:     "status",
		Aliases: []string{"source-status"},
		Short:   "Show ESPN sync history and latest status",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := withESPNService(cmd.Context(), opts, func(_ context.Context, svc *espnsvc.Service, _ loadedConfig) (any, error) {
				runs, err := svc.SourceStatus(cmd.Context(), limit)
				if err != nil {
					return nil, err
				}
				latest, err := svc.LatestSync(cmd.Context())
				if err != nil {
					return nil, err
				}
				return map[string]any{"runs": runs, "latest": latest}, nil
			})
			if err != nil {
				return err
			}
			payload := v.(map[string]any)
			runs := payload["runs"].([]espn.SyncRun)
			if opts.OutputJSON {
				return writeJSON(cmd, payload)
			}
			printESPNSyncRuns(cmd, runs)
			if latest, ok := payload["latest"].(*espn.SyncRun); ok && latest != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "\nLatest sync: #%d %s (%s)\n", latest.ID, latest.Status, latest.CompletedAt.Format(time.RFC3339))
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "Maximum sync runs to show")
	return cmd
}

func newESPNWarningsCmd(opts *cliOptions) *cobra.Command {
	var limit int
	var syncRunID int64
	cmd := &cobra.Command{
		Use:   "warnings",
		Short: "Show parse warnings for latest or selected ESPN sync run",
		RunE: func(cmd *cobra.Command, _ []string) error {
			var runID *int64
			if cmd.Flags().Changed("sync-run") {
				runID = &syncRunID
			}
			v, err := withESPNService(cmd.Context(), opts, func(_ context.Context, svc *espnsvc.Service, _ loadedConfig) (any, error) {
				return svc.Warnings(cmd.Context(), runID, limit)
			})
			if err != nil {
				return err
			}
			rows := v.([]espn.ParseWarning)
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"ok": true, "count": len(rows), "warnings": rows})
			}
			if len(rows) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No ESPN parse warnings found.")
				return nil
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tSYNC_RUN\tTYPE\tMESSAGE\tCREATED_AT")
			for _, row := range rows {
				fmt.Fprintf(w, "%d\t%d\t%s\t%s\t%s\n", row.ID, row.SyncRunID, row.WarningType, abbreviate(row.Message, 80), row.CreatedAt.Format(time.RFC3339))
			}
			w.Flush()
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 50, "Maximum warnings to show")
	cmd.Flags().Int64Var(&syncRunID, "sync-run", 0, "Specific ESPN sync run ID (defaults to latest)")
	return cmd
}

type loadedConfig struct {
	Config config.Config
	Paths  config.Paths
}

func withESPNService(ctx context.Context, opts *cliOptions, fn func(context.Context, *espnsvc.Service, loadedConfig) (any, error)) (any, error) {
	cfg, paths, err := loadConfigWithOverrides(opts)
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
	repo := espnrepo.New(store.DB())
	svc := espnsvc.New(repo)
	return fn(ctx, svc, loadedConfig{Config: cfg, Paths: paths})
}

func printESPNRosterTable(cmd *cobra.Command, rows []espn.RosterSnapshot) {
	if len(rows) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No ESPN roster snapshot rows found.")
		return
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SYNC_RUN\tPLAYER\tTEAM\tSLOT\tROLE\tPITCHER\tSTATUS")
	for _, row := range rows {
		pitcher := "no"
		if row.IsPitcher {
			pitcher = "yes"
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n", row.SyncRunID, row.PlayerName, firstNonEmpty(row.MLBTeam, "-"), firstNonEmpty(row.RosterSlot, "-"), firstNonEmpty(row.Role, "-"), pitcher, firstNonEmpty(row.StatusTag, "-"))
	}
	w.Flush()
}

func printESPNSyncRuns(cmd *cobra.Command, rows []espn.SyncRun) {
	if len(rows) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No ESPN sync runs found.")
		return
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tTYPE\tLEAGUE\tTEAM\tSEASON\tSTATUS\tWARNINGS\tCOMPLETED_AT")
	for _, row := range rows {
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%d\t%s\t%d\t%s\n", row.ID, row.SyncType, row.LeagueID, row.TeamID, row.Season, row.Status, row.WarningCount, row.CompletedAt.Format(time.RFC3339))
	}
	w.Flush()
}
