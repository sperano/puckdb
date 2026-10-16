package matching

import "strings"

// yahooTeam is how Yahoo names one NHL team. yahooTeams is the only table
// of Yahoo team names: the Yahoo-ID matcher reads the display names
// (yahooTeamName) and the news resolver the abbreviations
// (NHLAbbrevForYahooTeam). NHL IDs and full names stay in teamsByAbbrev,
// which every nhlAbbrev must be a key of (TestYahooTeamsMatchNHLTeams).
type yahooTeam struct {
	// nhlAbbrev is the team's NHL abbreviation.
	nhlAbbrev string
	// name is the team as Yahoo Sports player page titles show it.
	name string
	// yahooAbbrevs lists Yahoo's abbreviations of the team where they differ
	// from nhlAbbrev.
	yahooAbbrevs []string
	// relocatedTo is the NHL abbreviation the franchise plays under now;
	// empty for a current team. The relocated team keeps its own row for
	// player pages that still show it, while its abbreviation resolves to
	// relocatedTo: player news is about the current season.
	relocatedTo string
}

// nhlClubCount is the number of current NHL teams, one yahooTeams row each.
const nhlClubCount = 32

var yahooTeams = []yahooTeam{
	// Atlantic Division
	{nhlAbbrev: "BOS", name: "Boston"},
	{nhlAbbrev: "BUF", name: "Buffalo"},
	{nhlAbbrev: "DET", name: "Detroit"},
	{nhlAbbrev: "FLA", name: "Florida"},
	{nhlAbbrev: "MTL", name: "Montreal", yahooAbbrevs: []string{"MON"}},
	{nhlAbbrev: "OTT", name: "Ottawa"},
	{nhlAbbrev: "TBL", name: "Tampa Bay", yahooAbbrevs: []string{"TB"}},
	{nhlAbbrev: "TOR", name: "Toronto"},

	// Metropolitan Division
	{nhlAbbrev: "CAR", name: "Carolina"},
	{nhlAbbrev: "CBJ", name: "Columbus", yahooAbbrevs: []string{"CLS"}},
	{nhlAbbrev: "NJD", name: "New Jersey", yahooAbbrevs: []string{"NJ"}},
	{nhlAbbrev: "NYI", name: "NY Islanders"},
	{nhlAbbrev: "NYR", name: "NY Rangers"},
	{nhlAbbrev: "PHI", name: "Philadelphia"},
	{nhlAbbrev: "PIT", name: "Pittsburgh"},
	{nhlAbbrev: "WSH", name: "Washington", yahooAbbrevs: []string{"WAS"}},

	// Central Division
	{nhlAbbrev: "CHI", name: "Chicago"},
	{nhlAbbrev: "COL", name: "Colorado"},
	{nhlAbbrev: "DAL", name: "Dallas"},
	{nhlAbbrev: "MIN", name: "Minnesota"},
	{nhlAbbrev: "NSH", name: "Nashville", yahooAbbrevs: []string{"NAS"}},
	{nhlAbbrev: "STL", name: "St. Louis"},
	{nhlAbbrev: "UTA", name: "Utah", yahooAbbrevs: []string{"UTAH"}},
	{nhlAbbrev: "WPG", name: "Winnipeg"},

	// Pacific Division
	{nhlAbbrev: "ANA", name: "Anaheim"},
	{nhlAbbrev: "CGY", name: "Calgary", yahooAbbrevs: []string{"CAL"}},
	{nhlAbbrev: "EDM", name: "Edmonton"},
	{nhlAbbrev: "LAK", name: "Los Angeles", yahooAbbrevs: []string{"LA"}},
	{nhlAbbrev: "SEA", name: "Seattle"},
	{nhlAbbrev: "SJS", name: "San Jose", yahooAbbrevs: []string{"SJ"}},
	{nhlAbbrev: "VAN", name: "Vancouver"},
	{nhlAbbrev: "VGK", name: "Vegas"},

	// Relocated teams
	{nhlAbbrev: "ARI", name: "Arizona", relocatedTo: "UTA"},
}

// yahooNamesByNHLAbbrev and nhlAbbrevsByYahoo index yahooTeams.
var yahooNamesByNHLAbbrev, nhlAbbrevsByYahoo = indexYahooTeams(yahooTeams)

// indexYahooTeams returns the Yahoo display name per NHL abbreviation and
// the NHL abbreviation per Yahoo abbreviation that differs from it.
func indexYahooTeams(teams []yahooTeam) (names, nhlAbbrevs map[string]string) {
	names = make(map[string]string, len(teams))
	nhlAbbrevs = make(map[string]string)
	for _, t := range teams {
		names[t.nhlAbbrev] = t.name
		for _, abbrev := range t.yahooAbbrevs {
			nhlAbbrevs[abbrev] = t.nhlAbbrev
		}
		if t.relocatedTo != "" {
			nhlAbbrevs[t.nhlAbbrev] = t.relocatedTo
		}
	}
	return names, nhlAbbrevs
}

// yahooTeamName returns the Yahoo display name of an NHL team, empty when
// the abbreviation has none.
func yahooTeamName(nhlAbbrev string) string {
	return yahooNamesByNHLAbbrev[nhlAbbrev]
}

// NHLAbbrevForYahooTeam returns the NHL abbreviation of a Yahoo team
// abbreviation, ignoring case and surrounding space. An abbreviation Yahoo
// shares with the NHL comes back uppercased; a relocated team's, as its
// current team's.
func NHLAbbrevForYahooTeam(abbrev string) string {
	abbrev = strings.ToUpper(strings.TrimSpace(abbrev))
	if nhl, ok := nhlAbbrevsByYahoo[abbrev]; ok {
		return nhl
	}
	return abbrev
}
