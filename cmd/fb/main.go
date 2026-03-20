package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fantasy-baseball/internal/app"
	"fantasy-baseball/internal/config"
	"fantasy-baseball/internal/logging"
	"fantasy-baseball/internal/store/sqlite"
	"fantasy-baseball/internal/version"

	"github.com/spf13/cobra"
)

type cliOptions struct {
	AppDir      string
	ConfigPath  string
	DBPath      string
	LogLevel    string
	Environment string

	OutputJSON bool

	DryRun              bool
	RequireConfirmation bool
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	opts := &cliOptions{}

	root := &cobra.Command{
		Use:           "fb",
		Short:         "Fantasy baseball local-first automation CLI",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().StringVar(&opts.AppDir, "app-dir", "", "Override app directory (env: FB_APP_DIR)")
	root.PersistentFlags().StringVar(&opts.ConfigPath, "config", "", "Override config path (env: FB_CONFIG_PATH)")
	root.PersistentFlags().StringVar(&opts.DBPath, "db-path", "", "Override SQLite db path (env: FB_DB_PATH)")
	root.PersistentFlags().StringVar(&opts.LogLevel, "log-level", "", "Override log level (env: FB_LOG_LEVEL)")
	root.PersistentFlags().StringVar(&opts.Environment, "environment", "", "Override runtime environment (env: FB_ENVIRONMENT)")
	root.PersistentFlags().BoolVar(&opts.OutputJSON, "json", false, "Output results as JSON")
	root.PersistentFlags().BoolVar(&opts.DryRun, "dry-run", false, "Preview actions without writing state")
	root.PersistentFlags().BoolVar(&opts.RequireConfirmation, "require-confirmation", true, "Require confirmation for write actions")

	root.AddCommand(
		newVersionCmd(opts),
		newInitCmd(opts),
		newConfigCmd(opts),
		newDBCmd(opts),
		newHealthcheckCmd(opts),
		newForecasterCmd(opts),
		newESPNCmd(opts),
		newPitchersCmd(opts),
	)

	return root
}

func newVersionCmd(opts *cliOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version and build info",
		RunE: func(cmd *cobra.Command, _ []string) error {
			printer := app.Printer{Out: cmd.OutOrStdout(), JSON: opts.OutputJSON}
			info := version.Info()
			if opts.OutputJSON {
				return printer.Println(info)
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "fb %s\ncommit: %s\nbuild_date: %s\n", info["version"], info["commit"], info["build_date"])
			return err
		},
	}
}

func newInitCmd(opts *cliOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize local app directories, config, and database",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			printer := app.Printer{Out: cmd.OutOrStdout(), JSON: opts.OutputJSON}
			execOpts := app.ExecutionOptions{DryRun: opts.DryRun, RequireConfirmation: opts.RequireConfirmation}

			paths, err := config.ResolvePaths(toOverrides(opts))
			if err != nil {
				return err
			}

			logger, err := logging.New(firstNonEmpty(opts.LogLevel, "info"), cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			logger = logging.WithCommand(ctx, logger, "init")
			logger.Debug("resolved init paths", "app_dir", paths.AppDir, "config_path", paths.ConfigPath, "db_path", paths.DBPath)

			steps := []map[string]any{}

			for _, dir := range []string{paths.AppDir, filepath.Join(paths.AppDir, "logs"), filepath.Join(paths.AppDir, "cache")} {
				created, stepErr := ensureDir(dir, execOpts.DryRun)
				if stepErr != nil {
					return stepErr
				}
				steps = append(steps, map[string]any{"action": "ensure_dir", "path": dir, "created": created})
			}

			cfgDefault := config.Default()
			if opts.AppDir != "" {
				cfgDefault.AppDir = opts.AppDir
			}
			if opts.DBPath != "" {
				cfgDefault.DBPath = opts.DBPath
			} else if opts.AppDir != "" {
				cfgDefault.DBPath = filepath.Join(opts.AppDir, "fb.db")
			}

			cfgCreated, err := ensureConfig(paths.ConfigPath, cfgDefault, execOpts.DryRun)
			if err != nil {
				return err
			}
			steps = append(steps, map[string]any{"action": "ensure_config", "path": paths.ConfigPath, "created": cfgCreated})

			dbCreated, err := ensureDB(paths.DBPath, execOpts.DryRun)
			if err != nil {
				return err
			}
			steps = append(steps, map[string]any{"action": "ensure_db", "path": paths.DBPath, "created": dbCreated})

			migrateApplied := 0
			if !execOpts.DryRun {
				s, err := sqlite.Open(paths.DBPath)
				if err != nil {
					return err
				}
				defer s.Close()

				applied, err := s.Migrate(ctx)
				if err != nil {
					return err
				}
				migrateApplied = len(applied)
			}
			steps = append(steps, map[string]any{"action": "migrate", "path": paths.DBPath, "applied": migrateApplied, "dry_run": execOpts.DryRun})

			if opts.OutputJSON {
				return printer.Println(map[string]any{
					"ok":                   true,
					"command":              "init",
					"dry_run":              execOpts.DryRun,
					"require_confirmation": execOpts.RequireConfirmation,
					"paths":                paths,
					"steps":                steps,
					"next_steps":           []string{"Run `fb healthcheck`", "Run `fb db status`"},
				})
			}

			for _, step := range steps {
				action := fmt.Sprint(step["action"])
				path := fmt.Sprint(step["path"])
				switch action {
				case "migrate":
					if execOpts.DryRun {
						if _, err := fmt.Fprintf(cmd.OutOrStdout(), "- migrate: dry-run preview for %s\n", path); err != nil {
							return err
						}
						continue
					}
					if _, err := fmt.Fprintf(cmd.OutOrStdout(), "- migrate: applied %v migration(s)\n", step["applied"]); err != nil {
						return err
					}
					continue
				default:
					state := "skipped"
					if created, ok := step["created"].(bool); ok && created {
						state = "created"
					}
					if _, err := fmt.Fprintf(cmd.OutOrStdout(), "- %s: %s (%s)\n", action, path, state); err != nil {
						return err
					}
				}
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "\nNext steps:\n1. fb healthcheck\n2. fb db status\n"); err != nil {
				return err
			}
			return nil
		},
	}
}

func newConfigCmd(opts *cliOptions) *cobra.Command {
	cfgCmd := &cobra.Command{Use: "config", Short: "Config operations"}
	cfgCmd.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Show effective loaded config",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, _, err := config.Load(toOverrides(opts))
			if err != nil {
				return err
			}
			printer := app.Printer{Out: cmd.OutOrStdout(), JSON: true}
			if opts.OutputJSON {
				return printer.Println(cfg)
			}
			b, err := json.MarshalIndent(cfg, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal effective config: %w", err)
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), string(b))
			return err
		},
	})
	return cfgCmd
}

func newDBCmd(opts *cliOptions) *cobra.Command {
	dbCmd := &cobra.Command{Use: "db", Short: "Database operations"}

	dbCmd.AddCommand(&cobra.Command{
		Use:   "migrate",
		Short: "Apply pending database migrations",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			cfg, _, err := config.Load(toOverrides(opts))
			if err != nil {
				return err
			}
			execOpts := executionOptionsFromConfigAndFlags(cmd, cfg, opts)
			printer := app.Printer{Out: cmd.OutOrStdout(), JSON: opts.OutputJSON}

			if execOpts.DryRun {
				s, err := sqlite.Open(cfg.DBPath)
				if err != nil {
					return err
				}
				defer s.Close()
				status, err := s.MigrationStatus(ctx)
				if err != nil {
					return err
				}
				pending := 0
				for _, st := range status {
					if !st.Applied {
						pending++
					}
				}
				if opts.OutputJSON {
					return printer.Println(map[string]any{"ok": true, "command": "db migrate", "dry_run": true, "pending": pending, "status": status})
				}
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "[DRY-RUN] pending migrations: %d\n", pending)
				return err
			}

			s, err := sqlite.Open(cfg.DBPath)
			if err != nil {
				return err
			}
			defer s.Close()

			applied, err := s.Migrate(ctx)
			if err != nil {
				return err
			}

			if opts.OutputJSON {
				return printer.Println(map[string]any{"ok": true, "command": "db migrate", "applied_count": len(applied), "applied": applied})
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Applied %d migration(s).\n", len(applied))
			return err
		},
	})

	dbCmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show database path and migration status",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			cfg, _, err := config.Load(toOverrides(opts))
			if err != nil {
				return err
			}

			s, err := sqlite.Open(cfg.DBPath)
			if err != nil {
				return err
			}
			defer s.Close()

			status, err := s.MigrationStatus(ctx)
			if err != nil {
				return err
			}

			applied := 0
			for _, st := range status {
				if st.Applied {
					applied++
				}
			}

			printer := app.Printer{Out: cmd.OutOrStdout(), JSON: opts.OutputJSON}
			if opts.OutputJSON {
				return printer.Println(map[string]any{
					"ok":             true,
					"db_path":        cfg.DBPath,
					"applied":        applied,
					"total":          len(status),
					"pending":        len(status) - applied,
					"migration_list": status,
				})
			}

			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Database: %s\n", cfg.DBPath); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Migrations: %d/%d applied (%d pending)\n", applied, len(status), len(status)-applied); err != nil {
				return err
			}
			for _, st := range status {
				state := "pending"
				if st.Applied {
					state = "applied"
				}
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "- %04d_%s: %s\n", st.Version, st.Name, state); err != nil {
					return err
				}
			}
			return nil
		},
	})

	return dbCmd
}

func newHealthcheckCmd(opts *cliOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "healthcheck",
		Short: "Validate config, db connectivity, and migration inspection",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 8*time.Second)
			defer cancel()

			checks := make([]app.CheckResult, 0, 5)

			var cfg config.Config
			var loadErr error
			checks = append(checks, app.Check(ctx, "config.load", func(context.Context) error {
				cfg, _, loadErr = config.Load(toOverrides(opts))
				return loadErr
			}))

			checks = append(checks, app.Check(ctx, "config.validate", func(context.Context) error {
				if loadErr != nil {
					return loadErr
				}
				return cfg.Validate()
			}))

			checks = append(checks, app.Check(ctx, "db.path", func(context.Context) error {
				if loadErr != nil {
					return loadErr
				}
				parent := filepath.Dir(cfg.DBPath)
				info, err := os.Stat(parent)
				if err != nil {
					return fmt.Errorf("db parent directory %q: %w", parent, err)
				}
				if !info.IsDir() {
					return fmt.Errorf("db parent path is not a directory: %s", parent)
				}
				return nil
			}))

			var s *sqlite.Store
			checks = append(checks, app.Check(ctx, "db.connectivity", func(c context.Context) error {
				if loadErr != nil {
					return loadErr
				}
				var err error
				s, err = sqlite.Open(cfg.DBPath)
				if err != nil {
					return err
				}
				return s.Ping(c)
			}))
			if s != nil {
				defer s.Close()
			}

			checks = append(checks, app.Check(ctx, "migrations.inspect", func(c context.Context) error {
				if s == nil {
					return errors.New("store unavailable")
				}
				_, err := s.MigrationStatus(c)
				return err
			}))

			ok := true
			for _, c := range checks {
				if c.Status != "ok" {
					ok = false
					break
				}
			}

			printer := app.Printer{Out: cmd.OutOrStdout(), JSON: opts.OutputJSON}
			if opts.OutputJSON {
				return printer.Println(map[string]any{"ok": ok, "command": "healthcheck", "checks": checks})
			}

			for _, c := range checks {
				line := fmt.Sprintf("[OK] %s", c.Name)
				if c.Status != "ok" {
					line = fmt.Sprintf("[FAIL] %s: %s", c.Name, c.Error)
				}
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), line); err != nil {
					return err
				}
			}
			if ok {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "Healthcheck: PASS")
				return err
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "Healthcheck: FAIL")
			return err
		},
	}
}

func toOverrides(opts *cliOptions) config.Overrides {
	return config.Overrides{
		AppDir:      opts.AppDir,
		ConfigPath:  opts.ConfigPath,
		DBPath:      opts.DBPath,
		LogLevel:    opts.LogLevel,
		Environment: opts.Environment,
	}
}

func executionOptionsFromConfigAndFlags(cmd *cobra.Command, cfg config.Config, opts *cliOptions) app.ExecutionOptions {
	execOpts := app.ExecutionOptions{
		DryRun:              cfg.Execution.DryRun,
		RequireConfirmation: cfg.Execution.RequireConfirmation,
	}
	if cmd.Flags().Changed("dry-run") {
		execOpts.DryRun = opts.DryRun
	}
	if cmd.Flags().Changed("require-confirmation") {
		execOpts.RequireConfirmation = opts.RequireConfirmation
	}
	return execOpts
}

func ensureDir(path string, dryRun bool) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("stat path %s: %w", path, err)
	}
	if dryRun {
		return true, nil
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return false, fmt.Errorf("create directory %s: %w", path, err)
	}
	return true, nil
}

func ensureConfig(path string, cfg config.Config, dryRun bool) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("stat config file %s: %w", path, err)
	}
	if dryRun {
		return true, nil
	}
	if err := config.SaveDefault(path, cfg); err != nil {
		return false, err
	}
	return true, nil
}

func ensureDB(path string, dryRun bool) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("stat db file %s: %w", path, err)
	}
	if dryRun {
		return true, nil
	}
	return sqlite.EnsureDatabaseFile(path)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
