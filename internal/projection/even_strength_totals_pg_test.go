package projection

// PostgreSQL-backed coverage for InsertEvenStrengthPairTOIForGame and
// InsertEvenStrengthSkaterGamesForGame, the per-game build of
// even_strength_pair_toi and even_strength_skater_games run by the shift
// chart import. The fixture (seedLinemateSegments, shared with
// linemate_query_test.go) has three even-strength windows in period 1:
// [0,10) 5v5, [10,20) 4v4, [20,30) 3v3; [30,40) loses the away goalie and
// contributes nothing. Each club's five skaters (offsets 1-5 home, 6-10
// away) are active for a shrinking prefix of those windows, so player 1's
// total even-strength TOI is 30 seconds and shared 30/30/20/10 seconds with
// teammates 2/3/4/5 respectively; the mirrored away skaters (6-10) are
// identical on their own club. Only players +2 and +5 score an
// even-strength point (see linemate_query_test.go's goal fixture).

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/require"
)

func TestEvenStrengthTotals_MatchesFixtureAndIsIdempotent(t *testing.T) {
	pool := openProjectionTestDB(t)
	cleanupLinemateFixture(t, pool)
	t.Cleanup(func() { cleanupLinemateFixture(t, pool) })
	seedLinemateSegments(t, pool)

	wantPairs := expectedLinemateFixturePairTOI()
	wantSkaterGames := expectedLinemateFixtureSkaterGames()
	require.ElementsMatch(t, wantPairs, listEvenStrengthPairTOI(t, pool, testLinemateGame))
	require.ElementsMatch(t, wantSkaterGames, listEvenStrengthSkaterGames(t, pool, testLinemateGame))

	rebuildEvenStrengthTotals(t, pool, testLinemateGame)
	require.ElementsMatch(t, wantPairs, listEvenStrengthPairTOI(t, pool, testLinemateGame), "rebuild is idempotent")
	require.ElementsMatch(t, wantSkaterGames, listEvenStrengthSkaterGames(t, pool, testLinemateGame), "rebuild is idempotent")

	cleanupLinemateFixture(t, pool)
	require.Empty(t, listEvenStrengthPairTOI(t, pool, testLinemateGame), "deleting the game cascades")
	require.Empty(t, listEvenStrengthSkaterGames(t, pool, testLinemateGame), "deleting the game cascades")
}

// expectedLinemateFixturePairTOI computes both directions of shared
// even-strength TOI for one club's five skaters, whose windows shrink by one
// player every 10 seconds (see the package doc comment), and mirrors it onto
// the other club.
func expectedLinemateFixturePairTOI() []sqlcdb.EvenStrengthPairToi {
	toi := func(a, b int) int32 {
		// Both are active while windows keep at least max(a, b) players
		// (players are dropped in ascending order after 5, 4, then 3 remain).
		switch {
		case a <= 3 && b <= 3:
			return 30
		case a <= 4 && b <= 4:
			return 20
		default:
			return 10
		}
	}
	var rows []sqlcdb.EvenStrengthPairToi
	for _, base := range []int64{0, 5} { // home offsets 1-5, away offsets 6-10
		for a := 1; a <= 5; a++ {
			for b := 1; b <= 5; b++ {
				if a == b {
					continue
				}
				rows = append(rows, sqlcdb.EvenStrengthPairToi{
					GameID:           testLinemateGame,
					PlayerID:         testPlayerBase + base + int64(a),
					TeammateID:       testPlayerBase + base + int64(b),
					SharedToiSeconds: toi(a, b),
				})
			}
		}
	}
	return rows
}

func expectedLinemateFixtureSkaterGames() []sqlcdb.EvenStrengthSkaterGame {
	toiByOffset := map[int64]int32{1: 30, 2: 30, 3: 30, 4: 20, 5: 10}
	// Both goals in the fixture are scored by home players (+2, +5); no away
	// player is ever credited with an even-strength point.
	pointsByPlayer := map[int64]int32{testPlayerBase + 2: 1, testPlayerBase + 5: 1}
	var rows []sqlcdb.EvenStrengthSkaterGame
	for _, club := range []struct {
		team    int64
		offsets [5]int64
	}{
		{testHomeTeam, [5]int64{1, 2, 3, 4, 5}},
		{testAwayTeam, [5]int64{6, 7, 8, 9, 10}},
	} {
		for i, offset := range club.offsets {
			shapeOffset := int64(i + 1) // this club's skater position, 1-5, for its TOI shape
			playerID := testPlayerBase + offset
			rows = append(rows, sqlcdb.EvenStrengthSkaterGame{
				GameID:     testLinemateGame,
				PlayerID:   playerID,
				TeamID:     club.team,
				TOISeconds: toiByOffset[shapeOffset],
				Points:     pointsByPlayer[playerID],
			})
		}
	}
	return rows
}

func listEvenStrengthPairTOI(t *testing.T, pool *pgxpool.Pool, gameID int64) []sqlcdb.EvenStrengthPairToi {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
SELECT game_id, player_id, teammate_id, shared_toi_seconds
FROM even_strength_pair_toi
WHERE game_id = $1`, gameID)
	require.NoError(t, err)
	pairs, err := pgx.CollectRows(rows, pgx.RowToStructByPos[sqlcdb.EvenStrengthPairToi])
	require.NoError(t, err)
	return pairs
}

func listEvenStrengthSkaterGames(t *testing.T, pool *pgxpool.Pool, gameID int64) []sqlcdb.EvenStrengthSkaterGame {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
SELECT game_id, player_id, team_id, toi_seconds, points
FROM even_strength_skater_games
WHERE game_id = $1`, gameID)
	require.NoError(t, err)
	games, err := pgx.CollectRows(rows, pgx.RowToStructByPos[sqlcdb.EvenStrengthSkaterGame])
	require.NoError(t, err)
	return games
}
