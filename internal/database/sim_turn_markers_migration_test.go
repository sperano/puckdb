package database

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// simTurnMarkersVersion adds the unique indexes of the draft_pick and
// daily_turn_done markers of sim_transactions.
const simTurnMarkersVersion = 27

// sqlStateUniqueViolation is the SQLSTATE of a unique index violation.
const sqlStateUniqueViolation = "23505"

// The indexes of the simulation's turn markers, as PostgreSQL names them
// in a violation and the simulation worker matches them.
const (
	draftPickMarkerIndex = "ux_sim_transactions_draft_pick"
	dailyTurnMarkerIndex = "ux_sim_transactions_daily_turn_done"
)

// seedSimAgent inserts a pool and an agent at the schema before
// simTurnMarkersVersion and returns their ids.
func seedSimAgent(t *testing.T, conn *pgx.Conn) (poolID, agentID int32) {
	t.Helper()
	ctx := context.Background()
	execSQL(t, conn, `INSERT INTO seasons (id, standings_start, standings_end)
		VALUES (20242025, DATE '2024-10-04', DATE '2025-04-17')`)
	require.NoError(t, conn.QueryRow(ctx, `INSERT INTO sim_pools (name, season, num_teams, waiver_days,
		draft_rounds, max_llm_cost_usd_per_pool, categories)
		VALUES ('markers', 20242025, 2, 2, 2, 0, ARRAY['G']) RETURNING id`).Scan(&poolID))
	require.NoError(t, conn.QueryRow(ctx, `INSERT INTO sim_agents (pool_id, provider, model, strategy, team_name)
		VALUES ($1, 'anthropic', 'claude-haiku-4-5', 'balanced', 'Markers') RETURNING id`, poolID).Scan(&agentID))
	return poolID, agentID
}

// insertMarker inserts a row of type typ at (pool, agent, date) with
// round and pick (NULL when 0).
const insertMarker = `INSERT INTO sim_transactions (pool_id, agent_id, date, type, round, pick)
	VALUES ($1, $2, $3, $4, NULLIF($5::int, 0), NULLIF($6::int, 0))`

// Up keeps the first of each set of duplicate daily_turn_done markers and
// leaves every other row alone; afterwards a second draft_pick for one
// slot or a second marker for one day is refused, while other slots,
// days and transaction types stay free. Down drops the indexes; up runs
// again over the duplicates down allowed.
func TestIntegration_SimTurnMarkersMigration(t *testing.T) {
	conn, dbURL := freshDatabase(t)
	migrateTo(t, dbURL, simTurnMarkersVersion-1)
	poolID, agentID := seedSimAgent(t, conn)
	const day, nextDay = "2024-11-15", "2024-11-16"
	for range 3 {
		execSQL(t, conn, insertMarker, poolID, agentID, day, "daily_turn_done", 0, 0)
		execSQL(t, conn, insertMarker, poolID, agentID, day, "pass", 0, 0)
	}
	execSQL(t, conn, insertMarker, poolID, agentID, nextDay, "daily_turn_done", 0, 0)
	execSQL(t, conn, insertMarker, poolID, agentID, day, "draft_pick", 1, 1)
	execSQL(t, conn, insertMarker, poolID, agentID, day, "draft_pick", 2, 2)
	var firstMarker int32
	require.NoError(t, conn.QueryRow(context.Background(), `SELECT min(id) FROM sim_transactions
		WHERE type = 'daily_turn_done' AND date = $1`, day).Scan(&firstMarker))

	migrateTo(t, dbURL, simTurnMarkersVersion)
	assert.Equal(t, 1, countRows(t, conn, `SELECT count(*) FROM sim_transactions
		WHERE type = 'daily_turn_done' AND date = $1 AND id = $2`, day, firstMarker), "the first marker stays")
	assert.Equal(t, 2, countRows(t, conn, `SELECT count(*) FROM sim_transactions WHERE type = 'daily_turn_done'`))
	assert.Equal(t, 3, countRows(t, conn, `SELECT count(*) FROM sim_transactions WHERE type = 'pass'`),
		"other types keep their rows")
	assert.Equal(t, 2, countRows(t, conn, `SELECT count(*) FROM sim_transactions WHERE type = 'draft_pick'`))

	requireRejected(t, conn, "a second pick in one slot",
		rejection{code: sqlStateUniqueViolation, constraint: draftPickMarkerIndex},
		insertMarker, poolID, agentID, nextDay, "draft_pick", 1, 1)
	requireRejected(t, conn, "a second marker for one day",
		rejection{code: sqlStateUniqueViolation, constraint: dailyTurnMarkerIndex},
		insertMarker, poolID, agentID, day, "daily_turn_done", 0, 0)
	execSQL(t, conn, insertMarker, poolID, agentID, day, "draft_pick", 1, 2)
	execSQL(t, conn, insertMarker, poolID, agentID, "2024-11-17", "daily_turn_done", 0, 0)
	execSQL(t, conn, insertMarker, poolID, agentID, day, "pass", 0, 0)

	migrateTo(t, dbURL, simTurnMarkersVersion-1)
	execSQL(t, conn, insertMarker, poolID, agentID, day, "daily_turn_done", 0, 0)
	assert.Equal(t, 2, countRows(t, conn, `SELECT count(*) FROM sim_transactions
		WHERE type = 'daily_turn_done' AND date = $1`, day), "down allows duplicates again")

	migrateTo(t, dbURL, simTurnMarkersVersion)
	assert.Equal(t, 1, countRows(t, conn, `SELECT count(*) FROM sim_transactions
		WHERE type = 'daily_turn_done' AND date = $1`, day))
}

// Up refuses duplicate draft picks, naming each slot, and changes
// nothing; once an operator has removed the extra pick, forcing the
// previous version and migrating again succeeds.
func TestIntegration_SimTurnMarkersMigrationRefusesDuplicateDraftPicks(t *testing.T) {
	conn, dbURL := freshDatabase(t)
	migrateTo(t, dbURL, simTurnMarkersVersion-1)
	poolID, agentID := seedSimAgent(t, conn)
	const day = "2024-10-08"
	execSQL(t, conn, insertMarker, poolID, agentID, day, "draft_pick", 1, 2)
	execSQL(t, conn, insertMarker, poolID, agentID, day, "draft_pick", 1, 2)
	execSQL(t, conn, insertMarker, poolID, agentID, day, "daily_turn_done", 0, 0)
	execSQL(t, conn, insertMarker, poolID, agentID, day, "daily_turn_done", 0, 0)

	m, err := openMigrator(migrationsFS, dbURL)
	require.NoError(t, err)
	defer func() { _, _ = m.Close() }()
	err = m.Migrate.Migrate(simTurnMarkersVersion)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sim_transactions has duplicate draft picks")
	assert.Contains(t, err.Error(), fmt.Sprintf("pool %d agent %d round 1 pick 2", poolID, agentID))
	assert.Equal(t, 2, countRows(t, conn, `SELECT count(*) FROM sim_transactions WHERE type = 'daily_turn_done'`),
		"the refused migration rolls back")
	assert.Equal(t, 0, countRows(t, conn, `SELECT count(*) FROM pg_indexes WHERE indexname = $1`, draftPickMarkerIndex))

	execSQL(t, conn, `DELETE FROM sim_transactions WHERE id = (SELECT max(id) FROM sim_transactions WHERE type = 'draft_pick')`)
	require.NoError(t, m.Migrate.Force(simTurnMarkersVersion-1))
	require.NoError(t, m.Migrate.Migrate(simTurnMarkersVersion))
	assert.Equal(t, 1, countRows(t, conn, `SELECT count(*) FROM sim_transactions WHERE type = 'daily_turn_done'`))
	assert.Equal(t, 1, countRows(t, conn, `SELECT count(*) FROM pg_indexes WHERE indexname = $1`, draftPickMarkerIndex))
}
