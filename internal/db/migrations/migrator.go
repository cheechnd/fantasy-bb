package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed files/*.sql
var migrationFiles embed.FS

type AppliedMigration struct {
	Version int       `json:"version"`
	Name    string    `json:"name"`
	At      time.Time `json:"applied_at"`
}

type Status struct {
	Version   int        `json:"version"`
	Name      string     `json:"name"`
	Applied   bool       `json:"applied"`
	AppliedAt *time.Time `json:"applied_at,omitempty"`
}

type Migrator struct {
	fs fs.FS
}

func New() *Migrator {
	return &Migrator{fs: migrationFiles}
}

func (m *Migrator) Apply(ctx context.Context, db *sql.DB) ([]AppliedMigration, error) {
	if err := ensureTable(ctx, db); err != nil {
		return nil, err
	}

	files, err := m.listFiles()
	if err != nil {
		return nil, err
	}

	applied, err := appliedVersions(ctx, db)
	if err != nil {
		return nil, err
	}

	results := make([]AppliedMigration, 0)
	for _, mf := range files {
		if _, ok := applied[mf.Version]; ok {
			continue
		}

		sqlBytes, err := fs.ReadFile(m.fs, mf.Path)
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", mf.Path, err)
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("start tx for migration %d: %w", mf.Version, err)
		}

		if _, err := tx.ExecContext(ctx, string(sqlBytes)); err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("apply migration %d (%s): %w", mf.Version, mf.Name, err)
		}

		now := time.Now().UTC()
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO schema_migrations(version, name, applied_at)
			VALUES (?, ?, ?)
		`, mf.Version, mf.Name, now.Format(time.RFC3339)); err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("record migration %d: %w", mf.Version, err)
		}

		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit migration %d: %w", mf.Version, err)
		}

		results = append(results, AppliedMigration{
			Version: mf.Version,
			Name:    mf.Name,
			At:      now,
		})
	}

	return results, nil
}

func (m *Migrator) Status(ctx context.Context, db *sql.DB) ([]Status, error) {
	if err := ensureTable(ctx, db); err != nil {
		return nil, err
	}
	files, err := m.listFiles()
	if err != nil {
		return nil, err
	}
	applied, err := appliedVersions(ctx, db)
	if err != nil {
		return nil, err
	}

	out := make([]Status, 0, len(files))
	for _, mf := range files {
		s := Status{Version: mf.Version, Name: mf.Name}
		if at, ok := applied[mf.Version]; ok {
			tm := at
			s.Applied = true
			s.AppliedAt = &tm
		}
		out = append(out, s)
	}
	return out, nil
}

type migrationFile struct {
	Version int
	Name    string
	Path    string
}

func (m *Migrator) listFiles() ([]migrationFile, error) {
	entries, err := fs.ReadDir(m.fs, "files")
	if err != nil {
		return nil, fmt.Errorf("read migration directory: %w", err)
	}

	out := make([]migrationFile, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".sql" {
			continue
		}
		version, name, err := parseName(e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, migrationFile{
			Version: version,
			Name:    name,
			Path:    filepath.ToSlash(filepath.Join("files", e.Name())),
		})
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].Version < out[j].Version
	})

	return out, nil
}

func parseName(filename string) (int, string, error) {
	parts := strings.SplitN(strings.TrimSuffix(filename, ".sql"), "_", 2)
	if len(parts) != 2 {
		return 0, "", fmt.Errorf("invalid migration file name %q (expected NNNN_name.sql)", filename)
	}
	version, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, "", fmt.Errorf("invalid migration version in %q: %w", filename, err)
	}
	return version, parts[1], nil
}

func ensureTable(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at TEXT NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("ensure schema_migrations table: %w", err)
	}
	return nil
}

func appliedVersions(ctx context.Context, db *sql.DB) (map[int]time.Time, error) {
	rows, err := db.QueryContext(ctx, `SELECT version, applied_at FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("query applied migrations: %w", err)
	}
	defer rows.Close()

	out := map[int]time.Time{}
	for rows.Next() {
		var version int
		var appliedAtRaw string
		if err := rows.Scan(&version, &appliedAtRaw); err != nil {
			return nil, fmt.Errorf("scan applied migration row: %w", err)
		}
		appliedAt, err := time.Parse(time.RFC3339, appliedAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse applied_at for migration %d: %w", version, err)
		}
		out[version] = appliedAt
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate applied migrations: %w", err)
	}
	return out, nil
}
