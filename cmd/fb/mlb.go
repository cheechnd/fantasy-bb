package main

import (
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"fantasy-baseball/internal/mlb"

	"github.com/spf13/cobra"
)

func newMLBCmd(opts *cliOptions) *cobra.Command {
	cmd := &cobra.Command{Use: "mlb", Short: "MLB read-only schedule and matchup data"}
	cmd.AddGroup(&cobra.Group{ID: "show", Title: "Show"})
	scheduleCmd := newMLBScheduleCmd(opts)
	scheduleCmd.GroupID = "show"
	cmd.AddCommand(scheduleCmd)
	return cmd
}

func newMLBScheduleCmd(opts *cliOptions) *cobra.Command {
	var dateRaw string
	var fromRaw string
	var toRaw string
	var timezoneRaw string
	cmd := &cobra.Command{
		Use:   "schedule",
		Short: "Show MLB schedule, scores, probable pitching matchups, and start times",
		RunE: func(cmd *cobra.Command, _ []string) error {
			tz := time.Local
			if strings.TrimSpace(timezoneRaw) != "" {
				loc, err := time.LoadLocation(strings.TrimSpace(timezoneRaw))
				if err != nil {
					return fmt.Errorf("invalid --timezone %q: %w", timezoneRaw, err)
				}
				tz = loc
			}

			var fromDate, toDate time.Time
			switch {
			case strings.TrimSpace(fromRaw) != "" || strings.TrimSpace(toRaw) != "":
				if strings.TrimSpace(fromRaw) == "" || strings.TrimSpace(toRaw) == "" {
					return fmt.Errorf("provide both --from and --to")
				}
				fromPtr, err := parseDateFlag(strings.TrimSpace(fromRaw))
				if err != nil {
					return err
				}
				toPtr, err := parseDateFlag(strings.TrimSpace(toRaw))
				if err != nil {
					return err
				}
				fromDate, toDate = *fromPtr, *toPtr
			default:
				target := strings.TrimSpace(dateRaw)
				if target == "" {
					now := time.Now().In(tz)
					target = now.Format("2006-01-02")
				}
				dtPtr, err := parseDateFlag(target)
				if err != nil {
					return err
				}
				fromDate, toDate = *dtPtr, *dtPtr
			}

			svc := mlb.New(20*time.Second, "")
			res, err := svc.Schedule(cmd.Context(), fromDate, toDate, tz)
			if err != nil {
				return err
			}

			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"ok": true, "schedule": res})
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Date window: %s to %s\n", res.FromDate, res.ToDate)
			fmt.Fprintf(cmd.OutOrStdout(), "Timezone: %s\n", res.Timezone)
			fmt.Fprintf(cmd.OutOrStdout(), "Games: %d\n\n", res.GameCount)
			printMLBScheduleTable(cmd, res.Games, tz)
			return nil
		},
	}
	cmd.Flags().StringVar(&dateRaw, "date", "", "Single date (YYYY-MM-DD). Defaults to today in --timezone.")
	cmd.Flags().StringVar(&fromRaw, "from", "", "Start date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&toRaw, "to", "", "End date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&timezoneRaw, "timezone", "", "IANA timezone for display (e.g. America/New_York)")
	return cmd
}

func printMLBScheduleTable(cmd *cobra.Command, games []mlb.ScheduleGame, tz *time.Location) {
	if len(games) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No MLB games found for this date window.")
		return
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "DATE\tTIME\tAWAY\tHOME\tPITCHING_MATCHUP\tSCORE\tSTATUS")
	for _, g := range games {
		date := g.GameDate.In(tz).Format("2006-01-02")
		timeLabel := g.GameDate.In(tz).Format("3:04 PM")
		if g.StartTimeTBD {
			timeLabel = "TBD"
		}
		awaySP := firstNonEmpty(g.AwayProbableSP, "TBD")
		homeSP := firstNonEmpty(g.HomeProbableSP, "TBD")
		pitching := fmt.Sprintf("%s vs %s", awaySP, homeSP)
		score := "-"
		if g.AwayScore != nil && g.HomeScore != nil {
			score = fmt.Sprintf("%d-%d", *g.AwayScore, *g.HomeScore)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", date, timeLabel, g.AwayTeam, g.HomeTeam, pitching, score, firstNonEmpty(g.Status, "-"))
	}
	w.Flush()
}
