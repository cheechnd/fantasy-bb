package parser

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"fantasy-baseball/internal/forecaster"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("testdata", name)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func TestParseRealFixtureProducesRows(t *testing.T) {
	p := New()
	result, err := p.Parse(readFixture(t, "forecaster_sample.html"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if result.RawRowCount == 0 {
		t.Fatal("expected at least one team row")
	}
	if len(result.Starts) < 200 {
		t.Fatalf("expected many starts from sample fixture, got %d", len(result.Starts))
	}

	offCount := 0
	scheduledCount := 0
	for _, s := range result.Starts {
		if s.Status == forecaster.StatusOff {
			offCount++
		}
		if s.Status == forecaster.StatusScheduled {
			scheduledCount++
		}
	}
	if offCount == 0 {
		t.Fatal("expected OFF rows in real fixture")
	}
	if scheduledCount == 0 {
		t.Fatal("expected scheduled rows in real fixture")
	}
}

func TestParseEdgeCasesHandlesOFFTBDAndMarkers(t *testing.T) {
	p := New()
	p.now = func() time.Time { return time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local) }

	result, err := p.Parse(readFixture(t, "edge_cases.html"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(result.Starts) != 4 {
		t.Fatalf("expected 4 starts, got %d", len(result.Starts))
	}

	if result.Starts[1].Status != forecaster.StatusOff {
		t.Fatalf("expected second row to be OFF, got %s", result.Starts[1].Status)
	}
	if result.Starts[1].PitcherName != "" {
		t.Fatalf("expected OFF row to blank pitcher, got %q", result.Starts[1].PitcherName)
	}
	if result.Starts[2].Status != forecaster.StatusTBD {
		t.Fatalf("expected third row to be TBD, got %s", result.Starts[2].Status)
	}
	if result.Starts[2].ThrowsHand != "" {
		t.Fatalf("expected TBD row throws blank, got %q", result.Starts[2].ThrowsHand)
	}
	if result.Starts[0].ProjectedFPTS == nil || *result.Starts[0].ProjectedFPTS != 12.5 {
		t.Fatalf("expected first row fpts 12.5, got %+v", result.Starts[0].ProjectedFPTS)
	}
	if result.Starts[3].ProjectedFPTS == nil || *result.Starts[3].ProjectedFPTS != 11.1 {
		t.Fatalf("expected fourth row fpts 11.1, got %+v", result.Starts[3].ProjectedFPTS)
	}

	markerWarning := false
	for _, w := range result.Warnings {
		if w.WarningType == "opp_game_marker" {
			markerWarning = true
			break
		}
	}
	if !markerWarning {
		t.Fatal("expected warning for removed game marker")
	}

	if result.Starts[0].GameDate == nil || result.Starts[0].GameDate.Format("2006-01-02") != "2026-09-15" {
		t.Fatalf("expected normalized date 2026-09-15, got %+v", result.Starts[0].GameDate)
	}
}
