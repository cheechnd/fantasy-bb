package relievers

import "testing"

func TestParseDepthChartHTMLJacobLatzCloser(t *testing.T) {
	raw := []byte(`<html><head><meta name="DC.date.issued" content="2026-08-22T11:46:00Z"></head><body>
` + manyTeams() + `<p><img class="floatleft" src="x"> <b><a href="/mlb/team/_/name/tex/texas-rangers">TEXAS RANGERS</a></b><br />
<b>Closer:</b> <b><i><a href="https://www.espn.com/mlb/player/_/id/3210625/jacob-latz">Jacob Latz</a></i></b> (60.0%)<br />
<b>Primary setup:</b> <a href="https://www.espn.com/mlb/player/_/id/4413990/chase-silseth">Chase Silseth</a> (0.2%)<br />
<b>Secondary setup:</b> <a href="https://www.espn.com/mlb/player/_/id/36056/jakob-junis">Jakob Junis</a> (2.7%)<br />
<b>Middle relief:</b> <b><i><a href="https://www.espn.com/mlb/player/_/id/4187628/peyton-gray">Peyton Gray</a></i></b> (0.1%)</p>
<p><img class="floatleft" src="x"> <b><a href="/mlb/team/_/name/sd/san-diego-padres">SAN DIEGO PADRES</a></b><br />
<b>Closer:</b> <a href="https://www.espn.com/mlb/player/_/id/4730225/mason-miller">Mason Miller</a> (99.5%)<br />
<b>Primary setup:</b> <a href="https://www.espn.com/mlb/player/_/id/40917/adrian-morejon">Adrian Morejon</a> (17.7%)<br />
<b>Secondary setup:</b> <a href="https://www.espn.com/mlb/player/_/id/41014/jeremiah-estrada">Jeremiah Estrada</a> (7.0%)<br />
<b>Sleeper:</b> <a href="https://www.espn.com/mlb/player/_/id/5288685/bradgley-rodriguez">Bradgley Rodriguez</a> (1.2%)</p>
</body></html>`)
	parsed, err := parseDepthChartHTML(raw)
	if err != nil {
		t.Fatalf("parseDepthChartHTML: %v", err)
	}
	var latz *DepthChartEntry
	for i := range parsed.Rows {
		if parsed.Rows[i].PlayerName == "Jacob Latz" {
			latz = &parsed.Rows[i]
			break
		}
	}
	if latz == nil {
		t.Fatal("Jacob Latz row not found")
	}
	if latz.ReliefRole != "closer" {
		t.Fatalf("expected closer, got %q", latz.ReliefRole)
	}
	if latz.MLBTeam != "TEX" {
		t.Fatalf("expected TEX, got %q", latz.MLBTeam)
	}
	if latz.ESPNPlayerID == nil || *latz.ESPNPlayerID != 3210625 {
		t.Fatalf("expected ESPN player id 3210625, got %#v", latz.ESPNPlayerID)
	}
	if latz.RosterPercent == nil || *latz.RosterPercent != 60.0 {
		t.Fatalf("expected roster percent 60.0, got %#v", latz.RosterPercent)
	}
	var gray *DepthChartEntry
	for i := range parsed.Rows {
		if parsed.Rows[i].PlayerName == "Peyton Gray" {
			gray = &parsed.Rows[i]
			break
		}
	}
	if gray == nil || gray.ReliefRole != "middle_relief" {
		t.Fatalf("expected wrapped middle relief row for Peyton Gray, got %#v", gray)
	}
	var sleeper *DepthChartEntry
	for i := range parsed.Rows {
		if parsed.Rows[i].PlayerName == "Bradgley Rodriguez" {
			sleeper = &parsed.Rows[i]
			break
		}
	}
	if sleeper == nil || sleeper.ReliefRole != "sleeper" || sleeper.MLBTeam != "SD" {
		t.Fatalf("expected sleeper row for Bradgley Rodriguez, got %#v", sleeper)
	}
	if parsed.SourceDate != "2026-08-22T11:46:00Z" {
		t.Fatalf("expected source date, got %q", parsed.SourceDate)
	}
	if parsed.Teams != 30 {
		t.Fatalf("expected 30 distinct teams, got %d", parsed.Teams)
	}
	var athleticsCloser *DepthChartEntry
	for i := range parsed.Rows {
		if parsed.Rows[i].PlayerName == "A One" && parsed.Rows[i].MLBTeam == "OAK" {
			athleticsCloser = &parsed.Rows[i]
			break
		}
	}
	if athleticsCloser == nil {
		t.Fatal("THE ATHLETICS heading did not resolve to OAK")
	}
}

func TestParseDepthChartHTMLFailsLowCoverage(t *testing.T) {
	raw := []byte(`<html><body><p><b><a href="/mlb/team/_/name/tex/texas-rangers">TEXAS RANGERS</a></b><br />
<b>Closer:</b> <a href="https://www.espn.com/mlb/player/_/id/3210625/jacob-latz">Jacob Latz</a> (60.0%)</p></body></html>`)
	_, err := parseDepthChartHTML(raw)
	if err == nil {
		t.Fatal("expected low coverage parse failure")
	}
}

func manyTeams() string {
	teams := []string{"ARIZONA DIAMONDBACKS", "ATLANTA BRAVES", "BALTIMORE ORIOLES", "BOSTON RED SOX", "CHICAGO CUBS", "CHICAGO WHITE SOX", "CINCINNATI REDS", "CLEVELAND GUARDIANS", "COLORADO ROCKIES", "DETROIT TIGERS", "HOUSTON ASTROS", "KANSAS CITY ROYALS", "LOS ANGELES ANGELS", "LOS ANGELES DODGERS", "MIAMI MARLINS", "MILWAUKEE BREWERS", "MINNESOTA TWINS", "NEW YORK METS", "NEW YORK YANKEES", "THE ATHLETICS", "PHILADELPHIA PHILLIES", "PITTSBURGH PIRATES", "SAN DIEGO PADRES", "SAN FRANCISCO GIANTS", "SEATTLE MARINERS", "ST. LOUIS CARDINALS", "TAMPA BAY RAYS", "TORONTO BLUE JAYS", "WASHINGTON NATIONALS"}
	out := ""
	for _, team := range teams {
		out += `<p><b><a href="/mlb/team/_/name/x/x">` + team + `</a></b><br />` +
			`<b>Closer:</b> <a href="https://www.espn.com/mlb/player/_/id/1/a">A One</a> (1.0%)<br />` +
			`<b>Primary setup:</b> <a href="https://www.espn.com/mlb/player/_/id/2/b">B Two</a> (1.0%)<br />` +
			`<b>Secondary setup:</b> <a href="https://www.espn.com/mlb/player/_/id/3/c">C Three</a> (1.0%)</p>`
	}
	return out
}
