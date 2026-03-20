package matching

import (
	"testing"

	"fantasy-baseball/internal/forecaster"
	"fantasy-baseball/internal/pitchers"
)

func TestNormalizeName(t *testing.T) {
	got := NormalizeName("  Luis L. Ortiz ")
	if got != "luis l ortiz" {
		t.Fatalf("NormalizeName = %q", got)
	}
}

func TestMatchMatchedAndUnmatched(t *testing.T) {
	cands := []Candidate{{NormalizedName: "gerrit cole", DisplayName: "Gerrit Cole", Teams: map[string]struct{}{"NYY": {}}}}
	m := Match("gerrit cole", "", cands)
	if m.MatchStatus != pitchers.MatchStatusMatched {
		t.Fatalf("expected matched, got %s", m.MatchStatus)
	}
	u := Match("No Such Pitcher", "", cands)
	if u.MatchStatus != pitchers.MatchStatusUnmatched {
		t.Fatalf("expected unmatched, got %s", u.MatchStatus)
	}
}

func TestMatchAmbiguousThenTeamTiebreak(t *testing.T) {
	cands := []Candidate{
		{NormalizedName: "john smith", DisplayName: "John Smith", Teams: map[string]struct{}{"NYY": {}}},
		{NormalizedName: "john smith", DisplayName: "John Smith", Teams: map[string]struct{}{"LAD": {}}},
	}
	a := Match("John Smith", "", cands)
	if a.MatchStatus != pitchers.MatchStatusAmbiguous {
		t.Fatalf("expected ambiguous, got %s", a.MatchStatus)
	}
	m := Match("John Smith", "LAD", cands)
	if m.MatchStatus != pitchers.MatchStatusMatched || m.MatchedPitcherTeam != "LAD" {
		t.Fatalf("expected LAD tie-break match, got %+v", m)
	}
}

func TestBuildCandidates(t *testing.T) {
	starts := []forecaster.ProbableStart{{PitcherName: "Gerrit Cole", Team: "NYY"}, {PitcherName: "Gerrit Cole", Team: "NYY"}, {PitcherName: "", Team: "NYY"}}
	cands := BuildCandidates(starts)
	if len(cands) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(cands))
	}
}
