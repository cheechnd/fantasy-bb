package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"fantasy-baseball/internal/config"
)

func TestWriteBaseCandidates(t *testing.T) {
	candidates := writeBaseCandidates("https://lm-api-reads.fantasy.espn.com")
	if len(candidates) == 0 {
		t.Fatalf("expected non-empty candidates")
	}
	if candidates[0] != "https://lm-api-writes.fantasy.espn.com" {
		t.Fatalf("expected lm-api host first, got %q", candidates[0])
	}
}

func TestExecuteAddDropRejectsHTMLPostResponse(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/apis/v3/games/flb/seasons/2026/segments/0/leagues/123", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":              123,
			"scoringPeriodId": 1,
			"teams": []map[string]any{
				{"id": 8, "primaryOwner": "{MEM-1}"},
			},
			"members": []map[string]any{
				{"id": "{MEM-1}", "isLeagueManager": false},
			},
		})
	})
	mux.HandleFunc("/apis/v3/games/flb/seasons/2026/segments/0/leagues/123/transactions", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!DOCTYPE html><html><body>Redirecting</body></html>"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	t.Setenv("TEST_ESPN_S2", "token")
	t.Setenv("TEST_ESPN_SWID", "{abc}")

	cfg := config.Default()
	cfg.ESPN.BaseURL = srv.URL
	cfg.League.LeagueID = "123"
	cfg.League.TeamID = "8"
	cfg.League.Season = 2026
	cfg.Auth.ESPNS2Env = "TEST_ESPN_S2"
	cfg.Auth.SWIDEnv = "TEST_ESPN_SWID"

	addID := int64(1)
	dropID := int64(2)
	_, err := NewESPNWriter(0).ExecuteAddDrop(context.Background(), cfg, WriteRequest{
		AddESPNPlayerID:  &addID,
		DropESPNPlayerID: &dropID,
	})
	if err == nil {
		t.Fatalf("expected HTML response error")
	}
	errText := strings.ToLower(err.Error())
	if !strings.Contains(errText, "not json-compatible") && !strings.Contains(errText, "html response") {
		t.Fatalf("expected non-json/html error, got %v", err)
	}
}
