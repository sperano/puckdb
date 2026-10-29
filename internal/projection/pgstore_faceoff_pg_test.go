package projection

// PostgreSQL-backed test proving ListProjectionSkaterHistory counts faceoffs
// from play_events correctly: a player with several faceoff events in one
// game must still show correct (non-multiplied) sums for every other stat.
// Skips unless PUCKDB_TEST_PG_URL names a test database (see CLAUDE.md
// "Database-backed tests").

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/database"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/require"
)

const (
	envTestPGURL     = "PUCKDB_TEST_PG_URL"
	testDBNameMarker = "test"

	faceoffTestSeason  = 20252026
	faceoffLowerSeason = 20242025
	faceoffUpperSeason = 20262027

	faceoffGameAID = 3_000_100_001
	faceoffGameBID = 3_000_100_002

	faceoffPlayerOneID  = 3_100_001
	faceoffPlayerTwoID  = 3_100_002
	faceoffUntrackedID  = 3_100_999
	faceoffHomeTeamID   = 1
	faceoffAwayTeamID   = 2
	faceoffSweater      = 9
	faceoffPlayerOneTOI = 1_200
	faceoffPlayerTwoTOI = 1_100
)

var pgFaceoffMigrateOnce struct {
	sync.Once
	err error
}

func openFaceoffTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv(envTestPGURL)
	if dbURL == "" {
		t.Skipf("set %s to run the PostgreSQL faceoff projection tests", envTestPGURL)
	}
	require.Contains(t, dbURL, testDBNameMarker,
		"%s must name a dedicated test database (URL containing %q)", envTestPGURL, testDBNameMarker)
	pgFaceoffMigrateOnce.Do(func() { pgFaceoffMigrateOnce.err = database.MigrateUp(dbURL) })
	require.NoError(t, pgFaceoffMigrateOnce.err, "migrate test database")

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

// seedFaceoffGames inserts two completed regular-season games: game A gives
// player one three faceoff wins and two losses against player two (five
// play_events rows on top of one game_skater_stats row each, so a join that
// multiplied rows instead of pre-aggregating them would inflate goals/hits
// five-fold); game B gives player one four more wins against an untracked
// player who never gets a game_skater_stats row, proving that an opponent
// absent from game_skater_stats neither errors nor leaks into the sums.
func seedFaceoffGames(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()

	// Registered before any insert so a failure partway through seeding
	// still cleans up whatever did commit, instead of leaking rows that
	// collide with the next run.
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, err := pool.Exec(cleanupCtx, `DELETE FROM play_events WHERE game_id = ANY($1)`,
			[]int64{faceoffGameAID, faceoffGameBID})
		require.NoError(t, err)
		_, err = pool.Exec(cleanupCtx, `DELETE FROM games WHERE id = ANY($1)`,
			[]int64{faceoffGameAID, faceoffGameBID})
		require.NoError(t, err)
		_, err = pool.Exec(cleanupCtx, `DELETE FROM players WHERE id = ANY($1)`,
			[]int64{faceoffPlayerOneID, faceoffPlayerTwoID})
		require.NoError(t, err)
	})

	_, err := pool.Exec(ctx, `INSERT INTO players (id, first_name, last_name, position) VALUES
		($1, 'One', 'Fixture', 'C'), ($2, 'Two', 'Fixture', 'C')`,
		faceoffPlayerOneID, faceoffPlayerTwoID)
	require.NoError(t, err)

	gameDateA := time.Date(2025, time.November, 1, 0, 0, 0, 0, time.UTC)
	gameDateB := time.Date(2025, time.November, 3, 0, 0, 0, 0, time.UTC)
	for _, game := range []struct {
		id   int64
		date time.Time
	}{{faceoffGameAID, gameDateA}, {faceoffGameBID, gameDateB}} {
		_, err := pool.Exec(ctx, `INSERT INTO games (id, season, game_type, game_date, home_team_id, away_team_id, game_state)
			VALUES ($1, $2, 'regular_season', $3, $4, $5, 'FINAL')`,
			game.id, faceoffTestSeason, game.date, faceoffHomeTeamID, faceoffAwayTeamID)
		require.NoError(t, err)
	}

	insertSkaterStats := func(gameID, playerID int64, isHome bool, goals, hits, toi int) {
		_, err := pool.Exec(ctx, `INSERT INTO game_skater_stats
			(game_id, player_id, team_id, is_home, sweater_number, position, goals, hits, toi_seconds)
			VALUES ($1, $2, $3, $4, $5, 'C', $6, $7, $8)`,
			gameID, playerID, faceoffHomeTeamID, isHome, faceoffSweater, goals, hits, toi)
		require.NoError(t, err)
	}
	insertSkaterStats(faceoffGameAID, faceoffPlayerOneID, true, 2, 3, faceoffPlayerOneTOI)
	insertSkaterStats(faceoffGameAID, faceoffPlayerTwoID, false, 1, 0, faceoffPlayerTwoTOI)
	insertSkaterStats(faceoffGameBID, faceoffPlayerOneID, true, 1, 1, faceoffPlayerOneTOI)

	insertFaceoff := func(gameID, eventID, winner, loser int64) {
		sortOrder := int32(eventID)
		_, err := pool.Exec(ctx, `INSERT INTO play_events
			(game_id, event_id, period, period_type, time_in_period, time_remaining, type_desc_key, sort_order, winning_player_id, losing_player_id)
			VALUES ($1, $2, 1, 'REG', '00:00', '20:00', 'faceoff', $3, $4, $5)`,
			gameID, eventID, sortOrder, winner, loser)
		require.NoError(t, err)
	}
	// Game A: three wins and two losses for player one against player two.
	insertFaceoff(faceoffGameAID, 1, faceoffPlayerOneID, faceoffPlayerTwoID)
	insertFaceoff(faceoffGameAID, 2, faceoffPlayerOneID, faceoffPlayerTwoID)
	insertFaceoff(faceoffGameAID, 3, faceoffPlayerOneID, faceoffPlayerTwoID)
	insertFaceoff(faceoffGameAID, 4, faceoffPlayerTwoID, faceoffPlayerOneID)
	insertFaceoff(faceoffGameAID, 5, faceoffPlayerTwoID, faceoffPlayerOneID)
	// Game B: four more wins for player one against a player untracked in
	// game_skater_stats.
	insertFaceoff(faceoffGameBID, 1, faceoffPlayerOneID, faceoffUntrackedID)
	insertFaceoff(faceoffGameBID, 2, faceoffPlayerOneID, faceoffUntrackedID)
	insertFaceoff(faceoffGameBID, 3, faceoffPlayerOneID, faceoffUntrackedID)
	insertFaceoff(faceoffGameBID, 4, faceoffPlayerOneID, faceoffUntrackedID)
}

func TestListProjectionSkaterHistoryCountsFaceoffsWithoutInflatingOtherSums(t *testing.T) {
	pool := openFaceoffTestDB(t)
	seedFaceoffGames(t, pool)

	rows, err := sqlcdb.New(pool).ListProjectionSkaterHistory(context.Background(), sqlcdb.ListProjectionSkaterHistoryParams{
		Season:   int32(faceoffUpperSeason),
		Season_2: int32(faceoffLowerSeason),
		GameDate: dateValue(time.Date(2025, time.December, 1, 0, 0, 0, 0, time.UTC)),
	})
	require.NoError(t, err)

	rowByPlayer := make(map[int64]sqlcdb.ListProjectionSkaterHistoryRow, len(rows))
	for _, row := range rows {
		if row.Season == faceoffTestSeason && (row.PlayerID == faceoffPlayerOneID || row.PlayerID == faceoffPlayerTwoID) {
			rowByPlayer[row.PlayerID] = row
		}
	}
	require.Contains(t, rowByPlayer, int64(faceoffPlayerOneID))
	require.Contains(t, rowByPlayer, int64(faceoffPlayerTwoID))

	playerOne := rowByPlayer[faceoffPlayerOneID]
	require.Equal(t, int32(2), playerOne.GamesPlayed, "player one's two games must not be multiplied by their nine faceoff rows")
	require.EqualValues(t, faceoffPlayerOneTOI*2, playerOne.TOISeconds)
	require.EqualValues(t, 3, playerOne.Goals, "goals must stay 2+1, not be inflated by the five play_events rows in game A")
	require.EqualValues(t, 4, playerOne.Hits)
	require.EqualValues(t, 7, playerOne.FaceoffsWon, "3 wins in game A plus 4 in game B")
	require.EqualValues(t, 2, playerOne.FaceoffsLost, "2 losses in game A, none in game B")

	playerTwo := rowByPlayer[faceoffPlayerTwoID]
	require.Equal(t, int32(1), playerTwo.GamesPlayed)
	require.EqualValues(t, faceoffPlayerTwoTOI, playerTwo.TOISeconds)
	require.EqualValues(t, 1, playerTwo.Goals, "goals must stay 1, not be inflated by the five play_events rows in game A")
	require.EqualValues(t, 0, playerTwo.Hits)
	require.EqualValues(t, 2, playerTwo.FaceoffsWon, "2 wins in game A")
	require.EqualValues(t, 3, playerTwo.FaceoffsLost, "3 losses in game A")
}
