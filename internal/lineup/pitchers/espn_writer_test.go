package pitchers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"fantasy-baseball/internal/config"
)

func TestExecuteLineupMoveFuturePeriodUsesFutureRosterType(t *testing.T) {
	var gotType string
	var gotScoringPeriod int64

	mux := http.NewServeMux()
	mux.HandleFunc("/apis/v3/games/flb/seasons/2026/segments/0/leagues/123", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":              123,
			"scoringPeriodId": 153,
			"status": map[string]any{
				"transactionScoringPeriod": 153,
			},
			"teams": []map[string]any{
				{"id": 8, "primaryOwner": "{MEM-1}"},
			},
			"members": []map[string]any{
				{"id": "{MEM-1}", "isLeagueManager": false},
			},
		})
	})
	mux.HandleFunc("/apis/v3/games/flb/seasons/2026/segments/0/leagues/123/transactions/", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		gotType, _ = body["type"].(string)
		if v, ok := body["scoringPeriodId"].(float64); ok {
			gotScoringPeriod = int64(v)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "EXECUTED"})
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

	targetSP := 154
	_, err := NewESPNWriter(0).ExecuteLineupMove(context.Background(), cfg, LineupWriteRequest{
		ESPNPlayerID:    41221,
		PlayerName:      "Logan Gilbert",
		FromSlot:        "P",
		ToSlot:          "BE",
		ScoringPeriodID: &targetSP,
	})
	if err != nil {
		t.Fatalf("ExecuteLineupMove: %v", err)
	}
	if gotType != "FUTURE_ROSTER" {
		t.Fatalf("request type = %q, want FUTURE_ROSTER", gotType)
	}
	if gotScoringPeriod != int64(targetSP) {
		t.Fatalf("scoringPeriodId = %d, want %d", gotScoringPeriod, targetSP)
	}
}

func TestExecuteLineupMoveCurrentPeriodUsesRosterType(t *testing.T) {
	var gotType string

	mux := http.NewServeMux()
	mux.HandleFunc("/apis/v3/games/flb/seasons/2026/segments/0/leagues/123", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":              123,
			"scoringPeriodId": 153,
			"status": map[string]any{
				"transactionScoringPeriod": 153,
			},
			"teams": []map[string]any{
				{"id": 8, "primaryOwner": "{MEM-1}"},
			},
			"members": []map[string]any{
				{"id": "{MEM-1}", "isLeagueManager": false},
			},
		})
	})
	mux.HandleFunc("/apis/v3/games/flb/seasons/2026/segments/0/leagues/123/transactions/", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		gotType, _ = body["type"].(string)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "EXECUTED"})
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

	targetSP := 153
	_, err := NewESPNWriter(0).ExecuteLineupMove(context.Background(), cfg, LineupWriteRequest{
		ESPNPlayerID:    41221,
		PlayerName:      "Logan Gilbert",
		FromSlot:        "P",
		ToSlot:          "BE",
		ScoringPeriodID: &targetSP,
	})
	if err != nil {
		t.Fatalf("ExecuteLineupMove: %v", err)
	}
	if gotType != "ROSTER" {
		t.Fatalf("request type = %q, want ROSTER", gotType)
	}
}
