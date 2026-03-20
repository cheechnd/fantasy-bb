package matching

import (
	"regexp"
	"sort"
	"strings"

	"fantasy-baseball/internal/forecaster"
	"fantasy-baseball/internal/pitchers"
)

type Candidate struct {
	NormalizedName string
	DisplayName    string
	Teams          map[string]struct{}
}

func BuildCandidates(starts []forecaster.ProbableStart) []Candidate {
	idx := map[string]*Candidate{}
	for _, s := range starts {
		if strings.TrimSpace(s.PitcherName) == "" {
			continue
		}
		key := NormalizeName(s.PitcherName)
		if key == "" {
			continue
		}
		c, ok := idx[key]
		if !ok {
			c = &Candidate{NormalizedName: key, DisplayName: strings.TrimSpace(s.PitcherName), Teams: map[string]struct{}{}}
			idx[key] = c
		}
		team := strings.ToUpper(strings.TrimSpace(s.Team))
		if team != "" {
			c.Teams[team] = struct{}{}
		}
	}

	out := make([]Candidate, 0, len(idx))
	for _, c := range idx {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].DisplayName < out[j].DisplayName
	})
	return out
}

func Match(name string, mlbTeam string, candidates []Candidate) pitchers.MatchResult {
	key := NormalizeName(name)
	result := pitchers.MatchResult{
		InputPlayerName:     name,
		InputMLBTeam:        strings.ToUpper(strings.TrimSpace(mlbTeam)),
		NormalizedLookupKey: key,
	}
	if key == "" {
		result.MatchStatus = pitchers.MatchStatusUnmatched
		result.Explanation = "empty normalized player name"
		return result
	}

	matching := make([]Candidate, 0)
	for _, c := range candidates {
		if c.NormalizedName == key {
			matching = append(matching, c)
		}
	}
	if len(matching) == 0 {
		result.MatchStatus = pitchers.MatchStatusUnmatched
		result.Explanation = "no normalized name match in probable starts"
		return result
	}
	if len(matching) == 1 {
		result.MatchStatus = pitchers.MatchStatusMatched
		result.MatchedPitcherName = matching[0].DisplayName
		result.MatchedPitcherTeam = firstTeam(matching[0].Teams)
		return result
	}

	if result.InputMLBTeam != "" {
		teamFiltered := make([]Candidate, 0)
		for _, c := range matching {
			if _, ok := c.Teams[result.InputMLBTeam]; ok {
				teamFiltered = append(teamFiltered, c)
			}
		}
		if len(teamFiltered) == 1 {
			result.MatchStatus = pitchers.MatchStatusMatched
			result.MatchedPitcherName = teamFiltered[0].DisplayName
			result.MatchedPitcherTeam = firstTeam(teamFiltered[0].Teams)
			result.Explanation = "matched using team tie-breaker"
			return result
		}
		if len(teamFiltered) > 1 {
			matching = teamFiltered
		}
	}

	result.MatchStatus = pitchers.MatchStatusAmbiguous
	result.Explanation = "multiple probable-start pitcher candidates"
	for _, c := range matching {
		result.CandidateDisplayList = append(result.CandidateDisplayList, c.DisplayName+" ["+firstTeam(c.Teams)+"]")
	}
	sort.Strings(result.CandidateDisplayList)
	return result
}

var nonWordRe = regexp.MustCompile(`[^a-z0-9\s]`)
var spaceRe = regexp.MustCompile(`\s+`)

func NormalizeName(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.ReplaceAll(s, "'", "")
	s = nonWordRe.ReplaceAllString(s, " ")
	s = spaceRe.ReplaceAllString(strings.TrimSpace(s), " ")
	return s
}

func firstTeam(teams map[string]struct{}) string {
	if len(teams) == 0 {
		return ""
	}
	list := make([]string, 0, len(teams))
	for t := range teams {
		list = append(list, t)
	}
	sort.Strings(list)
	return list[0]
}
