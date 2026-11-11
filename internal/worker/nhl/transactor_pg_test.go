package nhl

// PostgreSQL-backed coverage for PgxTransactor. It skips unless
// PUCKDB_TEST_PG_URL names a dedicated test database.

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/evenstrength"
	"github.com/sperano/puckdb/internal/fixtures/pgtest"
	"github.com/stretchr/testify/require"
)

const (
	transactorTestGameID = 3_960_000_001
	transactorTestPlayer = 9_960_001
)

// insertTransactorTestGame (re)creates the test game with no shifts and
// deletes it, with its cascaded even-strength rows, when the test ends.
func insertTransactorTestGame(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	cleanup := func() {
		_, err := pool.Exec(context.Background(), `DELETE FROM games WHERE id = $1`, transactorTestGameID)
		require.NoError(t, err)
	}
	cleanup()
	t.Cleanup(cleanup)
	_, err := pool.Exec(context.Background(), `
INSERT INTO games (id, season, game_type, game_date, game_state, home_team_id, away_team_id)
VALUES ($1, 20232024, 'regular_season', '2024-01-10', 'FINAL', 1, 2)`, transactorTestGameID)
	require.NoError(t, err)
}

func countTransactorTestPairTOI(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT count(*) FROM even_strength_pair_toi WHERE game_id = $1`, transactorTestGameID).Scan(&n))
	return n
}

func TestPgxTransactor_RollsBackRebuildOnError(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	insertTransactorTestGame(t, pool)
	_, err := pool.Exec(ctx, `
INSERT INTO even_strength_pair_toi (game_id, player_id, teammate_id, shared_toi_seconds)
VALUES ($1, $2, $2 + 1::bigint, 30)`, transactorTestGameID, transactorTestPlayer)
	require.NoError(t, err)

	tx := NewPgxTransactor(pool)
	errAbort := errors.New("abort after delete")
	err = tx.InTx(ctx, func(q EvenStrengthTotalsRebuilder) error {
		require.NoError(t, q.DeleteEvenStrengthPairTOIForGame(ctx, transactorTestGameID))
		return errAbort
	})
	require.ErrorIs(t, err, errAbort)
	require.Equal(t, 1, countTransactorTestPairTOI(t, pool), "a failed rebuild keeps the previous rows")

	// The game has no shifts, so a committed rebuild leaves it empty.
	err = tx.InTx(ctx, func(q EvenStrengthTotalsRebuilder) error {
		inserted, err := evenstrength.Rebuild(ctx, q, transactorTestGameID)
		require.Zero(t, inserted)
		return err
	})
	require.NoError(t, err)
	require.Zero(t, countTransactorTestPairTOI(t, pool))
}
