package projection

// PostgreSQL-backed coverage for InsertEvenStrengthSegmentsForGame, the
// per-game build of even_strength_segments run by the shift chart import.

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/require"
)

// segmentWindow is one stored segment with its skater count per club.
type segmentWindow struct {
	Period, StartSecond, EndSecond int32
	HomeSkaters, AwaySkaters       int64
}

func TestInsertEvenStrengthSegmentsForGame_StoresEvenStrengthSkaters(t *testing.T) {
	pool := openProjectionTestDB(t)
	cleanupLinemateFixture(t, pool)
	t.Cleanup(func() { cleanupLinemateFixture(t, pool) })
	seedLinemateSegments(t, pool)

	home := []int64{1, 2, 3, 4, 5}
	away := []int64{6, 7, 8, 9, 10}
	var want []sqlcdb.EvenStrengthSegment
	// [0,10) is 5v5, [10,20) 4v4, [20,30) 3v3; [30,40) loses the away goalie.
	// The goalie rows, the unparsable clock and the duplicate shift add nothing.
	for _, window := range []struct{ start, end, skaters int32 }{{0, 10, 5}, {10, 20, 4}, {20, 30, 3}} {
		for _, club := range []struct {
			team    int64
			players []int64
		}{{testHomeTeam, home}, {testAwayTeam, away}} {
			for _, p := range club.players[:window.skaters] {
				want = append(want, sqlcdb.EvenStrengthSegment{
					GameID: testLinemateGame, Period: 1, StartSecond: window.start, EndSecond: window.end,
					TeamID: club.team, PlayerID: testPlayerBase + p,
				})
			}
		}
	}
	require.Equal(t, want, listEvenStrengthSegments(t, pool, testLinemateGame))

	queries := sqlcdb.New(pool)
	ctx := context.Background()
	_, err := queries.InsertEvenStrengthSegmentsForGame(ctx, testLinemateGame)
	require.Error(t, err, "a rebuild must delete the game's rows first")

	require.NoError(t, queries.DeleteEvenStrengthSegmentsForGame(ctx, testLinemateGame))
	inserted, err := queries.InsertEvenStrengthSegmentsForGame(ctx, testLinemateGame)
	require.NoError(t, err)
	require.EqualValues(t, len(want), inserted)
	require.Equal(t, want, listEvenStrengthSegments(t, pool, testLinemateGame), "rebuild is idempotent")

	cleanupLinemateFixture(t, pool)
	require.Empty(t, listEvenStrengthSegments(t, pool, testLinemateGame), "deleting the game cascades")
}

func TestInsertEvenStrengthSegmentsForGame_KeepsOnlyEqualStrengthWithBothGoalies(t *testing.T) {
	pool := openProjectionTestDB(t)
	seedEquivFixture(t, pool)

	rows, err := pool.Query(context.Background(), `
SELECT period, start_second, end_second,
       count(*) FILTER (WHERE team_id = $2),
       count(*) FILTER (WHERE team_id = $3)
FROM even_strength_segments
WHERE game_id = $1
GROUP BY period, start_second, end_second
ORDER BY period, start_second`, equivGameA, equivHomeTeam, equivAwayTeam)
	require.NoError(t, err)
	windows, err := pgx.CollectRows(rows, pgx.RowToStructByPos[segmentWindow])
	require.NoError(t, err)

	// Excluded: [60,90) 5v4, [180,200) empty away net, [240,260) six home
	// skaters with an empty home net. The shift of the player without
	// box-score rows would otherwise split [90,150) at 100.
	require.Equal(t, []segmentWindow{
		{1, 0, 60, 5, 5},
		{1, 90, 150, 4, 4},
		{1, 150, 180, 3, 3},
		{1, 200, 240, 5, 5},
		{2, 0, 45, 5, 5},
		{2, 45, 50, 5, 5},
		{2, 50, 100, 5, 5},
	}, windows)
}

func listEvenStrengthSegments(t *testing.T, pool *pgxpool.Pool, gameID int64) []sqlcdb.EvenStrengthSegment {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
SELECT game_id, period, start_second, end_second, team_id, player_id
FROM even_strength_segments
WHERE game_id = $1
ORDER BY period, start_second, team_id, player_id`, gameID)
	require.NoError(t, err)
	segments, err := pgx.CollectRows(rows, pgx.RowToStructByPos[sqlcdb.EvenStrengthSegment])
	require.NoError(t, err)
	return segments
}
