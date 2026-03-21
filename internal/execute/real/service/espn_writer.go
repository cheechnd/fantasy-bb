package service

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
		httpClient: &http.Client{
			Timeout: timeout,
			// Do not auto-follow redirects so we can surface auth/host issues clearly.
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		},
		userAgent:  "fantasy-baseball/fb espn-execution",
	}
}

func (w *ESPNWriter) ExecuteAddDrop(ctx context.Context, cfg config.Config, req WriteRequest) (WriteResult, error) {
	if req.AddESPNPlayerID == nil || req.DropESPNPlayerID == nil {
		return WriteResult{}, fmt.Errorf("missing ESPN player IDs for add/drop request")
	}
	teamID, err := strconv.ParseInt(strings.TrimSpace(cfg.League.TeamID), 10, 64)
	if err != nil || teamID <= 0 {
		return WriteResult{}, fmt.Errorf("invalid league.team_id %q for execution", cfg.League.TeamID)
	}
	creds, err := cfg.LoadESPNCredentialsFromEnv()
	if err != nil {
		return WriteResult{}, err
	}

	baseURL, probeInfo, err := w.resolveWriteBase(ctx, cfg, creds)
	if err != nil {
		return WriteResult{}, err
	}
	endpoint := fmt.Sprintf("%s/apis/v3/games/flb/seasons/%d/segments/0/leagues/%s/transactions/", baseURL, cfg.League.Season, url.PathEscape(cfg.League.LeagueID))
	meta, err := w.resolveWriteMeta(ctx, cfg, creds, teamID)
	if err != nil {
		return WriteResult{}, fmt.Errorf("resolve write context metadata: %w", err)
	}

	body := map[string]any{
		"isLeagueManager": meta.IsLeagueManager,
		"teamId":          teamID,
		"type":            "FREEAGENT",
		"memberId":        meta.MemberID,
		"scoringPeriodId": meta.ScoringPeriodID,
		"executionType": "EXECUTE",
		"items": []map[string]any{
			{
				"type":     "ADD",
				"playerId": *req.AddESPNPlayerID,
				"toTeamId": teamID,
			},
			{
				"type":       "DROP",
				"playerId":   *req.DropESPNPlayerID,
				"fromTeamId": teamID,
			},
		},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return WriteResult{}, fmt.Errorf("marshal add/drop request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return WriteResult{}, fmt.Errorf("create add/drop request: %w", err)
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
		return WriteResult{Endpoint: endpoint}, fmt.Errorf("execute add/drop request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode <= 399 {
		location := strings.TrimSpace(resp.Header.Get("Location"))
		if location == "" {
			location = "(missing location header)"
		}
		return WriteResult{
			Endpoint:       endpoint,
			ResponseStatus: resp.StatusCode,
			OK:             false,
		}, fmt.Errorf("espn add/drop request redirected (%d) to %s; write host/auth likely invalid", resp.StatusCode, location)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return WriteResult{Endpoint: endpoint, ResponseStatus: resp.StatusCode}, fmt.Errorf("read add/drop response: %w", err)
	}
	result := WriteResult{
		Endpoint:       endpoint,
		ResponseStatus: resp.StatusCode,
		OK:             resp.StatusCode >= 200 && resp.StatusCode <= 299,
	}
	if len(raw) > 0 {
		trimmed := strings.TrimSpace(string(raw))
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "<!doctype html") || strings.HasPrefix(lower, "<html") {
			msg := "received HTML response instead of ESPN API JSON"
			if len(trimmed) > 120 {
				trimmed = trimmed[:120] + "..."
			}
			return WriteResult{
				Endpoint:        endpoint,
				ResponseStatus:  resp.StatusCode,
				ResponseMessage: fmt.Sprintf("%s (probe=%s)", trimmed, probeInfo),
				OK:              false,
			}, fmt.Errorf("%s", msg)
		}
		contentType := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
		if contentType != "" && !strings.Contains(contentType, "json") {
			return WriteResult{
				Endpoint:        endpoint,
				ResponseStatus:  resp.StatusCode,
				ResponseMessage: fmt.Sprintf("unexpected content-type %q", contentType),
				OK:              false,
			}, fmt.Errorf("espn add/drop response content-type %q is not JSON-compatible", contentType)
		}
		var parsed map[string]any
		if err := json.Unmarshal(raw, &parsed); err == nil {
			result.ResponseJSON = parsed
			if msg := firstString(parsed["message"], parsed["error"], parsed["status"]); msg != "" {
				result.ResponseMessage = msg
			}
		} else {
			msg := strings.TrimSpace(string(raw))
			if len(msg) > 240 {
				msg = msg[:240] + "..."
			}
			result.ResponseMessage = msg
		}
	}
	if result.ResponseJSON == nil {
		return result, fmt.Errorf("espn add/drop response was not valid JSON")
	}
	if errMsg := firstString(result.ResponseJSON["error"], result.ResponseJSON["errorMessage"]); errMsg != "" {
		return result, fmt.Errorf("espn add/drop API error: %s", errMsg)
	}
	if !result.OK {
		msg := result.ResponseMessage
		if msg == "" {
			msg = "non-2xx response"
		}
		return result, fmt.Errorf("espn add/drop request failed with status %d: %s", resp.StatusCode, msg)
	}
	return result, nil
}

func (w *ESPNWriter) resolveWriteBase(ctx context.Context, cfg config.Config, creds config.ESPNCredentials) (string, string, error) {
	candidates := writeBaseCandidates(cfg.ESPN.BaseURL)
	if len(candidates) == 0 {
		candidates = []string{"https://lm-api.fantasy.espn.com", "https://fantasy.espn.com"}
	}

	var errs []string
	for _, base := range candidates {
		if strings.Contains(base, "lm-api-reads.fantasy.espn.com") {
			errs = append(errs, fmt.Sprintf("%s: read-only host skipped for writes", base))
			continue
		}
		info, err := w.probeWriteBase(ctx, cfg, creds, base)
		if err == nil {
			return base, info, nil
		}
		errs = append(errs, fmt.Sprintf("%s: %v", base, err))
	}
	return "", "", fmt.Errorf("could not find ESPN write API host; probe failures: %s", strings.Join(errs, "; "))
}

func (w *ESPNWriter) probeWriteBase(ctx context.Context, cfg config.Config, creds config.ESPNCredentials, base string) (string, error) {
	endpoint := fmt.Sprintf("%s/apis/v3/games/flb/seasons/%d/segments/0/leagues/%s?view=mSettings", strings.TrimRight(base, "/"), cfg.League.Season, url.PathEscape(cfg.League.LeagueID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", w.userAgent)
	req.AddCookie(&http.Cookie{Name: "espn_s2", Value: creds.ESPNS2})
	req.AddCookie(&http.Cookie{Name: "SWID", Value: creds.SWID})

	resp, err := w.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 && resp.StatusCode <= 399 {
		location := strings.TrimSpace(resp.Header.Get("Location"))
		return "", fmt.Errorf("redirected to %s", location)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 128<<10))
	if err != nil {
		return "", fmt.Errorf("read probe response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// Write host may reject GET with 405 while still being the correct write API host.
		// Treat this as a successful probe signal when ESPN returns the expected method error.
		if resp.StatusCode == http.StatusMethodNotAllowed {
			msg := strings.ToLower(strings.TrimSpace(string(raw)))
			if strings.Contains(msg, "http method not supported") || strings.Contains(msg, "method not allowed") {
				return fmt.Sprintf("host=%s,status=%d(method-not-allowed-ok)", strings.TrimSpace(base), resp.StatusCode), nil
			}
		}
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 120 {
			msg = msg[:120] + "..."
		}
		return "", fmt.Errorf("status %d (%s)", resp.StatusCode, msg)
	}

	trimmed := strings.TrimSpace(string(raw))
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "<!doctype html") || strings.HasPrefix(lower, "<html") {
		return "", fmt.Errorf("probe returned HTML")
	}
	contentType := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
	if contentType != "" && !strings.Contains(contentType, "json") {
		return "", fmt.Errorf("probe content-type %q not JSON", contentType)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("probe payload is not JSON: %w", err)
	}
	return fmt.Sprintf("host=%s,status=%d", strings.TrimSpace(base), resp.StatusCode), nil
}

func writeBaseCandidates(configuredBase string) []string {
	base := strings.TrimRight(strings.TrimSpace(configuredBase), "/")
	if base == "" {
		base = "https://lm-api-reads.fantasy.espn.com"
	}

	add := func(out *[]string, v string) {
		v = strings.TrimRight(strings.TrimSpace(v), "/")
		if v == "" {
			return
		}
		for _, e := range *out {
			if e == v {
				return
			}
		}
		*out = append(*out, v)
	}

	out := make([]string, 0, 4)
	if strings.Contains(base, "lm-api-reads.fantasy.espn.com") {
		add(&out, strings.Replace(base, "lm-api-reads.fantasy.espn.com", "lm-api-writes.fantasy.espn.com", 1))
		add(&out, strings.Replace(base, "lm-api-reads.fantasy.espn.com", "lm-api.fantasy.espn.com", 1))
		add(&out, strings.Replace(base, "lm-api-reads.fantasy.espn.com", "fantasy.espn.com", 1))
	} else {
		add(&out, base)
		add(&out, strings.Replace(base, "lm-api.fantasy.espn.com", "lm-api-writes.fantasy.espn.com", 1))
		add(&out, strings.Replace(base, "fantasy.espn.com", "lm-api.fantasy.espn.com", 1))
		add(&out, strings.Replace(base, "lm-api.fantasy.espn.com", "fantasy.espn.com", 1))
	}
	add(&out, "https://lm-api-writes.fantasy.espn.com")
	add(&out, "https://lm-api.fantasy.espn.com")
	add(&out, "https://fantasy.espn.com")
	return out
}

type writeMeta struct {
	MemberID       string
	ScoringPeriodID int64
	IsLeagueManager bool
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

	out := writeMeta{
		ScoringPeriodID: pickInt64Any(raw, "scoringPeriodId"),
	}
	if out.ScoringPeriodID <= 0 {
		out.ScoringPeriodID = 1
	}

	ownerID := ""
	if teams, ok := raw["teams"].([]any); ok {
		for _, t := range teams {
			tm, ok := t.(map[string]any)
			if !ok {
				continue
			}
			if pickInt64Any(tm, "id") != teamID {
				continue
			}
			ownerID = pickStringAny(tm, "primaryOwner")
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
			member, ok := m.(map[string]any)
			if !ok {
				continue
			}
			candidateID := pickStringAny(member, "id")
			if ownerID != "" && !strings.EqualFold(candidateID, ownerID) {
				continue
			}
			out.MemberID = candidateID
			if v, ok := member["isLeagueManager"].(bool); ok {
				out.IsLeagueManager = v
			}
			break
		}
	}
	if out.MemberID == "" && ownerID != "" {
		out.MemberID = ownerID
	}
	if out.MemberID == "" {
		return writeMeta{}, fmt.Errorf("member id not found for team %d", teamID)
	}
	return out, nil
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

func pickInt64Any(m map[string]any, key string) int64 {
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
	case json.Number:
		i, _ := v.Int64()
		return i
	}
	return 0
}

func pickStringAny(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}
