package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"fantasy-baseball/internal/db/migrations"

	_ "modernc.org/sqlite"
)

type Store struct {
	path     string
	db       *sql.DB
	migrator *migrations.Migrator
}

func EnsureDatabaseFile(path string) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("create db parent directory: %w", err)
	}
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("stat db file: %w", err)
	}
	f, err := os.OpenFile(path, os.O_RDONLY|os.O_CREATE, 0o644)
	if err != nil {
		return false, fmt.Errorf("create db file: %w", err)
	}
	if err := f.Close(); err != nil {
		return true, fmt.Errorf("close db file: %w", err)
	}
	return true, nil
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable sqlite foreign keys: %w", err)
	}
	return &Store{
		path:     path,
		db:       db,
		migrator: migrations.New(),
	}, nil
}

func (s *Store) Path() string {
	return s.path
}

func (s *Store) DB() *sql.DB {
	return s.db
}

func (s *Store) Ping(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping sqlite db: %w", err)
	}
	return nil
}

func (s *Store) Migrate(ctx context.Context) ([]migrations.AppliedMigration, error) {
	return s.migrator.Apply(ctx, s.db)
}

func (s *Store) MigrationStatus(ctx context.Context) ([]migrations.Status, error) {
	return s.migrator.Status(ctx, s.db)
}

func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}
