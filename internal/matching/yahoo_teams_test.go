package matching

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// yahooNamePrefixes expands the short forms Yahoo display names use, so a
// display name can be compared with the start of the NHL full name.
var yahooNamePrefixes = map[string]string{"ny ": "new york "}

// TestYahooTeamsMatchNHLTeams cross-checks yahooTeams with teamsByAbbrev:
// one row per current club, each a known NHL team whose Yahoo display name
// starts its NHL full name, so a missing or swapped row fails.
func TestYahooTeamsMatchNHLTeams(t *testing.T) {
	t.Parallel()

	seen := make(map[string]bool, len(yahooTeams))
	names := make(map[string]string, len(yahooTeams))
	current := 0
	for _, team := range yahooTeams {
		require.False(t, seen[team.nhlAbbrev], "%s has two rows", team.nhlAbbrev)
		seen[team.nhlAbbrev] = true
		if team.relocatedTo == "" {
			current++
		}

		info, ok := teamsByAbbrev[team.nhlAbbrev]
		require.True(t, ok, "%s is not an NHL team abbreviation", team.nhlAbbrev)
		require.NotEmpty(t, team.name, "%s has no Yahoo name", team.nhlAbbrev)
		if other, dup := names[team.name]; dup {
			t.Errorf("%s and %s share the Yahoo name %q", other, team.nhlAbbrev, team.name)
		}
		names[team.name] = team.nhlAbbrev

		name := NormalizeName(team.name)
		for short, long := range yahooNamePrefixes {
			name = strings.Replace(name, short, long, 1)
		}
		assert.True(t, strings.HasPrefix(NormalizeName(info.FullName), name),
			"%s: Yahoo name %q does not name %q", team.nhlAbbrev, team.name, info.FullName)
	}
	assert.Equal(t, nhlClubCount, current, "current clubs in yahooTeams")
}

// TestYahooTeamAbbrevsResolveToCurrentClubs guards NHLAbbrevForYahooTeam:
// every Yahoo abbreviation is listed once, never shadows an NHL
// abbreviation, and resolves to a club that plays now.
func TestYahooTeamAbbrevsResolveToCurrentClubs(t *testing.T) {
	t.Parallel()

	current := make(map[string]bool, nhlClubCount)
	for _, team := range yahooTeams {
		if team.relocatedTo == "" {
			current[team.nhlAbbrev] = true
		}
	}
	listed := make(map[string]string)
	for _, team := range yahooTeams {
		for _, abbrev := range team.yahooAbbrevs {
			if other, dup := listed[abbrev]; dup {
				t.Errorf("Yahoo abbreviation %s is listed for %s and %s", abbrev, other, team.nhlAbbrev)
			}
			listed[abbrev] = team.nhlAbbrev
			_, shadows := teamsByAbbrev[abbrev]
			assert.False(t, shadows, "Yahoo abbreviation %s of %s is another NHL team's", abbrev, team.nhlAbbrev)
		}
		if team.relocatedTo != "" {
			assert.Empty(t, team.yahooAbbrevs, "relocated %s resolves through relocatedTo only", team.nhlAbbrev)
			assert.True(t, current[team.relocatedTo], "%s relocated to %s, not a current club", team.nhlAbbrev, team.relocatedTo)
		}
	}
	for yahoo, nhl := range nhlAbbrevsByYahoo {
		assert.True(t, current[nhl], "Yahoo %s resolves to %s, not a current club", yahoo, nhl)
	}
}

// TestArizonaUtahTransition pins every encoding of the Coyotes' move: NHL
// IDs per season, the Yahoo display names the matcher compares, and the
// Yahoo abbreviations news resolves.
func TestArizonaUtahTransition(t *testing.T) {
	t.Parallel()

	const (
		coyotesID     = 53
		utahHCID      = 59
		utahMammothID = 68
		utahHCSeason  = 20242025
	)
	id, err := LookupTeamID("ARI")
	require.NoError(t, err)
	assert.Equal(t, int64(coyotesID), id)
	id, err = LookupTeamIDForSeason("UTA", utahHCSeason)
	require.NoError(t, err)
	assert.Equal(t, int64(utahHCID), id)
	id, err = LookupTeamIDForSeason("UTA", utahMammothFirstSeason)
	require.NoError(t, err)
	assert.Equal(t, int64(utahMammothID), id)

	assert.Equal(t, "Arizona", yahooTeamName("ARI"))
	assert.Equal(t, "Utah", yahooTeamName("UTA"))

	for _, abbrev := range []string{"ARI", "Ari", "UTA", "UTAH", " utah "} {
		assert.Equal(t, "UTA", NHLAbbrevForYahooTeam(abbrev), abbrev)
	}
}

func TestNHLAbbrevForYahooTeam(t *testing.T) {
	t.Parallel()

	tests := []struct{ yahoo, want string }{
		{"nj", "NJD"},
		{"TB", "TBL"},
		{"LA", "LAK"},
		{"SJ", "SJS"},
		{"Was", "WSH"},
		{"Mon", "MTL"},
		{"Cls", "CBJ"},
		{"Nas", "NSH"},
		{"Cal", "CGY"},
		{"Tor", "TOR"},
		{"", ""},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, NHLAbbrevForYahooTeam(tc.yahoo), tc.yahoo)
	}
}

func TestYahooTeamName(t *testing.T) {
	t.Parallel()

	tests := []struct{ nhl, want string }{
		{"BOS", "Boston"},
		{"NYR", "NY Rangers"},
		{"NYI", "NY Islanders"},
		{"TBL", "Tampa Bay"},
		{"STL", "St. Louis"},
		{"VGK", "Vegas"},
		{"HFD", ""},
		{"", ""},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, yahooTeamName(tc.nhl), tc.nhl)
	}
}
