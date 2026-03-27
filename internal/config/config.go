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
	DefaultTeamEnv       = "FB_TEAM"
)

type Config struct {
	AppDir       string             `json:"app_dir"`
	DBPath       string             `json:"db_path"`
	LogLevel     string             `json:"log_level"`
	Environment  string             `json:"environment"`
	League       LeagueConfig       `json:"league"`
	Auth         AuthConfig         `json:"auth"`
	ESPN         ESPNConfig         `json:"espn"`
	Execution    ExecutionConfig    `json:"execution"`
	Planning     PlanningConfig     `json:"planning"`
	Pickups      PickupsConfig      `json:"pickups"`
	Transactions TransactionsConfig `json:"transactions"`
	Lineup       LineupConfig       `json:"lineup"`
	Monitoring   MonitoringConfig   `json:"monitoring"`
	Features     FeaturesConfig     `json:"features"`

	ActiveTeam          string `json:"-"`
	TeamContextSource   string `json:"-"`
	UsingTeamRegistry   bool   `json:"-"`
	UsingLegacyFallback bool   `json:"-"`
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
	DryRun              bool                     `json:"dry_run"`
	RequireConfirmation bool                     `json:"require_confirmation"`
	Preflight           ExecutionPreflightConfig `json:"preflight"`
	Real                ExecutionRealConfig      `json:"real"`
	Hardening           ExecutionHardeningConfig `json:"hardening"`
}

type ExecutionPreflightConfig struct {
	DefaultLimit                 int  `json:"default_limit"`
	MaxLimit                     int  `json:"max_limit"`
	CandidateRefreshLimit        int  `json:"candidate_refresh_limit"`
	StaleHoursThreshold          int  `json:"stale_hours_threshold"`
	RequireLiveRosterCheck       bool `json:"require_live_roster_check"`
	RequireLiveAvailabilityCheck bool `json:"require_live_availability_check"`
}

type ExecutionRealConfig struct {
	Enabled                    bool `json:"enabled"`
	RequireConfirmation        bool `json:"require_confirmation"`
	AllowRepeatExecution       bool `json:"allow_repeat_execution"`
	VerificationTimeoutSeconds int  `json:"verification_timeout_seconds"`
}

type ExecutionHardeningConfig struct {
	BlockOnPriorSuccess                  bool `json:"block_on_prior_success"`
	BlockOnAmbiguousPriorAttempt         bool `json:"block_on_ambiguous_prior_attempt"`
	VerificationRecheckLimit             int  `json:"verification_recheck_limit"`
	VerificationPendingWindowSeconds     int  `json:"verification_pending_window_seconds"`
	TreatMixedReconciliationInconclusive bool `json:"treat_mixed_reconciliation_as_inconclusive"`
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
	TBDPenalty               float64 `json:"tbd_penalty"`
	MissingProjectionPenalty float64 `json:"missing_projection_penalty"`
	AmbiguousMatchPenalty    float64 `json:"ambiguous_match_penalty"`
}

type PickupsConfig struct {
	Pitchers PickupPitchersConfig `json:"pitchers"`
}

type PickupPitchersConfig struct {
	DefaultCandidateLimit    int     `json:"default_candidate_limit"`
	MaxCandidateLimit        int     `json:"max_candidate_limit"`
	MinStreamerTotalFPTS     float64 `json:"min_streamer_total_fpts"`
	StrongUpgradeDeltaFPTS   float64 `json:"strong_upgrade_delta_fpts"`
	MarginalUpgradeDeltaFPTS float64 `json:"marginal_upgrade_delta_fpts"`
	RiskyMonitorMinTotalFPTS float64 `json:"risky_monitor_min_total_fpts"`
}

type TransactionsConfig struct {
	Pitchers TransactionPitchersConfig `json:"pitchers"`
	AdHoc    TransactionAdHocConfig    `json:"ad_hoc"`
}

type TransactionPitchersConfig struct {
	TopMoveLimit                   int     `json:"top_move_limit"`
	MaxPairings                    int     `json:"max_pairings"`
	StrongMoveDeltaFPTS            float64 `json:"strong_move_delta_fpts"`
	MarginalMoveDeltaFPTS          float64 `json:"marginal_move_delta_fpts"`
	RiskyMoveMinDeltaFPTS          float64 `json:"risky_move_min_delta_fpts"`
	UncertaintyPenaltyTBD          float64 `json:"uncertainty_penalty_tbd"`
	UncertaintyPenaltyMissingProj  float64 `json:"uncertainty_penalty_missing_projection"`
	UncertaintyPenaltyAmbiguous    float64 `json:"uncertainty_penalty_ambiguous_match"`
	AllowCompareAgainstLikelyStart bool    `json:"allow_compare_against_likely_start"`
	WontDropMinPercentOwned        float64 `json:"wont_drop_min_percent_owned"`
}

type TransactionAdHocConfig struct {
	Enabled                    bool `json:"enabled"`
	MaxRecentRequests          int  `json:"max_recent_requests"`
	RequirePitchersOnly        bool `json:"require_pitchers_only"`
	ReuseBoundedCandidateLimit int  `json:"reuse_bounded_candidate_limit"`
}

type LineupConfig struct {
	Pitchers LineupPitchersConfig `json:"pitchers"`
}

type LineupPitchersConfig struct {
	Enabled                     bool `json:"enabled"`
	AutoGenerateFromPitcherPlan bool `json:"auto_generate_from_pitcher_plan"`
	AllowMonitorActions         bool `json:"allow_monitor_actions"`
	RequireConfirmation         bool `json:"require_confirmation"`
	BlockOnAmbiguousSlotMapping bool `json:"block_on_ambiguous_slot_mapping"`
}

type MonitoringConfig struct {
	PlansStaleHours               int  `json:"plans_stale_hours"`
	LineupStaleHours              int  `json:"lineup_stale_hours"`
	PickupsStaleHours             int  `json:"pickups_stale_hours"`
	CandidatePoolStaleHours       int  `json:"candidate_pool_stale_hours"`
	ApprovalStaleHours            int  `json:"approval_stale_hours"`
	ExecutionFollowupHours        int  `json:"execution_followup_hours"`
	RequireLiveRecheckForApproved bool `json:"require_live_recheck_for_approved_items"`
}

type FeaturesConfig struct {
	EnableWriteActions      bool `json:"enable_write_actions"`
	EnableBrowserAutomation bool `json:"enable_browser_automation"`
	EnableLLMExplanations   bool `json:"enable_llm_explanations"`
}

type Paths struct {
	AppDir          string `json:"app_dir"`
	ConfigPath      string `json:"config_path"`
	DBPath          string `json:"db_path"`
	TeamsPath       string `json:"teams_path"`
	CurrentTeamPath string `json:"current_team_path"`
}

type Overrides struct {
	AppDir      string
	ConfigPath  string
	DBPath      string
	LogLevel    string
	Environment string
	Team        string
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
			Preflight: ExecutionPreflightConfig{
				DefaultLimit:                 100,
				MaxLimit:                     100,
				CandidateRefreshLimit:        100,
				StaleHoursThreshold:          12,
				RequireLiveRosterCheck:       true,
				RequireLiveAvailabilityCheck: true,
			},
			Real: ExecutionRealConfig{
				Enabled:                    false,
				RequireConfirmation:        true,
				AllowRepeatExecution:       false,
				VerificationTimeoutSeconds: 20,
			},
			Hardening: ExecutionHardeningConfig{
				BlockOnPriorSuccess:                  true,
				BlockOnAmbiguousPriorAttempt:         true,
				VerificationRecheckLimit:             3,
				VerificationPendingWindowSeconds:     30,
				TreatMixedReconciliationInconclusive: true,
			},
		},
		Planning: PlanningConfig{
			Pitchers: PitcherPlanningConfig{
				AutoStartMinTotalFPTS:    20.0,
				LikelyStartMinTotalFPTS:  12.0,
				MonitorMinTotalFPTS:      6.0,
				TBDPenalty:               3.0,
				MissingProjectionPenalty: 4.0,
				AmbiguousMatchPenalty:    5.0,
			},
		},
		Pickups: PickupsConfig{
			Pitchers: PickupPitchersConfig{
				DefaultCandidateLimit:    100,
				MaxCandidateLimit:        100,
				MinStreamerTotalFPTS:     8.0,
				StrongUpgradeDeltaFPTS:   5.0,
				MarginalUpgradeDeltaFPTS: 1.5,
				RiskyMonitorMinTotalFPTS: 6.0,
			},
		},
		Transactions: TransactionsConfig{
			Pitchers: TransactionPitchersConfig{
				TopMoveLimit:                   10,
				MaxPairings:                    25,
				StrongMoveDeltaFPTS:            5.0,
				MarginalMoveDeltaFPTS:          1.5,
				RiskyMoveMinDeltaFPTS:          0.5,
				UncertaintyPenaltyTBD:          2.0,
				UncertaintyPenaltyMissingProj:  3.0,
				UncertaintyPenaltyAmbiguous:    4.0,
				AllowCompareAgainstLikelyStart: false,
				WontDropMinPercentOwned:        85.0,
			},
			AdHoc: TransactionAdHocConfig{
				Enabled:                    true,
				MaxRecentRequests:          25,
				RequirePitchersOnly:        true,
				ReuseBoundedCandidateLimit: 100,
			},
		},
		Lineup: LineupConfig{
			Pitchers: LineupPitchersConfig{
				Enabled:                     true,
				AutoGenerateFromPitcherPlan: true,
				AllowMonitorActions:         false,
				RequireConfirmation:         true,
				BlockOnAmbiguousSlotMapping: true,
			},
		},
		Monitoring: MonitoringConfig{
			PlansStaleHours:               12,
			LineupStaleHours:              4,
			PickupsStaleHours:             6,
			CandidatePoolStaleHours:       4,
			ApprovalStaleHours:            2,
			ExecutionFollowupHours:        1,
			RequireLiveRecheckForApproved: true,
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
		AppDir:          appDirExpanded,
		ConfigPath:      configPathExpanded,
		DBPath:          dbPathExpanded,
		TeamsPath:       filepath.Join(appDirExpanded, "teams.json"),
		CurrentTeamPath: filepath.Join(appDirExpanded, "current-team"),
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

	cfg, err = applyTeamContext(cfg, paths, overrides)
	if err != nil {
		return Config{}, paths, err
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
	if c.Transactions.Pitchers.TopMoveLimit <= 0 {
		problems = append(problems, "transactions.pitchers.top_move_limit must be > 0")
	}
	if c.Transactions.Pitchers.MaxPairings <= 0 {
		problems = append(problems, "transactions.pitchers.max_pairings must be > 0")
	}
	if c.Transactions.Pitchers.StrongMoveDeltaFPTS < 0 {
		problems = append(problems, "transactions.pitchers.strong_move_delta_fpts must be >= 0")
	}
	if c.Transactions.Pitchers.MarginalMoveDeltaFPTS < 0 {
		problems = append(problems, "transactions.pitchers.marginal_move_delta_fpts must be >= 0")
	}
	if c.Transactions.Pitchers.RiskyMoveMinDeltaFPTS < 0 {
		problems = append(problems, "transactions.pitchers.risky_move_min_delta_fpts must be >= 0")
	}
	if c.Transactions.Pitchers.StrongMoveDeltaFPTS < c.Transactions.Pitchers.MarginalMoveDeltaFPTS {
		problems = append(problems, "transactions.pitchers.strong_move_delta_fpts must be >= marginal_move_delta_fpts")
	}
	if c.Transactions.Pitchers.MarginalMoveDeltaFPTS < c.Transactions.Pitchers.RiskyMoveMinDeltaFPTS {
		problems = append(problems, "transactions.pitchers.marginal_move_delta_fpts must be >= risky_move_min_delta_fpts")
	}
	if c.Transactions.Pitchers.UncertaintyPenaltyTBD < 0 {
		problems = append(problems, "transactions.pitchers.uncertainty_penalty_tbd must be >= 0")
	}
	if c.Transactions.Pitchers.UncertaintyPenaltyMissingProj < 0 {
		problems = append(problems, "transactions.pitchers.uncertainty_penalty_missing_projection must be >= 0")
	}
	if c.Transactions.Pitchers.UncertaintyPenaltyAmbiguous < 0 {
		problems = append(problems, "transactions.pitchers.uncertainty_penalty_ambiguous_match must be >= 0")
	}
	if c.Transactions.Pitchers.WontDropMinPercentOwned < 0 || c.Transactions.Pitchers.WontDropMinPercentOwned > 100 {
		problems = append(problems, "transactions.pitchers.wont_drop_min_percent_owned must be between 0 and 100")
	}
	if c.Transactions.AdHoc.MaxRecentRequests <= 0 || c.Transactions.AdHoc.MaxRecentRequests > 500 {
		problems = append(problems, "transactions.ad_hoc.max_recent_requests must be between 1 and 500")
	}
	if c.Transactions.AdHoc.ReuseBoundedCandidateLimit <= 0 || c.Transactions.AdHoc.ReuseBoundedCandidateLimit > 200 {
		problems = append(problems, "transactions.ad_hoc.reuse_bounded_candidate_limit must be between 1 and 200")
	}
	if c.Execution.Preflight.DefaultLimit <= 0 {
		problems = append(problems, "execution.preflight.default_limit must be > 0")
	}
	if c.Execution.Preflight.MaxLimit <= 0 {
		problems = append(problems, "execution.preflight.max_limit must be > 0")
	}
	if c.Execution.Preflight.DefaultLimit > c.Execution.Preflight.MaxLimit {
		problems = append(problems, "execution.preflight.default_limit must be <= max_limit")
	}
	if c.Execution.Preflight.CandidateRefreshLimit <= 0 {
		problems = append(problems, "execution.preflight.candidate_refresh_limit must be > 0")
	}
	if c.Execution.Preflight.StaleHoursThreshold < 0 {
		problems = append(problems, "execution.preflight.stale_hours_threshold must be >= 0")
	}
	if c.Execution.Real.VerificationTimeoutSeconds <= 0 || c.Execution.Real.VerificationTimeoutSeconds > 120 {
		problems = append(problems, "execution.real.verification_timeout_seconds must be between 1 and 120")
	}
	if c.Execution.Hardening.VerificationRecheckLimit <= 0 || c.Execution.Hardening.VerificationRecheckLimit > 20 {
		problems = append(problems, "execution.hardening.verification_recheck_limit must be between 1 and 20")
	}
	if c.Execution.Hardening.VerificationPendingWindowSeconds < 0 || c.Execution.Hardening.VerificationPendingWindowSeconds > 600 {
		problems = append(problems, "execution.hardening.verification_pending_window_seconds must be between 0 and 600")
	}
	if c.Lineup.Pitchers.RequireConfirmation && !c.Execution.Real.RequireConfirmation {
		problems = append(problems, "lineup.pitchers.require_confirmation requires execution.real.require_confirmation=true")
	}
	if c.Monitoring.PlansStaleHours < 0 || c.Monitoring.PlansStaleHours > 168 {
		problems = append(problems, "monitoring.plans_stale_hours must be between 0 and 168")
	}
	if c.Monitoring.LineupStaleHours < 0 || c.Monitoring.LineupStaleHours > 168 {
		problems = append(problems, "monitoring.lineup_stale_hours must be between 0 and 168")
	}
	if c.Monitoring.PickupsStaleHours < 0 || c.Monitoring.PickupsStaleHours > 168 {
		problems = append(problems, "monitoring.pickups_stale_hours must be between 0 and 168")
	}
	if c.Monitoring.CandidatePoolStaleHours < 0 || c.Monitoring.CandidatePoolStaleHours > 168 {
		problems = append(problems, "monitoring.candidate_pool_stale_hours must be between 0 and 168")
	}
	if c.Monitoring.ApprovalStaleHours < 0 || c.Monitoring.ApprovalStaleHours > 168 {
		problems = append(problems, "monitoring.approval_stale_hours must be between 0 and 168")
	}
	if c.Monitoring.ExecutionFollowupHours < 0 || c.Monitoring.ExecutionFollowupHours > 168 {
		problems = append(problems, "monitoring.execution_followup_hours must be between 0 and 168")
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
