package forecaster

import (
	"encoding/json"
	"time"
)

type SourceType string

const (
	SourceTypeFile SourceType = "file"
	SourceTypeURL  SourceType = "url"
)

type Status string

const (
	StatusScheduled Status = "scheduled"
	StatusOff       Status = "off"
	StatusTBD       Status = "tbd"
	StatusUnknown   Status = "unknown"
)

type ImportRun struct {
	ID                 int64      `json:"id"`
	SourceType         SourceType `json:"source_type"`
	SourceIdentifier   string     `json:"source_identifier"`
	ImportedAt         time.Time  `json:"imported_at"`
	RawRowCount        int        `json:"raw_row_count"`
	ProbableStartCount int        `json:"probable_start_count"`
	WarningCount       int        `json:"warning_count"`
	Status             string     `json:"status"`
	NotesJSON          string     `json:"notes_json,omitempty"`
}

type ProbableStart struct {
	ID            int64      `json:"id"`
	ImportRunID   int64      `json:"import_run_id"`
	SourceDateRaw string     `json:"source_date_text"`
	GameDate      *time.Time `json:"game_date,omitempty"`
	Team          string     `json:"team"`
	Opponent      string     `json:"opponent"`
	PitcherName   string     `json:"pitcher_name"`
	ThrowsHand    string     `json:"throws_hand"`
	ProjectedFPTS *float64   `json:"projected_fpts,omitempty"`
	Status        Status     `json:"status"`
	RawFieldsJSON string     `json:"raw_fields_json,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

type ParseWarning struct {
	ID             int64     `json:"id"`
	ImportRunID    int64     `json:"import_run_id"`
	WarningType    string    `json:"warning_type"`
	Message        string    `json:"message"`
	RowContextJSON string    `json:"row_context_json,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

type ProbableStartInput struct {
	SourceDateRaw string                 `json:"source_date_text"`
	GameDate      *time.Time             `json:"game_date,omitempty"`
	Team          string                 `json:"team"`
	Opponent      string                 `json:"opponent"`
	PitcherName   string                 `json:"pitcher_name"`
	ThrowsHand    string                 `json:"throws_hand"`
	ProjectedFPTS *float64               `json:"projected_fpts,omitempty"`
	Status        Status                 `json:"status"`
	RawFields     map[string]interface{} `json:"raw_fields,omitempty"`
}

type ParseWarningInput struct {
	WarningType string                 `json:"warning_type"`
	Message     string                 `json:"message"`
	RowContext  map[string]interface{} `json:"row_context,omitempty"`
}

func (p ProbableStartInput) RawFieldsJSON() string {
	if len(p.RawFields) == 0 {
		return ""
	}
	b, _ := json.Marshal(p.RawFields)
	return string(b)
}

func (w ParseWarningInput) RowContextJSON() string {
	if len(w.RowContext) == 0 {
		return ""
	}
	b, _ := json.Marshal(w.RowContext)
	return string(b)
}

type ListFilter struct {
	From       *time.Time
	To         *time.Time
	Team       string
	Pitcher    string
	ThrowsHand string
	MinFPTS    *float64
	IncludeTBD bool
	ImportRun  *int64
}

type TopFilter struct {
	From      *time.Time
	To        *time.Time
	TopN      int
	MinFPTS   *float64
	Team      string
	ImportRun *int64
}

type ClearResult struct {
	ImportRunsDeleted     int64 `json:"import_runs_deleted"`
	ProbableStartsDeleted int64 `json:"probable_starts_deleted"`
	WarningsDeleted       int64 `json:"warnings_deleted"`
}
