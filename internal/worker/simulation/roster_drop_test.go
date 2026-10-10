package simulation

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testRosterDrop(t *testing.T) rosterDrop {
	return rosterDrop{PoolID: 1, AgentID: 7, Date: pgDate(t, "2024-11-15"), PlayerID: 8478402, Reasoning: "cut"}
}

func TestRemoveAndLogDrop_DeletesAndLogsDropRow(t *testing.T) {
	const dropTxID int32 = 42
	q := &stubSimQueries{
		deleteRosterRowsDefault: 1,
		insertDropReturn:        sqlcdb.SimTransaction{ID: dropTxID},
	}
	d := testRosterDrop(t)

	id, err := removeAndLogDrop(context.Background(), q, d)
	require.NoError(t, err)
	assert.Equal(t, dropTxID, id)
	require.Len(t, q.deleteRosterRowsCalls, 1)
	assert.Equal(t, sqlcdb.DeleteSimRosterRowsParams{PoolID: 1, AgentID: 7, PlayerID: 8478402}, q.deleteRosterRowsCalls[0])
	require.Len(t, q.insertDropCalls, 1)
	assert.Equal(t, sqlcdb.InsertSimTransactionDropParams{
		PoolID: 1, AgentID: 7, Date: d.Date,
		PlayerID: pgtype.Int8{Int64: 8478402, Valid: true}, Reasoning: "cut",
	}, q.insertDropCalls[0])
}

// No roster row removed means no waiver window either: the drop row is
// what puts a player on waivers.
func TestRemoveAndLogDrop_VanishedPlayerLogsNothing(t *testing.T) {
	q := &stubSimQueries{}

	_, err := removeAndLogDrop(context.Background(), q, testRosterDrop(t))
	require.ErrorIs(t, err, errDropVanished)
	assert.Empty(t, q.insertDropCalls)
}

func TestRemoveAndLogDrop_DeleteErrorLogsNothing(t *testing.T) {
	q := &stubSimQueries{deleteRosterRowsErr: errors.New("connection lost")}

	_, err := removeAndLogDrop(context.Background(), q, testRosterDrop(t))
	require.Error(t, err)
	assert.NotErrorIs(t, err, errDropVanished)
	assert.Empty(t, q.insertDropCalls)
}
