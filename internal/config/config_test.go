package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpandPath(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	got, err := ExpandPath("~/.fantasy-baseball/fb.db")
	if err != nil {
		t.Fatalf("ExpandPath returned error: %v", err)
	}
	want := filepath.Join(tmpHome, ".fantasy-baseball", "fb.db")
	if got != want {
		t.Fatalf("ExpandPath = %q, want %q", got, want)
	}
}

func TestLoadAndValidateConfig(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.json")
	cfg := Default()
	cfg.AppDir = filepath.Join(tmp, "app")
	cfg.DBPath = filepath.Join(cfg.AppDir, "fb.db")
	cfg.LogLevel = "debug"

	if err := SaveDefault(configPath, cfg); err != nil {
		t.Fatalf("SaveDefault: %v", err)
	}

	loaded, paths, err := Load(Overrides{ConfigPath: configPath, AppDir: cfg.AppDir})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.DBPath != cfg.DBPath {
		t.Fatalf("loaded db path = %q, want %q", loaded.DBPath, cfg.DBPath)
	}
	if paths.ConfigPath != configPath {
		t.Fatalf("resolved config path = %q, want %q", paths.ConfigPath, configPath)
	}
}

func TestLoadMissingConfig(t *testing.T) {
	_, _, err := Load(Overrides{ConfigPath: filepath.Join(t.TempDir(), "missing.json")})
	if !errors.Is(err, ErrConfigNotFound) {
		t.Fatalf("Load missing config error = %v, want ErrConfigNotFound", err)
	}
}

func TestValidateFriendlyErrors(t *testing.T) {
	cfg := Default()
	cfg.LogLevel = "verbose"
	cfg.Environment = "qa"
	cfg.League.Platform = ""
	cfg.Auth.ESPNS2Env = ""
	cfg.ESPN.TimeoutSeconds = 0
	cfg.Execution.Preflight.DefaultLimit = 0
	cfg.Monitoring.PlansStaleHours = -1
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error")
	}
	msg := err.Error()
	for _, part := range []string{"log_level", "environment", "league.platform", "auth.espn_s2_env", "espn.timeout_seconds", "execution.preflight.default_limit", "monitoring.plans_stale_hours"} {
		if !strings.Contains(msg, part) {
			t.Fatalf("validation error missing %q: %s", part, msg)
		}
	}
}

func TestDefaultMonitoringConfig(t *testing.T) {
	cfg := Default()
	if cfg.Monitoring.PlansStaleHours <= 0 {
		t.Fatalf("expected positive plans stale hours, got %d", cfg.Monitoring.PlansStaleHours)
	}
	if cfg.Monitoring.CandidatePoolStaleHours <= 0 {
		t.Fatalf("expected positive candidate pool stale hours, got %d", cfg.Monitoring.CandidatePoolStaleHours)
	}
	if !cfg.Monitoring.RequireLiveRecheckForApproved {
		t.Fatal("expected monitoring.require_live_recheck_for_approved_items default true")
	}
}

func TestResolvePathsWithEnvOverride(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv(DefaultAppDirEnv, filepath.Join(tmp, "env-app"))
	paths, err := ResolvePaths(Overrides{})
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	if paths.AppDir != filepath.Join(tmp, "env-app") {
		t.Fatalf("AppDir = %q", paths.AppDir)
	}
	if !strings.HasSuffix(paths.ConfigPath, filepath.Join("env-app", "config.json")) {
		t.Fatalf("ConfigPath = %q", paths.ConfigPath)
	}
	if !strings.HasSuffix(paths.DBPath, filepath.Join("env-app", "fb.db")) {
		t.Fatalf("DBPath = %q", paths.DBPath)
	}
}

func TestSaveDefaultCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	if err := SaveDefault(path, Default()); err != nil {
		t.Fatalf("SaveDefault: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config file not created: %v", err)
	}
}

func TestValidateESPNUsage(t *testing.T) {
	cfg := Default()
	cfg.League.LeagueID = ""
	cfg.League.TeamID = ""
	err := cfg.ValidateESPNUsage()
	if err == nil {
		t.Fatal("expected ValidateESPNUsage to fail")
	}
	msg := err.Error()
	for _, part := range []string{"league.league_id", "league.team_id"} {
		if !strings.Contains(msg, part) {
			t.Fatalf("ValidateESPNUsage error missing %q: %s", part, msg)
		}
	}
}

func TestLoadESPNCredentialsFromEnv(t *testing.T) {
	cfg := Default()
	cfg.League.LeagueID = "123"
	cfg.League.TeamID = "4"
	t.Setenv(cfg.Auth.ESPNS2Env, "cookie-a")
	t.Setenv(cfg.Auth.SWIDEnv, "{cookie-b}")

	creds, err := cfg.LoadESPNCredentialsFromEnv()
	if err != nil {
		t.Fatalf("LoadESPNCredentialsFromEnv: %v", err)
	}
	if creds.ESPNS2 != "cookie-a" || creds.SWID != "{cookie-b}" {
		t.Fatalf("unexpected creds: %#v", creds)
	}
}
