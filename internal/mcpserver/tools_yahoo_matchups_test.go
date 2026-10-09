package mcpserver

import (
	"context"
	"math"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	firstMatchupWeek         int32 = 1
	secondMatchupWeek        int32 = 2
	firstFixtureTeamID       int32 = 10
	filteredFixtureTeamID    int32 = 20
	secondFixtureTeamID      int32 = 30
	firstWeekMatchupCount          = 2
	filteredTeamMatchupCount       = 3
	allMatchupCount                = 4
)

type matchupQueryCalls struct {
	leagueIDs []int32
	weeks     []sqlcdb.GetYahooMatchupsByWeekParams
	teams     []sqlcdb.GetYahooMatchupsByTeamParams
}

type matchupYahooQueries struct {
	*fakeYahooQueries
	calls    matchupQueryCalls
	matchups []sqlcdb.YahooMatchup
}

func newMatchupYahooQueries() *matchupYahooQueries {
	return &matchupYahooQueries{
		fakeYahooQueries: newFakeYahooQueries(),
		matchups: []sqlcdb.YahooMatchup{
			{LeagueID: allowedLeagueID, Week: firstMatchupWeek, Team1ID: firstFixtureTeamID, Team2ID: filteredFixtureTeamID},
			{LeagueID: allowedLeagueID, Week: secondMatchupWeek, Team1ID: firstFixtureTeamID, Team2ID: secondFixtureTeamID},
			{LeagueID: allowedLeagueID, Week: firstMatchupWeek, Team1ID: secondFixtureTeamID, Team2ID: filteredFixtureTeamID},
			{LeagueID: allowedLeagueID, Week: secondMatchupWeek, Team1ID: secondFixtureTeamID, Team2ID: filteredFixtureTeamID},
		},
	}
}

func (q *matchupYahooQueries) GetYahooMatchupsByLeague(_ context.Context, leagueID int32) ([]sqlcdb.YahooMatchup, error) {
	q.calls.leagueIDs = append(q.calls.leagueIDs, leagueID)
	return q.matchups, nil
}

func (q *matchupYahooQueries) GetYahooMatchupsByWeek(_ context.Context, params sqlcdb.GetYahooMatchupsByWeekParams) ([]sqlcdb.YahooMatchup, error) {
	q.calls.weeks = append(q.calls.weeks, params)
	return matchupsForWeek(q.matchups, params.Week), nil
}

func (q *matchupYahooQueries) GetYahooMatchupsByTeam(_ context.Context, params sqlcdb.GetYahooMatchupsByTeamParams) ([]sqlcdb.YahooMatchup, error) {
	q.calls.teams = append(q.calls.teams, params)
	return matchupsForTeam(q.matchups, params.Team1ID), nil
}

func TestYahooMatchupsToolFiltersByWeek(t *testing.T) {
	q := newMatchupYahooQueries()
	tool := yahooTestServer(q, []string{allowedLeagueKey}).GetTool("get_yahoo_matchups")
	require.NotNil(t, tool)

	result := callTool(t, tool.Handler, matchupArgs("week", int(firstMatchupWeek)))
	require.False(t, result.IsError, resultText(t, result))
	assert.Equal(t, []sqlcdb.GetYahooMatchupsByWeekParams{{LeagueID: allowedLeagueID, Week: firstMatchupWeek}}, q.calls.weeks)
	assert.Empty(t, q.calls.teams)
	assert.Empty(t, q.calls.leagueIDs)
	assert.Equal(t, firstWeekMatchupCount, matchupCSVRows(t, result))
}

func TestYahooMatchupsToolFiltersByTeam(t *testing.T) {
	q := newMatchupYahooQueries()
	tool := yahooTestServer(q, []string{allowedLeagueKey}).GetTool("get_yahoo_matchups")
	require.NotNil(t, tool)

	result := callTool(t, tool.Handler, matchupArgs("team_id", int(filteredFixtureTeamID)))
	require.False(t, result.IsError, resultText(t, result))
	assert.Equal(t, []sqlcdb.GetYahooMatchupsByTeamParams{{LeagueID: allowedLeagueID, Team1ID: filteredFixtureTeamID}}, q.calls.teams)
	assert.Empty(t, q.calls.weeks)
	assert.Empty(t, q.calls.leagueIDs)
	assert.Equal(t, filteredTeamMatchupCount, matchupCSVRows(t, result))
}

func TestYahooMatchupsToolFiltersTeamByWeek(t *testing.T) {
	q := newMatchupYahooQueries()
	tool := yahooTestServer(q, []string{allowedLeagueKey}).GetTool("get_yahoo_matchups")
	require.NotNil(t, tool)

	result := callTool(t, tool.Handler, map[string]any{
		leagueIDArg: float64(allowedLeagueID),
		"week":      float64(firstMatchupWeek),
		"team_id":   float64(filteredFixtureTeamID),
	})
	require.False(t, result.IsError, resultText(t, result))
	assert.Equal(t, []sqlcdb.GetYahooMatchupsByTeamParams{{LeagueID: allowedLeagueID, Team1ID: filteredFixtureTeamID}}, q.calls.teams)
	assert.Empty(t, q.calls.weeks)
	assert.Empty(t, q.calls.leagueIDs)
	assert.Equal(t, firstWeekMatchupCount, matchupCSVRows(t, result))
}

func TestYahooMatchupsToolWithoutFiltersReturnsLeague(t *testing.T) {
	q := newMatchupYahooQueries()
	tool := yahooTestServer(q, []string{allowedLeagueKey}).GetTool("get_yahoo_matchups")
	require.NotNil(t, tool)

	result := callTool(t, tool.Handler, map[string]any{leagueIDArg: float64(allowedLeagueID)})
	require.False(t, result.IsError, resultText(t, result))
	assert.Equal(t, []int32{allowedLeagueID}, q.calls.leagueIDs)
	assert.Empty(t, q.calls.weeks)
	assert.Empty(t, q.calls.teams)
	assert.Equal(t, allMatchupCount, matchupCSVRows(t, result))
}

func TestYahooMatchupsToolArgumentsStayOptionalAndLeagueGuarded(t *testing.T) {
	q := newMatchupYahooQueries()
	tool := yahooTestServer(q, []string{allowedLeagueKey}).GetTool("get_yahoo_matchups")
	require.NotNil(t, tool)
	assert.Contains(t, tool.Tool.InputSchema.Properties, "week")
	assert.Contains(t, tool.Tool.InputSchema.Properties, "team_id")
	assert.NotContains(t, tool.Tool.InputSchema.Required, "week")
	assert.NotContains(t, tool.Tool.InputSchema.Required, "team_id")
	assert.Contains(t, tool.Tool.Description, "When both filters are given")
	assertPositiveIntegerSchema(t, tool.Tool.InputSchema.Properties["week"])
	assertPositiveIntegerSchema(t, tool.Tool.InputSchema.Properties["team_id"])

	result := callTool(t, tool.Handler, map[string]any{
		leagueIDArg: float64(otherLeagueID),
		"week":      float64(firstMatchupWeek),
		"team_id":   float64(filteredFixtureTeamID),
	})
	assert.True(t, result.IsError)
	assert.Empty(t, q.calls.leagueIDs)
	assert.Empty(t, q.calls.weeks)
	assert.Empty(t, q.calls.teams)
}

func TestYahooMatchupsToolRejectsInvalidFilters(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{name: "week", args: matchupArgs("week", -1), want: "invalid week -1" + positiveInt4Range},
		{name: "zero week", args: matchupArgs("week", 0), want: "invalid week 0" + positiveInt4Range},
		{name: "team_id", args: matchupArgs("team_id", -1), want: "invalid team_id -1" + positiveInt4Range},
		{name: "zero team_id", args: matchupArgs("team_id", 0), want: "invalid team_id 0" + positiveInt4Range},
		{name: "fractional week", args: matchupArgs("week", 1.5), want: "invalid week 1.5" + positiveInt4Range},
		{name: "fractional team_id", args: matchupArgs("team_id", 20.5), want: "invalid team_id 20.5" + positiveInt4Range},
		{name: "week overflow", args: matchupArgs("week", math.MaxInt32+1), want: "invalid week 2147483648" + positiveInt4Range},
		{name: "team_id overflow", args: matchupArgs("team_id", math.MaxInt32+1), want: "invalid team_id 2147483648" + positiveInt4Range},
		{name: "non-numeric week", args: matchupArgs("week", "two"), want: `invalid week "two"` + positiveInt4Range},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := newMatchupYahooQueries()
			tool := yahooTestServer(q, []string{allowedLeagueKey}).GetTool("get_yahoo_matchups")
			result := callTool(t, tool.Handler, tt.args)
			assert.True(t, result.IsError)
			assert.Equal(t, tt.want, resultText(t, result))
			assert.Empty(t, q.calls.leagueIDs)
			assert.Empty(t, q.calls.weeks)
			assert.Empty(t, q.calls.teams)
		})
	}
}

// positiveInt4Range ends the error for an integer outside 1..MaxInt32.
const positiveInt4Range = ": want an integer from 1 to 2147483647"

func matchupArgs(name string, value any) map[string]any {
	return map[string]any{leagueIDArg: float64(allowedLeagueID), name: value}
}

func assertPositiveIntegerSchema(t *testing.T, property any) {
	t.Helper()
	schema, ok := property.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(minimumPositiveInteger), schema["minimum"])
	assert.Equal(t, float64(math.MaxInt32), schema["maximum"])
	assert.Equal(t, float64(minimumPositiveInteger), schema["multipleOf"])
}

func matchupCSVRows(t *testing.T, result *mcp.CallToolResult) int {
	t.Helper()
	rows := parseCSV(t, resultText(t, result))
	return len(rows) - 1
}

func matchupsForWeek(matchups []sqlcdb.YahooMatchup, week int32) []sqlcdb.YahooMatchup {
	filtered := make([]sqlcdb.YahooMatchup, 0, len(matchups))
	for _, matchup := range matchups {
		if matchup.Week == week {
			filtered = append(filtered, matchup)
		}
	}
	return filtered
}

func matchupsForTeam(matchups []sqlcdb.YahooMatchup, teamID int32) []sqlcdb.YahooMatchup {
	filtered := make([]sqlcdb.YahooMatchup, 0, len(matchups))
	for _, matchup := range matchups {
		if matchup.Team1ID == teamID || matchup.Team2ID == teamID {
			filtered = append(filtered, matchup)
		}
	}
	return filtered
}
