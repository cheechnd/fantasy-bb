package relievers

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"fantasy-baseball/internal/pitchers/matching"

	"github.com/PuerkitoBio/goquery"
)

type parsedChart struct {
	SourceDate string
	Teams      int
	Rows       []DepthChartEntry
	Warnings   []string
}

var (
	issuedMetaRe = regexp.MustCompile(`(?i)<meta\s+name=["']DC\.date\.issued["']\s+content=["']([^"']+)`)
	playerIDRe   = regexp.MustCompile(`/id/(\d+)/`)
	pctRe        = regexp.MustCompile(`\(([0-9]+(?:\.[0-9]+)?)%\)`)
	roleLabels   = map[string]string{
		"closer":              "closer",
		"closer-by-committee": "closer_by_committee",
		"primary setup":       "primary_setup",
		"secondary setup":     "secondary_setup",
		"middle relief":       "middle_relief",
		"injured list":        "injured_list",
	}
	teamNameToCode = map[string]string{
		"ARIZONA DIAMONDBACKS":  "ARI",
		"ATLANTA BRAVES":        "ATL",
		"BALTIMORE ORIOLES":     "BAL",
		"BOSTON RED SOX":        "BOS",
		"CHICAGO CUBS":          "CHC",
		"CHICAGO WHITE SOX":     "CHW",
		"CINCINNATI REDS":       "CIN",
		"CLEVELAND GUARDIANS":   "CLE",
		"COLORADO ROCKIES":      "COL",
		"DETROIT TIGERS":        "DET",
		"HOUSTON ASTROS":        "HOU",
		"KANSAS CITY ROYALS":    "KC",
		"LOS ANGELES ANGELS":    "LAA",
		"LOS ANGELES DODGERS":   "LAD",
		"MIAMI MARLINS":         "MIA",
		"MILWAUKEE BREWERS":     "MIL",
		"MINNESOTA TWINS":       "MIN",
		"NEW YORK METS":         "NYM",
		"NEW YORK YANKEES":      "NYY",
		"ATHLETICS":             "OAK",
		"THE ATHLETICS":         "OAK",
		"OAKLAND ATHLETICS":     "OAK",
		"PHILADELPHIA PHILLIES": "PHI",
		"PITTSBURGH PIRATES":    "PIT",
		"SAN DIEGO PADRES":      "SD",
		"SAN FRANCISCO GIANTS":  "SF",
		"SEATTLE MARINERS":      "SEA",
		"ST. LOUIS CARDINALS":   "STL",
		"ST LOUIS CARDINALS":    "STL",
		"TAMPA BAY RAYS":        "TB",
		"TEXAS RANGERS":         "TEX",
		"TORONTO BLUE JAYS":     "TOR",
		"WASHINGTON NATIONALS":  "WSH",
	}
)

func parseDepthChartHTML(raw []byte) (parsedChart, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(raw))
	if err != nil {
		return parsedChart{}, fmt.Errorf("parse reliever depth chart html: %w", err)
	}
	out := parsedChart{SourceDate: sourceDate(raw)}
	seenTeams := map[string]struct{}{}
	doc.Find("p").Each(func(_ int, p *goquery.Selection) {
		teamCode := ""
		p.Find("a[href*='/mlb/team/_/name/']").EachWithBreak(func(_ int, a *goquery.Selection) bool {
			name := strings.ToUpper(strings.TrimSpace(a.Text()))
			if code := teamNameToCode[name]; code != "" {
				teamCode = code
				return false
			}
			return true
		})
		if teamCode == "" {
			return
		}
		seenTeams[teamCode] = struct{}{}
		p.Find("b").Each(func(_ int, b *goquery.Selection) {
			label := strings.TrimSuffix(strings.TrimSpace(b.Text()), ":")
			role := roleLabels[strings.ToLower(label)]
			if role == "" {
				return
			}
			a := b.NextFiltered("a")
			if a.Length() == 0 {
				return
			}
			name := strings.TrimSpace(a.Text())
			if name == "" {
				return
			}
			href, _ := a.Attr("href")
			var playerID *int64
			if m := playerIDRe.FindStringSubmatch(href); len(m) == 2 {
				if id, err := strconv.ParseInt(m[1], 10, 64); err == nil {
					playerID = &id
				}
			}
			var pct *float64
			if parentText := p.Text(); parentText != "" {
				idx := strings.Index(parentText, name)
				if idx >= 0 {
					tail := parentText[idx+len(name):]
					if m := pctRe.FindStringSubmatch(tail); len(m) == 2 {
						if v, err := strconv.ParseFloat(m[1], 64); err == nil {
							pct = &v
						}
					}
				}
			}
			out.Rows = append(out.Rows, DepthChartEntry{
				ESPNPlayerID:    playerID,
				PlayerName:      name,
				NormalizedName:  matching.NormalizeName(name),
				MLBTeam:         teamCode,
				ReliefRole:      role,
				SourceRoleLabel: label,
				RosterPercent:   pct,
				MatchStatus:     "unmatched",
				MatchReason:     "not matched against latest roster/free-agent snapshot",
			})
		})
	})
	out.Teams = len(seenTeams)
	if out.Teams != 30 || len(out.Rows) < 80 {
		return out, fmt.Errorf("reliever depth chart parse coverage too low: teams=%d rows=%d", out.Teams, len(out.Rows))
	}
	return out, nil
}

func sourceDate(raw []byte) string {
	if m := issuedMetaRe.FindSubmatch(raw); len(m) == 2 {
		if t, err := time.Parse(time.RFC3339, string(m[1])); err == nil {
			return t.UTC().Format(time.RFC3339)
		}
		return string(m[1])
	}
	return ""
}
