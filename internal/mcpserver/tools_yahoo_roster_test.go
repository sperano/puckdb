package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/sperano/puckdb/internal/fixtures/pgtest"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	teamRosterTestTeam int32 = 4
	teamRosterTestDate       = "2026-11-18"
	// pgRosterLaterDate is a later roster day of pgRosterOtherTeamID only,
	// added by TestLatestRosterDatesReadDatabase.
	pgRosterLaterDate = "2026-11-05"
)

// teamRosterQueries serves get_yahoo_roster's two queries.
type teamRosterQueries struct {
	*fakeYahooQueries
	rows        []sqlcdb.YahooRosterPlayer
	latest      pgtype.Date
	latestErr   error
	latestCalls []sqlcdb.GetLatestYahooTeamRosterDateParams
}

func (q *teamRosterQueries) GetYahooRosterWithPlayers(context.Context, sqlcdb.GetYahooRosterWithPlayersParams) ([]sqlcdb.YahooRosterPlayer, error) {
	return q.rows, nil
}

func (q *teamRosterQueries) GetLatestYahooTeamRosterDate(_ context.Context, arg sqlcdb.GetLatestYahooTeamRosterDateParams) (pgtype.Date, error) {
	q.latestCalls = append(q.latestCalls, arg)
	return q.latest, q.latestErr
}

func callTeamRosterTool(t *testing.T, q yahooQueries) *mcp.CallToolResult {
	t.Helper()
	tool := yahooTestServer(q, nil).GetTool("get_yahoo_roster")
	require.NotNil(t, tool)
	return callTool(t, tool.Handler, map[string]any{
		leagueIDArg: float64(allowedLeagueID), "team_id": float64(teamRosterTestTeam), "date": teamRosterTestDate,
	})
}

func TestYahooRosterToolRowsNeedNoLatestDate(t *testing.T) {
	q := &teamRosterQueries{fakeYahooQueries: newFakeYahooQueries(),
		rows: []sqlcdb.YahooRosterPlayer{{TeamID: teamRosterTestTeam, SelectedPosition: "C"}}}
	result := callTeamRosterTool(t, q)
	require.False(t, result.IsError, resultText(t, result))
	assert.NotContains(t, resultText(t, result), "no roster imported")
	assert.Empty(t, q.latestCalls)
}

func TestYahooRosterToolEmptyDateGivesLatestDate(t *testing.T) {
	q := &teamRosterQueries{fakeYahooQueries: newFakeYahooQueries(),
		latest: pgtype.Date{Time: time.Date(2026, time.November, 17, 0, 0, 0, 0, time.UTC), Valid: true}}
	result := callTeamRosterTool(t, q)
	require.False(t, result.IsError, resultText(t, result))
	assert.Equal(t, fmt.Sprintf("no roster imported for team %d of league %d on %s; latest imported date: 2026-11-17",
		teamRosterTestTeam, allowedLeagueID, teamRosterTestDate), resultText(t, result))
	assert.Equal(t, []sqlcdb.GetLatestYahooTeamRosterDateParams{{LeagueID: allowedLeagueID, TeamID: teamRosterTestTeam}}, q.latestCalls)
}

func TestYahooRosterToolTeamWithoutAnyRoster(t *testing.T) {
	q := &teamRosterQueries{fakeYahooQueries: newFakeYahooQueries()}
	result := callTeamRosterTool(t, q)
	require.False(t, result.IsError, resultText(t, result))
	assert.Equal(t, fmt.Sprintf("no roster imported for team %d of league %d on %s; no roster of this team has been imported",
		teamRosterTestTeam, allowedLeagueID, teamRosterTestDate), resultText(t, result))
}

func TestYahooRosterToolLatestDateError(t *testing.T) {
	q := &teamRosterQueries{fakeYahooQueries: newFakeYahooQueries(), latestErr: errors.New("connection reset")}
	result := callTeamRosterTool(t, q)
	assert.True(t, result.IsError)
	assert.Equal(t, fmt.Sprintf("load latest roster date of team %d in league %d: connection reset",
		teamRosterTestTeam, allowedLeagueID), resultText(t, result))
}

// TestLatestRosterDatesReadDatabase runs both tools on a date with no rows
// against the real queries: the team's latest date counts only its own
// rows, the league's every team's.
func TestLatestRosterDatesReadDatabase(t *testing.T) {
	pool := pgtest.Open(t)
	cleanup := func() {
		deleteTestLeague(t, pool)
		_, err := pool.Exec(context.Background(), `DELETE FROM yahoo_league_players WHERE league_key = $1`, pgTestLeagueKey)
		require.NoError(t, err)
		_, err = pool.Exec(context.Background(), `DELETE FROM players WHERE id = $1`, pgRosterNHLPlayerID)
		require.NoError(t, err)
	}
	cleanup()
	t.Cleanup(cleanup)
	seedLeagueRosters(t, pool)
	_, err := pool.Exec(context.Background(), `INSERT INTO yahoo_team_rosters (league_id, team_id, date, player_id, player_key, selected_position)
		VALUES ($1, $2, $3, $4, $5, 'LW')`, pgTestLeagueID, pgRosterOtherTeamID, pgRosterLaterDate,
		pgRosterNamelessYahooID, fmt.Sprintf("465.p.%d", pgRosterNamelessYahooID))
	require.NoError(t, err)

	server := yahooTestServer(sqlcdb.New(pool), []string{pgTestLeagueKey})
	team := callTool(t, server.GetTool("get_yahoo_roster").Handler, map[string]any{
		leagueIDArg: float64(pgTestLeagueID), "team_id": float64(pgTestTeamID), "date": teamRosterTestDate,
	})
	require.False(t, team.IsError, resultText(t, team))
	assert.Equal(t, fmt.Sprintf("no roster imported for team %d of league %d on %s; latest imported date: %s",
		pgTestTeamID, pgTestLeagueID, teamRosterTestDate, rosterDate), resultText(t, team))

	league := leagueRostersOutput(t, callTool(t, server.GetTool(leagueRostersToolName).Handler,
		map[string]any{leagueIDArg: float64(pgTestLeagueID), "date": teamRosterTestDate}))
	assert.Contains(t, league.header, " roster_rows=0 returned=0 latest_roster_date="+pgRosterLaterDate)
}
