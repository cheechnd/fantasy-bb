package migrations

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestApplyAndStatus(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "migrate.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	m := New()
	ctx := context.Background()

	applied, err := m.Apply(ctx, db)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(applied) == 0 {
		t.Fatal("expected at least one migration to apply")
	}

	status, err := m.Status(ctx, db)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(status) == 0 {
		t.Fatal("expected at least one migration status")
	}
	for _, st := range status {
		if !st.Applied {
			t.Fatalf("migration %d should be applied", st.Version)
		}
	}

	appliedAgain, err := m.Apply(ctx, db)
	if err != nil {
		t.Fatalf("Apply second call: %v", err)
	}
	if len(appliedAgain) != 0 {
		t.Fatalf("expected no migrations on second apply, got %d", len(appliedAgain))
	}
}
