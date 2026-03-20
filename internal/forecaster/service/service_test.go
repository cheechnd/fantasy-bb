package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"fantasy-baseball/internal/forecaster"
	"fantasy-baseball/internal/store/sqlite"
)

func newTestService(t *testing.T) (*Service, *forecaster.Repository) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "fb.db")
	if _, err := sqlite.EnsureDatabaseFile(dbPath); err != nil {
		t.Fatalf("EnsureDatabaseFile: %v", err)
	}
	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("store.Migrate: %v", err)
	}
	repo := forecaster.NewRepository(store.DB())
	return New(repo), repo
}

func TestImportFromFileAndPersistence(t *testing.T) {
	svc, repo := newTestService(t)
	fixturePath := filepath.Join("..", "parser", "testdata", "edge_cases.html")

	summary, err := svc.ImportFromFile(context.Background(), fixturePath)
	if err != nil {
		t.Fatalf("ImportFromFile: %v", err)
	}
	if summary.ProbableStartCount != 4 {
		t.Fatalf("expected 4 probable starts, got %d", summary.ProbableStartCount)
	}
	if summary.WarningCount == 0 {
		t.Fatal("expected at least one warning from edge-case fixture")
	}

	rows, err := repo.ListProbableStarts(context.Background(), forecaster.ListFilter{IncludeTBD: true})
	if err != nil {
		t.Fatalf("ListProbableStarts: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("expected 4 persisted rows, got %d", len(rows))
	}

	runs, err := repo.SourceStatus(context.Background(), 10)
	if err != nil {
		t.Fatalf("SourceStatus: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 import run, got %d", len(runs))
	}
	if runs[0].WarningCount == 0 {
		t.Fatal("expected warning count in import run")
	}

	warnings, err := svc.Warnings(context.Background(), nil, 20)
	if err != nil {
		t.Fatalf("Warnings: %v", err)
	}
	if len(warnings) == 0 {
		t.Fatal("expected warnings rows to be queryable")
	}
}

func TestTopAndDateWindowFilters(t *testing.T) {
	svc, _ := newTestService(t)
	fixturePath := filepath.Join("..", "parser", "testdata", "forecaster_sample.html")
	if _, err := svc.ImportFromFile(context.Background(), fixturePath); err != nil {
		t.Fatalf("ImportFromFile: %v", err)
	}

	from := time.Date(time.Now().Year(), 9, 15, 0, 0, 0, 0, time.Local)
	to := from.AddDate(0, 0, 2)
	rows, err := svc.Top(context.Background(), forecaster.TopFilter{From: &from, To: &to, TopN: 5})
	if err != nil {
		t.Fatalf("Top: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected top rows")
	}
	for i := 1; i < len(rows); i++ {
		if rows[i-1].ProjectedFPTS != nil && rows[i].ProjectedFPTS != nil && *rows[i-1].ProjectedFPTS < *rows[i].ProjectedFPTS {
			t.Fatalf("rows not sorted by projected fpts desc")
		}
	}
}
