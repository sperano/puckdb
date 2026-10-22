package store

import "encoding/xml"

// ============================================================================
// LEAGUE STANDINGS TYPES
// The <standings> element of the Yahoo league standings sub-resource
// (league/{league_key}/standings). Values stay as Yahoo's text; the resource
// package converts and validates them (resource.TeamStandingRecords).
// ============================================================================

// Standings lists the league's teams in rank order, each carrying its
// team_standings.
type Standings struct {
	XMLName xml.Name       `xml:"standings"`
	Teams   StandingsTeams `xml:"teams"`
}

// StandingsTeams is the ranked teams collection of a standings document.
type StandingsTeams struct {
	XMLName xml.Name `xml:"teams"`
	Count   int      `xml:"count,attr"`
	Slice   []Team   `xml:"team"`
}

// TeamStandings is a team's place in its league: its rank and, in
// head-to-head leagues, its record. Yahoo serves an empty rank before the
// season's first game and percentages such as ".571".
type TeamStandings struct {
	XMLName     xml.Name `xml:"team_standings"`
	Rank        string   `xml:"rank"`
	PlayoffSeed string   `xml:"playoff_seed"`
	// OutcomeTotals is nil in leagues without a head-to-head record
	// (rotisserie).
	OutcomeTotals *OutcomeTotals `xml:"outcome_totals"`
	// DivisionalOutcomeTotals is the record against division opponents; nil
	// in leagues without divisions.
	DivisionalOutcomeTotals *OutcomeTotals `xml:"divisional_outcome_totals"`
	Streak                  *Streak        `xml:"streak"`
	PointsFor               string         `xml:"points_for"`
	PointsAgainst           string         `xml:"points_against"`
	PointsChange            string         `xml:"points_change"`
	PointsBack              string         `xml:"points_back"`
}

// OutcomeTotals is a head-to-head record. It carries no XMLName because the
// same shape serves <outcome_totals> and <divisional_outcome_totals>.
type OutcomeTotals struct {
	Wins       string `xml:"wins"`
	Losses     string `xml:"losses"`
	Ties       string `xml:"ties"`
	Percentage string `xml:"percentage"`
}

// Streak is a team's current run of results ("win" or "loss", and its length).
type Streak struct {
	XMLName xml.Name `xml:"streak"`
	Type    string   `xml:"type"`
	Value   string   `xml:"value"`
}
