package store_test

import (
	"encoding/xml"
	"testing"

	"github.com/sperano/puckdb/internal/fixtures/yahoofixtures"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The standings document decodes into the league's Standings with every
// team's text values intact; elements Yahoo leaves out decode as nil.
func TestStandingsUnmarshal_H2H(t *testing.T) {
	t.Parallel()
	var content store.FantasyContent
	require.NoError(t, xml.Unmarshal(yahoofixtures.Read(yahoofixtures.H2HStandings), &content))

	teams := content.League.Standings.Teams
	assert.True(t, teams.HasCount)
	assert.Equal(t, yahoofixtures.TeamsInH2HStandings, teams.Count)
	require.Len(t, teams.Slice, yahoofixtures.TeamsInH2HStandings)

	first := teams.Slice[0]
	assert.Equal(t, 3, first.ID)
	assert.Equal(t, "Ice Breakers", first.Name)
	require.NotNil(t, first.TeamStandings)
	assert.Equal(t, "1", first.TeamStandings.Rank)
	assert.Equal(t, "1", first.TeamStandings.PlayoffSeed)
	require.NotNil(t, first.TeamStandings.OutcomeTotals)
	assert.Equal(t, store.OutcomeTotals{Wins: "4", Losses: "1", Ties: "0", Percentage: ".800"}, *first.TeamStandings.OutcomeTotals)
	assert.Nil(t, first.TeamStandings.DivisionalOutcomeTotals)
	require.NotNil(t, first.TeamStandings.Streak)
	assert.Equal(t, "win", first.TeamStandings.Streak.Type)
	assert.Equal(t, "3", first.TeamStandings.Streak.Value)
	assert.Equal(t, "612.5", first.TeamStandings.PointsFor)
	assert.Equal(t, "480.25", first.TeamStandings.PointsAgainst)

	second := teams.Slice[1]
	require.NotNil(t, second.TeamStandings.DivisionalOutcomeTotals)
	assert.Equal(t, store.OutcomeTotals{Wins: "2", Losses: "0", Ties: "0"}, *second.TeamStandings.DivisionalOutcomeTotals)
}

func TestStandingsUnmarshal_Roto(t *testing.T) {
	t.Parallel()
	var content store.FantasyContent
	require.NoError(t, xml.Unmarshal(yahoofixtures.Read(yahoofixtures.RotoStandings), &content))

	teams := content.League.Standings.Teams.Slice
	require.Len(t, teams, 2)
	require.NotNil(t, teams[0].TeamStandings)
	assert.Nil(t, teams[0].TeamStandings.OutcomeTotals)
	assert.Nil(t, teams[0].TeamStandings.Streak)
	assert.Equal(t, "84.5", teams[0].TeamStandings.PointsFor)
	assert.Equal(t, "+1.5", teams[0].TeamStandings.PointsChange)
	assert.Equal(t, "5.5", teams[1].TeamStandings.PointsBack)
}

// A document without standings (the settings resource) leaves the standings
// empty and the team's standings nil, so the existing resources keep
// parsing as before.
func TestStandingsUnmarshal_AbsentInOtherDocuments(t *testing.T) {
	t.Parallel()
	var settings store.FantasyContent
	require.NoError(t, xml.Unmarshal(yahoofixtures.Read(yahoofixtures.PointsLeague), &settings))
	assert.Empty(t, settings.League.Standings.Teams.Slice)

	var team store.FantasyContent
	require.NoError(t, xml.Unmarshal([]byte(`<fantasy_content><team><team_id>1</team_id>
<team_stats><coverage_type>date</coverage_type><date>2026-10-22</date><stats><stat><stat_id>1</stat_id><value>2</value></stat></stats></team_stats>
</team></fantasy_content>`), &team))
	assert.Nil(t, team.Team.TeamStandings)
	assert.Equal(t, "date", team.Team.TeamStats.CoverageType)
	require.Len(t, team.Team.TeamStats.Stats.Slice, 1)
}

// The teams count attribute is kept apart from its absence: count="0" is a
// statement, a missing attribute is none.
func TestStandingsTeamsUnmarshal_PreservesCountPresence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		xml      string
		hasCount bool
		count    int
		teams    int
	}{
		{name: "missing", xml: `<teams><team><team_id>1</team_id></team></teams>`, teams: 1},
		{name: "explicit zero", xml: `<teams count="0"><team><team_id>1</team_id></team></teams>`, hasCount: true, teams: 1},
		{name: "positive", xml: `<teams count="2"><team><team_id>1</team_id></team><team><team_id>2</team_id></team></teams>`,
			hasCount: true, count: 2, teams: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got store.StandingsTeams
			require.NoError(t, xml.Unmarshal([]byte(tt.xml), &got))
			assert.Equal(t, tt.hasCount, got.HasCount)
			assert.Equal(t, tt.count, got.Count)
			assert.Len(t, got.Slice, tt.teams)
			assert.Equal(t, "teams", got.XMLName.Local)
		})
	}
}
