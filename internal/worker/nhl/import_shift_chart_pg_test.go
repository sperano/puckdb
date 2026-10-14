package nhl

// PostgreSQL-backed check that the even-strength rebuild leaves the
// connection's planner settings alone. It skips unless PUCKDB_TEST_PG_URL
// names a dedicated test database.

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

const (
	nestLoopSetting = "enable_nestloop"
	settingOn       = "on"
	singleConn      = 1
)

// showNestLoop reads enable_nestloop through q: a connection or an open
// transaction.
func showNestLoop(t *testing.T, q interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}) string {
	t.Helper()
	var value string
	require.NoError(t, q.QueryRow(context.Background(), "SELECT current_setting($1)", nestLoopSetting).Scan(&value))
	return value
}

// openNestLoopOnTestDB opens a single-connection pool whose session starts
// with enable_nestloop on, whatever the server, database or role default, so
// a rebuild that turned it off would show. With one connection, every
// statement runs on the connection under test.
func openNestLoopOnTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(migratedTransactorTestURL(t))
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams[nestLoopSetting] = settingOn
	cfg.MaxConns = singleConn
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

func TestRebuildEvenStrengthTotals_LeavesNestedLoopsOnForTheConnection(t *testing.T) {
	pool := openNestLoopOnTestDB(t)
	require.Equal(t, settingOn, showNestLoop(t, pool), "session baseline")
	insertTransactorTestGame(t, pool)

	act := &ImportActivities{Tx: NewPgxTransactor(pool)}
	inserted, err := act.rebuildEvenStrengthTotals(context.Background(), transactorTestGameID)
	require.NoError(t, err)
	require.Zero(t, inserted, "the test game has no shifts")
	require.Equal(t, settingOn, showNestLoop(t, pool))
}
