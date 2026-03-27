package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"fantasy-baseball/internal/config"

	"github.com/spf13/cobra"
)

func newTeamCmd(opts *cliOptions) *cobra.Command {
	cmd := &cobra.Command{Use: "team", Short: "Manage multi-team context"}
	cmd.AddCommand(
		newTeamAddCmd(opts),
		newTeamAliasCmd(opts),
		newTeamImportLegacyCmd(opts),
		newTeamListCmd(opts),
		newTeamUseCmd(opts),
		newTeamCurrentCmd(opts),
		newTeamEnvCmd(opts),
		newTeamShowCmd(opts),
		newTeamRemoveCmd(opts),
		newTeamRenameCmd(opts),
	)
	return cmd
}

func newTeamAddCmd(opts *cliOptions) *cobra.Command {
	var displayName, alias, leagueID, teamID, dbPath string
	var season int
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add a team context",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			if name == "" {
				return fmt.Errorf("team name is required")
			}
			if strings.TrimSpace(leagueID) == "" || strings.TrimSpace(teamID) == "" || season <= 0 {
				return fmt.Errorf("--league-id, --team-id, and --season are required")
			}
			paths, err := loadTeamPaths(opts)
			if err != nil {
				return err
			}
			reg, err := config.LoadTeamRegistry(paths.TeamsPath)
			if err != nil {
				return err
			}
			if config.TeamNameOrAliasExists(reg, name, "") {
				return fmt.Errorf("team name %q already exists (name or alias)", name)
			}
			alias = strings.TrimSpace(alias)
			if alias != "" && config.TeamNameOrAliasExists(reg, alias, "") {
				return fmt.Errorf("team alias %q already exists (name or alias)", alias)
			}

			finalDBPath := strings.TrimSpace(dbPath)
			if finalDBPath == "" {
				finalDBPath = filepath.Join(paths.AppDir, "teams", sanitizeTeamName(name), "fb.db")
			}

			now := time.Now().UTC()
			reg.Teams = append(reg.Teams, config.TeamEntry{
				Name:        name,
				Alias:       alias,
				DisplayName: strings.TrimSpace(displayName),
				League: config.LeagueConfig{
					Platform: "espn",
					LeagueID: strings.TrimSpace(leagueID),
					TeamID:   strings.TrimSpace(teamID),
					Season:   season,
				},
				DBPath:    finalDBPath,
				CreatedAt: now,
				UpdatedAt: now,
			})
			sort.SliceStable(reg.Teams, func(i, j int) bool {
				return strings.ToLower(reg.Teams[i].Name) < strings.ToLower(reg.Teams[j].Name)
			})
			if err := config.SaveTeamRegistry(paths.TeamsPath, reg); err != nil {
				return err
			}

			current, err := config.ReadCurrentTeam(paths.CurrentTeamPath)
			if err != nil {
				return err
			}
			if strings.TrimSpace(current) == "" {
				if err := config.WriteCurrentTeam(paths.CurrentTeamPath, name); err != nil {
					return err
				}
			}

			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{
					"ok":      true,
					"name":    name,
					"alias":   alias,
					"db_path": finalDBPath,
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Added team: %s\n", name)
			if alias != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Alias: %s\n", alias)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "League: %s Team: %s Season: %d\n", leagueID, teamID, season)
			fmt.Fprintf(cmd.OutOrStdout(), "DB: %s\n", finalDBPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&displayName, "display-name", "", "Optional display name")
	cmd.Flags().StringVar(&alias, "alias", "", "Optional team alias (unique across names/aliases)")
	cmd.Flags().StringVar(&leagueID, "league-id", "", "ESPN league ID")
	cmd.Flags().StringVar(&teamID, "team-id", "", "ESPN team ID")
	cmd.Flags().IntVar(&season, "season", 0, "Season year")
	cmd.Flags().StringVar(&dbPath, "db-path", "", "Optional db path override for this team")
	return cmd
}

func newTeamAliasCmd(opts *cliOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "alias <name-or-alias> <alias>",
		Short: "Set or update a team's alias",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := strings.TrimSpace(args[0])
			alias := strings.TrimSpace(args[1])
			if alias == "" {
				return fmt.Errorf("alias is required")
			}
			paths, err := loadTeamPaths(opts)
			if err != nil {
				return err
			}
			reg, err := config.LoadTeamRegistry(paths.TeamsPath)
			if err != nil {
				return err
			}
			team := config.FindTeam(reg, ref)
			if team == nil {
				return fmt.Errorf("team %q not found", ref)
			}
			if config.TeamNameOrAliasExists(reg, alias, team.Name) {
				return fmt.Errorf("team alias %q already exists (name or alias)", alias)
			}

			team.Alias = alias
			team.UpdatedAt = time.Now().UTC()
			if err := config.SaveTeamRegistry(paths.TeamsPath, reg); err != nil {
				return err
			}
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"ok": true, "name": team.Name, "alias": alias})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Team alias set: %s -> %s\n", team.Name, alias)
			return nil
		},
	}
}

func newTeamImportLegacyCmd(opts *cliOptions) *cobra.Command {
	var alias string
	var setCurrent bool
	cmd := &cobra.Command{
		Use:   "import-legacy <name>",
		Short: "Import current config league/db as a team entry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			if name == "" {
				return fmt.Errorf("team name is required")
			}

			ov := toOverrides(opts)
			ov.Team = ""
			cfg, paths, err := config.Load(ov)
			if err != nil {
				return err
			}
			reg, err := config.LoadTeamRegistry(paths.TeamsPath)
			if err != nil {
				return err
			}
			if config.TeamNameOrAliasExists(reg, name, "") {
				return fmt.Errorf("team name %q already exists (name or alias)", name)
			}
			alias = strings.TrimSpace(alias)
			if alias != "" && config.TeamNameOrAliasExists(reg, alias, "") {
				return fmt.Errorf("team alias %q already exists (name or alias)", alias)
			}

			now := time.Now().UTC()
			reg.Teams = append(reg.Teams, config.TeamEntry{
				Name:      name,
				Alias:     alias,
				League:    cfg.League,
				DBPath:    cfg.DBPath,
				CreatedAt: now,
				UpdatedAt: now,
			})
			sort.SliceStable(reg.Teams, func(i, j int) bool {
				return strings.ToLower(reg.Teams[i].Name) < strings.ToLower(reg.Teams[j].Name)
			})
			if err := config.SaveTeamRegistry(paths.TeamsPath, reg); err != nil {
				return err
			}
			if setCurrent {
				if err := config.WriteCurrentTeam(paths.CurrentTeamPath, name); err != nil {
					return err
				}
			}

			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{
					"ok":          true,
					"name":        name,
					"alias":       alias,
					"set_current": setCurrent,
					"league_id":   cfg.League.LeagueID,
					"team_id":     cfg.League.TeamID,
					"season":      cfg.League.Season,
					"db_path":     cfg.DBPath,
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Imported legacy team: %s\n", name)
			if alias != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Alias: %s\n", alias)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "League: %s Team: %s Season: %d\n", cfg.League.LeagueID, cfg.League.TeamID, cfg.League.Season)
			fmt.Fprintf(cmd.OutOrStdout(), "DB: %s\n", cfg.DBPath)
			if setCurrent {
				fmt.Fprintf(cmd.OutOrStdout(), "Current team: %s\n", name)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&alias, "alias", "", "Optional team alias (unique across names/aliases)")
	cmd.Flags().BoolVar(&setCurrent, "set-current", true, "Set imported team as current")
	return cmd
}

func newTeamListCmd(opts *cliOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured teams",
		RunE: func(cmd *cobra.Command, _ []string) error {
			paths, err := loadTeamPaths(opts)
			if err != nil {
				return err
			}
			reg, err := config.LoadTeamRegistry(paths.TeamsPath)
			if err != nil {
				return err
			}
			current, err := config.ReadCurrentTeam(paths.CurrentTeamPath)
			if err != nil {
				return err
			}
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"current": current, "count": len(reg.Teams), "teams": reg.Teams})
			}
			if len(reg.Teams) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No teams configured.")
				return nil
			}
			for _, t := range reg.Teams {
				marker := " "
				if strings.EqualFold(strings.TrimSpace(current), strings.TrimSpace(t.Name)) {
					marker = "*"
				}
				if strings.TrimSpace(t.Alias) != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "%s %s [%s] (%s/%s %d)\n", marker, t.Name, t.Alias, t.League.LeagueID, t.League.TeamID, t.League.Season)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "%s %s (%s/%s %d)\n", marker, t.Name, t.League.LeagueID, t.League.TeamID, t.League.Season)
				}
			}
			return nil
		},
	}
}

func newTeamEnvCmd(opts *cliOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "env [name-or-alias]",
		Short: "Print shell exports for team context",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			paths, err := loadTeamPaths(opts)
			if err != nil {
				return err
			}
			reg, err := config.LoadTeamRegistry(paths.TeamsPath)
			if err != nil {
				return err
			}
			ref := ""
			if len(args) > 0 {
				ref = strings.TrimSpace(args[0])
			}
			if ref == "" {
				current, err := config.ReadCurrentTeam(paths.CurrentTeamPath)
				if err != nil {
					return err
				}
				ref = strings.TrimSpace(current)
			}
			if ref == "" {
				return fmt.Errorf("no team provided and no current team set")
			}
			team := config.FindTeam(reg, ref)
			if team == nil {
				return fmt.Errorf("team %q not found", ref)
			}

			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{
					"team": team.Name,
					"exports": map[string]string{
						"FB_TEAM": team.Name,
					},
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "export FB_TEAM=%q\n", team.Name)
			return nil
		},
	}
}

func newTeamUseCmd(opts *cliOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "use <name-or-alias>",
		Short: "Set current team context",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := strings.TrimSpace(args[0])
			paths, err := loadTeamPaths(opts)
			if err != nil {
				return err
			}
			reg, err := config.LoadTeamRegistry(paths.TeamsPath)
			if err != nil {
				return err
			}
			team := config.FindTeam(reg, ref)
			if team == nil {
				return fmt.Errorf("team %q not found", ref)
			}
			if err := config.WriteCurrentTeam(paths.CurrentTeamPath, team.Name); err != nil {
				return err
			}
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"ok": true, "current": team.Name, "alias": team.Alias})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Current team: %s\n", team.Name)
			return nil
		},
	}
}

func newTeamCurrentCmd(opts *cliOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "current",
		Short: "Show current team context",
		RunE: func(cmd *cobra.Command, _ []string) error {
			paths, err := loadTeamPaths(opts)
			if err != nil {
				return err
			}
			reg, err := config.LoadTeamRegistry(paths.TeamsPath)
			if err != nil {
				return err
			}
			current, err := config.ReadCurrentTeam(paths.CurrentTeamPath)
			if err != nil {
				return err
			}
			trimmed := strings.TrimSpace(current)
			team := config.FindTeam(reg, trimmed)

			if opts.OutputJSON {
				if team == nil {
					return writeJSON(cmd, map[string]any{"current": trimmed})
				}
				return writeJSON(cmd, map[string]any{"current": team.Name, "alias": team.Alias})
			}
			if trimmed == "" {
				fmt.Fprintln(cmd.OutOrStdout(), "No current team set.")
				return nil
			}
			if team == nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Current team: %s\n", trimmed)
				return nil
			}
			if strings.TrimSpace(team.Alias) != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Current team: %s [%s]\n", team.Name, team.Alias)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Current team: %s\n", team.Name)
			return nil
		},
	}
}

func newTeamShowCmd(opts *cliOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "show <name-or-alias>",
		Short: "Show one team definition",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := strings.TrimSpace(args[0])
			paths, err := loadTeamPaths(opts)
			if err != nil {
				return err
			}
			reg, err := config.LoadTeamRegistry(paths.TeamsPath)
			if err != nil {
				return err
			}
			team := config.FindTeam(reg, ref)
			if team == nil {
				return fmt.Errorf("team %q not found", ref)
			}
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"team": team})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Name: %s\n", team.Name)
			fmt.Fprintf(cmd.OutOrStdout(), "Alias: %s\n", firstNonEmpty(team.Alias, "-"))
			fmt.Fprintf(cmd.OutOrStdout(), "Display: %s\n", firstNonEmpty(team.DisplayName, "-"))
			fmt.Fprintf(cmd.OutOrStdout(), "League: %s Team: %s Season: %d\n", team.League.LeagueID, team.League.TeamID, team.League.Season)
			fmt.Fprintf(cmd.OutOrStdout(), "DB: %s\n", team.DBPath)
			return nil
		},
	}
}

func newTeamRemoveCmd(opts *cliOptions) *cobra.Command {
	var deleteDB bool
	cmd := &cobra.Command{
		Use:   "remove <name-or-alias>",
		Short: "Remove a team context",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := strings.TrimSpace(args[0])
			paths, err := loadTeamPaths(opts)
			if err != nil {
				return err
			}
			reg, err := config.LoadTeamRegistry(paths.TeamsPath)
			if err != nil {
				return err
			}
			target := config.FindTeam(reg, ref)
			if target == nil {
				return fmt.Errorf("team %q not found", ref)
			}

			out := make([]config.TeamEntry, 0, len(reg.Teams))
			var removed *config.TeamEntry
			for i := range reg.Teams {
				t := reg.Teams[i]
				if strings.EqualFold(t.Name, target.Name) {
					copy := t
					removed = &copy
					continue
				}
				out = append(out, t)
			}
			if removed == nil {
				return fmt.Errorf("team %q not found", ref)
			}
			reg.Teams = out
			if err := config.SaveTeamRegistry(paths.TeamsPath, reg); err != nil {
				return err
			}

			current, _ := config.ReadCurrentTeam(paths.CurrentTeamPath)
			if strings.EqualFold(strings.TrimSpace(current), strings.TrimSpace(removed.Name)) {
				if len(reg.Teams) > 0 {
					_ = config.WriteCurrentTeam(paths.CurrentTeamPath, reg.Teams[0].Name)
				} else {
					_ = config.ClearCurrentTeam(paths.CurrentTeamPath)
				}
			}

			if deleteDB {
				dbPath, err := config.ExpandPath(removed.DBPath)
				if err == nil && dbPath != "" {
					_ = removeFileIfExists(dbPath)
				}
			}
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"ok": true, "removed": removed.Name})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed team: %s\n", removed.Name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&deleteDB, "delete-db", false, "Also delete team db file")
	return cmd
}

func newTeamRenameCmd(opts *cliOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "rename <old-name-or-alias> <new-name>",
		Short: "Rename a team context",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			oldRef := strings.TrimSpace(args[0])
			newName := strings.TrimSpace(args[1])
			if oldRef == "" || newName == "" {
				return fmt.Errorf("both old and new names are required")
			}
			paths, err := loadTeamPaths(opts)
			if err != nil {
				return err
			}
			reg, err := config.LoadTeamRegistry(paths.TeamsPath)
			if err != nil {
				return err
			}
			oldTeam := config.FindTeam(reg, oldRef)
			if oldTeam == nil {
				return fmt.Errorf("team %q not found", oldRef)
			}
			if config.TeamNameOrAliasExists(reg, newName, oldTeam.Name) {
				return fmt.Errorf("team %q already exists", newName)
			}
			var found bool
			for i := range reg.Teams {
				if strings.EqualFold(reg.Teams[i].Name, oldTeam.Name) {
					reg.Teams[i].Name = newName
					reg.Teams[i].UpdatedAt = time.Now().UTC()
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("team %q not found", oldRef)
			}
			if err := config.SaveTeamRegistry(paths.TeamsPath, reg); err != nil {
				return err
			}
			current, _ := config.ReadCurrentTeam(paths.CurrentTeamPath)
			if strings.EqualFold(strings.TrimSpace(current), oldTeam.Name) {
				_ = config.WriteCurrentTeam(paths.CurrentTeamPath, newName)
			}
			if opts.OutputJSON {
				return writeJSON(cmd, map[string]any{"ok": true, "old": oldTeam.Name, "new": newName})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Renamed team: %s -> %s\n", oldTeam.Name, newName)
			return nil
		},
	}
}

func sanitizeTeamName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return "team"
	}
	n = strings.ReplaceAll(n, " ", "-")
	out := make([]rune, 0, len(n))
	for _, r := range n {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return "team"
	}
	return string(out)
}

func removeFileIfExists(path string) error {
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func loadTeamPaths(opts *cliOptions) (config.Paths, error) {
	ov := toOverrides(opts)
	ov.Team = ""
	return config.ResolvePaths(ov)
}
