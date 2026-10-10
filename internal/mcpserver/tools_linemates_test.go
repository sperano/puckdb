package mcpserver

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const linemateToolName = "get_player_linemates"

// wantLinemateColumns pins the get_player_linemates CSV header.
var wantLinemateColumns = []string{
	"team_id", "team_abbrev", "teammate_id", "first_name", "last_name", "position",
	"games_together", "shared_toi_seconds", "share_pct",
}

type linemateQueryFake struct {
	calls  int
	params sqlcdb.ListPlayerEvenStrengthLinematesParams
	rows   []sqlcdb.ListPlayerEvenStrengthLinematesRow
	err    error
}

func (f *linemateQueryFake) ListPlayerEvenStrengthLinemates(_ context.Context, params sqlcdb.ListPlayerEvenStrengthLinematesParams) ([]sqlcdb.ListPlayerEvenStrengthLinematesRow, error) {
	f.calls++
	f.params = params
	return f.rows, f.err
}

func linemateTestServer(q linemateQueries) *server.MCPServer {
	srv := server.NewMCPServer("test", serverVersion)
	registerLinemateTools(srv, q)
	return srv
}

func callLinemateTool(t *testing.T, q linemateQueries, args map[string]any) string {
	t.Helper()
	tool := linemateTestServer(q).GetTool(linemateToolName)
	require.NotNil(t, tool)
	result := callTool(t, tool.Handler, args)
	text := resultText(t, result)
	require.False(t, result.IsError, text)
	return text
}

func TestLinemateToolDefaults(t *testing.T) {
	q := &linemateQueryFake{}
	callLinemateTool(t, q, map[string]any{playerIDArg: float64(matchedPlayerID), "season": float64(testSeasonID)})

	require.Equal(t, 1, q.calls)
	assert.Equal(t, sqlcdb.ListPlayerEvenStrengthLinematesParams{
		PlayerID:    matchedPlayerID,
		Season:      pgtype.Int4{Int32: testSeasonID, Valid: true},
		ResultLimit: defaultLinemateLimit,
	}, q.params, "no dates, both game types (NULL), default limit")
}

func TestLinemateToolPassesEveryFilter(t *testing.T) {
	q := &linemateQueryFake{}
	callLinemateTool(t, q, map[string]any{
		playerIDArg:         float64(matchedPlayerID),
		linemateStartArg:    "2025-10-01",
		linemateEndArg:      "2025-10-01",
		linemateGameTypeArg: " Playoffs ",
		"limit":             float64(maxLinemateLimit),
	})

	assert.False(t, q.params.Season.Valid)
	assert.Equal(t, "2025-10-01", q.params.StartDate.Time.Format("2006-01-02"))
	assert.Equal(t, q.params.StartDate, q.params.EndDate, "a one-day range is valid")
	assert.Equal(t, sqlcdb.NullGameType{GameType: sqlcdb.GameTypePlayoffs, Valid: true}, q.params.GameType)
	assert.Equal(t, int32(maxLinemateLimit), q.params.ResultLimit)
}

func TestLinemateToolAcceptsOneDateBound(t *testing.T) {
	for _, arg := range []string{linemateStartArg, linemateEndArg} {
		q := &linemateQueryFake{}
		callLinemateTool(t, q, map[string]any{playerIDArg: float64(matchedPlayerID), arg: "2025-10-01"})
		assert.Equal(t, 1, q.calls, arg)
	}
}

func TestLinemateToolRejectsBadArgumentsBeforeSQL(t *testing.T) {
	base := map[string]any{playerIDArg: float64(matchedPlayerID), "season": float64(testSeasonID)}
	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{"no season or dates", map[string]any{playerIDArg: float64(matchedPlayerID)}, "pass a season, a start_date/end_date range, or both"},
		{"reversed range", withArg(withArg(base, linemateStartArg, "2025-10-02"), linemateEndArg, "2025-10-01"), "start_date is after end_date"},
		{"malformed date", withArg(base, linemateEndArg, "10/01/2025"), "invalid end_date format"},
		{"unknown game type", withArg(base, linemateGameTypeArg, " All_Star"), `unknown game_type " All_Star"; accepted: regular_season, playoffs, preseason`},
		{"zero limit", withArg(base, "limit", float64(0)), "invalid limit 0"},
		{"limit above maximum", withArg(base, "limit", float64(maxLinemateLimit+1)), "invalid limit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := &linemateQueryFake{}
			result := callTool(t, linemateTestServer(q).GetTool(linemateToolName).Handler, tt.args)
			assert.True(t, result.IsError)
			assert.Contains(t, resultText(t, result), tt.want)
			assert.Zero(t, q.calls)
		})
	}
}

func TestLinemateToolReportsQueryError(t *testing.T) {
	q := &linemateQueryFake{err: errors.New("boom")}
	result := callTool(t, linemateTestServer(q).GetTool(linemateToolName).Handler,
		map[string]any{playerIDArg: float64(matchedPlayerID), "season": float64(testSeasonID)})
	assert.True(t, result.IsError)
	assert.Equal(t, "boom", resultText(t, result))
}

func TestLinemateToolRendersCSV(t *testing.T) {
	q := &linemateQueryFake{rows: []sqlcdb.ListPlayerEvenStrengthLinematesRow{{
		TeamID:           8,
		TeamAbbrev:       pgtype.Text{String: "MTL", Valid: true},
		TeammateID:       8481540,
		FirstName:        pgtype.Text{String: "Cole", Valid: true},
		LastName:         pgtype.Text{String: "Caufield", Valid: true},
		Position:         sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionRW, Valid: true},
		GamesTogether:    70,
		SharedToiSeconds: 52000,
		SharePct:         61.5,
	}}}
	rows := parseCSV(t, callLinemateTool(t, q, map[string]any{playerIDArg: float64(matchedPlayerID), "season": float64(testSeasonID)}))
	assert.Equal(t, [][]string{
		wantLinemateColumns,
		{"8", "MTL", "8481540", "Cole", "Caufield", "RW", "70", "52000", "61.5"},
	}, rows)
}

// PostgreSQL fixture: one player traded from club A to club B in season
// linemateSeason, with games of every kind the filters must tell apart.
const (
	linemateIDBase     int64 = 990771000
	linematePlayer           = linemateIDBase + 1
	linemateTeammate2        = linemateIDBase + 2
	linemateTeammate3        = linemateIDBase + 3
	linemateTeammate4        = linemateIDBase + 4
	linemateTeammate5        = linemateIDBase + 5
	linemateClubA            = linemateIDBase + 10
	linemateClubB            = linemateIDBase + 11
	linemateSeason     int32 = 20242025
	linematePrevSeason int32 = 20232024
	linemateGameBase         = linemateIDBase * 10
)

// linemateGame is one fixture game: the player's even-strength TOI and
// the shared TOI per teammate.
type linemateGame struct {
	season   int32
	gameType sqlcdb.GameType
	date     string
	club     int64
	toi      int32
	shared   map[int64]int32
}

var linemateGames = []linemateGame{
	{linemateSeason, sqlcdb.GameTypeRegularSeason, "2024-10-10", linemateClubA, 600, map[int64]int32{linemateTeammate2: 300, linemateTeammate3: 200}},
	{linemateSeason, sqlcdb.GameTypeRegularSeason, "2024-11-10", linemateClubA, 600, map[int64]int32{linemateTeammate2: 400, linemateTeammate4: 100}},
	{linemateSeason, sqlcdb.GameTypeRegularSeason, "2025-01-10", linemateClubB, 500, map[int64]int32{linemateTeammate5: 250}},
	{linematePrevSeason, sqlcdb.GameTypeRegularSeason, "2024-03-01", linemateClubA, 600, map[int64]int32{linemateTeammate2: 450}},
	{linemateSeason, sqlcdb.GameTypePlayoffs, "2025-04-25", linemateClubA, 700, map[int64]int32{linemateTeammate3: 600}},
	{linemateSeason, sqlcdb.GameTypePreseason, "2024-09-25", linemateClubA, 400, map[int64]int32{linemateTeammate2: 400}},
}

func TestLinemateQueryAggregatesPerClubAndFilters(t *testing.T) {
	pool := openMCPTestDB(t)
	cleanupLinemateFixture(t, pool)
	t.Cleanup(func() { cleanupLinemateFixture(t, pool) })
	seedLinemateFixture(t, pool)
	q := sqlcdb.New(pool)

	// Club A in the season, regular season and playoffs: 1900 s of the
	// player's even-strength time; club B: 500 s.
	assertLinemates(t, q, map[string]any{"season": float64(linemateSeason)}, [][]string{
		{"A", "3", "2", "800", "42.1"},
		{"A", "2", "2", "700", "36.8"},
		{"B", "5", "1", "250", "50"},
		{"A", "4", "1", "100", "5.3"},
	})
	assertLinemates(t, q, map[string]any{"season": float64(linemateSeason), "limit": float64(2)}, [][]string{
		{"A", "3", "2", "800", "42.1"},
		{"A", "2", "2", "700", "36.8"},
	})
	assertLinemates(t, q, map[string]any{"season": float64(linemateSeason), linemateGameTypeArg: "regular_season"}, [][]string{
		{"A", "2", "2", "700", "58.3"},
		{"B", "5", "1", "250", "50"},
		{"A", "3", "1", "200", "16.7"},
		{"A", "4", "1", "100", "8.3"},
	})
	assertLinemates(t, q, map[string]any{"season": float64(linemateSeason), linemateGameTypeArg: "preseason"}, [][]string{
		{"A", "2", "1", "400", "100"},
	})
	assertLinemates(t, q, map[string]any{linemateStartArg: "2024-11-01", linemateEndArg: "2025-01-31"}, [][]string{
		{"A", "2", "1", "400", "66.7"},
		{"B", "5", "1", "250", "50"},
		{"A", "4", "1", "100", "16.7"},
	})
	assertLinemates(t, q, map[string]any{linemateEndArg: "2024-06-30"}, [][]string{
		{"A", "2", "1", "450", "75"},
	})
}

// assertLinemates calls the tool for linematePlayer and compares club
// abbreviation, teammate offset, games, shared seconds and share per row.
func assertLinemates(t *testing.T, q *sqlcdb.Queries, args map[string]any, want [][]string) {
	t.Helper()
	args[playerIDArg] = float64(linematePlayer)
	rows := parseCSV(t, callLinemateTool(t, q, args))
	require.Equal(t, wantLinemateColumns, rows[0])
	got := make([][]string, 0, len(rows)-1)
	for _, row := range rows[1:] {
		// Every fixture teammate is "Mate <offset from linemateIDBase>".
		assert.Equal(t, "Mate", row[3])
		got = append(got, []string{row[1], row[4], row[6], row[7], row[8]})
	}
	assert.Equal(t, want, got, "%v", args)
}

func seedLinemateFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		_, err := pool.Exec(ctx, sql, args...)
		require.NoError(t, err)
	}
	exec(`INSERT INTO players (id, first_name, last_name, position)
	      SELECT $1 + id, 'Mate', id::text, 'C' FROM generate_series(1, 5) AS id`, linemateIDBase)
	for _, season := range []int32{linematePrevSeason, linemateSeason} {
		// Seasons are reference data other tests may also seed; they stay.
		exec(`INSERT INTO seasons (id, standings_start, standings_end)
		      VALUES ($1, '2000-10-01', '2001-04-15') ON CONFLICT (id) DO NOTHING`, season)
		exec(`INSERT INTO season_teams (season, team_id, full_name, abbrev, division_name, division_abbrev)
		      VALUES ($1, $2, 'Club A', 'A', 'D', 'D'), ($1, $3, 'Club B', 'B', 'D', 'D')`,
			season, linemateClubA, linemateClubB)
	}
	for i, g := range linemateGames {
		gameID := linemateGameBase + int64(i)
		exec(`INSERT INTO games (id, season, game_type, game_date, game_state, home_team_id, away_team_id)
		      VALUES ($1, $2, $3, $4, 'FINAL', $5, $6)`,
			gameID, g.season, g.gameType, g.date, g.club, linemateIDBase+99)
		exec(`INSERT INTO even_strength_skater_games (game_id, player_id, team_id, toi_seconds)
		      VALUES ($1, $2, $3, $4)`, gameID, linematePlayer, g.club, g.toi)
		for teammate, seconds := range g.shared {
			// Both directions, as the import stores them, plus a pair
			// without the player that must not count.
			exec(`INSERT INTO even_strength_pair_toi (game_id, player_id, teammate_id, shared_toi_seconds)
			      VALUES ($1, $2, $3, $4), ($1, $3, $2, $4)`, gameID, linematePlayer, teammate, seconds)
		}
		exec(`INSERT INTO even_strength_pair_toi (game_id, player_id, teammate_id, shared_toi_seconds)
		      VALUES ($1, $2, $3, 1) ON CONFLICT DO NOTHING`, gameID, linemateTeammate2, linemateTeammate4)
	}
}

// cleanupLinemateFixture removes the fixture; the even-strength rows
// cascade from games.
func cleanupLinemateFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	_, err := pool.Exec(ctx, `DELETE FROM games WHERE id BETWEEN $1 AND $1 + 99`, linemateGameBase)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `DELETE FROM season_teams WHERE team_id IN ($1, $2)`, linemateClubA, linemateClubB)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `DELETE FROM players WHERE id BETWEEN $1 + 1 AND $1 + 5`, linemateIDBase)
	require.NoError(t, err)
}
