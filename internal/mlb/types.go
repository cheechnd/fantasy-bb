package mlb

import "time"

type ScheduleGame struct {
	GameID         int64     `json:"game_id"`
	GameDate       time.Time `json:"game_date"`
	Status         string    `json:"status"`
	AwayTeam       string    `json:"away_team"`
	HomeTeam       string    `json:"home_team"`
	AwayProbableSP string    `json:"away_probable_sp,omitempty"`
	HomeProbableSP string    `json:"home_probable_sp,omitempty"`
	AwayScore      *int      `json:"away_score,omitempty"`
	HomeScore      *int      `json:"home_score,omitempty"`
	StartTimeTBD   bool      `json:"start_time_tbd"`
}

type ScheduleResult struct {
	FromDate       string         `json:"from_date"`
	ToDate         string         `json:"to_date"`
	Timezone       string         `json:"timezone"`
	SourceEndpoint string         `json:"source_endpoint"`
	GameCount      int            `json:"game_count"`
	Games          []ScheduleGame `json:"games"`
}
