package simulation

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
)

// contextTestAgentID is the agent whose rows the context loaders read.
const contextTestAgentID = 7

// TestLoadRosterAndPositionsRendersPlayerColumns pins the prompt's roster
// columns: the position enum, the raw team ID, and empty strings for NULL.
func TestLoadRosterAndPositionsRendersPlayerColumns(t *testing.T) {
	t.Parallel()
	acts := &Activities{Queries: &stubSimQueries{
		listFullRosterByAgent: map[int32][]sqlcdb.SimRoster{contextTestAgentID: {
			{PlayerID: 1, Slot: string(SlotC)},
			{PlayerID: 2, Slot: string(SlotBN)},
		}},
		getPlayerByID: map[int64]sqlcdb.Player{
			1: {FirstName: "Sidney", LastName: "Crosby",
				Position: sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionC, Valid: true},
				TeamID:   pgtype.Int8{Int64: 5, Valid: true}},
			2: {FirstName: "No", LastName: "Team", TeamID: pgtype.Int8{Int64: 9}},
		},
	}}

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivityWithOptions(func(ctx context.Context) ([]RosterRow, error) {
		_, _, rows, err := acts.loadRosterAndPositions(ctx, 1, contextTestAgentID, PoolConfig{})
		return rows, err
	}, activity.RegisterOptions{Name: "loadRosterAndPositions"})
	result, err := env.ExecuteActivity("loadRosterAndPositions")
	require.NoError(t, err)
	var rows []RosterRow
	require.NoError(t, result.Get(&rows))

	require.Len(t, rows, 2)
	assert.Equal(t, "C", rows[0].NHLPosition)
	assert.Equal(t, "5", rows[0].Team)
	assert.Equal(t, "", rows[1].NHLPosition)
	assert.Equal(t, "", rows[1].Team)
}

func TestLoadAgentPendingClaimsRendersResolveDate(t *testing.T) {
	t.Parallel()
	acts := &Activities{Queries: &stubSimQueries{
		listPendingByAgentRows: map[int32][]sqlcdb.SimWaiverClaim{contextTestAgentID: {
			{PlayerID: 1, ProcessDate: pgtype.Date{Time: time.Date(2024, 11, 3, 0, 0, 0, 0, time.UTC), Valid: true}},
			{PlayerID: 2},
		}},
		getPlayerByID: map[int64]sqlcdb.Player{
			1: {FirstName: "Sidney", LastName: "Crosby"},
			2: {FirstName: "No", LastName: "Date"},
		},
	}}

	ids, rows, err := acts.loadAgentPendingClaims(context.Background(), 1, contextTestAgentID)
	require.NoError(t, err)
	assert.Equal(t, []int64{1, 2}, ids)
	require.Len(t, rows, 2)
	assert.Equal(t, "2024-11-03", rows[0].ResolvesOn)
	assert.Equal(t, "", rows[1].ResolvesOn)
}
