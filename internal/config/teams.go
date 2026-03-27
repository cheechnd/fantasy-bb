package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type TeamRegistry struct {
	Version int         `json:"version"`
	Teams   []TeamEntry `json:"teams"`
}

type TeamEntry struct {
	Name        string       `json:"name"`
	DisplayName string       `json:"display_name,omitempty"`
	League      LeagueConfig `json:"league"`
	DBPath      string       `json:"db_path"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

func LoadTeamRegistry(path string) (TeamRegistry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return TeamRegistry{Version: 1, Teams: []TeamEntry{}}, nil
		}
		return TeamRegistry{}, fmt.Errorf("read teams registry: %w", err)
	}
	var reg TeamRegistry
	if err := json.Unmarshal(data, &reg); err != nil {
		return TeamRegistry{}, fmt.Errorf("parse teams registry: %w", err)
	}
	if reg.Version == 0 {
		reg.Version = 1
	}
	if reg.Teams == nil {
		reg.Teams = []TeamEntry{}
	}
	return reg, nil
}

func SaveTeamRegistry(path string, reg TeamRegistry) error {
	if reg.Version == 0 {
		reg.Version = 1
	}
	if reg.Teams == nil {
		reg.Teams = []TeamEntry{}
	}
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal teams registry: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create teams dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write teams registry: %w", err)
	}
	return nil
}

func ReadCurrentTeam(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("read current team: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

func WriteCurrentTeam(path, team string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create app dir: %w", err)
	}
	return os.WriteFile(path, []byte(strings.TrimSpace(team)+"\n"), 0o644)
}

func ClearCurrentTeam(path string) error {
	err := os.Remove(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func FindTeam(reg TeamRegistry, name string) *TeamEntry {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return nil
	}
	for i := range reg.Teams {
		if strings.EqualFold(strings.TrimSpace(reg.Teams[i].Name), key) {
			return &reg.Teams[i]
		}
	}
	return nil
}

func applyTeamContext(cfg Config, paths Paths, overrides Overrides) (Config, error) {
	requestedTeam := strings.TrimSpace(firstNonEmpty(overrides.Team, os.Getenv(DefaultTeamEnv)))
	if requestedTeam == "" {
		current, err := ReadCurrentTeam(paths.CurrentTeamPath)
		if err != nil {
			return cfg, err
		}
		requestedTeam = strings.TrimSpace(current)
		if requestedTeam != "" {
			cfg.TeamContextSource = "current-team"
		}
	} else {
		cfg.TeamContextSource = "flag-or-env"
	}

	if requestedTeam == "" {
		cfg.UsingLegacyFallback = true
		cfg.TeamContextSource = "legacy"
		return cfg, nil
	}

	reg, err := LoadTeamRegistry(paths.TeamsPath)
	if err != nil {
		return cfg, err
	}
	team := FindTeam(reg, requestedTeam)
	if team == nil {
		return cfg, fmt.Errorf("team %q not found in %s", requestedTeam, paths.TeamsPath)
	}
	dbPath, err := ExpandPath(team.DBPath)
	if err != nil {
		return cfg, fmt.Errorf("expand team db_path: %w", err)
	}

	cfg.ActiveTeam = team.Name
	cfg.UsingTeamRegistry = true
	cfg.UsingLegacyFallback = false
	if cfg.TeamContextSource == "" {
		cfg.TeamContextSource = "teams"
	}
	cfg.DBPath = dbPath
	cfg.League = team.League
	return cfg, nil
}
