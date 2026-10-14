package evenstrength

// PostgreSQL test harness and game fixtures for the even-strength rebuild.
// testdata/nhl_games.json holds three real games of October 2025, converted
// from the public NHL API on 2026-10-13 (shift charts from
// api.nhle.com/stats/rest/en/shiftcharts, box-score rosters and goals from
// api-web.nhle.com/v1/gamecenter/<id>/{boxscore,play-by-play}):
// 2025020001 FLA-CHI, 2025020004 TOR-MTL (late empty-net goals) and
// 2025020006 EDM-CGY (3v3 overtime and a shootout). Each fixture game is
// stored under a test game ID so it never collides with other packages' data.

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/database"
	"github.com/stretchr/testify/require"
)

const (
	envTestPGURL = "PUCKDB_TEST_PG_URL"
	testDBMarker = "test"
	nhlGamesPath = "testdata/nhl_games.json"
	testGameBase = 3_970_000_000
	// testShiftsPerGame spaces shift IDs: game g's shifts are
	// g*testShiftsPerGame + index.
	testShiftsPerGame = 10_000
	// fixturePlayerName marks the players rows a fixture inserted, so cleanup
	// never deletes a row another package's fixture owns.
	fixturePlayerName = "EvenStrengthFixture"
	fixturePosition   = "C"
	fixtureTOISeconds = 60
	fixturePeriodType = "REG"
	fixtureClockLeft  = "00:00"
	fixtureDetailCode = "0"
)

var migrateOnce struct {
	sync.Once
	err error
}

func openTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv(envTestPGURL)
	if dbURL == "" {
		t.Skipf("set %s to run PostgreSQL even-strength tests", envTestPGURL)
	}
	require.Contains(t, dbURL, testDBMarker, "test database URL must contain %q", testDBMarker)
	migrateOnce.Do(func() { migrateOnce.err = database.MigrateUp(dbURL) })
	require.NoError(t, migrateOnce.err)
	pool, err := pgxpool.New(context.Background(), dbURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

// fixtureGame is one game in the shape of testdata/nhl_games.json.
type fixtureGame struct {
	ID       int64          `json:"id"`
	Season   int32          `json:"season"`
	Date     string         `json:"date"`
	HomeTeam int64          `json:"homeTeam"`
	AwayTeam int64          `json:"awayTeam"`
	Skaters  [][2]int64     `json:"skaters"` // player, team
	Goalies  [][2]int64     `json:"goalies"` // player, team
	Shifts   []fixtureShift `json:"shifts"`
	Goals    []fixtureGoal  `json:"goals"`
	name     string         // subtest name
	testID   int64          // game ID the fixture is stored under
}

// fixtureShift is [player, team, period, start, end, typeCode].
type fixtureShift struct {
	Player, Team int64
	Period       int32
	Start, End   string
	TypeCode     int
}

func (s *fixtureShift) UnmarshalJSON(b []byte) error {
	return json.Unmarshal(b, &[]any{&s.Player, &s.Team, &s.Period, &s.Start, &s.End, &s.TypeCode})
}

// fixtureGoal is [period, clock, situationCode, scorer, assist1, assist2];
// null situation codes and players stay nil.
type fixtureGoal struct {
	Period                   int32
	Clock                    string
	Situation                *int32
	Scorer, Assist1, Assist2 *int64
}

func (g *fixtureGoal) UnmarshalJSON(b []byte) error {
	return json.Unmarshal(b, &[]any{&g.Period, &g.Clock, &g.Situation, &g.Scorer, &g.Assist1, &g.Assist2})
}

func loadNHLGames(t *testing.T) []fixtureGame {
	t.Helper()
	raw, err := os.ReadFile(nhlGamesPath)
	require.NoError(t, err)
	var file struct {
		Games []fixtureGame `json:"games"`
	}
	require.NoError(t, json.Unmarshal(raw, &file))
	require.NotEmpty(t, file.Games)
	for i := range file.Games {
		file.Games[i].name = strconv.FormatInt(file.Games[i].ID, 10)
		file.Games[i].testID = testGameBase + int64(i) + 1
	}
	return file.Games
}

// seedGame stores one fixture game (players, game, box-score rows, shifts,
// goals) and removes it when the test ends.
func seedGame(t *testing.T, pool *pgxpool.Pool, g fixtureGame) {
	t.Helper()
	cleanupGame(t, pool, g)
	t.Cleanup(func() { cleanupGame(t, pool, g) })
	for _, p := range append(append([][2]int64{}, g.Skaters...), g.Goalies...) {
		execFixture(t, pool, `
INSERT INTO players (id, first_name, last_name) VALUES ($1, $2, $3)
ON CONFLICT (id) DO NOTHING`, p[0], fixturePlayerName, strconv.FormatInt(p[0], 10))
	}
	execFixture(t, pool, `
INSERT INTO games (id, season, game_type, game_date, game_state, home_team_id, away_team_id)
VALUES ($1, $2, 'regular_season', $3::date, 'OFF', $4, $5)`,
		g.testID, g.Season, g.Date, g.HomeTeam, g.AwayTeam)
	seedBoxScore(t, pool, g)
	for i, s := range g.Shifts {
		execFixture(t, pool, `
INSERT INTO shifts (
    id, game_id, player_id, team_id, period, start_time, end_time, duration,
    shift_number, type_code, detail_code, event_number
) VALUES ($1, $2, $3, $4, $5, $6, $7, '00:00', $8, $9, $10, $11)`,
			g.testID*testShiftsPerGame+int64(i), g.testID,
			s.Player, s.Team, s.Period, s.Start, s.End, i, strconv.Itoa(s.TypeCode), fixtureDetailCode, i)
	}
	for i, goal := range g.Goals {
		eventID := int64(i) + 1
		execFixture(t, pool, `
INSERT INTO play_events (
    game_id, event_id, period, period_type, time_in_period, time_remaining,
    situation_code, type_desc_key, sort_order,
    scoring_player_id, assist1_player_id, assist2_player_id
) VALUES ($1, $2, $3, $4, $5, $6, $7, 'goal', $8, $9, $10, $11)`,
			g.testID, eventID, goal.Period, fixturePeriodType, goal.Clock, fixtureClockLeft,
			goal.Situation, i, goal.Scorer, goal.Assist1, goal.Assist2)
	}
}

func seedBoxScore(t *testing.T, pool *pgxpool.Pool, g fixtureGame) {
	t.Helper()
	for i, p := range g.Skaters {
		execFixture(t, pool, `
INSERT INTO game_skater_stats (game_id, player_id, team_id, is_home, sweater_number, position, toi_seconds)
VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			g.testID, p[0], p[1], p[1] == g.HomeTeam, i, fixturePosition, fixtureTOISeconds)
	}
	for i, p := range g.Goalies {
		execFixture(t, pool, `
INSERT INTO game_goalie_stats (game_id, player_id, team_id, is_home, sweater_number, starter, toi_seconds)
VALUES ($1, $2, $3, $4, $5, TRUE, $6)`,
			g.testID, p[0], p[1], p[1] == g.HomeTeam, len(g.Skaters)+i, fixtureTOISeconds)
	}
}

func cleanupGame(t *testing.T, pool *pgxpool.Pool, g fixtureGame) {
	t.Helper()
	for _, stmt := range []string{
		`DELETE FROM shifts WHERE game_id = $1`,
		`DELETE FROM play_events WHERE game_id = $1`,
		`DELETE FROM games WHERE id = $1`, // cascades to box scores and totals
	} {
		execFixture(t, pool, stmt, g.testID)
	}
	ids := make([]int64, 0, len(g.Skaters)+len(g.Goalies))
	for _, p := range append(append([][2]int64{}, g.Skaters...), g.Goalies...) {
		ids = append(ids, p[0])
	}
	execFixture(t, pool, `
DELETE FROM players p
WHERE p.id = ANY($1) AND p.first_name = $2
  AND NOT EXISTS (SELECT 1 FROM game_skater_stats s WHERE s.player_id = p.id)
  AND NOT EXISTS (SELECT 1 FROM game_goalie_stats s WHERE s.player_id = p.id)`,
		ids, fixturePlayerName)
}

// execFixture runs one fixture statement. pgx prepares parameterized SQL,
// and PostgreSQL rejects multiple commands in one prepared statement.
func execFixture(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	_, err := pool.Exec(context.Background(), sql, args...)
	require.NoError(t, err)
}
