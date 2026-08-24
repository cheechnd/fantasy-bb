package main

import "testing"

func TestProjectionStartsFromRawNormalizesAndOrdersStarts(t *testing.T) {
	got := projectionStartsFromRaw([]interface{}{
		map[string]interface{}{
			"game_date":      "2026-08-30",
			"opponent":       "@NYY",
			"projected_fpts": "10.3",
			"status":         "projected",
		},
		map[string]interface{}{
			"game_date":      "2026-08-24",
			"opponent":       "MIA",
			"projected_fpts": 10.5,
			"status":         "confirmed",
		},
	})

	if len(got) != 2 {
		t.Fatalf("expected 2 starts, got %d", len(got))
	}
	if got[0].GameDate != "2026-08-24" || got[0].Opponent != "MIA" || got[0].HomeAway != "home" || got[0].Status != "confirmed" {
		t.Fatalf("unexpected first start: %#v", got[0])
	}
	if got[0].ProjectedFPTS == nil || *got[0].ProjectedFPTS != 10.5 {
		t.Fatalf("unexpected first projected fpts: %#v", got[0].ProjectedFPTS)
	}
	if got[1].GameDate != "2026-08-30" || got[1].Opponent != "NYY" || got[1].HomeAway != "away" || got[1].Status != "projected" {
		t.Fatalf("unexpected second start: %#v", got[1])
	}
	if got[1].ProjectedFPTS == nil || *got[1].ProjectedFPTS != 10.3 {
		t.Fatalf("unexpected second projected fpts: %#v", got[1].ProjectedFPTS)
	}
}

func TestProjectionStartsFromRawReturnsEmptySliceForNoStarts(t *testing.T) {
	got := projectionStartsFromRaw(nil)
	if got == nil {
		t.Fatal("expected empty non-nil slice")
	}
	if len(got) != 0 {
		t.Fatalf("expected no starts, got %d", len(got))
	}
}
