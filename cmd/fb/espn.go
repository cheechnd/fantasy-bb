package main

import (
	"context"
	"encoding/json"
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
	cmd.AddGroup(
		&cobra.Group{ID: "checks", Title: "Checks"},
		&cobra.Group{ID: "ingest", Title: "Ingestion"},
		&cobra.Group{ID: "inspect", Title: "Inspection"},
	)
	validateCmd := newESPNValidateCmd(opts)
	validateCmd.GroupID = "checks"
	syncCmd := newESPNSyncCmd(opts)
	syncCmd.GroupID = "ingest"
	freeAgentsCmd := newESPNFreeAgentsCmd(opts)
	freeAgentsCmd.GroupID = "ingest"
	showCmd := newESPNShowCmd(opts)
	showCmd.GroupID = "inspect"
	statusCmd := newESPNSourceStatusCmd(opts)
	statusCmd.GroupID = "inspect"
	warningsCmd := newESPNWarningsCmd(opts)
	warningsCmd.GroupID = "inspect"
	cmd.AddCommand(validateCmd, syncCmd, freeAgentsCmd, showCmd, statusCmd, warningsCmd)
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
	cmd.AddGroup(&cobra.Group{ID: "sync", Title: "Sync Commands"})
	rosterCmd := newESPNSyncRosterCmd(opts)
	rosterCmd.GroupID = "sync"
	cmd.AddCommand(rosterCmd)
	return cmd
}

func newESPNFreeAgentsCmd(opts *cliOptions) *cobra.Command {
	cmd := &cobra.Command{Use: "free-agents", Short: "Bounded read-only ESPN free-agent ingestion"}
	cmd.AddGroup(&cobra.Group{ID: "ingest", Title: "Candidate Ingestion"})
	pitchersCmd := newESPNFreeAgentsPitchersCmd(opts)
	pitchersCmd.GroupID = "ingest"
	cmd.AddCommand(pitchersCmd)
	return cmd
}

func newESPNFreeAgentsPitchersCmd(opts *cliOptions) *cobra.Command {
	var limit int
	var search string
	var team string
	cmd := &cobra.Command{
		Use:   "pitchers",
		Short: "Fetch a bounded free-agent pitcher candidate pool",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := withESPNService(cmd.Context(), opts, func(_ context.Context, svc *espnsvc.Service, cfg loadedConfig) (any, error) {
				return svc.SyncFreeAgentPitchers(cmd.Context(), cfg.Config, espnsvc.FreeAgentOptions{
					Limit:  limit,
					Search: search,
					Team:   team,
				})
			})
			if err != nil {
				return err
			}
			summary := v.(espn.CandidateSummary)
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"ok": true, "summary": summary})
			}
			if summary.CandidateRunID != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Candidate run: %d\n", *summary.CandidateRunID)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Synced at: %s\n", summary.SyncedAt)
			fmt.Fprintf(cmd.OutOrStdout(), "Query type: %s\n", summary.QueryType)
			if summary.QueryText != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Search text: %s\n", summary.QueryText)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Effective limit: %d\n", summary.EffectiveLimit)
			fmt.Fprintf(cmd.OutOrStdout(), "Candidates fetched: %d\n", summary.CandidateCount)
			fmt.Fprintf(cmd.OutOrStdout(), "Warnings: %d\n", summary.WarningCount)
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "Candidate limit (bounded by config max)")
	cmd.Flags().StringVar(&search, "search", "", "Optional case-insensitive name filter")
	cmd.Flags().StringVar(&team, "team", "", "Optional MLB team filter (e.g. NYY)")
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
	cmd.AddGroup(
		&cobra.Group{ID: "core", Title: "Core Snapshots"},
		&cobra.Group{ID: "candidates", Title: "Candidate Snapshots"},
	)
	rosterCmd := newESPNShowRosterCmd(opts)
	rosterCmd.GroupID = "core"
	leagueCmd := newESPNShowLeagueCmd(opts)
	leagueCmd.GroupID = "core"
	freeAgentsCmd := newESPNShowFreeAgentsCmd(opts)
	freeAgentsCmd.GroupID = "candidates"
	cmd.AddCommand(rosterCmd, leagueCmd, freeAgentsCmd)
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
				enhanced := make([]map[string]any, 0, len(rows))
				for _, row := range rows {
					item := map[string]any{
						"id":              row.ID,
						"sync_run_id":     row.SyncRunID,
						"espn_player_id":  row.ESPNPlayerID,
						"player_name":     row.PlayerName,
						"normalized_name": row.NormalizedName,
						"mlb_team":        row.MLBTeam,
						"roster_slot":     row.RosterSlot,
						"is_pitcher":      row.IsPitcher,
						"role":            row.Role,
						"status_tag":      row.StatusTag,
						"raw_player_json": row.RawPlayerJSON,
						"created_at":      row.CreatedAt,
					}
					if pct, ok := percentOwnedFromRaw(row.RawPlayerJSON); ok {
						item["owned_percent"] = pct
					}
					enhanced = append(enhanced, item)
				}
				return writeJSON(cmd, map[string]any{"ok": true, "count": len(rows), "rows": enhanced})
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

func newESPNShowFreeAgentsCmd(opts *cliOptions) *cobra.Command {
	var candidateRunID int64
	var limit int
	cmd := &cobra.Command{
		Use:   "free-agents",
		Short: "Show latest or selected ESPN free-agent pitcher candidates",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := withESPNService(cmd.Context(), opts, func(_ context.Context, svc *espnsvc.Service, _ loadedConfig) (any, error) {
				var run *espn.CandidateRun
				if cmd.Flags().Changed("candidate-run") {
					r, err := svc.CandidateRunByID(cmd.Context(), candidateRunID)
					if err != nil {
						return nil, err
					}
					run = r
				} else {
					r, err := svc.LatestCandidateRun(cmd.Context())
					if err != nil {
						return nil, err
					}
					run = r
				}
				if run == nil {
					return map[string]any{"run": nil, "rows": []espn.FreeAgentCandidate{}}, nil
				}
				runID := run.ID
				rows, err := svc.Candidates(cmd.Context(), &runID, limit)
				if err != nil {
					return nil, err
				}
				return map[string]any{"run": run, "rows": rows}, nil
			})
			if err != nil {
				return err
			}
			payload := v.(map[string]any)
			if opts.OutputJSON {
				return writeJSON(cmd, payload)
			}
			run, _ := payload["run"].(*espn.CandidateRun)
			if run == nil {
				fmt.Fprintln(cmd.OutOrStdout(), "No ESPN candidate runs found. Run `fb espn free-agents pitchers --limit N` first.")
				return nil
			}
			rows := payload["rows"].([]espn.FreeAgentCandidate)
			fmt.Fprintf(cmd.OutOrStdout(), "Candidate run: %d\n", run.ID)
			fmt.Fprintf(cmd.OutOrStdout(), "Started: %s\n", run.StartedAt.Format(time.RFC3339))
			fmt.Fprintf(cmd.OutOrStdout(), "Status: %s\n", run.Status)
			fmt.Fprintf(cmd.OutOrStdout(), "Candidates: %d\n", run.CandidateCount)
			fmt.Fprintf(cmd.OutOrStdout(), "Warnings: %d\n\n", run.WarningCount)
			printESPNFreeAgentsTable(cmd, rows)
			return nil
		},
	}
	cmd.Flags().Int64Var(&candidateRunID, "candidate-run", 0, "Specific ESPN candidate run ID (defaults to latest)")
	cmd.Flags().IntVar(&limit, "limit", 100, "Maximum rows to show")
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
	fmt.Fprintln(w, "SYNC_RUN\tPLAYER\tTEAM\tSLOT\tROLE\tPITCHER\tSTATUS\tOWNED%")
	for _, row := range rows {
		pitcher := "no"
		if row.IsPitcher {
			pitcher = "yes"
		}
		owned := "-"
		if pct, ok := percentOwnedFromRaw(row.RawPlayerJSON); ok {
			owned = fmt.Sprintf("%.1f", pct)
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", row.SyncRunID, row.PlayerName, firstNonEmpty(row.MLBTeam, "-"), firstNonEmpty(row.RosterSlot, "-"), firstNonEmpty(row.Role, "-"), pitcher, firstNonEmpty(row.StatusTag, "-"), owned)
	}
	w.Flush()
}

func percentOwnedFromRaw(raw string) (float64, bool) {
	if raw == "" || raw == "{}" {
		return 0, false
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		return 0, false
	}
	ownership, ok := body["ownership"].(map[string]any)
	if !ok {
		return 0, false
	}
	v, ok := ownership["percentOwned"]
	if !ok || v == nil {
		return 0, false
	}
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	default:
		return 0, false
	}
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

func printESPNFreeAgentsTable(cmd *cobra.Command, rows []espn.FreeAgentCandidate) {
	if len(rows) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No free-agent candidates found for this run.")
		return
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "RUN\tPLAYER\tTEAM\tROLE\tPITCHER\tSTATUS")
	for _, row := range rows {
		pitcher := "no"
		if row.IsPitcher {
			pitcher = "yes"
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\n", row.CandidateRunID, row.PlayerName, firstNonEmpty(row.MLBTeam, "-"), firstNonEmpty(row.Role, "-"), pitcher, firstNonEmpty(row.StatusTag, "-"))
	}
	w.Flush()
}
