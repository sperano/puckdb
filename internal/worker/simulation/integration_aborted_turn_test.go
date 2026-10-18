//go:build integration

package simulation

// DB-backed tests of the aborted-turn write (recordAbortedTurn): the
// billed rounds of a turn whose activity context ended reach the pool
// total without replacing a newer attempt's turn row.

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

// An attempt cut off after a billed round (StartToClose deadline, worker
// shutdown) still adds that round to the pool total, through a real
// transaction opened after the activity context ended, and writes no day
// marker. The retry runs the day; the pool total then holds both
// attempts while the retry's turn row replaces the aborted one.
func TestIntegrationAbortedTurnSpendReachesPoolTotal(t *testing.T) {
	pgPool := requireIntegrationEnv(t)
	ctx := context.Background()
	f := newCostCapFixture(t, ctx, pgPool, "costcap_aborted")
	agentID := f.agents[0]
	in := f.manageInput(agentID, costCapDayOne, costCapNoLimitUsd)

	cutOff := usageRound(dailyToolResponse(ToolUpdateNotes, `{"notes":"cut off"}`, ""), 1)
	env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	aborting := &Activities{Queries: f.acts.Queries, Tx: f.acts.Tx}
	cancelDuringTurn(t, env, aborting, cutOff)
	env.RegisterActivity(aborting.ManageRoster)
	_, err := env.ExecuteActivity(aborting.ManageRoster, in)
	require.ErrorContains(t, err, "daily turn aborted")

	abortedCost := EstimateCost(costCapAgent.Provider, costCapAgent.Model, *cutOff.Usage)
	require.Greater(t, abortedCost, 0.0)
	assert.InDelta(t, abortedCost, poolSpend(t, ctx, pgPool, f.poolID), costDelta, "the billed round reached the pool total")
	assert.Zero(t, dailyMarkers(t, ctx, pgPool, f.poolID), "no marker: the retry must run the day")
	assert.Equal(t, []string{string(TurnStatusErrored)}, turnStatuses(t, ctx, pgPool, f.poolID))

	f.llm.responses = []*llm.Response{usageRound(finalTextResponse("retried"), 2)}
	managed, err := runSimActivity[ManageRosterResult](f.acts.ManageRoster, in)
	require.NoError(t, err)
	assert.InDelta(t, abortedCost+managed.CostUsd, poolSpend(t, ctx, pgPool, f.poolID), costDelta, "both attempts billed")
	assert.Equal(t, 1, dailyMarkers(t, ctx, pgPool, f.poolID))
	assert.Equal(t, []string{string(TurnStatusOK)}, turnStatuses(t, ctx, pgPool, f.poolID),
		"the retry's turn row replaced the aborted one")
}

// Temporal may start the retry before a timed-out attempt's detached
// write lands. That late write, against a real transaction, still adds
// its spend but leaves the retry's committed turn row (and its rounds)
// in place instead of replacing it with an errored one.
func TestIntegrationLateAbortedTurnKeepsRetryTurn(t *testing.T) {
	pgPool := requireIntegrationEnv(t)
	ctx := context.Background()
	f := newCostCapFixture(t, ctx, pgPool, "costcap_late_abort")
	in := f.manageInput(f.agents[0], costCapDayOne, costCapNoLimitUsd)

	retry := []*llm.Response{usageRound(dailyToolResponse(ToolUpdateNotes, `{"notes":"retried"}`, ""), 1), usageRound(finalTextResponse("retried"), 2)}
	f.llm.responses = retry
	managed, err := runSimActivity[ManageRosterResult](f.acts.ManageRoster, in)
	require.NoError(t, err)

	stale := []roundCapture{{Response: usageRound(dailyToolResponse(ToolUpdateNotes, `{"notes":"stale"}`, ""), 3)}}
	late := dailyTurnTelemetry(in, dailyOutcome{
		captures: stale,
		spend:    turnSpendOf(costCapAgent, stale),
		toolCaptures: []ToolCallCapture{{
			ToolName: ToolUpdateNotes, ArgumentsRaw: `{"notes":"stale"}`, Outcome: ToolCallOutcomeAccepted,
		}},
	}, false)
	cause := errors.New("daily turn aborted")
	require.Same(t, cause, f.acts.recordAbortedTurn(ctx, late, cause), "the late write succeeds")

	assert.InDelta(t, managed.CostUsd+late.Header.Spend.CostUsd, poolSpend(t, ctx, pgPool, f.poolID), costDelta,
		"the stale attempt's spend still counts")
	assert.Equal(t, []string{string(TurnStatusOK)}, turnStatuses(t, ctx, pgPool, f.poolID),
		"the retry's turn row stays")
	var rounds, roundRows, toolRows int
	require.NoError(t, pgPool.QueryRow(ctx, `
		SELECT t.rounds,
		       (SELECT COUNT(*) FROM sim_agent_turn_rounds r WHERE r.turn_id = t.id),
		       (SELECT COUNT(*) FROM sim_agent_tool_calls c WHERE c.turn_id = t.id)
		FROM sim_agent_turns t WHERE t.pool_id = $1`, f.poolID).Scan(&rounds, &roundRows, &toolRows))
	assert.Equal(t, []int{len(retry), len(retry), 1}, []int{rounds, roundRows, toolRows},
		"the retry's rounds and tool call are untouched")
}

// turnStatuses lists the status of every sim_agent_turns row of the pool.
func turnStatuses(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, poolID int32) []string {
	t.Helper()
	rows, err := pgPool.Query(ctx, `SELECT status FROM sim_agent_turns WHERE pool_id = $1 ORDER BY id`, poolID)
	require.NoError(t, err)
	statuses, err := pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(t, err)
	return statuses
}

// dailyMarkers counts the pool's daily_turn_done rows.
func dailyMarkers(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, poolID int32) int {
	t.Helper()
	var n int
	require.NoError(t, pgPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM sim_transactions WHERE pool_id = $1 AND type = 'daily_turn_done'`, poolID).Scan(&n))
	return n
}
