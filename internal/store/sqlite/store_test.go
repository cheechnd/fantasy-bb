package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureDatabaseFile(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "db", "fb.db")
	created, err := EnsureDatabaseFile(dbPath)
	if err != nil {
		t.Fatalf("EnsureDatabaseFile: %v", err)
	}
	if !created {
		t.Fatal("expected created=true on first call")
	}
	created, err = EnsureDatabaseFile(dbPath)
	if err != nil {
		t.Fatalf("EnsureDatabaseFile second call: %v", err)
	}
	if created {
		t.Fatal("expected created=false on second call")
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("db file missing: %v", err)
	}
}

func TestOpenPingAndMigrate(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "fb.db")
	if _, err := EnsureDatabaseFile(dbPath); err != nil {
		t.Fatalf("EnsureDatabaseFile: %v", err)
	}

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	if err := s.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	applied, err := s.Migrate(ctx)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if len(applied) == 0 {
		t.Fatal("expected at least one applied migration")
	}
}
