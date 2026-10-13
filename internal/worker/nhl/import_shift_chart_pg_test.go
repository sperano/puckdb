package nhl

// PostgreSQL-backed coverage for the planner setting of the even-strength
// rebuild. It skips unless PUCKDB_TEST_PG_URL names a dedicated test
// database.

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/require"
)

const (
	nestLoopSetting = "enable_nestloop"
	settingOn       = "on"
	settingOff      = "off"
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
// with enable_nestloop on, whatever the server, database or role default:
// the tests below need a baseline the transaction-local setting differs
// from, or "off inside the transaction" would prove nothing. With one
// connection, every statement runs on the connection under test.
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

func TestDisableNestedLoopsForTransaction_RevertsWhenTransactionEnds(t *testing.T) {
	pool := openNestLoopOnTestDB(t)
	ctx := context.Background()
	conn, err := pool.Acquire(ctx)
	require.NoError(t, err)
	t.Cleanup(conn.Release)
	require.Equal(t, settingOn, showNestLoop(t, conn), "session baseline")

	ends := map[string]func(pgx.Tx) error{
		"commit":   func(tx pgx.Tx) error { return tx.Commit(ctx) },
		"rollback": func(tx pgx.Tx) error { return tx.Rollback(ctx) },
	}
	for name, end := range ends {
		t.Run(name, func(t *testing.T) {
			tx, err := conn.Begin(ctx)
			require.NoError(t, err)
			require.NoError(t, sqlcdb.New(tx).DisableNestedLoopsForTransaction(ctx))
			require.Equal(t, settingOff, showNestLoop(t, tx), "inside the transaction")

			require.NoError(t, end(tx))
			require.Equal(t, settingOn, showNestLoop(t, conn), "same connection after the transaction")
		})
	}
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
