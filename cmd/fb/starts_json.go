package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"fantasy-baseball/internal/pickups"
	pitchers "fantasy-baseball/internal/pitchers"
)

type projectionStartJSON struct {
	GameDate      string   `json:"game_date"`
	Opponent      string   `json:"opponent,omitempty"`
	HomeAway      string   `json:"home_away,omitempty"`
	ProjectedFPTS *float64 `json:"projected_fpts"`
	Status        string   `json:"status,omitempty"`
}

func projectionStartsFromDetails(details map[string]interface{}) []projectionStartJSON {
	if details == nil {
		return []projectionStartJSON{}
	}
	return projectionStartsFromRaw(details["starts"])
}

func projectionStartsFromRaw(raw any) []projectionStartJSON {
	out := []projectionStartJSON{}
	switch starts := raw.(type) {
	case nil:
		return out
	case []pitchers.PitcherStart:
		for _, st := range starts {
			out = append(out, newProjectionStart(st.Date, st.Opponent, st.ProjectedFPTS, st.Status))
		}
	case []pickups.Start:
		for _, st := range starts {
			out = append(out, newProjectionStart(st.Date, st.Opponent, st.ProjectedFPTS, st.Status))
		}
	case []interface{}:
		for _, v := range starts {
			m, ok := v.(map[string]interface{})
			if !ok {
				continue
			}
			date := firstStringValue(m, "game_date", "date")
			opp := firstStringValue(m, "opponent")
			status := firstStringValue(m, "status")
			var fptsPtr *float64
			if rawFPTS, ok := m["projected_fpts"]; ok && rawFPTS != nil {
				fpts, ok := projectionStartToFloat(rawFPTS)
				if !ok {
					out = append(out, newProjectionStart(date, opp, nil, status))
					continue
				}
				v := fpts
				fptsPtr = &v
			}
			out = append(out, newProjectionStart(date, opp, fptsPtr, status))
		}
	case []map[string]interface{}:
		for _, m := range starts {
			date := firstStringValue(m, "game_date", "date")
			opp := firstStringValue(m, "opponent")
			status := firstStringValue(m, "status")
			var fptsPtr *float64
			if rawFPTS, ok := m["projected_fpts"]; ok && rawFPTS != nil {
				fpts, ok := projectionStartToFloat(rawFPTS)
				if !ok {
					out = append(out, newProjectionStart(date, opp, nil, status))
					continue
				}
				v := fpts
				fptsPtr = &v
			}
			out = append(out, newProjectionStart(date, opp, fptsPtr, status))
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].GameDate != out[j].GameDate {
			return out[i].GameDate < out[j].GameDate
		}
		if out[i].Opponent != out[j].Opponent {
			return out[i].Opponent < out[j].Opponent
		}
		return out[i].Status < out[j].Status
	})
	return out
}

func newProjectionStart(date, opponent string, projectedFPTS *float64, status string) projectionStartJSON {
	opp := strings.TrimSpace(opponent)
	homeAway := ""
	if strings.HasPrefix(opp, "@") {
		homeAway = "away"
		opp = strings.TrimPrefix(opp, "@")
	} else if opp != "" && !strings.EqualFold(opp, "UNK") {
		homeAway = "home"
	}
	return projectionStartJSON{
		GameDate:      strings.TrimSpace(date),
		Opponent:      strings.TrimSpace(opp),
		HomeAway:      homeAway,
		ProjectedFPTS: projectedFPTS,
		Status:        strings.TrimSpace(status),
	}
}

func formatProjectionFPTS(value *float64) string {
	if value == nil {
		return "-"
	}
	return fmt.Sprintf("%.1f", *value)
}

func firstStringValue(m map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			if s := strings.TrimSpace(projectionStartToString(v)); s != "" {
				return s
			}
		}
	}
	return ""
}

func projectionStartToFloat(raw any) (float64, bool) {
	switch v := raw.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case int32:
		return float64(v), true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f, err == nil
	default:
		return 0, false
	}
}

func projectionStartToString(raw any) string {
	switch v := raw.(type) {
	case nil:
		return ""
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		return fmt.Sprint(v)
	}
}
