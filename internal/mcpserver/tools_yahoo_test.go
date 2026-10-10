package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// yahooTestServer registers the yahoo toolset over q with the given
// allowlist, the same way NewServer does.
func yahooTestServer(q yahooQueries, leagueKeys []string) *server.MCPServer {
	srv := server.NewMCPServer("test", serverVersion)
	registerYahooTools(srv, q, newLeagueGuard(q, leagueKeys))
	return srv
}

// TestYahooToolsAreLeagueGuarded calls every yahoo tool with a league
// outside the allowlist. A league-scoped tool must refuse it before any
// query runs: the fake stubs no tool query, so a tool that skipped the guard
// panics on the nil embedded interface. Tools without league_id must be
// listed in leagueIndependentYahooTools.
func TestYahooToolsAreLeagueGuarded(t *testing.T) {
	srv := yahooTestServer(newFakeYahooQueries(), []string{allowedLeagueKey})
	tools := srv.ListTools()
	require.Len(t, tools, len(expectedYahooTools))

	for name := range leagueIndependentYahooTools {
		require.Contains(t, tools, name, "leagueIndependentYahooTools names a tool that is not registered")
	}
	for name, tool := range tools {
		t.Run(name, func(t *testing.T) {
			if _, ok := leagueIndependentYahooTools[name]; ok {
				assert.NotContains(t, tool.Tool.InputSchema.Properties, leagueIDArg,
					"a tool taking league_id must be guarded, not league-independent")
				return
			}
			assert.Contains(t, tool.Tool.InputSchema.Required, leagueIDArg,
				"a yahoo tool must take league_id or be listed in leagueIndependentYahooTools")
			result := callGuardedTool(t, tool.Handler, otherLeagueID)
			assert.True(t, result.IsError)
			assert.Equal(t, fmt.Sprintf("unknown league_id %d", otherLeagueID), resultText(t, result))
		})
	}
}

// callGuardedTool calls handler for leagueID and turns a panic (a query run
// without the guard) into a test failure naming the cause.
func callGuardedTool(t *testing.T, handler server.ToolHandlerFunc, leagueID int32) (result *mcp.CallToolResult) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("tool queried the database for a refused league (missing leagueGuard.scoped?): %v", r)
		}
	}()
	return callTool(t, handler, map[string]any{leagueIDArg: float64(leagueID)})
}

// rosterYahooQueries records the roster and unrostered queries.
type rosterYahooQueries struct {
	*fakeYahooQueries
	rosters    []sqlcdb.GetYahooRosterWithPlayersParams
	unrostered []sqlcdb.GetUnrosteredSkatersParams
}

func (q *rosterYahooQueries) GetYahooRosterWithPlayers(_ context.Context, arg sqlcdb.GetYahooRosterWithPlayersParams) ([]sqlcdb.YahooRosterPlayer, error) {
	q.rosters = append(q.rosters, arg)
	return nil, nil
}

func (q *rosterYahooQueries) GetUnrosteredSkaters(_ context.Context, arg sqlcdb.GetUnrosteredSkatersParams) ([]sqlcdb.SkaterRecentStat, error) {
	q.unrostered = append(q.unrostered, arg)
	return nil, nil
}

func TestYahooRosterToolValidatesTeamID(t *testing.T) {
	const teamID int32 = 7
	q := &rosterYahooQueries{fakeYahooQueries: newFakeYahooQueries()}
	tool := yahooTestServer(q, nil).GetTool("get_yahoo_roster")
	args := map[string]any{leagueIDArg: float64(allowedLeagueID), "team_id": float64(teamID), "date": "2025-10-08"}

	result := callTool(t, tool.Handler, args)
	require.False(t, result.IsError, resultText(t, result))
	require.Len(t, q.rosters, 1)
	assert.Equal(t, teamID, q.rosters[0].TeamID)

	invalid := map[string]any{
		"absent":     nil,
		"zero":       float64(0),
		"negative":   float64(-teamID),
		"fractional": float64(teamID) + 0.5,
		"overflow":   float64(int64(teamID) + 1<<32),
	}
	for name, value := range invalid {
		t.Run(name, func(t *testing.T) {
			q.rosters = nil
			changed := maps.Clone(args)
			delete(changed, "team_id")
			if value != nil {
				changed["team_id"] = value
			}
			result := callTool(t, tool.Handler, changed)
			assert.True(t, result.IsError)
			assert.Contains(t, resultText(t, result), "team_id")
			assert.Empty(t, q.rosters)
		})
	}
}

func TestUnrosteredToolValidatesSeasonAndLimit(t *testing.T) {
	q := &rosterYahooQueries{fakeYahooQueries: newFakeYahooQueries()}
	tool := yahooTestServer(q, nil).GetTool("get_unrostered_skaters")
	args := map[string]any{leagueIDArg: float64(allowedLeagueID), "season": float64(testSeasonID), "date": "2025-10-08"}

	result := callTool(t, tool.Handler, args)
	require.False(t, result.IsError, resultText(t, result))
	require.Len(t, q.unrostered, 1)
	assert.Equal(t, testSeasonID, q.unrostered[0].Season)
	assert.Equal(t, int32(defaultResultLimit), q.unrostered[0].Limit)

	q.unrostered = nil
	result = callTool(t, tool.Handler, withArg(args, "limit", float64(0)))
	require.False(t, result.IsError, resultText(t, result))
	require.Len(t, q.unrostered, 1)
	assert.Zero(t, q.unrostered[0].Limit, "an explicit 0 limit is passed on")

	for name, change := range map[string][2]any{
		"wrapped season":    {"season", float64(wrappedSeasonID)},
		"fractional season": {"season", 20252026.9},
		"negative limit":    {"limit", float64(-1)},
		"fractional limit":  {"limit", 2.5},
	} {
		t.Run(name, func(t *testing.T) {
			q.unrostered = nil
			result := callTool(t, tool.Handler, withArg(args, change[0].(string), change[1]))
			assert.True(t, result.IsError)
			assert.Contains(t, resultText(t, result), "invalid "+change[0].(string))
			assert.Empty(t, q.unrostered)
		})
	}
}

func TestYahooLeaguesToolListsOnlyServedLeagues(t *testing.T) {
	q := newFakeYahooQueries()
	tests := map[string]struct {
		leagueKeys []string
		want       []string
	}{
		"unrestricted": {nil, []string{allowedLeagueKey, otherLeagueKey}},
		"restricted":   {[]string{allowedLeagueKey}, []string{allowedLeagueKey}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tool := yahooTestServer(q, tt.leagueKeys).GetTool("get_yahoo_leagues")
			require.NotNil(t, tool)
			rows := parseCSV(t, resultText(t, callTool(t, tool.Handler, nil)))
			keyCol := columnIndex(t, rows[0], "league_key")
			var got []string
			for _, row := range rows[1:] {
				got = append(got, row[keyCol])
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestYahooTeamsToolQueriesAllowedLeague(t *testing.T) {
	q := newFakeYahooQueries()
	tool := yahooTestServer(q, []string{allowedLeagueKey}).GetTool("get_yahoo_teams_by_league")
	require.NotNil(t, tool)

	result := callTool(t, tool.Handler, map[string]any{leagueIDArg: float64(allowedLeagueID)})
	assert.False(t, result.IsError, resultText(t, result))
	assert.Equal(t, []int32{allowedLeagueID}, q.teamCalls)
}

// TestYahooTeamsToolUnrestrictedSkipsLookup pins today's behaviour without
// --mcp-yahoo-leagues: every league is served and no extra query runs.
func TestYahooTeamsToolUnrestrictedSkipsLookup(t *testing.T) {
	q := newFakeYahooQueries()
	tool := yahooTestServer(q, nil).GetTool("get_yahoo_teams_by_league")
	require.NotNil(t, tool)

	result := callTool(t, tool.Handler, map[string]any{leagueIDArg: float64(missingLeagueID)})
	assert.False(t, result.IsError, resultText(t, result))
	assert.Equal(t, []int32{missingLeagueID}, q.teamCalls)
	assert.Zero(t, q.lookups)
}

func TestYahooSeasonTeamTotalsToolReturnsTeamRows(t *testing.T) {
	q := newFakeYahooQueries()
	q.totals = []sqlcdb.YahooSeasonTeamTotal{
		{LeagueID: allowedLeagueID, TeamID: 3, TeamName: "Leaders", Season: 2025, NumTeams: 12, ScoringType: "head", Goals: 210, Assists: 340, PlusMinus: 25, PIM: 400, PPP: 150, SOG: 2100, Wins: 40, Ga: 180},
		{LeagueID: allowedLeagueID, TeamID: 7, TeamName: "Trailers", Season: 2025, NumTeams: 12, ScoringType: "head", Goals: 150, Assists: 260, PlusMinus: -12, PIM: 310, PPP: 98, SOG: 1800, Wins: 31, Ga: 205},
	}
	tool := yahooTestServer(q, []string{allowedLeagueKey}).GetTool("get_yahoo_season_team_totals")
	require.NotNil(t, tool)

	result := callTool(t, tool.Handler, map[string]any{leagueIDArg: float64(allowedLeagueID)})
	require.False(t, result.IsError, resultText(t, result))
	assert.Equal(t, []int32{allowedLeagueID}, q.totalsCalls)

	rows := parseCSV(t, resultText(t, result))
	require.Len(t, rows, len(q.totals)+1)
	nameCol := columnIndex(t, rows[0], "team_name")
	plusMinusCol := columnIndex(t, rows[0], "plus_minus")
	gaCol := columnIndex(t, rows[0], "ga")
	assert.Equal(t, []string{"Leaders", "25", "180"}, []string{rows[1][nameCol], rows[1][plusMinusCol], rows[1][gaCol]})
	assert.Equal(t, []string{"Trailers", "-12", "205"}, []string{rows[2][nameCol], rows[2][plusMinusCol], rows[2][gaCol]})
}

func TestYahooSeasonTeamTotalsToolReportsQueryError(t *testing.T) {
	q := newFakeYahooQueries()
	q.totalsErr = errors.New("connection refused")
	tool := yahooTestServer(q, nil).GetTool("get_yahoo_season_team_totals")
	require.NotNil(t, tool)

	result := callTool(t, tool.Handler, map[string]any{leagueIDArg: float64(allowedLeagueID)})
	assert.True(t, result.IsError)
	assert.Equal(t, "connection refused", resultText(t, result))
}

// columnIndex finds a CSV header column.
func columnIndex(t *testing.T, header []string, name string) int {
	t.Helper()
	for i, col := range header {
		if col == name {
			return i
		}
	}
	t.Fatalf("column %q not in header %v", name, header)
	return -1
}
