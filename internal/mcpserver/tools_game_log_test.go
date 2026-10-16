package mcpserver

import (
	"context"
	"encoding/csv"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/database"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var gameLogTools = []string{"get_skater_game_log", "get_goalie_game_log"}

func TestGameLogSeasonOnlyUsesSeasonQuery(t *testing.T) {
	for _, tool := range gameLogTools {
		t.Run(tool, func(t *testing.T) {
			result, db := callNHLTool(t, tool, map[string]any{playerIDArg: float64(matchedPlayerID), "season": float64(testSeasonID)})
			require.False(t, result.IsError, resultText(t, result))
			require.Len(t, db.calls, 1)
			assert.Contains(t, db.calls[0].sql, "g.season = $2")
			assert.Equal(t, []any{matchedPlayerID, testSeasonID}, db.calls[0].args)
		})
	}
}

func TestGameLogDateRangeBindsBounds(t *testing.T) {
	day := func(text string) pgtype.Date {
		d, errResult := parseDate("test", text)
		require.Nil(t, errResult)
		return d
	}
	open := func(modifier pgtype.InfinityModifier) pgtype.Date {
		return pgtype.Date{InfinityModifier: modifier, Valid: true}
	}
	tests := map[string]struct {
		args       map[string]any
		start, end pgtype.Date
	}{
		"both bounds": {
			args:  map[string]any{startDateArg: "2025-10-01", endDateArg: "2025-10-31"},
			start: day("2025-10-01"), end: day("2025-10-31"),
		},
		"same day": {
			args:  map[string]any{startDateArg: "2025-10-08", endDateArg: "2025-10-08"},
			start: day("2025-10-08"), end: day("2025-10-08"),
		},
		"start only": {
			args:  map[string]any{startDateArg: "2025-10-01"},
			start: day("2025-10-01"), end: open(pgtype.Infinity),
		},
		"end only, empty start": {
			args:  map[string]any{startDateArg: "", endDateArg: "2025-10-31"},
			start: open(pgtype.NegativeInfinity), end: day("2025-10-31"),
		},
		"with season": {
			args:  map[string]any{"season": float64(testSeasonID), startDateArg: "2025-10-01"},
			start: day("2025-10-01"), end: open(pgtype.Infinity),
		},
	}
	for _, tool := range gameLogTools {
		for name, tc := range tests {
			t.Run(tool+"/"+name, func(t *testing.T) {
				args := map[string]any{playerIDArg: float64(matchedPlayerID)}
				for key, value := range tc.args {
					args[key] = value
				}
				result, db := callNHLTool(t, tool, args)
				require.False(t, result.IsError, resultText(t, result))
				require.Len(t, db.calls, 1)
				assert.Contains(t, db.calls[0].sql, "g.game_date >= $2 AND g.game_date <= $3")
				assert.Equal(t, []any{matchedPlayerID, tc.start, tc.end}, db.calls[0].args)
			})
		}
	}
}

func TestGameLogRejectsBadScopeBeforeSQL(t *testing.T) {
	tests := map[string]struct {
		args map[string]any
		want string
	}{
		"nothing": {
			args: map[string]any{},
			want: "season is required unless start_date or end_date is given",
		},
		"only empty dates": {
			args: map[string]any{startDateArg: "", endDateArg: ""},
			want: "season is required unless start_date or end_date is given",
		},
		"end before start": {
			args: map[string]any{startDateArg: "2025-10-31", endDateArg: "2025-10-01"},
			want: "end_date 2025-10-01 is before start_date 2025-10-31",
		},
		"malformed start": {
			args: map[string]any{"season": float64(testSeasonID), startDateArg: "2025-13-01"},
			want: `invalid start_date "2025-13-01": want a date as YYYY-MM-DD`,
		},
		"malformed end": {
			args: map[string]any{startDateArg: "2025-10-01", endDateArg: "Oct 31"},
			want: `invalid end_date "Oct 31": want a date as YYYY-MM-DD`,
		},
		"number as date": {
			args: map[string]any{endDateArg: float64(20251031)},
			want: "invalid end_date 20251031: want a date as YYYY-MM-DD",
		},
	}
	for _, tool := range gameLogTools {
		for name, tc := range tests {
			t.Run(tool+"/"+name, func(t *testing.T) {
				args := map[string]any{playerIDArg: float64(matchedPlayerID)}
				for key, value := range tc.args {
					args[key] = value
				}
				result, db := callNHLTool(t, tool, args)
				assert.True(t, result.IsError)
				assert.Equal(t, tc.want, resultText(t, result))
				assert.Empty(t, db.calls)
			})
		}
	}
}

func TestKeepSeason(t *testing.T) {
	seasonOf := func(season int32) int32 { return season }
	rows := []int32{testSeasonID - 10001, testSeasonID, testSeasonID}

	kept, err := keepSeason(gameLogScope{season: testSeasonID, seasonGiven: true}, slices.Clone(rows), nil, seasonOf)
	require.NoError(t, err)
	assert.Equal(t, []int32{testSeasonID, testSeasonID}, kept)

	kept, err = keepSeason(gameLogScope{}, slices.Clone(rows), nil, seasonOf)
	require.NoError(t, err)
	assert.Equal(t, rows, kept)
}

// PostgreSQL-backed coverage: the open bounds must reach the real date
// comparison as -infinity/infinity, and the season filter must apply to
// the rows the date range returns. It skips unless PUCKDB_TEST_PG_URL
// names a dedicated test database.

const (
	gameLogTestPGURL    = "PUCKDB_TEST_PG_URL"
	gameLogTestDBMarker = "test"
	// gameLogTestTeam is a team ID no real club uses.
	gameLogTestTeam      = 9_960
	gameLogTestOpponent  = 9_961
	gameLogTestSkater    = 9_960_001
	gameLogTestGoalie    = 9_960_002
	gameLogTestGameBase  = 9_960_000_000
	gameLogPriorSeasonID = testSeasonID - 10001
)

// gameLogTestGames straddle a season boundary: game_id → date, season.
var gameLogTestGames = []struct {
	id     int64
	date   string
	season int32
}{
	{gameLogTestGameBase + 1, "2025-04-10", gameLogPriorSeasonID},
	{gameLogTestGameBase + 2, "2025-10-08", testSeasonID},
	{gameLogTestGameBase + 3, "2025-10-20", testSeasonID},
	{gameLogTestGameBase + 4, "2025-11-05", testSeasonID},
}

var gameLogMigrateOnce struct {
	sync.Once
	err error
}

func openGameLogTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv(gameLogTestPGURL)
	if dbURL == "" {
		t.Skipf("set %s to run PostgreSQL game log tests", gameLogTestPGURL)
	}
	require.Contains(t, dbURL, gameLogTestDBMarker, "test database URL must contain %q", gameLogTestDBMarker)
	gameLogMigrateOnce.Do(func() { gameLogMigrateOnce.err = database.MigrateUp(dbURL) })
	require.NoError(t, gameLogMigrateOnce.err)
	pool, err := pgxpool.New(context.Background(), dbURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

func execGameLogFixture(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	_, err := pool.Exec(context.Background(), sql, args...)
	require.NoError(t, err)
}

func cleanupGameLogFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	execGameLogFixture(t, pool, `DELETE FROM games WHERE id BETWEEN $1 AND $1 + 100`, gameLogTestGameBase)
	execGameLogFixture(t, pool, `DELETE FROM players WHERE id IN ($1, $2)`, gameLogTestSkater, gameLogTestGoalie)
	execGameLogFixture(t, pool, `DELETE FROM season_teams WHERE team_id IN ($1, $2)`, gameLogTestTeam, gameLogTestOpponent)
}

func seedGameLogFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	for _, season := range []int32{gameLogPriorSeasonID, testSeasonID} {
		// Other packages' tests may truncate seasons; an existing row is kept.
		execGameLogFixture(t, pool, `
INSERT INTO seasons (id, standings_start, standings_end)
VALUES ($1, make_date($1 / 10000, 10, 1), make_date($1 % 10000, 6, 30)) ON CONFLICT DO NOTHING`, season)
		execGameLogFixture(t, pool, `
INSERT INTO season_teams (season, team_id, full_name, abbrev, division_name, division_abbrev)
VALUES ($1, $2, 'Game Log Test', 'GLT', 'Test', 'T'), ($1, $3, 'Game Log Opponent', 'GLO', 'Test', 'T')`,
			season, gameLogTestTeam, gameLogTestOpponent)
	}
	execGameLogFixture(t, pool, `
INSERT INTO players (id, first_name, last_name) VALUES ($1, 'Game', 'Skater'), ($2, 'Game', 'Goalie')`,
		gameLogTestSkater, gameLogTestGoalie)
	for _, game := range gameLogTestGames {
		execGameLogFixture(t, pool, `
INSERT INTO games (id, season, game_type, game_date, game_state, home_team_id, away_team_id)
VALUES ($1, $2, 'regular_season', $3, 'FINAL', $4, $5)`,
			game.id, game.season, game.date, gameLogTestTeam, gameLogTestOpponent)
		execGameLogFixture(t, pool, `
INSERT INTO game_skater_stats (game_id, player_id, team_id, is_home, sweater_number, position)
VALUES ($1, $2, $3, TRUE, 14, 'C')`, game.id, gameLogTestSkater, gameLogTestTeam)
		execGameLogFixture(t, pool, `
INSERT INTO game_goalie_stats (game_id, player_id, team_id, is_home, sweater_number)
VALUES ($1, $2, $3, TRUE, 35)`, game.id, gameLogTestGoalie, gameLogTestTeam)
	}
}

// gameIDsOf reads the game_id column of a CSV tool result.
func gameIDsOf(t *testing.T, result *mcp.CallToolResult) []string {
	t.Helper()
	text := resultText(t, result)
	require.False(t, result.IsError, text)
	if text == noResultsText {
		return nil
	}
	records, err := csv.NewReader(strings.NewReader(text)).ReadAll()
	require.NoError(t, err)
	column := slices.Index(records[0], "game_id")
	require.GreaterOrEqual(t, column, 0, "no game_id column: %v", records[0])
	var ids []string
	for _, record := range records[1:] {
		ids = append(ids, record[column])
	}
	return ids
}

func TestGameLogToolsAgainstPostgres(t *testing.T) {
	pool := openGameLogTestDB(t)
	cleanupGameLogFixture(t, pool)
	t.Cleanup(func() { cleanupGameLogFixture(t, pool) })
	seedGameLogFixture(t, pool)

	srv := server.NewMCPServer("test", serverVersion)
	registerNHLTools(srv, sqlcdb.New(pool))
	game := func(n int) string { return strconv.FormatInt(gameLogTestGames[n-1].id, 10) }

	tests := map[string]struct {
		args map[string]any
		want []string
	}{
		"season only":              {args: map[string]any{"season": float64(testSeasonID)}, want: []string{game(2), game(3), game(4)}},
		"start only":               {args: map[string]any{startDateArg: "2025-10-15"}, want: []string{game(3), game(4)}},
		"end only spans seasons":   {args: map[string]any{endDateArg: "2025-10-10"}, want: []string{game(1), game(2)}},
		"bounds are inclusive":     {args: map[string]any{startDateArg: "2025-10-08", endDateArg: "2025-10-20"}, want: []string{game(2), game(3)}},
		"season narrows the range": {args: map[string]any{"season": float64(testSeasonID), startDateArg: "2025-04-01", endDateArg: "2025-10-10"}, want: []string{game(2)}},
		"range outside the season": {args: map[string]any{"season": float64(testSeasonID), endDateArg: "2025-06-30"}},
	}
	players := map[string]int64{"get_skater_game_log": gameLogTestSkater, "get_goalie_game_log": gameLogTestGoalie}
	for _, tool := range gameLogTools {
		for name, tc := range tests {
			t.Run(tool+"/"+name, func(t *testing.T) {
				args := map[string]any{playerIDArg: float64(players[tool])}
				for key, value := range tc.args {
					args[key] = value
				}
				result := callTool(t, srv.GetTool(tool).Handler, args)
				assert.Equal(t, tc.want, gameIDsOf(t, result))
			})
		}
	}
}
