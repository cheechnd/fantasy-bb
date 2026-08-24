package mlb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestScheduleFormatsGameDateInDisplayTimezone(t *testing.T) {
	payload := []byte(`{
	  "dates": [{
	    "date": "2026-08-24",
	    "games": [{
	      "gamePk": 823183,
	      "gameDate": "2026-08-25T01:45:00Z",
	      "startTimeTBD": false,
	      "status": {"detailedState": "Scheduled"},
	      "teams": {
	        "away": {"team": {"name": "Cincinnati Reds"}, "probablePitcher": {"fullName": "Chase Burns"}},
	        "home": {"team": {"name": "San Francisco Giants"}, "probablePitcher": {"fullName": ""}}
	      }
	    }]
	  }]
	}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	tz, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	svc := &Service{
		httpClient: srv.Client(),
		baseURL:    srv.URL,
		userAgent:  "test",
	}
	from := time.Date(2026, 8, 24, 0, 0, 0, 0, tz)

	res, err := svc.Schedule(context.Background(), from, from, tz)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if len(res.Games) != 1 {
		t.Fatalf("expected 1 game, got %d", len(res.Games))
	}
	game := res.Games[0]
	if game.GameDate != "2026-08-24" {
		t.Fatalf("expected local game_date 2026-08-24, got %q", game.GameDate)
	}
	if game.GameTime != "9:45 PM" {
		t.Fatalf("expected local game_time 9:45 PM, got %q", game.GameTime)
	}
	if game.GameDateTimeUTC != "2026-08-25T01:45:00Z" {
		t.Fatalf("expected explicit UTC timestamp, got %q", game.GameDateTimeUTC)
	}
}
