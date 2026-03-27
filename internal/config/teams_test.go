package config

import (
	"path/filepath"
	"testing"
)

func TestLoadAppliesCurrentTeamFromRegistry(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.json")
	cfg := Default()
	cfg.AppDir = tmp
	cfg.DBPath = filepath.Join(tmp, "legacy.db")
	cfg.League.LeagueID = "111"
	cfg.League.TeamID = "1"
	cfg.League.Season = 2026
	if err := SaveDefault(cfgPath, cfg); err != nil {
		t.Fatalf("SaveDefault: %v", err)
	}

	teamsPath := filepath.Join(tmp, "teams.json")
	currentPath := filepath.Join(tmp, "current-team")
	teamDB := filepath.Join(tmp, "teams", "alpha", "fb.db")
	reg := TeamRegistry{
		Version: 1,
		Teams: []TeamEntry{
			{
				Name: "alpha",
				League: LeagueConfig{
					Platform: "espn",
					LeagueID: "222",
					TeamID:   "8",
					Season:   2027,
				},
				DBPath: teamDB,
			},
		},
	}
	if err := SaveTeamRegistry(teamsPath, reg); err != nil {
		t.Fatalf("SaveTeamRegistry: %v", err)
	}
	if err := WriteCurrentTeam(currentPath, "alpha"); err != nil {
		t.Fatalf("WriteCurrentTeam: %v", err)
	}

	loaded, _, err := Load(Overrides{ConfigPath: cfgPath, AppDir: tmp})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.ActiveTeam != "alpha" {
		t.Fatalf("ActiveTeam = %q, want alpha", loaded.ActiveTeam)
	}
	if !loaded.UsingTeamRegistry {
		t.Fatal("UsingTeamRegistry = false, want true")
	}
	if loaded.League.LeagueID != "222" || loaded.League.TeamID != "8" || loaded.League.Season != 2027 {
		t.Fatalf("loaded league = %#v, want team league override", loaded.League)
	}
	if loaded.DBPath != teamDB {
		t.Fatalf("DBPath = %q, want %q", loaded.DBPath, teamDB)
	}
}

func TestLoadFallsBackToLegacyWithoutTeamRegistry(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.json")
	cfg := Default()
	cfg.AppDir = tmp
	cfg.DBPath = filepath.Join(tmp, "legacy.db")
	cfg.League.LeagueID = "111"
	cfg.League.TeamID = "1"
	if err := SaveDefault(cfgPath, cfg); err != nil {
		t.Fatalf("SaveDefault: %v", err)
	}

	loaded, _, err := Load(Overrides{ConfigPath: cfgPath, AppDir: tmp})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !loaded.UsingLegacyFallback {
		t.Fatal("UsingLegacyFallback = false, want true")
	}
	if loaded.UsingTeamRegistry {
		t.Fatal("UsingTeamRegistry = true, want false")
	}
	if loaded.League.LeagueID != "111" || loaded.League.TeamID != "1" {
		t.Fatalf("loaded league = %#v, want legacy config league", loaded.League)
	}
}

func TestLoadFailsForUnknownRequestedTeam(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.json")
	cfg := Default()
	cfg.AppDir = tmp
	cfg.DBPath = filepath.Join(tmp, "legacy.db")
	cfg.League.LeagueID = "111"
	cfg.League.TeamID = "1"
	if err := SaveDefault(cfgPath, cfg); err != nil {
		t.Fatalf("SaveDefault: %v", err)
	}

	_, _, err := Load(Overrides{ConfigPath: cfgPath, AppDir: tmp, Team: "missing"})
	if err == nil {
		t.Fatal("expected Load to fail for missing team override")
	}
}
