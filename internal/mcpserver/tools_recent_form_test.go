package mcpserver

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recentFormQueryFake struct {
	skaterCalls int
	goalieCalls int
	skater      sqlcdb.GetSkaterRecentFormParams
	goalie      sqlcdb.GetGoalieRecentFormParams
}

func (f *recentFormQueryFake) GetSkaterRecentForm(_ context.Context, params sqlcdb.GetSkaterRecentFormParams) ([]sqlcdb.GetSkaterRecentFormRow, error) {
	f.skaterCalls++
	f.skater = params
	return nil, nil
}

func (f *recentFormQueryFake) GetGoalieRecentForm(_ context.Context, params sqlcdb.GetGoalieRecentFormParams) ([]sqlcdb.GetGoalieRecentFormRow, error) {
	f.goalieCalls++
	f.goalie = params
	return nil, nil
}

// recentFormTestNow is already November 1 in UTC (the server's zone in
// the scratch image) but still October 31 in the NHL's schedule zone.
var recentFormTestNow = time.Date(2025, 11, 1, 3, 30, 0, 0, time.UTC)

func recentFormTestTool(t *testing.T, queries recentFormQueries) server.ServerTool {
	t.Helper()
	srv := server.NewMCPServer("test", serverVersion)
	registerRecentFormTools(srv, queries, func() time.Time { return recentFormTestNow })
	tool := srv.GetTool(recentFormToolName)
	require.NotNil(t, tool)
	return *tool
}

func testDate(t *testing.T, text string) pgtype.Date {
	t.Helper()
	day, err := time.Parse(time.DateOnly, text)
	require.NoError(t, err)
	return pgtype.Date{Time: day, Valid: true}
}

func TestRecentFormToolDefaults(t *testing.T) {
	queries := &recentFormQueryFake{}
	tool := recentFormTestTool(t, queries)

	result := callTool(t, tool.Handler, map[string]any{"season": float64(testSeasonID), recentFormGroupArg: "Skaters"})

	require.False(t, result.IsError, resultText(t, result))
	assert.Equal(t, 1, queries.skaterCalls)
	assert.Zero(t, queries.goalieCalls)
	assert.Equal(t, sqlcdb.GetSkaterRecentFormParams{
		Season: testSeasonID, GameType: sqlcdb.GameTypeRegularSeason, AsOf: testDate(t, "2025-10-31"),
		Days: defaultRecentFormDays, ResultLimit: defaultRecentFormLimit,
	}, queries.skater)
	assert.Equal(t, "# group=skaters season=20252026 game_type=regular_season window_start=2025-10-02 as_of=2025-10-31 days=30\n"+
		"# players\n"+noResultsText+"\n", resultText(t, result))
}

func TestRecentFormToolPassesSkaterArguments(t *testing.T) {
	queries := &recentFormQueryFake{}
	tool := recentFormTestTool(t, queries)

	result := callTool(t, tool.Handler, map[string]any{
		"season": float64(testSeasonID), recentFormGroupArg: recentFormSkaters, recentFormAsOfArg: "2025-03-01",
		recentFormDaysArg: float64(1), seasonPositionArg: "d", "limit": float64(maxRecentFormLimit),
	})

	require.False(t, result.IsError, resultText(t, result))
	assert.Equal(t, sqlcdb.GetSkaterRecentFormParams{
		Season: testSeasonID, GameType: sqlcdb.GameTypeRegularSeason, AsOf: testDate(t, "2025-03-01"), Days: 1,
		Positions:   []string{string(sqlcdb.PlayerPositionD)},
		ResultLimit: maxRecentFormLimit,
	}, queries.skater)
	assert.True(t, strings.HasPrefix(resultText(t, result),
		"# group=skaters season=20252026 game_type=regular_season window_start=2025-03-01 as_of=2025-03-01 days=1 position=D\n"),
		resultText(t, result))
}

func TestRecentFormToolExpandsForwards(t *testing.T) {
	queries := &recentFormQueryFake{}
	tool := recentFormTestTool(t, queries)

	result := callTool(t, tool.Handler, map[string]any{
		"season": float64(testSeasonID), recentFormGroupArg: recentFormSkaters, seasonPositionArg: "f",
	})

	require.False(t, result.IsError, resultText(t, result))
	assert.Equal(t, []string{"C", "LW", "RW", "F"}, queries.skater.Positions)
	assert.Contains(t, resultText(t, result), " position=F\n")
}

func TestRecentFormToolPassesGoalieArguments(t *testing.T) {
	queries := &recentFormQueryFake{}
	tool := recentFormTestTool(t, queries)

	result := callTool(t, tool.Handler, map[string]any{
		"season": float64(testSeasonID), recentFormGroupArg: recentFormGoalies, recentFormAsOfArg: "2025-03-01",
		recentFormDaysArg: float64(maxRecentFormDays), seasonPositionArg: "G",
	})

	require.False(t, result.IsError, resultText(t, result))
	assert.Zero(t, queries.skaterCalls)
	assert.Equal(t, sqlcdb.GetGoalieRecentFormParams{
		Season: testSeasonID, GameType: sqlcdb.GameTypeRegularSeason, AsOf: testDate(t, "2025-03-01"),
		Days: maxRecentFormDays, MinToiSeconds: minGoalieAppearanceSeconds, ResultLimit: defaultRecentFormLimit,
	}, queries.goalie)
	assert.Contains(t, resultText(t, result), " window_start=2024-03-01 as_of=2025-03-01 days=366\n")
}

func TestRecentFormToolRejectsInvalidArgumentsBeforeSQL(t *testing.T) {
	valid := map[string]any{"season": float64(testSeasonID), recentFormGroupArg: recentFormSkaters}
	tests := map[string]struct {
		change map[string]any
		drop   string
		want   string
	}{
		"missing group":    {drop: recentFormGroupArg, want: "group is required"},
		"empty group":      {change: map[string]any{recentFormGroupArg: " "}, want: "group is required"},
		"singular group":   {change: map[string]any{recentFormGroupArg: "skater"}, want: `unknown group "skater"; accepted: skaters, goalies`},
		"non-string group": {change: map[string]any{recentFormGroupArg: float64(1)}, want: "invalid group 1: want a string"},
		"impossible as_of": {change: map[string]any{recentFormAsOfArg: "2025-02-30"}, want: "invalid as_of"},
		"zero days":        {change: map[string]any{recentFormDaysArg: float64(0)}, want: "invalid days 0: want an integer from 1 to 366"},
		"too many days":    {change: map[string]any{recentFormDaysArg: float64(maxRecentFormDays + 1)}, want: "invalid days"},
		"zero limit":       {change: map[string]any{"limit": float64(0)}, want: "invalid limit 0: want an integer from 1 to 1000"},
		"limit beyond max": {change: map[string]any{"limit": float64(maxRecentFormLimit + 1)}, want: "invalid limit"},
		"goalie position":  {change: map[string]any{seasonPositionArg: "G"}, want: "accepted positions: C, LW, RW, F, D"},
		"skater position for goalies": {
			change: map[string]any{recentFormGroupArg: recentFormGoalies, seasonPositionArg: "D"}, want: "accepted position: G",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			args := map[string]any{}
			for key, value := range valid {
				if key != tc.drop {
					args[key] = value
				}
			}
			for key, value := range tc.change {
				args[key] = value
			}
			queries := &recentFormQueryFake{}
			result := callTool(t, recentFormTestTool(t, queries).Handler, args)
			assert.True(t, result.IsError)
			assert.Contains(t, resultText(t, result), tc.want)
			assert.Zero(t, queries.skaterCalls+queries.goalieCalls)
		})
	}
}

// PostgreSQL-backed coverage: the window bounds, season and game type
// filters, the goalie relief cut-off and last_team_id. It skips unless
// PUCKDB_TEST_PG_URL names a dedicated test database.

const (
	// recentFormTestTeam and the next two IDs are team IDs no real club uses.
	recentFormTestTeam     = 9_970
	recentFormTestOpponent = 9_971
	recentFormTestNewTeam  = 9_972
	recentFormTestForward  = 9_970_001
	recentFormTestDefender = 9_970_002
	recentFormTestGoalie   = 9_970_003
	recentFormTestGameBase = 9_970_000_000
	// recentFormTestGameSpan is the range of game IDs above the base the
	// fixture may use.
	recentFormTestGameSpan = 100
	// recentFormTestAsOf and recentFormTestDays give the window
	// 2025-10-18..2025-10-31.
	recentFormTestAsOf = "2025-10-31"
	recentFormTestDays = 14
	// recentFormOutsideGoals marks a game outside the window: counting it
	// would show in the totals.
	recentFormOutsideGoals = 5
	fullGameSeconds        = 3600
	reliefSeconds          = minGoalieAppearanceSeconds - 1
)

// recentFormTestGame is one fixture game and the forward's and goalie's
// lines in it.
type recentFormTestGame struct {
	id                 int64
	season             int32
	gameType           sqlcdb.GameType
	date               string
	forwardTeam        int64
	forwardGoals       int
	goalieDecision     string
	goalieTOISeconds   int
	goalieGoalsAgainst int
}

var recentFormTestGames = []recentFormTestGame{
	{recentFormTestGameBase + 1, testSeasonID, sqlcdb.GameTypeRegularSeason, "2025-10-17", recentFormTestTeam, recentFormOutsideGoals, "W", fullGameSeconds, 0},
	{recentFormTestGameBase + 2, testSeasonID, sqlcdb.GameTypeRegularSeason, "2025-10-18", recentFormTestTeam, 1, "W", fullGameSeconds, 2},
	{recentFormTestGameBase + 3, testSeasonID, sqlcdb.GameTypeRegularSeason, "2025-10-25", recentFormTestTeam, 1, "L", reliefSeconds, 3},
	{recentFormTestGameBase + 4, testSeasonID, sqlcdb.GameTypeRegularSeason, recentFormTestAsOf, recentFormTestNewTeam, 1, "OTL", fullGameSeconds, 3},
	{recentFormTestGameBase + 5, testSeasonID, sqlcdb.GameTypeRegularSeason, "2025-11-01", recentFormTestNewTeam, recentFormOutsideGoals, "W", fullGameSeconds, 0},
	{recentFormTestGameBase + 6, testSeasonID, sqlcdb.GameTypePlayoffs, "2025-10-20", recentFormTestTeam, recentFormOutsideGoals, "W", fullGameSeconds, 0},
	{recentFormTestGameBase + 7, gameLogPriorSeasonID, sqlcdb.GameTypeRegularSeason, "2025-10-21", recentFormTestTeam, recentFormOutsideGoals, "W", fullGameSeconds, 0},
}

func cleanupRecentFormFixture(t *testing.T, exec func(string, ...any)) {
	t.Helper()
	exec(`DELETE FROM games WHERE id BETWEEN $1 AND $2`, recentFormTestGameBase, recentFormTestGameBase+recentFormTestGameSpan)
	exec(`DELETE FROM players WHERE id BETWEEN $1 AND $2`, recentFormTestForward, recentFormTestGoalie)
	exec(`DELETE FROM season_teams WHERE team_id BETWEEN $1 AND $2`, recentFormTestTeam, recentFormTestNewTeam)
}

func seedRecentFormFixture(t *testing.T, exec func(string, ...any)) {
	t.Helper()
	for _, season := range []int32{gameLogPriorSeasonID, testSeasonID} {
		// Other packages' tests may truncate seasons; an existing row is kept.
		exec(`
INSERT INTO seasons (id, standings_start, standings_end)
VALUES ($1, make_date($1 / 10000, 10, 1), make_date($1 % 10000, 6, 30)) ON CONFLICT DO NOTHING`, season)
		for _, team := range []int64{recentFormTestTeam, recentFormTestOpponent, recentFormTestNewTeam} {
			exec(`
INSERT INTO season_teams (season, team_id, full_name, abbrev, division_name, division_abbrev)
VALUES ($1, $2, 'Recent Form Test', 'RFT', 'Test', 'T')`, season, team)
		}
	}
	exec(`
INSERT INTO players (id, first_name, last_name, position)
VALUES ($1, 'Form', 'Forward', 'C'), ($2, 'Form', 'Defender', 'D'), ($3, 'Form', 'Goalie', 'G')`,
		recentFormTestForward, recentFormTestDefender, recentFormTestGoalie)
	for _, game := range recentFormTestGames {
		exec(`
INSERT INTO games (id, season, game_type, game_date, game_state, home_team_id, away_team_id)
VALUES ($1, $2, $3, $4, 'FINAL', $5, $6)`,
			game.id, game.season, game.gameType, game.date, game.forwardTeam, recentFormTestOpponent)
		exec(`
INSERT INTO game_skater_stats (game_id, player_id, team_id, is_home, sweater_number, position, goals, points)
VALUES ($1, $2, $3, TRUE, 14, 'C', $4, $4)`, game.id, recentFormTestForward, game.forwardTeam, game.forwardGoals)
		exec(`
INSERT INTO game_goalie_stats (game_id, player_id, team_id, is_home, sweater_number, decision, toi_seconds, goals_against, shots_against, saves)
VALUES ($1, $2, $3, FALSE, 35, $4, $5, $6::smallint, 30, 30 - $6::smallint)`,
			game.id, recentFormTestGoalie, recentFormTestOpponent, game.goalieDecision, game.goalieTOISeconds, game.goalieGoalsAgainst)
	}
	// The defender plays one game in the window, with fewer points.
	exec(`
INSERT INTO game_skater_stats (game_id, player_id, team_id, is_home, sweater_number, position, assists, points)
VALUES ($1, $2, $3, TRUE, 44, 'D', 2, 2)`, recentFormTestGameBase+3, recentFormTestDefender, recentFormTestTeam)
}

// recentFormRows parses a get_recent_form result into maps keyed by
// column, one per player, skipping the "# " lines.
func recentFormRows(t *testing.T, result *mcp.CallToolResult) []map[string]string {
	t.Helper()
	text := resultText(t, result)
	require.False(t, result.IsError, text)
	var body []string
	for line := range strings.SplitSeq(strings.TrimSuffix(text, "\n"), "\n") {
		if !strings.HasPrefix(line, commentPrefix) {
			body = append(body, line)
		}
	}
	if len(body) == 1 && body[0] == noResultsText {
		return nil
	}
	records := parseCSV(t, strings.Join(body, "\n"))
	rows := make([]map[string]string, 0, len(records)-1)
	for _, record := range records[1:] {
		row := map[string]string{}
		for i, column := range records[0] {
			row[column] = record[i]
		}
		rows = append(rows, row)
	}
	return rows
}

func TestRecentFormToolAgainstPostgres(t *testing.T) {
	pool := openGameLogTestDB(t)
	exec := func(sql string, args ...any) { execGameLogFixture(t, pool, sql, args...) }
	cleanupRecentFormFixture(t, exec)
	t.Cleanup(func() { cleanupRecentFormFixture(t, exec) })
	seedRecentFormFixture(t, exec)

	tool := recentFormTestTool(t, sqlcdb.New(pool))
	call := func(args map[string]any) []map[string]string {
		t.Helper()
		full := map[string]any{"season": float64(testSeasonID), recentFormAsOfArg: recentFormTestAsOf, recentFormDaysArg: float64(recentFormTestDays)}
		for key, value := range args {
			full[key] = value
		}
		return recentFormRows(t, callTool(t, tool.Handler, full))
	}

	t.Run("skaters", func(t *testing.T) {
		rows := call(map[string]any{recentFormGroupArg: recentFormSkaters})
		require.Len(t, rows, 2)
		forward, defender := rows[0], rows[1]
		assert.Equal(t, "Forward", forward["last_name"])
		assert.Equal(t, "3", forward["gp"], "both window ends count; outside, playoff and other-season games do not")
		assert.Equal(t, "3", forward["goals"])
		assert.Equal(t, "3", forward["points"])
		assert.Equal(t, strconv.Itoa(recentFormTestNewTeam), forward["last_team_id"], "club of the last game in the window")
		assert.Equal(t, "Defender", defender["last_name"])
		assert.Equal(t, "2", defender["points"])
	})
	t.Run("skater position", func(t *testing.T) {
		rows := call(map[string]any{recentFormGroupArg: recentFormSkaters, seasonPositionArg: "D"})
		require.Len(t, rows, 1)
		assert.Equal(t, "Defender", rows[0]["last_name"])
	})
	t.Run("F selects every forward", func(t *testing.T) {
		rows := call(map[string]any{recentFormGroupArg: recentFormSkaters, seasonPositionArg: "F"})
		require.Len(t, rows, 1)
		assert.Equal(t, "Forward", rows[0]["last_name"], "a C is a forward")
	})
	t.Run("one-day window", func(t *testing.T) {
		rows := call(map[string]any{recentFormGroupArg: recentFormSkaters, recentFormDaysArg: float64(1)})
		require.Len(t, rows, 1)
		assert.Equal(t, "1", rows[0]["gp"])
		assert.Equal(t, strconv.Itoa(recentFormTestNewTeam), rows[0]["last_team_id"])
	})
	t.Run("goalies", func(t *testing.T) {
		rows := call(map[string]any{recentFormGroupArg: recentFormGoalies})
		require.Len(t, rows, 1)
		goalie := rows[0]
		assert.Equal(t, "2", goalie["gp"], "the relief stint is left out")
		assert.Equal(t, "1", goalie["wins"])
		assert.Equal(t, "0", goalie["losses"])
		assert.Equal(t, "1", goalie["ot_losses"])
		assert.Equal(t, "5", goalie["ga"])
		assert.Equal(t, "2.5", goalie["gaa"])
		assert.Equal(t, "0.917", goalie["sv_pct"])
		assert.Equal(t, strconv.Itoa(recentFormTestOpponent), goalie["last_team_id"])
	})
	t.Run("other season", func(t *testing.T) {
		rows := call(map[string]any{"season": float64(gameLogPriorSeasonID), recentFormGroupArg: recentFormSkaters})
		require.Len(t, rows, 1)
		assert.Equal(t, "1", rows[0]["gp"])
		assert.Equal(t, "5", rows[0]["goals"])
	})
}
