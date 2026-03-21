package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"fantasy-baseball/internal/config"
	espnclient "fantasy-baseball/internal/espn/client"
	"fantasy-baseball/internal/execute"
	"fantasy-baseball/internal/pitchers/matching"
)

type ESPNVerifier struct {
	client *espnclient.Client
}

func NewESPNVerifier(timeout time.Duration) *ESPNVerifier {
	return &ESPNVerifier{
		client: espnclient.New(timeout, "fantasy-baseball/fb espn-execution-verify"),
	}
}

func (v *ESPNVerifier) Verify(ctx context.Context, cfg config.Config, req WriteRequest, _ WriteResult) (execute.VerificationStatus, map[string]any, error) {
	creds, err := cfg.LoadESPNCredentialsFromEnv()
	if err != nil {
		return execute.VerificationStatusVerificationFailed, nil, err
	}
	fetch, err := v.client.FetchLeague(ctx, cfg, creds)
	if err != nil {
		return execute.VerificationStatusVerificationFailed, nil, err
	}

	addOnRoster, dropOnRoster, err := findRosterMembership(fetch.Payload, cfg.League.TeamID, req)
	if err != nil {
		return execute.VerificationStatusVerificationFailed, nil, fmt.Errorf("parse roster for verification: %w", err)
	}
	details := map[string]any{
		"endpoint":          fetch.Endpoint,
		"response_status":   fetch.ResponseStatus,
		"add_on_roster":     addOnRoster,
		"drop_still_roster": dropOnRoster,
	}
	switch {
	case addOnRoster && !dropOnRoster:
		details["message"] = "added pitcher appears on roster and drop pitcher removed"
		return execute.VerificationStatusVerified, details, nil
	case addOnRoster && dropOnRoster:
		details["message"] = "add pitcher appears on roster but drop pitcher still present"
		return execute.VerificationStatusUnverified, details, nil
	case !addOnRoster && !dropOnRoster:
		details["message"] = "drop pitcher removed but add pitcher not yet visible"
		return execute.VerificationStatusUnverified, details, nil
	default:
		details["message"] = "add pitcher not visible on roster"
		return execute.VerificationStatusUnverified, details, nil
	}
}

func findRosterMembership(payload []byte, teamID string, req WriteRequest) (bool, bool, error) {
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return false, false, err
	}
	teams, ok := raw["teams"].([]any)
	if !ok || len(teams) == 0 {
		return false, false, fmt.Errorf("teams array missing")
	}
	wantTeamID, err := strconv.ParseInt(strings.TrimSpace(teamID), 10, 64)
	if err != nil {
		return false, false, fmt.Errorf("invalid team id: %w", err)
	}
	addKey := matching.NormalizeName(req.AddPlayerName)
	dropKey := matching.NormalizeName(req.DropPlayerName)

	for _, t := range teams {
		tm, ok := t.(map[string]any)
		if !ok {
			continue
		}
		if pickInt64(tm, "id") != wantTeamID {
			continue
		}
		roster, ok := tm["roster"].(map[string]any)
		if !ok {
			return false, false, fmt.Errorf("team roster missing")
		}
		entries, ok := roster["entries"].([]any)
		if !ok {
			return false, false, fmt.Errorf("team roster entries missing")
		}

		addMatch := false
		dropMatch := false
		for _, e := range entries {
			entry, ok := e.(map[string]any)
			if !ok {
				continue
			}
			pool, ok := entry["playerPoolEntry"].(map[string]any)
			if !ok {
				continue
			}
			playerID := pickInt64(pool, "playerId")
			if playerID == 0 {
				if player, ok := pool["player"].(map[string]any); ok {
					playerID = pickInt64(player, "id")
				}
			}
			name := pickString(pool, "fullName")
			if name == "" {
				if player, ok := pool["player"].(map[string]any); ok {
					name = pickString(player, "fullName")
				}
			}
			nameKey := matching.NormalizeName(name)
			if req.AddESPNPlayerID != nil && playerID == *req.AddESPNPlayerID {
				addMatch = true
			}
			if req.DropESPNPlayerID != nil && playerID == *req.DropESPNPlayerID {
				dropMatch = true
			}
			if !addMatch && addKey != "" && nameKey == addKey {
				addMatch = true
			}
			if !dropMatch && dropKey != "" && nameKey == dropKey {
				dropMatch = true
			}
		}
		return addMatch, dropMatch, nil
	}
	return false, false, fmt.Errorf("team %s not found in payload", teamID)
}

func pickInt64(m map[string]any, key string) int64 {
	if m == nil {
		return 0
	}
	switch v := m[key].(type) {
	case float64:
		return int64(v)
	case int:
		return int64(v)
	case int64:
		return v
	case json.Number:
		i, _ := v.Int64()
		return i
	}
	return 0
}

func pickString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}
