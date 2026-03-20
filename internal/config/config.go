package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	DefaultAppDirEnv     = "FB_APP_DIR"
	DefaultConfigPathEnv = "FB_CONFIG_PATH"
	DefaultDBPathEnv     = "FB_DB_PATH"
	DefaultLogLevelEnv   = "FB_LOG_LEVEL"
	DefaultEnvEnv        = "FB_ENVIRONMENT"
)

type Config struct {
	AppDir      string          `json:"app_dir"`
	DBPath      string          `json:"db_path"`
	LogLevel    string          `json:"log_level"`
	Environment string          `json:"environment"`
	League      LeagueConfig    `json:"league"`
	Auth        AuthConfig      `json:"auth"`
	Execution   ExecutionConfig `json:"execution"`
	Features    FeaturesConfig  `json:"features"`
}

type LeagueConfig struct {
	Platform string `json:"platform"`
	LeagueID string `json:"league_id"`
	TeamID   string `json:"team_id"`
	Season   int    `json:"season"`
}

type AuthConfig struct {
	ESPNS2Env string `json:"espn_s2_env"`
	SWIDEnv   string `json:"swid_env"`
}

type ExecutionConfig struct {
	DryRun              bool `json:"dry_run"`
	RequireConfirmation bool `json:"require_confirmation"`
}

type FeaturesConfig struct {
	EnableWriteActions      bool `json:"enable_write_actions"`
	EnableBrowserAutomation bool `json:"enable_browser_automation"`
	EnableLLMExplanations   bool `json:"enable_llm_explanations"`
}

type Paths struct {
	AppDir     string `json:"app_dir"`
	ConfigPath string `json:"config_path"`
	DBPath     string `json:"db_path"`
}

type Overrides struct {
	AppDir      string
	ConfigPath  string
	DBPath      string
	LogLevel    string
	Environment string
}

var ErrConfigNotFound = errors.New("config file not found")

func Default() Config {
	return Config{
		AppDir:      "~/.fantasy-baseball",
		DBPath:      "~/.fantasy-baseball/fb.db",
		LogLevel:    "info",
		Environment: "development",
		League: LeagueConfig{
			Platform: "espn",
			Season:   2026,
		},
		Auth: AuthConfig{
			ESPNS2Env: "ESPN_S2",
			SWIDEnv:   "ESPN_SWID",
		},
		Execution: ExecutionConfig{
			DryRun:              true,
			RequireConfirmation: true,
		},
		Features: FeaturesConfig{
			EnableWriteActions:      false,
			EnableBrowserAutomation: false,
			EnableLLMExplanations:   false,
		},
	}
}

func ResolvePaths(overrides Overrides) (Paths, error) {
	appDir := firstNonEmpty(overrides.AppDir, os.Getenv(DefaultAppDirEnv), Default().AppDir)
	appDirExpanded, err := ExpandPath(appDir)
	if err != nil {
		return Paths{}, fmt.Errorf("expand app dir: %w", err)
	}

	configPath := firstNonEmpty(overrides.ConfigPath, os.Getenv(DefaultConfigPathEnv), filepath.Join(appDir, "config.json"))
	configPathExpanded, err := ExpandPath(configPath)
	if err != nil {
		return Paths{}, fmt.Errorf("expand config path: %w", err)
	}

	dbPath := firstNonEmpty(overrides.DBPath, os.Getenv(DefaultDBPathEnv), filepath.Join(appDir, "fb.db"))
	dbPathExpanded, err := ExpandPath(dbPath)
	if err != nil {
		return Paths{}, fmt.Errorf("expand db path: %w", err)
	}

	return Paths{
		AppDir:     appDirExpanded,
		ConfigPath: configPathExpanded,
		DBPath:     dbPathExpanded,
	}, nil
}

func Load(overrides Overrides) (Config, Paths, error) {
	paths, err := ResolvePaths(overrides)
	if err != nil {
		return Config{}, Paths{}, err
	}

	data, err := os.ReadFile(paths.ConfigPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, paths, ErrConfigNotFound
		}
		return Config{}, paths, fmt.Errorf("read config: %w", err)
	}

	cfg := Default()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, paths, fmt.Errorf("parse config json: %w", err)
	}

	cfg.AppDir = firstNonEmpty(overrides.AppDir, os.Getenv(DefaultAppDirEnv), cfg.AppDir)
	cfg.DBPath = firstNonEmpty(overrides.DBPath, os.Getenv(DefaultDBPathEnv), cfg.DBPath)
	cfg.LogLevel = firstNonEmpty(overrides.LogLevel, os.Getenv(DefaultLogLevelEnv), cfg.LogLevel)
	cfg.Environment = firstNonEmpty(overrides.Environment, os.Getenv(DefaultEnvEnv), cfg.Environment)

	cfg.AppDir, err = ExpandPath(cfg.AppDir)
	if err != nil {
		return Config{}, paths, fmt.Errorf("expand app_dir: %w", err)
	}
	cfg.DBPath, err = ExpandPath(cfg.DBPath)
	if err != nil {
		return Config{}, paths, fmt.Errorf("expand db_path: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, paths, err
	}

	return cfg, paths, nil
}

func SaveDefault(path string, cfg Config) error {
	cfgCopy := cfg
	if cfgCopy.AppDir == "" {
		cfgCopy.AppDir = Default().AppDir
	}
	if cfgCopy.DBPath == "" {
		cfgCopy.DBPath = Default().DBPath
	}

	data, err := json.MarshalIndent(cfgCopy, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal default config: %w", err)
	}
	data = append(data, '\n')

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}
	return nil
}

func (c Config) Validate() error {
	var problems []string

	if c.AppDir == "" {
		problems = append(problems, "app_dir is required")
	}
	if c.DBPath == "" {
		problems = append(problems, "db_path is required")
	}
	switch strings.ToLower(c.LogLevel) {
	case "debug", "info", "warn", "error":
	default:
		problems = append(problems, "log_level must be one of: debug, info, warn, error")
	}
	switch strings.ToLower(c.Environment) {
	case "development", "test", "staging", "production":
	default:
		problems = append(problems, "environment must be one of: development, test, staging, production")
	}
	if strings.TrimSpace(c.League.Platform) == "" {
		problems = append(problems, "league.platform is required")
	}
	if c.League.Season < 2000 || c.League.Season > 2100 {
		problems = append(problems, "league.season must be between 2000 and 2100")
	}
	if strings.TrimSpace(c.Auth.ESPNS2Env) == "" {
		problems = append(problems, "auth.espn_s2_env is required")
	}
	if strings.TrimSpace(c.Auth.SWIDEnv) == "" {
		problems = append(problems, "auth.swid_env is required")
	}

	if len(problems) > 0 {
		return fmt.Errorf("config validation failed: %s", strings.Join(problems, "; "))
	}
	return nil
}

func ExpandPath(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("determine home dir: %w", err)
		}
		if path == "~" {
			return home, nil
		}
		path = filepath.Join(home, path[2:])
	}
	return filepath.Clean(path), nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
