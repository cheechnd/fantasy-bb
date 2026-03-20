package input

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRosterValidatesAndLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "roster.json")
	if err := os.WriteFile(path, []byte(`[ {"player_name":" Gerrit Cole ","mlb_team":"nyy","role":"sp","status":"active"} ]`), 0o644); err != nil {
		t.Fatal(err)
	}
	players, err := LoadRoster(path)
	if err != nil {
		t.Fatalf("LoadRoster: %v", err)
	}
	if len(players) != 1 || players[0].PlayerName != "Gerrit Cole" || players[0].MLBTeam != "NYY" {
		t.Fatalf("unexpected roster parse: %+v", players)
	}
}

func TestLoadRosterRequiresPlayerName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "roster.json")
	_ = os.WriteFile(path, []byte(`[{}]`), 0o644)
	_, err := LoadRoster(path)
	if err == nil || !strings.Contains(err.Error(), "player_name") {
		t.Fatalf("expected player_name error, got %v", err)
	}
}

func TestLoadFreeAgentsValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "free_agents.json")
	_ = os.WriteFile(path, []byte(`[ {"player_name":"Jordan Wicks","ownership_pct":101} ]`), 0o644)
	_, err := LoadFreeAgents(path)
	if err == nil || !strings.Contains(err.Error(), "ownership_pct") {
		t.Fatalf("expected ownership_pct error, got %v", err)
	}
}
