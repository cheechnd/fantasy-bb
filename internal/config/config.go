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
	ESPN        ESPNConfig      `json:"espn"`
	Execution   ExecutionConfig `json:"execution"`
	Planning    PlanningConfig  `json:"planning"`
	Pickups     PickupsConfig   `json:"pickups"`
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

type ESPNConfig struct {
	BaseURL        string `json:"base_url"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

type ESPNCredentials struct {
	ESPNS2 string
	SWID   string
}

type PlanningConfig struct {
	Pitchers PitcherPlanningConfig `json:"pitchers"`
}

type PitcherPlanningConfig struct {
	AutoStartMinTotalFPTS    float64 `json:"auto_start_min_total_fpts"`
	LikelyStartMinTotalFPTS  float64 `json:"likely_start_min_total_fpts"`
	MonitorMinTotalFPTS      float64 `json:"monitor_min_total_fpts"`
	TwoStartAutoStartBonus   float64 `json:"two_start_auto_start_bonus"`
	TBDPenalty               float64 `json:"tbd_penalty"`
	MissingProjectionPenalty float64 `json:"missing_projection_penalty"`
	AmbiguousMatchPenalty    float64 `json:"ambiguous_match_penalty"`
}

type PickupsConfig struct {
	Pitchers PickupPitchersConfig `json:"pitchers"`
}

type PickupPitchersConfig struct {
	DefaultCandidateLimit   int     `json:"default_candidate_limit"`
	MaxCandidateLimit       int     `json:"max_candidate_limit"`
	MinStreamerTotalFPTS    float64 `json:"min_streamer_total_fpts"`
	StrongUpgradeDeltaFPTS  float64 `json:"strong_upgrade_delta_fpts"`
	MarginalUpgradeDeltaFPTS float64 `json:"marginal_upgrade_delta_fpts"`
	RiskyMonitorMinTotalFPTS float64 `json:"risky_monitor_min_total_fpts"`
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
		ESPN: ESPNConfig{
			BaseURL:        "https://lm-api-reads.fantasy.espn.com",
			TimeoutSeconds: 20,
		},
		Execution: ExecutionConfig{
			DryRun:              true,
			RequireConfirmation: true,
		},
		Planning: PlanningConfig{
			Pitchers: PitcherPlanningConfig{
				AutoStartMinTotalFPTS:    20.0,
				LikelyStartMinTotalFPTS:  12.0,
				MonitorMinTotalFPTS:      6.0,
				TwoStartAutoStartBonus:   2.0,
				TBDPenalty:               3.0,
				MissingProjectionPenalty: 4.0,
				AmbiguousMatchPenalty:    5.0,
			},
		},
		Pickups: PickupsConfig{
			Pitchers: PickupPitchersConfig{
				DefaultCandidateLimit:    25,
				MaxCandidateLimit:        50,
				MinStreamerTotalFPTS:     8.0,
				StrongUpgradeDeltaFPTS:   5.0,
				MarginalUpgradeDeltaFPTS: 1.5,
				RiskyMonitorMinTotalFPTS: 6.0,
			},
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
	if strings.TrimSpace(c.ESPN.BaseURL) == "" {
		problems = append(problems, "espn.base_url is required")
	}
	if c.ESPN.TimeoutSeconds <= 0 || c.ESPN.TimeoutSeconds > 120 {
		problems = append(problems, "espn.timeout_seconds must be between 1 and 120")
	}
	if c.Planning.Pitchers.AutoStartMinTotalFPTS < 0 {
		problems = append(problems, "planning.pitchers.auto_start_min_total_fpts must be >= 0")
	}
	if c.Planning.Pitchers.LikelyStartMinTotalFPTS < 0 {
		problems = append(problems, "planning.pitchers.likely_start_min_total_fpts must be >= 0")
	}
	if c.Planning.Pitchers.MonitorMinTotalFPTS < 0 {
		problems = append(problems, "planning.pitchers.monitor_min_total_fpts must be >= 0")
	}
	if c.Planning.Pitchers.AutoStartMinTotalFPTS < c.Planning.Pitchers.LikelyStartMinTotalFPTS {
		problems = append(problems, "planning.pitchers.auto_start_min_total_fpts must be >= likely_start_min_total_fpts")
	}
	if c.Planning.Pitchers.LikelyStartMinTotalFPTS < c.Planning.Pitchers.MonitorMinTotalFPTS {
		problems = append(problems, "planning.pitchers.likely_start_min_total_fpts must be >= monitor_min_total_fpts")
	}
	if c.Planning.Pitchers.TwoStartAutoStartBonus < 0 {
		problems = append(problems, "planning.pitchers.two_start_auto_start_bonus must be >= 0")
	}
	if c.Planning.Pitchers.TBDPenalty < 0 {
		problems = append(problems, "planning.pitchers.tbd_penalty must be >= 0")
	}
	if c.Planning.Pitchers.MissingProjectionPenalty < 0 {
		problems = append(problems, "planning.pitchers.missing_projection_penalty must be >= 0")
	}
	if c.Planning.Pitchers.AmbiguousMatchPenalty < 0 {
		problems = append(problems, "planning.pitchers.ambiguous_match_penalty must be >= 0")
	}
	if c.Pickups.Pitchers.DefaultCandidateLimit <= 0 {
		problems = append(problems, "pickups.pitchers.default_candidate_limit must be > 0")
	}
	if c.Pickups.Pitchers.MaxCandidateLimit <= 0 {
		problems = append(problems, "pickups.pitchers.max_candidate_limit must be > 0")
	}
	if c.Pickups.Pitchers.DefaultCandidateLimit > c.Pickups.Pitchers.MaxCandidateLimit {
		problems = append(problems, "pickups.pitchers.default_candidate_limit must be <= max_candidate_limit")
	}
	if c.Pickups.Pitchers.MinStreamerTotalFPTS < 0 {
		problems = append(problems, "pickups.pitchers.min_streamer_total_fpts must be >= 0")
	}
	if c.Pickups.Pitchers.StrongUpgradeDeltaFPTS < 0 {
		problems = append(problems, "pickups.pitchers.strong_upgrade_delta_fpts must be >= 0")
	}
	if c.Pickups.Pitchers.MarginalUpgradeDeltaFPTS < 0 {
		problems = append(problems, "pickups.pitchers.marginal_upgrade_delta_fpts must be >= 0")
	}
	if c.Pickups.Pitchers.RiskyMonitorMinTotalFPTS < 0 {
		problems = append(problems, "pickups.pitchers.risky_monitor_min_total_fpts must be >= 0")
	}

	if len(problems) > 0 {
		return fmt.Errorf("config validation failed: %s", strings.Join(problems, "; "))
	}
	return nil
}

func (c Config) ValidateESPNUsage() error {
	var problems []string
	if strings.ToLower(strings.TrimSpace(c.League.Platform)) != "espn" {
		problems = append(problems, "league.platform must be espn for ESPN commands")
	}
	if strings.TrimSpace(c.League.LeagueID) == "" {
		problems = append(problems, "league.league_id is required for ESPN commands")
	}
	if strings.TrimSpace(c.League.TeamID) == "" {
		problems = append(problems, "league.team_id is required for ESPN commands")
	}
	if c.League.Season < 2000 || c.League.Season > 2100 {
		problems = append(problems, "league.season must be between 2000 and 2100 for ESPN commands")
	}
	if strings.TrimSpace(c.Auth.ESPNS2Env) == "" {
		problems = append(problems, "auth.espn_s2_env is required for ESPN commands")
	}
	if strings.TrimSpace(c.Auth.SWIDEnv) == "" {
		problems = append(problems, "auth.swid_env is required for ESPN commands")
	}
	if len(problems) > 0 {
		return fmt.Errorf("espn configuration invalid: %s", strings.Join(problems, "; "))
	}
	return nil
}

func (c Config) LoadESPNCredentialsFromEnv() (ESPNCredentials, error) {
	if err := c.ValidateESPNUsage(); err != nil {
		return ESPNCredentials{}, err
	}
	espnS2 := strings.TrimSpace(os.Getenv(c.Auth.ESPNS2Env))
	swid := strings.TrimSpace(os.Getenv(c.Auth.SWIDEnv))
	var problems []string
	if espnS2 == "" {
		problems = append(problems, fmt.Sprintf("environment variable %q is not set", c.Auth.ESPNS2Env))
	}
	if swid == "" {
		problems = append(problems, fmt.Sprintf("environment variable %q is not set", c.Auth.SWIDEnv))
	}
	if len(problems) > 0 {
		return ESPNCredentials{}, fmt.Errorf("espn credentials missing: %s", strings.Join(problems, "; "))
	}
	return ESPNCredentials{ESPNS2: espnS2, SWID: swid}, nil
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
