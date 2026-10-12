package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	leaguePlayersToolName       = "get_yahoo_league_players"
	poolSeason            int32 = 20262027
	poolCenterID          int32 = 101
	poolWingerID          int32 = 102
	poolGoalieID          int32 = 103
	poolProspectID        int32 = 104
	poolCenterNHLID       int64 = 8471675
	poolGoalieNHLID       int64 = 8476883

	// PostgreSQL fixture of TestYahooLeaguePlayersToolReadsDatabase.
	pgPoolMatchedYahooID   = 990763001
	pgPoolUnmatchedYahooID = 990763002
	pgPoolNHLPlayerID      = 990763001
)

var poolFetchedAt = time.Date(2026, 9, 28, 6, 0, 0, 0, time.UTC)

// fakePoolQueries adds the pool query to fakeYahooQueries.
type fakePoolQueries struct {
	*fakeYahooQueries
	pool     []sqlcdb.ListYahooLeaguePlayersWithNHLRow
	poolErr  error
	poolKeys []string
}

func (f *fakePoolQueries) ListYahooLeaguePlayersWithNHL(_ context.Context, leagueKey string) ([]sqlcdb.ListYahooLeaguePlayersWithNHLRow, error) {
	f.poolKeys = append(f.poolKeys, leagueKey)
	return f.pool, f.poolErr
}

func newFakePoolQueries() *fakePoolQueries {
	base := newFakeYahooQueries()
	for i := range base.leagues {
		base.leagues[i].Season = poolSeason
	}
	return &fakePoolQueries{fakeYahooQueries: base, pool: []sqlcdb.ListYahooLeaguePlayersWithNHLRow{
		poolPlayer(poolCenterID, "Connor Center", "EDM", "C", []string{"C", "LW", "Util"}, "", poolCenterNHLID),
		poolPlayer(poolWingerID, "Wade Winger", "TOR", "RW", []string{"RW", "Util", "IR+"}, "IR-LT", 0),
		poolPlayer(poolGoalieID, "Gus Goalie", "MTL", "G", []string{"G"}, "DTD", poolGoalieNHLID),
		poolPlayer(poolProspectID, "Pat Prospect", "SJ", "D", []string{"D", "Util"}, "", 0),
	}}
}

// poolPlayer builds one stored pool row; nhlID 0 means unmatched.
func poolPlayer(id int32, name, team, primary string, eligible []string, status string, nhlID int64) sqlcdb.ListYahooLeaguePlayersWithNHLRow {
	row := sqlcdb.ListYahooLeaguePlayersWithNHLRow{
		LeagueKey: allowedLeagueKey, Season: poolSeason, LeagueID: allowedLeagueID, GameKey: 465,
		PlayerID: id, PlayerKey: fmt.Sprintf("465.p.%d", id), FullName: name, EditorialTeamAbbr: team,
		PrimaryPosition: primary, PositionType: "P", EligiblePositions: eligible, Status: status,
		FetchedAt:   pgtype.Timestamptz{Time: poolFetchedAt, Valid: true},
		NhlPlayerID: pgtype.Int8{Int64: nhlID, Valid: nhlID != 0},
	}
	if primary == "G" {
		row.PositionType = "G"
	}
	if status != "" {
		row.StatusFull = "Status " + status
		row.InjuryNote = "Lower body"
		row.OnDisabledList = status == "IR-LT"
	}
	return row
}

// callPoolTool calls get_yahoo_league_players for the allowed league with
// extra arguments.
func callPoolTool(t *testing.T, q yahooQueries, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	tool := yahooTestServer(q, nil).GetTool(leaguePlayersToolName)
	require.NotNil(t, tool)
	all := map[string]any{leagueIDArg: float64(allowedLeagueID)}
	for name, value := range args {
		all[name] = value
	}
	return callTool(t, tool.Handler, all)
}

// poolOutput splits a successful result into its header and player rows.
func poolOutput(t *testing.T, result *mcp.CallToolResult) (string, [][]string) {
	t.Helper()
	text := resultText(t, result)
	require.False(t, result.IsError, text)
	out := parseSettingsOutput(t, text)
	return out.header, out.sections[leaguePlayersSection]
}

// poolIDs returns the yahoo_player_id column of the player rows.
func poolIDs(t *testing.T, rows [][]string) []string {
	t.Helper()
	col := columnIndex(t, rows[0], "yahoo_player_id")
	ids := []string{}
	for _, row := range rows[1:] {
		ids = append(ids, row[col])
	}
	return ids
}

func TestYahooLeaguePlayersToolReportsPool(t *testing.T) {
	q := newFakePoolQueries()
	header, rows := poolOutput(t, callPoolTool(t, q, nil))

	assert.Equal(t, []string{allowedLeagueKey}, q.poolKeys, "the pool is read under the league row's key")
	assert.Equal(t, fmt.Sprintf("# league_id=%d league_key=%s season=%d pool_players=4 unmatched_players=2 "+
		"fetched_at=2026-09-28T06:00:00Z matching=4 returned=4", allowedLeagueID, allowedLeagueKey, poolSeason), header)
	assert.Equal(t, [][]string{
		{"yahoo_player_id", "player_key", "name", "team", "primary_position", "eligible_positions", "position_type",
			"status", "status_full", "injury_note", "on_disabled_list", "nhl_player_id"},
		{"101", "465.p.101", "Connor Center", "EDM", "C", "C,LW,Util", "P", "", "", "", "false", "8471675"},
		{"102", "465.p.102", "Wade Winger", "TOR", "RW", "RW,Util,IR+", "P", "IR-LT", "Status IR-LT", "Lower body", "true", ""},
		{"103", "465.p.103", "Gus Goalie", "MTL", "G", "G", "G", "DTD", "Status DTD", "Lower body", "false", "8476883"},
		{"104", "465.p.104", "Pat Prospect", "SJ", "D", "D,Util", "P", "", "", "", "false", ""},
	}, rows)
}

func TestYahooLeaguePlayersToolFilters(t *testing.T) {
	tests := map[string]struct {
		args     map[string]any
		want     []string
		matching int
	}{
		"one position":           {map[string]any{leaguePositionsArg: "LW"}, []string{"101"}, 1},
		"any of positions":       {map[string]any{leaguePositionsArg: " d , rw,D"}, []string{"102", "104"}, 2},
		"status code":            {map[string]any{leagueStatusArg: "ir-lt"}, []string{"102"}, 1},
		"any of status codes":    {map[string]any{leagueStatusArg: "DTD,IR-LT"}, []string{"102", "103"}, 2},
		"any status":             {map[string]any{leagueStatusArg: "any"}, []string{"102", "103"}, 2},
		"unmatched only":         {map[string]any{leagueUnmatchedOnlyArg: true}, []string{"102", "104"}, 2},
		"unmatched only as text": {map[string]any{leagueUnmatchedOnlyArg: "true"}, []string{"102", "104"}, 2},
		"matched too":            {map[string]any{leagueUnmatchedOnlyArg: false}, []string{"101", "102", "103", "104"}, 4},
		"filters combine": {map[string]any{leaguePositionsArg: "RW,D", leagueStatusArg: "any", leagueUnmatchedOnlyArg: true},
			[]string{"102"}, 1},
		"limit keeps the first rows": {map[string]any{"limit": float64(2)}, []string{"101", "102"}, 4},
		"no match":                   {map[string]any{leagueStatusArg: "SUSP"}, []string{}, 0},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			header, rows := poolOutput(t, callPoolTool(t, newFakePoolQueries(), tt.args))
			assert.Contains(t, header, " pool_players=4 unmatched_players=2 ",
				"pool counts ignore the filters")
			assert.Contains(t, header, fmt.Sprintf(" matching=%d returned=%d", tt.matching, len(tt.want)))
			if len(tt.want) == 0 {
				assert.Equal(t, [][]string{{noResultsText}}, rows)
				return
			}
			assert.Equal(t, tt.want, poolIDs(t, rows))
		})
	}
}

func TestYahooLeaguePlayersToolDefaultLimit(t *testing.T) {
	q := newFakePoolQueries()
	q.pool = nil
	for i := range defaultLeaguePlayersLimit + 1 {
		q.pool = append(q.pool, poolPlayer(int32(i+1), "Player", "EDM", "C", []string{"C"}, "", 0))
	}
	header, rows := poolOutput(t, callPoolTool(t, q, nil))
	assert.Contains(t, header, fmt.Sprintf(" matching=%d returned=%d", defaultLeaguePlayersLimit+1, defaultLeaguePlayersLimit))
	assert.Len(t, rows, defaultLeaguePlayersLimit+1, "header row plus the default number of players")
}

func TestYahooLeaguePlayersToolRejectsInvalidArguments(t *testing.T) {
	tests := map[string]struct {
		args map[string]any
		want string
	}{
		"unknown position":        {map[string]any{leaguePositionsArg: "C,Util"}, `invalid positions "UTIL": want any of C, LW, RW, D, G`},
		"positions not a string":  {map[string]any{leaguePositionsArg: []any{"C"}}, "invalid positions [C]: want a string"},
		"status not a string":     {map[string]any{leagueStatusArg: float64(1)}, "invalid status 1: want a string"},
		"unmatched_only not bool": {map[string]any{leagueUnmatchedOnlyArg: "maybe"}, `invalid unmatched_only "maybe": want true or false`},
		"zero limit":              {map[string]any{"limit": float64(0)}, fmt.Sprintf("invalid limit 0: want an integer from 1 to %d", maxLeaguePlayersLimit)},
		"limit over max": {map[string]any{"limit": float64(maxLeaguePlayersLimit + 1)},
			fmt.Sprintf("invalid limit %d: want an integer from 1 to %d", maxLeaguePlayersLimit+1, maxLeaguePlayersLimit)},
		"fractional limit": {map[string]any{"limit": 2.5}, fmt.Sprintf("invalid limit 2.5: want an integer from 1 to %d", maxLeaguePlayersLimit)},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			q := newFakePoolQueries()
			result := callPoolTool(t, q, tt.args)
			assert.True(t, result.IsError)
			assert.Equal(t, tt.want, resultText(t, result))
			assert.Empty(t, q.poolKeys, "an invalid argument must be refused before any query")
		})
	}
}

func TestYahooLeaguePlayersToolEmptyPool(t *testing.T) {
	q := newFakePoolQueries()
	q.pool = []sqlcdb.ListYahooLeaguePlayersWithNHLRow{}
	header, rows := poolOutput(t, callPoolTool(t, q, nil))
	assert.Equal(t, fmt.Sprintf("# league_id=%d league_key=%s season=%d pool_players=0 unmatched_players=0 matching=0 returned=0",
		allowedLeagueID, allowedLeagueKey, poolSeason), header, "no fetched_at without a pool")
	assert.Equal(t, [][]string{{noResultsText}}, rows)
}

// TestYahooLeaguePlayersToolMissingLeague pins that an unrestricted server,
// whose guard does not look leagues up, answers a missing league with the
// guard's unknown-league error instead of an empty pool.
func TestYahooLeaguePlayersToolMissingLeague(t *testing.T) {
	q := newFakePoolQueries()
	tool := yahooTestServer(q, nil).GetTool(leaguePlayersToolName)
	require.NotNil(t, tool)
	result := callTool(t, tool.Handler, map[string]any{leagueIDArg: float64(missingLeagueID)})
	assert.Equal(t, unknownLeagueResult(missingLeagueID), result)
	assert.Empty(t, q.poolKeys)
}

func TestYahooLeaguePlayersToolQueryErrors(t *testing.T) {
	q := newFakePoolQueries()
	q.poolErr = errors.New("connection reset")
	result := callPoolTool(t, q, nil)
	assert.True(t, result.IsError)
	assert.Equal(t, "load player pool of league "+allowedLeagueKey+": connection reset", resultText(t, result))

	q = newFakePoolQueries()
	q.lookupErr = errors.New("connection refused")
	result = callPoolTool(t, q, nil)
	assert.True(t, result.IsError)
	assert.Equal(t, fmt.Sprintf("load league %d: connection refused", allowedLeagueID), resultText(t, result))
	assert.Empty(t, q.poolKeys)
}

// TestYahooLeaguePlayersToolReadsDatabase runs the tool against the real
// query: one pool player matched to an NHL player through players.yahoo_id,
// one not.
func TestYahooLeaguePlayersToolReadsDatabase(t *testing.T) {
	pool := openMCPTestDB(t)
	cleanup := func() {
		deleteTestLeague(t, pool)
		_, err := pool.Exec(context.Background(), `DELETE FROM yahoo_league_players WHERE league_key = $1`, pgTestLeagueKey)
		require.NoError(t, err)
		_, err = pool.Exec(context.Background(), `DELETE FROM players WHERE id = $1`, pgPoolNHLPlayerID)
		require.NoError(t, err)
	}
	cleanup()
	t.Cleanup(cleanup)
	seedLeaguePool(t, pool)

	tool := yahooTestServer(sqlcdb.New(pool), []string{pgTestLeagueKey}).GetTool(leaguePlayersToolName)
	require.NotNil(t, tool)
	result := callTool(t, tool.Handler, map[string]any{leagueIDArg: float64(pgTestLeagueID), leaguePositionsArg: "LW"})
	header, rows := poolOutput(t, result)

	assert.Equal(t, fmt.Sprintf("# league_id=%d league_key=%s season=%d pool_players=2 unmatched_players=1 "+
		"fetched_at=2026-09-28T06:00:00Z matching=2 returned=2", pgTestLeagueID, pgTestLeagueKey, pgTestSeason), header)
	require.Len(t, rows, 3)
	nhlCol := columnIndex(t, rows[0], "nhl_player_id")
	eligibleCol := columnIndex(t, rows[0], "eligible_positions")
	assert.Equal(t, []string{fmt.Sprint(pgPoolMatchedYahooID), fmt.Sprint(pgPoolUnmatchedYahooID)}, poolIDs(t, rows))
	assert.Equal(t, []string{fmt.Sprint(pgPoolNHLPlayerID), "C,LW"}, []string{rows[1][nhlCol], rows[1][eligibleCol]})
	assert.Equal(t, []string{"", "LW"}, []string{rows[2][nhlCol], rows[2][eligibleCol]})
}

// seedLeaguePool stores the fixture league, one NHL player and two pool
// players, the first matched to that NHL player.
func seedLeaguePool(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	_, err := pool.Exec(ctx,
		`INSERT INTO yahoo_leagues (id, league_key, name, season) VALUES ($1, $2, 'MCP test', $3)`,
		pgTestLeagueID, pgTestLeagueKey, pgTestSeason)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO players (id, yahoo_id, first_name, last_name) VALUES ($1, $2, 'Matched', 'Player')`,
		pgPoolNHLPlayerID, pgPoolMatchedYahooID)
	require.NoError(t, err)
	for _, p := range []struct {
		id       int
		eligible []string
	}{{pgPoolMatchedYahooID, []string{"C", "LW"}}, {pgPoolUnmatchedYahooID, []string{"LW"}}} {
		_, err = pool.Exec(ctx,
			`INSERT INTO yahoo_league_players (league_key, season, league_id, game_key, player_id, player_key, full_name,
			 eligible_positions, fetched_at) VALUES ($1, $2, $3, 465, $4, $5, 'Pool Player', $6, $7)`,
			pgTestLeagueKey, pgTestSeason, pgTestLeagueID, p.id, fmt.Sprintf("465.p.%d", p.id), p.eligible, poolFetchedAt)
		require.NoError(t, err)
	}
}
