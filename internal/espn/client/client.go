package client

import (
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
)

type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type Client struct {
	httpClient HTTPClient
	userAgent  string
}

type FetchResult struct {
	Endpoint       string
	ResponseStatus int
	Payload        []byte
}

func New(timeout time.Duration, userAgent string) *Client {
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	if strings.TrimSpace(userAgent) == "" {
		userAgent = "fantasy-baseball/fb espn-readonly"
	}
	return &Client{httpClient: &http.Client{Timeout: timeout}, userAgent: userAgent}
}

func NewWithHTTPClient(httpClient HTTPClient, userAgent string) *Client {
	if strings.TrimSpace(userAgent) == "" {
		userAgent = "fantasy-baseball/fb espn-readonly"
	}
	return &Client{httpClient: httpClient, userAgent: userAgent}
}

func (c *Client) FetchLeague(ctx context.Context, cfg config.Config, creds config.ESPNCredentials) (FetchResult, error) {
	return c.FetchLeagueWithOptions(ctx, cfg, creds, LeagueFetchOptions{})
}

type LeagueFetchOptions struct {
	ScoringPeriodID *int
}

func (c *Client) FetchLeagueWithOptions(ctx context.Context, cfg config.Config, creds config.ESPNCredentials, opts LeagueFetchOptions) (FetchResult, error) {
	baseURL := strings.TrimRight(cfg.ESPN.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://lm-api-reads.fantasy.espn.com"
	}
	endpoint := fmt.Sprintf("%s/apis/v3/games/flb/seasons/%d/segments/0/leagues/%s", baseURL, cfg.League.Season, url.PathEscape(cfg.League.LeagueID))
	u, err := url.Parse(endpoint)
	if err != nil {
		return FetchResult{}, fmt.Errorf("build espn endpoint: %w", err)
	}
	q := u.Query()
	q.Add("view", "mTeam")
	q.Add("view", "mRoster")
	q.Add("view", "mSettings")
	if opts.ScoringPeriodID != nil && *opts.ScoringPeriodID > 0 {
		q.Set("scoringPeriodId", strconv.Itoa(*opts.ScoringPeriodID))
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return FetchResult{}, fmt.Errorf("create espn request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	req.AddCookie(&http.Cookie{Name: "espn_s2", Value: creds.ESPNS2})
	req.AddCookie(&http.Cookie{Name: "SWID", Value: creds.SWID})

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return FetchResult{}, fmt.Errorf("request espn league endpoint: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return FetchResult{}, fmt.Errorf("read espn response body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := strings.TrimSpace(string(body))
		if len(msg) > 240 {
			msg = msg[:240] + "..."
		}
		return FetchResult{Endpoint: u.String(), ResponseStatus: resp.StatusCode, Payload: body}, fmt.Errorf("espn request failed with status %d: %s", resp.StatusCode, msg)
	}
	return FetchResult{Endpoint: u.String(), ResponseStatus: resp.StatusCode, Payload: body}, nil
}

type FreeAgentFetchOptions struct {
	Limit         int
	Offset        int
	UseSlotFilter bool
	SlotIDs       []int
}

type MatchupFetchOptions struct {
	MatchupPeriodID *int
	ScoringPeriodID *int
}

func (c *Client) FetchFreeAgentPitchers(ctx context.Context, cfg config.Config, creds config.ESPNCredentials, opts FreeAgentFetchOptions) (FetchResult, error) {
	baseURL := strings.TrimRight(cfg.ESPN.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://lm-api-reads.fantasy.espn.com"
	}
	endpoint := fmt.Sprintf("%s/apis/v3/games/flb/seasons/%d/segments/0/leagues/%s", baseURL, cfg.League.Season, url.PathEscape(cfg.League.LeagueID))
	u, err := url.Parse(endpoint)
	if err != nil {
		return FetchResult{}, fmt.Errorf("build espn candidate endpoint: %w", err)
	}
	q := u.Query()
	q.Add("view", "kona_player_info")
	u.RawQuery = q.Encode()

	limit := opts.Limit
	if limit <= 0 {
		limit = 25
	}
	offset := opts.Offset
	if offset < 0 {
		offset = 0
	}

	players := map[string]any{
		"limit":  limit,
		"offset": offset,
		"sortPercOwned": map[string]any{
			"sortAsc":      false,
			"sortPriority": 1,
		},
		"filterStatus": map[string]any{
			"value": []string{"FREEAGENT", "WAIVERS"},
		},
	}
	if opts.UseSlotFilter && len(opts.SlotIDs) > 0 {
		players["filterSlotIds"] = map[string]any{
			"value": opts.SlotIDs,
		}
	}

	filter := map[string]any{
		"players": map[string]any{
			"limit": limit,
		},
	}
	filter["players"] = players
	filterJSON, _ := json.Marshal(filter)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return FetchResult{}, fmt.Errorf("create espn candidate request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("x-fantasy-filter", string(filterJSON))
	req.AddCookie(&http.Cookie{Name: "espn_s2", Value: creds.ESPNS2})
	req.AddCookie(&http.Cookie{Name: "SWID", Value: creds.SWID})

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return FetchResult{}, fmt.Errorf("request espn candidates endpoint: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 12<<20))
	if err != nil {
		return FetchResult{}, fmt.Errorf("read espn candidates response body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := strings.TrimSpace(string(body))
		if len(msg) > 240 {
			msg = msg[:240] + "..."
		}
		return FetchResult{Endpoint: u.String(), ResponseStatus: resp.StatusCode, Payload: body}, fmt.Errorf("espn candidate request failed with status %d: %s", resp.StatusCode, msg)
	}
	return FetchResult{Endpoint: u.String(), ResponseStatus: resp.StatusCode, Payload: body}, nil
}

func (c *Client) FetchMatchupScore(ctx context.Context, cfg config.Config, creds config.ESPNCredentials, opts MatchupFetchOptions) (FetchResult, error) {
	baseURL := strings.TrimRight(cfg.ESPN.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://lm-api-reads.fantasy.espn.com"
	}
	endpoint := fmt.Sprintf("%s/apis/v3/games/flb/seasons/%d/segments/0/leagues/%s", baseURL, cfg.League.Season, url.PathEscape(cfg.League.LeagueID))
	u, err := url.Parse(endpoint)
	if err != nil {
		return FetchResult{}, fmt.Errorf("build espn matchup endpoint: %w", err)
	}
	q := u.Query()
	q.Add("view", "mMatchupScore")
	q.Add("view", "mSettings")
	q.Add("view", "mTeam")
	if opts.MatchupPeriodID != nil && *opts.MatchupPeriodID > 0 {
		q.Set("matchupPeriodId", strconv.Itoa(*opts.MatchupPeriodID))
	}
	if opts.ScoringPeriodID != nil && *opts.ScoringPeriodID > 0 {
		q.Set("scoringPeriodId", strconv.Itoa(*opts.ScoringPeriodID))
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return FetchResult{}, fmt.Errorf("create espn matchup request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	req.AddCookie(&http.Cookie{Name: "espn_s2", Value: creds.ESPNS2})
	req.AddCookie(&http.Cookie{Name: "SWID", Value: creds.SWID})

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return FetchResult{}, fmt.Errorf("request espn matchup endpoint: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return FetchResult{}, fmt.Errorf("read espn matchup response body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := strings.TrimSpace(string(body))
		if len(msg) > 240 {
			msg = msg[:240] + "..."
		}
		return FetchResult{Endpoint: u.String(), ResponseStatus: resp.StatusCode, Payload: body}, fmt.Errorf("espn matchup request failed with status %d: %s", resp.StatusCode, msg)
	}
	return FetchResult{Endpoint: u.String(), ResponseStatus: resp.StatusCode, Payload: body}, nil
}

func (c *Client) FetchSeasonProTeamSchedules(ctx context.Context, cfg config.Config, creds config.ESPNCredentials) (FetchResult, error) {
	baseURL := strings.TrimRight(cfg.ESPN.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://lm-api-reads.fantasy.espn.com"
	}
	endpoint := fmt.Sprintf("%s/apis/v3/games/flb/seasons/%d", baseURL, cfg.League.Season)
	u, err := url.Parse(endpoint)
	if err != nil {
		return FetchResult{}, fmt.Errorf("build espn season schedule endpoint: %w", err)
	}
	q := u.Query()
	q.Add("view", "proTeamSchedules_wl")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return FetchResult{}, fmt.Errorf("create espn season schedule request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	req.AddCookie(&http.Cookie{Name: "espn_s2", Value: creds.ESPNS2})
	req.AddCookie(&http.Cookie{Name: "SWID", Value: creds.SWID})

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return FetchResult{}, fmt.Errorf("request espn season schedule endpoint: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return FetchResult{}, fmt.Errorf("read espn season schedule response body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := strings.TrimSpace(string(body))
		if len(msg) > 240 {
			msg = msg[:240] + "..."
		}
		return FetchResult{Endpoint: u.String(), ResponseStatus: resp.StatusCode, Payload: body}, fmt.Errorf("espn season schedule request failed with status %d: %s", resp.StatusCode, msg)
	}
	return FetchResult{Endpoint: u.String(), ResponseStatus: resp.StatusCode, Payload: body}, nil
}
