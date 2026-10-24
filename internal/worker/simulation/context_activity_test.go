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

// standingsTestCategories are the nine roto categories a stored standings
// snapshot carries, in StandingRow field order.
var standingsTestCategories = []Category{
	CategoryG, CategoryA, CategoryPM, CategoryPIM, CategoryPPP,
	CategorySOG, CategoryW, CategoryGA, CategoryGAA,
}

// standingsLoadRepeats is how many loads must agree before the order counts
// as stable; map iteration order would differ within a handful.
const standingsLoadRepeats = 50

// standingsTestRotoPoints gives each of four agents its roto points per
// category, in standingsTestCategories order, as UpdateStandings splits
// them under ties. Totals: agent 4 = 31.5, agents 2 and 9 tie at 22.5,
// agent 7 = 13.5, so the total tie breaks on agent ID.
var standingsTestRotoPoints = map[int32][]float64{
	2: {3, 3, 2, 1.5, 3, 1.5, 4, 1.5, 3},
	4: {4, 4, 4, 3.5, 4, 3.5, 2, 3.5, 3},
	7: {1, 1, 1, 1.5, 1, 1.5, 2, 1.5, 3},
	9: {2, 2, 3, 3.5, 2, 3.5, 2, 3.5, 1},
}

// standingsTestRows builds the snapshot rows for standingsTestRotoPoints,
// each row's value being its roto points times ten so values and points
// stay distinguishable.
func standingsTestRows(t *testing.T) []sqlcdb.SimStanding {
	t.Helper()
	var rows []sqlcdb.SimStanding
	for _, aid := range []int32{9, 7, 4, 2} {
		for i, cat := range standingsTestCategories {
			pts := standingsTestRotoPoints[aid][i]
			r, err := seedStandingRow(aid, string(cat), pts*10, pts)
			require.NoError(t, err)
			rows = append(rows, r)
		}
	}
	return rows
}

func standingsTestActivities(t *testing.T) *Activities {
	t.Helper()
	return &Activities{Queries: &stubSimQueries{
		getSimStandingsLatestDateReturn: pgDate(t, "2025-01-10"),
		listSimStandingsByDateRows:      standingsTestRows(t),
		listAgentsByPoolReturn: []sqlcdb.SimAgent{
			{ID: 2, TeamName: "Two"}, {ID: 4, TeamName: "Four"},
			{ID: 7, TeamName: "Seven"}, {ID: 9, TeamName: "Nine"},
		},
	}}
}

// TestLoadStandingsRowsOrdersByTotalThenAgent pins the prompt's team order:
// total roto points descending, agent ID ascending on a tie, identical on
// every load.
func TestLoadStandingsRowsOrdersByTotalThenAgent(t *testing.T) {
	t.Parallel()
	acts := standingsTestActivities(t)

	first, err := acts.loadStandingsRows(context.Background(), 1, contextTestAgentID)
	require.NoError(t, err)
	names := make([]string, len(first))
	for i, r := range first {
		names[i] = r.Agent
	}
	assert.Equal(t, []string{"Four", "Two", "Nine", "Seven"}, names)
	assert.Equal(t, []float64{31.5, 22.5, 22.5, 13.5},
		[]float64{first[0].TotalRotoPts, first[1].TotalRotoPts, first[2].TotalRotoPts, first[3].TotalRotoPts})
	assert.True(t, first[3].IsYou, "agent 7 is the caller")

	for range standingsLoadRepeats {
		again, err := acts.loadStandingsRows(context.Background(), 1, contextTestAgentID)
		require.NoError(t, err)
		require.Equal(t, first, again, "repeated loads must emit identical standings")
	}
}

// TestLoadStandingsRowsRanksEveryCategory pins competition ranks per
// category from the stored roto points (ties share the run's first
// position) and the raw values next to them.
func TestLoadStandingsRowsRanksEveryCategory(t *testing.T) {
	t.Parallel()
	rows, err := standingsTestActivities(t).loadStandingsRows(context.Background(), 1, contextTestAgentID)
	require.NoError(t, err)
	byName := make(map[string]StandingRow, len(rows))
	for _, r := range rows {
		byName[r.Agent] = r
	}

	// Ranks per agent in standingsTestCategories order.
	want := map[string][]float64{
		"Two":   {2, 2, 3, 3, 2, 3, 1, 3, 1},
		"Four":  {1, 1, 1, 1, 1, 1, 2, 1, 1},
		"Seven": {4, 4, 4, 3, 4, 3, 2, 3, 1},
		"Nine":  {3, 3, 2, 1, 3, 1, 2, 1, 4},
	}
	for name, ranks := range want {
		r := byName[name]
		got := []CategoryStanding{r.G, r.A, r.PlusMinus, r.PIM, r.PPP, r.SOG, r.W, r.GA, r.GAA}
		for i, cat := range standingsTestCategories {
			assert.Equal(t, ranks[i], got[i].Rank, "%s rank in %s", name, cat)
		}
	}
	assert.Equal(t, float64(40), byName["Four"].G.Value, "value is the stored stat, not roto points")
}
