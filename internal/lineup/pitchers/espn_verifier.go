package pitchers

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
)

type ESPNVerifier struct {
	client *espnclient.Client
}

func NewESPNVerifier(timeout time.Duration) *ESPNVerifier {
	return &ESPNVerifier{client: espnclient.New(timeout, "fantasy-baseball/fb espn-lineup-verify")}
}

func (v *ESPNVerifier) VerifyLineupMove(ctx context.Context, cfg config.Config, req LineupWriteRequest, _ LineupWriteResult) (execute.VerificationStatus, map[string]any, error) {
	creds, err := cfg.LoadESPNCredentialsFromEnv()
	if err != nil {
		return execute.VerificationStatusVerificationFailed, nil, err
	}
	fetch, err := v.client.FetchLeague(ctx, cfg, creds)
	if err != nil {
		return execute.VerificationStatusVerificationFailed, nil, err
	}
	ok, currentSlot, err := isPlayerInTargetSlot(fetch.Payload, cfg.League.TeamID, req.ESPNPlayerID, req.PlayerName, req.ToSlot)
	if err != nil {
		return execute.VerificationStatusVerificationFailed, nil, err
	}
	details := map[string]any{
		"endpoint":              fetch.Endpoint,
		"response_status":       fetch.ResponseStatus,
		"target_slot":           req.ToSlot,
		"current_slot":          currentSlot,
		"player_in_target_slot": ok,
	}
	if ok {
		details["inference"] = "likely_executed"
		return execute.VerificationStatusVerified, details, nil
	}
	details["inference"] = "inconclusive"
	return execute.VerificationStatusPending, details, nil
}

func isPlayerInTargetSlot(payload []byte, teamID string, playerID int64, playerName string, targetSlot string) (bool, string, error) {
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return false, "", fmt.Errorf("parse league payload: %w", err)
	}
	teams, ok := raw["teams"].([]any)
	if !ok {
		return false, "", fmt.Errorf("teams missing from payload")
	}
	wantTeamID, err := strconv.ParseInt(strings.TrimSpace(teamID), 10, 64)
	if err != nil {
		return false, "", fmt.Errorf("invalid team id: %w", err)
	}
	wantSlot := strings.ToUpper(strings.TrimSpace(targetSlot))
	wantName := strings.ToLower(strings.TrimSpace(playerName))
	for _, t := range teams {
		tm, ok := t.(map[string]any)
		if !ok || pickInt64(tm, "id") != wantTeamID {
			continue
		}
		roster, ok := tm["roster"].(map[string]any)
		if !ok {
			return false, "", fmt.Errorf("team roster missing")
		}
		entries, ok := roster["entries"].([]any)
		if !ok {
			return false, "", fmt.Errorf("team roster entries missing")
		}
		for _, e := range entries {
			entry, ok := e.(map[string]any)
			if !ok {
				continue
			}
			pool, ok := entry["playerPoolEntry"].(map[string]any)
			if !ok {
				continue
			}
			pid := pickInt64(pool, "playerId")
			name := pickString(pool, "fullName")
			if pid == 0 {
				if p, ok := pool["player"].(map[string]any); ok {
					pid = pickInt64(p, "id")
					if name == "" {
						name = pickString(p, "fullName")
					}
				}
			}
			if pid != playerID && !(pid == 0 && strings.ToLower(strings.TrimSpace(name)) == wantName) {
				continue
			}
			lsid := int(pickInt64(entry, "lineupSlotId"))
			current := lineupSlotLabel(lsid)
			return strings.EqualFold(current, wantSlot), current, nil
		}
	}
	return false, "", nil
}

func lineupSlotLabel(slot int) string {
	switch slot {
	case 13:
		return "P"
	case 14:
		return "SP"
	case 15:
		return "RP"
	case 16:
		return "BE"
	case 17:
		return "IL"
	default:
		return fmt.Sprintf("slot_%d", slot)
	}
}
