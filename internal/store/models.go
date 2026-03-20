package store

import "time"

type AppMeta struct {
	Key       string
	Value     string
	CreatedAt time.Time
}

type SyncRun struct {
	ID          int64
	Source      string
	Status      string
	StartedAt   *time.Time
	FinishedAt  *time.Time
	DetailsJSON string
	CreatedAt   time.Time
}

type RosterSnapshot struct {
	ID          int64
	Source      string
	Season      int
	PayloadJSON string
	CapturedAt  time.Time
	CreatedAt   time.Time
}

type Recommendation struct {
	ID            int64
	Status        string
	RationaleJSON string
	PayloadJSON   string
	CreatedAt     time.Time
}

type ExecutionPlan struct {
	ID         int64
	Status     string
	PlanJSON   string
	ExecutedAt *time.Time
	CreatedAt  time.Time
}
