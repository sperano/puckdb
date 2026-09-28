package nhl

// PostgreSQL-backed coverage for PgxTransactor. It skips unless
// PUCKDB_TEST_PG_URL names a dedicated test database.

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/database"
	"github.com/stretchr/testify/require"
)

const (
	transactorTestPGURL  = "PUCKDB_TEST_PG_URL"
	transactorTestMarker = "test"
	transactorTestGameID = 3_960_000_001
	transactorTestPlayer = 9_960_001
)

func openTransactorTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv(transactorTestPGURL)
	if dbURL == "" {
		t.Skipf("set %s to run PostgreSQL import tests", transactorTestPGURL)
	}
	require.Contains(t, dbURL, transactorTestMarker, "test database URL must contain %q", transactorTestMarker)
	require.NoError(t, database.MigrateUp(dbURL))
	pool, err := pgxpool.New(context.Background(), dbURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

func countTransactorTestPairTOI(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT count(*) FROM even_strength_pair_toi WHERE game_id = $1`, transactorTestGameID).Scan(&n))
	return n
}

func TestPgxTransactor_RollsBackRebuildOnError(t *testing.T) {
	pool := openTransactorTestDB(t)
	ctx := context.Background()
	cleanup := func() {
		_, err := pool.Exec(context.Background(), `DELETE FROM games WHERE id = $1`, transactorTestGameID)
		require.NoError(t, err)
	}
	cleanup()
	t.Cleanup(cleanup)
	_, err := pool.Exec(ctx, `
INSERT INTO games (id, season, game_type, game_date, game_state, home_team_id, away_team_id)
VALUES ($1, 20232024, 'regular_season', '2024-01-10', 'FINAL', 1, 2)`, transactorTestGameID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
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
		if err := q.DeleteEvenStrengthPairTOIForGame(ctx, transactorTestGameID); err != nil {
			return err
		}
		if err := q.DeleteEvenStrengthSkaterGamesForGame(ctx, transactorTestGameID); err != nil {
			return err
		}
		inserted, err := q.InsertEvenStrengthPairTOIForGame(ctx, transactorTestGameID)
		require.Zero(t, inserted)
		if err != nil {
			return err
		}
		inserted, err = q.InsertEvenStrengthSkaterGamesForGame(ctx, transactorTestGameID)
		require.Zero(t, inserted)
		return err
	})
	require.NoError(t, err)
	require.Zero(t, countTransactorTestPairTOI(t, pool))
}
