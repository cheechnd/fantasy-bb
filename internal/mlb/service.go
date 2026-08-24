package mlb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const defaultScheduleBaseURL = "https://statsapi.mlb.com"

type Service struct {
	httpClient *http.Client
	baseURL    string
	userAgent  string
}

func New(timeout time.Duration, userAgent string) *Service {
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	if strings.TrimSpace(userAgent) == "" {
		userAgent = "fantasy-baseball/fb mlb-readonly"
	}
	return &Service{
		httpClient: &http.Client{Timeout: timeout},
		baseURL:    defaultScheduleBaseURL,
		userAgent:  userAgent,
	}
}

func (s *Service) Schedule(ctx context.Context, fromDate, toDate time.Time, tz *time.Location) (ScheduleResult, error) {
	if tz == nil {
		tz = time.Local
	}
	fromDate = dayStart(fromDate, tz)
	toDate = dayStart(toDate, tz)
	if toDate.Before(fromDate) {
		return ScheduleResult{}, fmt.Errorf("--to must be on or after --from")
	}

	u, err := url.Parse(strings.TrimRight(s.baseURL, "/") + "/api/v1/schedule")
	if err != nil {
		return ScheduleResult{}, fmt.Errorf("build mlb schedule endpoint: %w", err)
	}
	q := u.Query()
	q.Set("sportId", "1")
	q.Set("startDate", fromDate.Format("2006-01-02"))
	q.Set("endDate", toDate.Format("2006-01-02"))
	q.Set("hydrate", "probablePitcher,linescore")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return ScheduleResult{}, fmt.Errorf("create mlb schedule request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", s.userAgent)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return ScheduleResult{}, fmt.Errorf("request mlb schedule: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return ScheduleResult{}, fmt.Errorf("read mlb schedule response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := strings.TrimSpace(string(body))
		if len(msg) > 240 {
			msg = msg[:240] + "..."
		}
		return ScheduleResult{}, fmt.Errorf("mlb schedule request failed with status %d: %s", resp.StatusCode, msg)
	}

	var payload scheduleResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return ScheduleResult{}, fmt.Errorf("parse mlb schedule payload: %w", err)
	}

	games := make([]ScheduleGame, 0, 32)
	for _, d := range payload.Dates {
		for _, g := range d.Games {
			gameDate, _ := time.Parse(time.RFC3339, g.GameDate)
			localDate := gameDate.In(tz)
			gameTime := localDate.Format("3:04 PM")
			if g.StartTimeTBD {
				gameTime = "TBD"
			}
			row := ScheduleGame{
				GameID:          g.GamePk,
				GameDate:        localDate.Format("2006-01-02"),
				GameTime:        gameTime,
				GameDateTime:    localDate.Format(time.RFC3339),
				GameDateTimeUTC: gameDate.UTC().Format(time.RFC3339),
				Status:          strings.TrimSpace(g.Status.DetailedState),
				AwayTeam:        strings.TrimSpace(g.Teams.Away.Team.Name),
				HomeTeam:        strings.TrimSpace(g.Teams.Home.Team.Name),
				AwayProbableSP:  strings.TrimSpace(g.Teams.Away.ProbablePitcher.FullName),
				HomeProbableSP:  strings.TrimSpace(g.Teams.Home.ProbablePitcher.FullName),
				StartTimeTBD:    g.StartTimeTBD,
				sortDateTime:    gameDate,
			}
			if g.Teams.Away.Score != nil {
				v := *g.Teams.Away.Score
				row.AwayScore = &v
			}
			if g.Teams.Home.Score != nil {
				v := *g.Teams.Home.Score
				row.HomeScore = &v
			}
			games = append(games, row)
		}
	}
	sort.SliceStable(games, func(i, j int) bool {
		if games[i].sortDateTime.Equal(games[j].sortDateTime) {
			return games[i].GameID < games[j].GameID
		}
		return games[i].sortDateTime.Before(games[j].sortDateTime)
	})

	return ScheduleResult{
		FromDate:       fromDate.Format("2006-01-02"),
		ToDate:         toDate.Format("2006-01-02"),
		Timezone:       tz.String(),
		SourceEndpoint: u.String(),
		GameCount:      len(games),
		Games:          games,
	}, nil
}

type scheduleResponse struct {
	Dates []struct {
		Date  string `json:"date"`
		Games []struct {
			GamePk       int64  `json:"gamePk"`
			GameDate     string `json:"gameDate"`
			StartTimeTBD bool   `json:"startTimeTBD"`
			Status       struct {
				DetailedState string `json:"detailedState"`
			} `json:"status"`
			Teams struct {
				Away scheduleTeamEntry `json:"away"`
				Home scheduleTeamEntry `json:"home"`
			} `json:"teams"`
		} `json:"games"`
	} `json:"dates"`
}

type scheduleTeamEntry struct {
	Team struct {
		Name string `json:"name"`
	} `json:"team"`
	ProbablePitcher struct {
		FullName string `json:"fullName"`
	} `json:"probablePitcher"`
	Score *int `json:"score"`
}

func dayStart(t time.Time, tz *time.Location) time.Time {
	if tz == nil {
		tz = time.Local
	}
	tt := t.In(tz)
	return time.Date(tt.Year(), tt.Month(), tt.Day(), 0, 0, 0, 0, tz)
}
