package input

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type RosterPlayer struct {
	PlayerName string `json:"player_name"`
	MLBTeam    string `json:"mlb_team,omitempty"`
	Role       string `json:"role,omitempty"`
	Status     string `json:"status,omitempty"`
	Locked     bool   `json:"locked,omitempty"`
	MustHold   bool   `json:"must_hold,omitempty"`
	Notes      string `json:"notes,omitempty"`
}

type FreeAgentPlayer struct {
	PlayerName   string   `json:"player_name"`
	MLBTeam      string   `json:"mlb_team,omitempty"`
	Role         string   `json:"role,omitempty"`
	Watch        bool     `json:"watch,omitempty"`
	OwnershipPct *float64 `json:"ownership_pct,omitempty"`
	Notes        string   `json:"notes,omitempty"`
}

func LoadRoster(path string) ([]RosterPlayer, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read roster file %q: %w", path, err)
	}
	var players []RosterPlayer
	if err := json.Unmarshal(b, &players); err != nil {
		return nil, fmt.Errorf("parse roster json: %w", err)
	}
	if len(players) == 0 {
		return nil, fmt.Errorf("roster json is empty")
	}
	for i := range players {
		players[i].PlayerName = strings.TrimSpace(players[i].PlayerName)
		players[i].MLBTeam = strings.ToUpper(strings.TrimSpace(players[i].MLBTeam))
		players[i].Role = strings.ToUpper(strings.TrimSpace(players[i].Role))
		players[i].Status = strings.ToLower(strings.TrimSpace(players[i].Status))
		if players[i].PlayerName == "" {
			return nil, fmt.Errorf("roster[%d].player_name is required", i)
		}
		if players[i].Role != "" {
			switch players[i].Role {
			case "SP", "RP", "P", "UNKNOWN":
			default:
				return nil, fmt.Errorf("roster[%d].role must be one of SP, RP, P, unknown", i)
			}
		}
		if players[i].Status != "" {
			switch players[i].Status {
			case "active", "injured", "stash", "unknown":
			default:
				return nil, fmt.Errorf("roster[%d].status must be one of active, injured, stash, unknown", i)
			}
		}
	}
	return players, nil
}

func LoadFreeAgents(path string) ([]FreeAgentPlayer, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read free agent file %q: %w", path, err)
	}
	var players []FreeAgentPlayer
	if err := json.Unmarshal(b, &players); err != nil {
		return nil, fmt.Errorf("parse free_agents json: %w", err)
	}
	if len(players) == 0 {
		return nil, fmt.Errorf("free_agents json is empty")
	}
	for i := range players {
		players[i].PlayerName = strings.TrimSpace(players[i].PlayerName)
		players[i].MLBTeam = strings.ToUpper(strings.TrimSpace(players[i].MLBTeam))
		players[i].Role = strings.ToUpper(strings.TrimSpace(players[i].Role))
		if players[i].PlayerName == "" {
			return nil, fmt.Errorf("free_agents[%d].player_name is required", i)
		}
		if players[i].Role != "" {
			switch players[i].Role {
			case "SP", "RP", "P", "UNKNOWN":
			default:
				return nil, fmt.Errorf("free_agents[%d].role must be one of SP, RP, P, unknown", i)
			}
		}
		if players[i].OwnershipPct != nil && (*players[i].OwnershipPct < 0 || *players[i].OwnershipPct > 100) {
			return nil, fmt.Errorf("free_agents[%d].ownership_pct must be between 0 and 100", i)
		}
	}
	return players, nil
}
