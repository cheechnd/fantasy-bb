package main

import (
	"context"
	"fmt"
	"sort"
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
	cmd := &cobra.Command{Use: "pickups", Short: "Available pitcher projection views (immediate FREEAGENT pool)"}
	cmd.AddGroup(
		&cobra.Group{ID: "generate", Title: "Generate"},
		&cobra.Group{ID: "inspect", Title: "Inspection"},
	)
	recommendCmd := newPickupsPlanCmd(opts)
	recommendCmd.GroupID = "generate"
	lastCmd := newPickupsLastCmd(opts)
	lastCmd.GroupID = "inspect"
	cmd.AddCommand(recommendCmd, lastCmd)
	return cmd
}

func newPickupsPlanCmd(opts *cliOptions) *cobra.Command {
	var fromRaw, toRaw string
	var topN int
	var syncRunID, importRunID, candidateRunID int64
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Generate neutral available-pitcher projection view (WAIVERS excluded)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := withPickupsService(cmd.Context(), opts, func(ctx context.Context, svc *picksvc.Service) (any, error) {
				opts2, err := buildPickupOptions(cmd, fromRaw, toRaw, topN, &syncRunID, &importRunID, &candidateRunID)
				if err != nil {
					return nil, err
				}
				return svc.Recommend(ctx, opts2)
			})
			if err != nil {
				return err
			}
			result := v.(pickups.RecommendResult)
			neutralRows := neutralPickupRows(result)
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{
					"recommendation_run_id": result.RecommendationRunID,
					"sync_run_id":           result.SyncRunID,
					"import_run_id":         result.ImportRunID,
					"candidate_run_id":      result.CandidateRunID,
					"window_start":          result.WindowStart,
					"window_end":            result.WindowEnd,
					"availability_filter":   "FREEAGENT_ONLY",
					"count":                 len(neutralRows),
					"rows":                  neutralRows,
				})
			}
			printPickupRecommendation(cmd, result, neutralRows)
			return nil
		},
	}
	cmd.Flags().StringVar(&fromRaw, "from", "", "Window start date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&toRaw, "to", "", "Window end date (YYYY-MM-DD)")
	cmd.Flags().Int64Var(&syncRunID, "sync-run", 0, "ESPN sync run ID (defaults to latest)")
	cmd.Flags().Int64Var(&importRunID, "import-run", 0, "Forecaster import run ID (defaults to latest)")
	cmd.Flags().Int64Var(&candidateRunID, "candidate-run", 0, "Candidate run ID (defaults to latest)")
	cmd.Flags().IntVar(&topN, "top", 10, "Maximum rows to keep in saved pickup run")
	return cmd
}

func newPickupsLastCmd(opts *cliOptions) *cobra.Command {
	var recommendationID int64
	cmd := &cobra.Command{
		Use:   "last",
		Short: "Show saved available-pitcher projection view (latest by default)",
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
			rows := payload["items"].([]pickups.RecommendationItem)
			neutralRows := neutralPickupRowsFromItems(rows)
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{
					"run":                 payload["run"],
					"availability_filter": "FREEAGENT_ONLY",
					"count":               len(neutralRows),
					"rows":                neutralRows,
				})
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
			printNeutralPickupRowsTable(cmd, neutralRows)
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

func buildPickupOptions(cmd *cobra.Command, fromRaw, toRaw string, topN int, syncRunID, importRunID, candidateRunID *int64) (pickups.RecommendOptions, error) {
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
	return out, nil
}

func printPickupRecommendation(cmd *cobra.Command, r pickups.RecommendResult, rows []neutralPickupRow) {
	fmt.Fprintf(cmd.OutOrStdout(), "Recommendation Run: %d\n", r.RecommendationRunID)
	fmt.Fprintf(cmd.OutOrStdout(), "Window: %s to %s\n\n", r.WindowStart, r.WindowEnd)
	fmt.Fprintln(cmd.OutOrStdout(), "Availability filter: immediate FREEAGENT only (WAIVERS excluded)")
	fmt.Fprintln(cmd.OutOrStdout())
	fmt.Fprintln(cmd.OutOrStdout(), "Available pitchers with projections")
	printNeutralPickupRowsTable(cmd, rows)
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

type neutralPickupRow struct {
	PlayerName          string   `json:"player_name"`
	MLBTeam             string   `json:"mlb_team,omitempty"`
	ProjectedStartCount int      `json:"projected_start_count"`
	Schedule            []string `json:"schedule,omitempty"`
	TotalProjectedFPTS  *float64 `json:"total_projected_fpts,omitempty"`
	BestStartFPTS       *float64 `json:"best_start_fpts,omitempty"`
	ProjectionState     string   `json:"projection_state"`
	AvailabilityState   string   `json:"availability_state"`
	Flags               []string `json:"flags,omitempty"`
	Notes               []string `json:"notes,omitempty"`
}

func neutralPickupRows(r pickups.RecommendResult) []neutralPickupRow {
	seed := make([]pickups.RecommendationItem, 0, len(r.TopCandidates)+len(r.RiskyMonitor)+len(r.Unmatched))
	seed = append(seed, r.TopCandidates...)
	seed = append(seed, r.RiskyMonitor...)
	seed = append(seed, r.Unmatched...)
	return neutralPickupRowsFromItems(seed)
}

func neutralPickupRowsFromItems(items []pickups.RecommendationItem) []neutralPickupRow {
	type keyed struct {
		key string
		row neutralPickupRow
	}
	byKey := map[string]neutralPickupRow{}
	order := []string{}
	for _, item := range items {
		key := strings.ToLower(strings.TrimSpace(item.PlayerName)) + "|" + strings.ToUpper(strings.TrimSpace(item.MLBTeam))
		row := toNeutralPickupRow(item)
		existing, exists := byKey[key]
		if !exists {
			byKey[key] = row
			order = append(order, key)
			continue
		}
		left := valueOrNeg(existing.BestStartFPTS)
		right := valueOrNeg(row.BestStartFPTS)
		if right > left {
			byKey[key] = row
		}
	}
	out := make([]neutralPickupRow, 0, len(order))
	for _, key := range order {
		out = append(out, byKey[key])
	}
	sort.SliceStable(out, func(i, j int) bool {
		l := valueOrNeg(out[i].BestStartFPTS)
		r := valueOrNeg(out[j].BestStartFPTS)
		if l != r {
			return l > r
		}
		lt := valueOrNeg(out[i].TotalProjectedFPTS)
		rt := valueOrNeg(out[j].TotalProjectedFPTS)
		if lt != rt {
			return lt > rt
		}
		return strings.ToLower(out[i].PlayerName) < strings.ToLower(out[j].PlayerName)
	})
	return out
}

func toNeutralPickupRow(item pickups.RecommendationItem) neutralPickupRow {
	state := "matched"
	switch item.ItemType {
	case pickups.ItemTypeUnmatched:
		state = "unmatched"
	case pickups.ItemTypeRiskyMonitor:
		state = "limited_confidence"
	}
	starts := pickupStarts(item)
	best := pickupBestStartFPTS(item)
	return neutralPickupRow{
		PlayerName:          item.PlayerName,
		MLBTeam:             item.MLBTeam,
		ProjectedStartCount: item.ProjectedStartCount,
		Schedule:            starts,
		TotalProjectedFPTS:  item.TotalProjectedFPTS,
		BestStartFPTS:       best,
		ProjectionState:     state,
		AvailabilityState:   "freeagent",
		Flags:               neutralFlags(item.Flags),
		Notes:               neutralNotes(item.Notes),
	}
}

func printNeutralPickupRowsTable(cmd *cobra.Command, rows []neutralPickupRow) {
	if len(rows) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "(none)")
		return
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PLAYER\tTEAM\tSTARTS\tSCHEDULE\tTOTAL_FPTS\tBEST_START_FPTS\tPROJECTION\tAVAILABILITY\tFLAGS\tNOTES")
	for _, row := range rows {
		total := "-"
		if row.TotalProjectedFPTS != nil {
			total = fmt.Sprintf("%.1f", *row.TotalProjectedFPTS)
		}
		best := "-"
		if row.BestStartFPTS != nil {
			best = fmt.Sprintf("%.1f", *row.BestStartFPTS)
		}
		sched := "-"
		if len(row.Schedule) > 0 {
			sched = strings.Join(row.Schedule, ", ")
		}
		fmt.Fprintf(
			w,
			"%s\t%s\t%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			row.PlayerName,
			firstNonEmpty(row.MLBTeam, "-"),
			row.ProjectedStartCount,
			sched,
			total,
			best,
			row.ProjectionState,
			row.AvailabilityState,
			strings.Join(row.Flags, ","),
			strings.Join(row.Notes, "; "),
		)
	}
	w.Flush()
}

func pickupStarts(item pickups.RecommendationItem) []string {
	rawStarts, ok := item.Details["starts"]
	if !ok || rawStarts == nil {
		return nil
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
	return parts
}

func pickupBestStartFPTS(item pickups.RecommendationItem) *float64 {
	if item.Details == nil {
		return nil
	}
	v, ok := item.Details["highest_single_start_fpts"]
	if !ok {
		return nil
	}
	switch t := v.(type) {
	case float64:
		return &t
	case *float64:
		return t
	default:
		return nil
	}
}

func neutralFlags(flags []string) []string {
	out := make([]string, 0, len(flags))
	for _, f := range flags {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if strings.EqualFold(f, "two_start_week") {
			continue
		}
		out = append(out, f)
	}
	return out
}

func neutralNotes(notes []string) []string {
	out := make([]string, 0, len(notes))
	for _, n := range notes {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		out = append(out, n)
	}
	return out
}

func valueOrNeg(v *float64) float64 {
	if v == nil {
		return -999999
	}
	return *v
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
