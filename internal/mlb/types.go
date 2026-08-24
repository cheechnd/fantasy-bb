package mlb

import "time"

type ScheduleGame struct {
	GameID          int64     `json:"game_id"`
	GameDate        string    `json:"game_date"`
	GameTime        string    `json:"game_time"`
	GameDateTime    string    `json:"game_datetime"`
	GameDateTimeUTC string    `json:"game_datetime_utc"`
	Status          string    `json:"status"`
	AwayTeam        string    `json:"away_team"`
	HomeTeam        string    `json:"home_team"`
	AwayProbableSP  string    `json:"away_probable_sp,omitempty"`
	HomeProbableSP  string    `json:"home_probable_sp,omitempty"`
	AwayScore       *int      `json:"away_score,omitempty"`
	HomeScore       *int      `json:"home_score,omitempty"`
	StartTimeTBD    bool      `json:"start_time_tbd"`
	sortDateTime    time.Time `json:"-"`
}

type ScheduleResult struct {
	FromDate       string         `json:"from_date"`
	ToDate         string         `json:"to_date"`
	Timezone       string         `json:"timezone"`
	SourceEndpoint string         `json:"source_endpoint"`
	GameCount      int            `json:"game_count"`
	Games          []ScheduleGame `json:"games"`
}
