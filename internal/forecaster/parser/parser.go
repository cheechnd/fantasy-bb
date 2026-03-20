package parser

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"fantasy-baseball/internal/forecaster"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
)

type Result struct {
	RawRowCount int
	Starts      []forecaster.ProbableStartInput
	Warnings    []forecaster.ParseWarningInput
}

type ForecasterParser struct {
	now func() time.Time
}

func New() *ForecasterParser {
	return &ForecasterParser{now: time.Now}
}

func (p *ForecasterParser) Parse(raw []byte) (Result, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(raw))
	if err != nil {
		return Result{}, fmt.Errorf("parse html: %w", err)
	}
	table := findForecasterTable(doc)
	if table.Length() == 0 {
		return Result{}, fmt.Errorf("no usable forecaster table found")
	}

	result := Result{}
	currentYear := p.now().Year()
	anchor := p.now()

	table.Find("tbody tr").Each(func(_ int, tr *goquery.Selection) {
		tds := tr.Find("td")
		if tds.Length() < 6 {
			return
		}

		team := normalizeTeamCell(tds.Eq(0))
		dateList := splitByBR(tds.Eq(1))
		oppList := splitByBR(tds.Eq(2))
		pitcherList := splitByBR(tds.Eq(3))
		handList := splitByBR(tds.Eq(4))
		fptsList := splitByBR(tds.Eq(5))
		if team == "" || len(dateList) == 0 {
			return
		}

		result.RawRowCount++

		oppList = removeGameMarkerOpps(oppList, &result, team)
		for i := 0; i < len(dateList); i++ {
			dateRaw := cleanText(dateList[i])
			opp := cleanOpponent(valueAt(oppList, i))

			if opp == "OFF" {
				if cleanText(valueAt(pitcherList, i)) != "" {
					pitcherList = insertBlankAt(pitcherList, i)
				}
				if cleanText(valueAt(handList, i)) != "" {
					handList = insertBlankAt(handList, i)
				}
				if cleanText(valueAt(fptsList, i)) != "" {
					fptsList = insertBlankAt(fptsList, i)
				}
			}
			pitcher := cleanText(valueAt(pitcherList, i))
			if strings.EqualFold(pitcher, "TBD") {
				if cleanText(valueAt(handList, i)) != "" {
					handList = insertBlankAt(handList, i)
				}
				if cleanText(valueAt(fptsList, i)) != "" {
					fptsList = insertBlankAt(fptsList, i)
				}
			}

			throws := normalizeThrows(valueAt(handList, i))
			fpts, fptsErr := parseFPTS(valueAt(fptsList, i))
			if fptsErr != nil {
				result.Warnings = append(result.Warnings, forecaster.ParseWarningInput{
					WarningType: "fpts_parse",
					Message:     fptsErr.Error(),
					RowContext: map[string]interface{}{
						"team": team,
						"date": dateRaw,
						"raw":  valueAt(fptsList, i),
					},
				})
			}

			gameDate, dateErr := parseGameDate(dateRaw, currentYear, anchor)
			if dateErr != nil {
				result.Warnings = append(result.Warnings, forecaster.ParseWarningInput{
					WarningType: "date_parse",
					Message:     dateErr.Error(),
					RowContext: map[string]interface{}{
						"team": team,
						"date": dateRaw,
					},
				})
			}

			status := deriveStatus(opp, pitcher)
			if status == forecaster.StatusOff {
				throws = ""
				fpts = nil
				pitcher = ""
			}

			result.Starts = append(result.Starts, forecaster.ProbableStartInput{
				SourceDateRaw: dateRaw,
				GameDate:      gameDate,
				Team:          team,
				Opponent:      normalizeOpponentForStorage(opp),
				PitcherName:   normalizePitcher(pitcher),
				ThrowsHand:    throws,
				ProjectedFPTS: fpts,
				Status:        status,
				RawFields: map[string]interface{}{
					"team":    team,
					"date":    dateRaw,
					"opp":     opp,
					"pitcher": pitcher,
					"throws":  valueAt(handList, i),
					"fpts":    valueAt(fptsList, i),
				},
			})
		}
	})

	if len(result.Starts) == 0 {
		return Result{}, fmt.Errorf("no probable starts found in forecaster table")
	}
	return result, nil
}

func findForecasterTable(doc *goquery.Document) *goquery.Selection {
	var found *goquery.Selection
	doc.Find("table").EachWithBreak(func(_ int, table *goquery.Selection) bool {
		headers := strings.ToUpper(cleanText(table.Find("th").Text()))
		if strings.Contains(headers, "TEAM") && strings.Contains(headers, "DATE") && strings.Contains(headers, "OPP") && strings.Contains(headers, "PITCHER") && strings.Contains(headers, "FPTS") {
			found = table
			return false
		}
		return true
	})
	if found == nil {
		return doc.Find("table").First()
	}
	return found
}

func splitByBR(sel *goquery.Selection) []string {
	if sel.Length() == 0 || len(sel.Nodes) == 0 {
		return nil
	}
	root := sel.Nodes[0]
	var parts []string
	var current strings.Builder
	var walk func(*html.Node)

	flush := func() {
		parts = append(parts, cleanText(current.String()))
		current.Reset()
	}

	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "br" {
			flush()
			return
		}
		if n.Type == html.TextNode {
			current.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}

	for c := root.FirstChild; c != nil; c = c.NextSibling {
		walk(c)
	}
	flush()
	return parts
}

var (
	teamCodeRe   = regexp.MustCompile(`/500/([a-z]{2,3})\.png`)
	gameMarkerRe = regexp.MustCompile(`(?i)\s*Gm\.\s*\d+`)
)

func normalizeTeamCell(sel *goquery.Selection) string {
	img, ok := sel.Find("img").Attr("src")
	if ok {
		m := teamCodeRe.FindStringSubmatch(img)
		if len(m) == 2 {
			return strings.ToUpper(m[1])
		}
	}
	text := strings.ToUpper(cleanText(sel.Text()))
	if len(text) <= 4 {
		return text
	}
	return ""
}

func cleanText(v string) string {
	v = strings.ReplaceAll(v, "\u00a0", " ")
	v = strings.ReplaceAll(v, "\n", " ")
	return strings.TrimSpace(strings.Join(strings.Fields(v), " "))
}

func valueAt(values []string, idx int) string {
	if idx < 0 || idx >= len(values) {
		return ""
	}
	return values[idx]
}

func insertBlankAt(values []string, idx int) []string {
	if idx < 0 {
		idx = 0
	}
	if idx > len(values) {
		idx = len(values)
	}
	values = append(values, "")
	copy(values[idx+1:], values[idx:])
	values[idx] = ""
	return values
}

func removeGameMarkerOpps(opps []string, result *Result, team string) []string {
	cleaned := make([]string, 0, len(opps))
	for _, raw := range opps {
		opp := cleanText(raw)
		if strings.Contains(strings.ToUpper(opp), "GM.") {
			opp = strings.TrimSpace(gameMarkerRe.ReplaceAllString(opp, ""))
			result.Warnings = append(result.Warnings, forecaster.ParseWarningInput{
				WarningType: "opp_game_marker",
				Message:     "cleaned game-marker opponent artifact",
				RowContext:  map[string]interface{}{"team": team, "raw": raw, "cleaned": opp},
			})
		}
		cleaned = append(cleaned, opp)
	}
	return cleaned
}

func cleanOpponent(opp string) string {
	opp = strings.ToUpper(cleanText(opp))
	opp = strings.ReplaceAll(opp, "*", "")
	opp = strings.ReplaceAll(opp, "G1", "")
	opp = strings.ReplaceAll(opp, "G2", "")
	opp = strings.TrimSpace(opp)
	if strings.HasSuffix(opp, "/") {
		opp = strings.TrimSuffix(opp, "/")
	}
	return opp
}

func normalizeOpponentForStorage(opp string) string {
	if opp == "" {
		return "UNK"
	}
	return opp
}

func normalizePitcher(name string) string {
	name = cleanText(name)
	if strings.EqualFold(name, "OFF") {
		return ""
	}
	if name == "" {
		return ""
	}
	return name
}

func normalizeThrows(v string) string {
	v = strings.ToUpper(cleanText(v))
	switch v {
	case "L", "R":
		return v
	default:
		return ""
	}
}

func parseFPTS(raw string) (*float64, error) {
	raw = cleanText(raw)
	if raw == "" || strings.EqualFold(raw, "N/A") {
		return nil, nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil, fmt.Errorf("unable to parse projected fpts %q", raw)
	}
	return &v, nil
}

func deriveStatus(opp string, pitcher string) forecaster.Status {
	opp = strings.ToUpper(cleanText(opp))
	pitcher = strings.ToUpper(cleanText(pitcher))
	if opp == "OFF" {
		return forecaster.StatusOff
	}
	if pitcher == "TBD" {
		return forecaster.StatusTBD
	}
	if pitcher != "" {
		return forecaster.StatusScheduled
	}
	if opp != "" {
		return forecaster.StatusUnknown
	}
	return forecaster.StatusUnknown
}

func parseGameDate(raw string, year int, anchor time.Time) (*time.Time, error) {
	raw = cleanText(raw)
	if raw == "" {
		return nil, nil
	}
	for _, layout := range []string{"Mon, 1/2", "Mon 1/2", "1/2", "01/02"} {
		if t, err := time.Parse(layout, raw); err == nil {
			candidate := time.Date(year, t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
			if int(t.Month()) <= 2 && int(anchor.Month()) >= 11 {
				candidate = candidate.AddDate(1, 0, 0)
			}
			if int(t.Month()) >= 11 && int(anchor.Month()) <= 2 {
				candidate = candidate.AddDate(-1, 0, 0)
			}
			return &candidate, nil
		}
	}
	return nil, fmt.Errorf("unable to parse date %q", raw)
}
