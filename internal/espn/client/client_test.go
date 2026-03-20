package client

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"fantasy-baseball/internal/config"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) Do(req *http.Request) (*http.Response, error) { return f(req) }

func TestFetchLeagueBuildsRequest(t *testing.T) {
	var gotURL string
	var gotCookie string
	var gotUA string
	c := NewWithHTTPClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		gotUA = req.Header.Get("User-Agent")
		for _, cookie := range req.Cookies() {
			gotCookie += cookie.Name + "=" + cookie.Value + ";"
		}
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(`{"settings":{},"teams":[]}`)),
			Header:     make(http.Header),
		}, nil
	}), "fb-test")

	cfg := config.Default()
	cfg.League.LeagueID = "123"
	cfg.League.Season = 2026
	cfg.ESPN.BaseURL = "https://fantasy.espn.com"
	_, err := c.FetchLeague(context.Background(), cfg, config.ESPNCredentials{ESPNS2: "abc", SWID: "xyz"})
	if err != nil {
		t.Fatalf("FetchLeague: %v", err)
	}
	if !strings.Contains(gotURL, "/apis/v3/games/flb/seasons/2026/segments/0/leagues/123") {
		t.Fatalf("unexpected URL: %s", gotURL)
	}
	if !strings.Contains(gotURL, "view=mTeam") || !strings.Contains(gotURL, "view=mRoster") {
		t.Fatalf("expected query views in URL: %s", gotURL)
	}
	if !strings.Contains(gotCookie, "espn_s2=abc") || !strings.Contains(gotCookie, "SWID=xyz") {
		t.Fatalf("expected auth cookies, got %s", gotCookie)
	}
	if gotUA != "fb-test" {
		t.Fatalf("unexpected user-agent: %s", gotUA)
	}
}

func TestFetchFreeAgentPitchersBuildsRequest(t *testing.T) {
	var gotURL string
	var gotFilter string
	c := NewWithHTTPClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		gotFilter = req.Header.Get("x-fantasy-filter")
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(`{"players":[]}`)),
			Header:     make(http.Header),
		}, nil
	}), "fb-test")

	cfg := config.Default()
	cfg.League.LeagueID = "123"
	cfg.League.Season = 2026
	cfg.ESPN.BaseURL = "https://fantasy.espn.com"
	_, err := c.FetchFreeAgentPitchers(context.Background(), cfg, config.ESPNCredentials{ESPNS2: "abc", SWID: "xyz"}, FreeAgentFetchOptions{Limit: 33})
	if err != nil {
		t.Fatalf("FetchFreeAgentPitchers: %v", err)
	}
	if !strings.Contains(gotURL, "view=kona_player_info") {
		t.Fatalf("expected kona_player_info view in URL: %s", gotURL)
	}
	if !strings.Contains(gotFilter, "\"limit\":33") {
		t.Fatalf("expected limit in x-fantasy-filter: %s", gotFilter)
	}
}
