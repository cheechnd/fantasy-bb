package pitchers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"fantasy-baseball/internal/config"
	espnclient "fantasy-baseball/internal/espn/client"
)

type ESPNWriter struct {
	httpClient *http.Client
	userAgent  string
}

func NewESPNWriter(timeout time.Duration) *ESPNWriter {
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	return &ESPNWriter{
		httpClient: &http.Client{Timeout: timeout, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }},
		userAgent:  "fantasy-baseball/fb espn-lineup-execution",
	}
}

func (w *ESPNWriter) ExecuteLineupMove(ctx context.Context, cfg config.Config, req LineupWriteRequest) (LineupWriteResult, error) {
	creds, err := cfg.LoadESPNCredentialsFromEnv()
	if err != nil {
		return LineupWriteResult{}, err
	}
	teamID, err := strconv.ParseInt(strings.TrimSpace(cfg.League.TeamID), 10, 64)
	if err != nil || teamID <= 0 {
		return LineupWriteResult{}, fmt.Errorf("invalid league.team_id %q", cfg.League.TeamID)
	}
	base := resolveWriteBase(cfg.ESPN.BaseURL)
	endpoint := fmt.Sprintf("%s/apis/v3/games/flb/seasons/%d/segments/0/leagues/%s/transactions/", base, cfg.League.Season, url.PathEscape(cfg.League.LeagueID))

	meta, err := w.resolveWriteMeta(ctx, cfg, creds, teamID)
	if err != nil {
		return LineupWriteResult{}, err
	}
	targetScoringPeriodID := meta.ScoringPeriodID
	if req.ScoringPeriodID != nil {
		targetScoringPeriodID = int64(*req.ScoringPeriodID)
	}
	if targetScoringPeriodID <= 0 {
		return LineupWriteResult{}, fmt.Errorf("invalid target scoring period id: %d", targetScoringPeriodID)
	}
	transactionType := "ROSTER"
	if meta.TransactionScoringPeriodID > 0 && targetScoringPeriodID > meta.TransactionScoringPeriodID {
		transactionType = "FUTURE_ROSTER"
	}

	fromSlotID := lineupSlotID(req.FromSlot)
	toSlotID := lineupSlotID(req.ToSlot)
	if fromSlotID <= 0 || toSlotID <= 0 {
		return LineupWriteResult{}, fmt.Errorf("invalid slot mapping from=%q to=%q", req.FromSlot, req.ToSlot)
	}

	body := map[string]any{
		"isLeagueManager": meta.IsLeagueManager,
		"teamId":          teamID,
		"type":            transactionType,
		"memberId":        meta.MemberID,
		"scoringPeriodId": targetScoringPeriodID,
		"executionType":   "EXECUTE",
		"items": []map[string]any{{
			"type":             "LINEUP",
			"playerId":         req.ESPNPlayerID,
			"fromLineupSlotId": fromSlotID,
			"toLineupSlotId":   toSlotID,
		}},
	}
	payload, _ := json.Marshal(body)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return LineupWriteResult{}, fmt.Errorf("create lineup request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", w.userAgent)
	httpReq.Header.Set("Origin", "https://fantasy.espn.com")
	httpReq.Header.Set("Referer", "https://fantasy.espn.com/")
	httpReq.Header.Set("x-fantasy-platform", "espn-fantasy-web")
	httpReq.Header.Set("x-fantasy-source", "kona")
	httpReq.AddCookie(&http.Cookie{Name: "espn_s2", Value: creds.ESPNS2})
	httpReq.AddCookie(&http.Cookie{Name: "SWID", Value: creds.SWID})

	resp, err := w.httpClient.Do(httpReq)
	if err != nil {
		return LineupWriteResult{Endpoint: endpoint}, fmt.Errorf("execute lineup request: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))

	res := LineupWriteResult{Endpoint: endpoint, ResponseStatus: resp.StatusCode, OK: resp.StatusCode >= 200 && resp.StatusCode <= 299}
	if len(raw) > 0 {
		trim := strings.TrimSpace(string(raw))
		lower := strings.ToLower(trim)
		if strings.HasPrefix(lower, "<!doctype html") || strings.HasPrefix(lower, "<html") {
			return res, fmt.Errorf("received HTML response instead of ESPN API JSON")
		}
		var js map[string]any
		if err := json.Unmarshal(raw, &js); err == nil {
			res.ResponseJSON = js
			res.Message = firstString(js["message"], js["error"], js["status"])
		} else {
			if len(trim) > 240 {
				trim = trim[:240] + "..."
			}
			res.Message = trim
		}
	}
	if !res.OK {
		msg := res.Message
		if msg == "" {
			msg = "non-2xx response"
		}
		return res, fmt.Errorf("espn lineup request failed with status %d: %s", resp.StatusCode, msg)
	}
	return res, nil
}

type writeMeta struct {
	MemberID                   string
	ScoringPeriodID            int64
	TransactionScoringPeriodID int64
	IsLeagueManager            bool
}

func (w *ESPNWriter) resolveWriteMeta(ctx context.Context, cfg config.Config, creds config.ESPNCredentials, teamID int64) (writeMeta, error) {
	cli := espnclient.New(time.Duration(cfg.ESPN.TimeoutSeconds)*time.Second, w.userAgent)
	fetch, err := cli.FetchLeague(ctx, cfg, creds)
	if err != nil {
		return writeMeta{}, err
	}
	var raw map[string]any
	if err := json.Unmarshal(fetch.Payload, &raw); err != nil {
		return writeMeta{}, fmt.Errorf("parse league payload: %w", err)
	}

	out := writeMeta{ScoringPeriodID: pickInt64(raw, "scoringPeriodId")}
	if out.ScoringPeriodID <= 0 {
		out.ScoringPeriodID = 1
	}
	if status, ok := raw["status"].(map[string]any); ok {
		out.TransactionScoringPeriodID = pickInt64(status, "transactionScoringPeriod")
	}
	if out.TransactionScoringPeriodID <= 0 {
		out.TransactionScoringPeriodID = out.ScoringPeriodID
	}

	ownerID := ""
	if teams, ok := raw["teams"].([]any); ok {
		for _, t := range teams {
			tm, ok := t.(map[string]any)
			if !ok || pickInt64(tm, "id") != teamID {
				continue
			}
			ownerID = pickString(tm, "primaryOwner")
			if ownerID == "" {
				if owners, ok := tm["owners"].([]any); ok && len(owners) > 0 {
					if s, ok := owners[0].(string); ok {
						ownerID = strings.TrimSpace(s)
					}
				}
			}
			break
		}
	}
	if members, ok := raw["members"].([]any); ok {
		for _, m := range members {
			mm, ok := m.(map[string]any)
			if !ok {
				continue
			}
			id := pickString(mm, "id")
			if ownerID != "" && !strings.EqualFold(id, ownerID) {
				continue
			}
			out.MemberID = id
			if v, ok := mm["isLeagueManager"].(bool); ok {
				out.IsLeagueManager = v
			}
			break
		}
	}
	if out.MemberID == "" {
		out.MemberID = ownerID
	}
	if out.MemberID == "" {
		return writeMeta{}, fmt.Errorf("could not resolve ESPN member id for write request")
	}
	return out, nil
}

func resolveWriteBase(configuredBase string) string {
	b := strings.TrimRight(strings.TrimSpace(configuredBase), "/")
	if b == "" {
		b = "https://lm-api-reads.fantasy.espn.com"
	}
	if strings.Contains(b, "lm-api-reads.fantasy.espn.com") {
		return strings.Replace(b, "lm-api-reads.fantasy.espn.com", "lm-api-writes.fantasy.espn.com", 1)
	}
	if strings.Contains(b, "fantasy.espn.com") {
		return strings.Replace(b, "fantasy.espn.com", "lm-api-writes.fantasy.espn.com", 1)
	}
	return b
}

func lineupSlotID(slot string) int {
	s := strings.ToUpper(strings.TrimSpace(slot))
	switch s {
	case "P":
		return 13
	case "SP":
		return 14
	case "RP":
		return 15
	case "BE", "BENCH":
		return 16
	case "IL":
		return 17
	default:
		if strings.HasPrefix(s, "SLOT_") {
			n, _ := strconv.Atoi(strings.TrimPrefix(s, "SLOT_"))
			return n
		}
		return 0
	}
}

func pickInt64(m map[string]any, key string) int64 {
	if m == nil {
		return 0
	}
	switch v := m[key].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
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

func firstString(values ...any) string {
	for _, v := range values {
		if s, ok := v.(string); ok {
			s = strings.TrimSpace(s)
			if s != "" {
				return s
			}
		}
	}
	return ""
}
