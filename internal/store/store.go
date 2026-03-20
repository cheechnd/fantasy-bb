package store

import (
	"context"

	"fantasy-baseball/internal/db/migrations"
)

type Store interface {
	Path() string
	Ping(context.Context) error
	Migrate(context.Context) ([]migrations.AppliedMigration, error)
	MigrationStatus(context.Context) ([]migrations.Status, error)
	Close() error
}
