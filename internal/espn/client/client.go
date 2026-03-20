package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
