//go:build integration

package simulation

// DB-backed cost accounting and cost-cap tests (SIM-D3, SIM-G3) against
// real PostgreSQL: every LLM turn adds exactly the cost its
// sim_agent_turns row records, a reached cap stops the next turn before
// the model runs, and the pause transition writes one cost_cap_reached
// row however many serial retries or overlapping attempts reach it.

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/llm"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

const (
	// costCapPlayer is outside the ranges other integration tests seed.
	costCapPlayer int64 = 5201
	costCapDayOne       = "2024-11-15"
	costCapDayTwo       = "2024-11-16"
	// costCapUsd is the cap of the cap-enforcement tests; their pool has
	// already spent costCapSpentUsd, over it.
	costCapUsd      = 1.0
	costCapSpentUsd = 1.5
	// costCapNoLimitUsd is a cap the spend tests never reach.
	costCapNoLimitUsd = 200.0
)

// costCapAgent is the priced agent every cost-cap test bills against.
var costCapAgent = AgentConfig{Provider: PricingProviderAnthropic, Model: "claude-haiku-4-5", Strategy: "balanced"}

// costCapFixture is a three-agent pool whose agents all talk to one
// scripted LLM client.
type costCapFixture struct {
	poolID int32
	agents []int32
	llm    *scriptedLLMClient
	acts   *Activities
}

func newCostCapFixture(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, name string) costCapFixture {
	t.Helper()
	seedSeason(t, ctx, pgPool, waiverRaceSeason)
	poolID, agents := insertWaiverTestPool(t, ctx, pgPool, name, "2024-10-08", "2024-12-31", oneBenchRoster)
	client := &scriptedLLMClient{t: t}
	return costCapFixture{
		poolID: poolID,
		agents: agents,
		llm:    client,
		acts: &Activities{
			Queries: sqlcdb.New(pgPool),
			Tx:      NewPgxTransactor(pgPool),
			AgentFactory: func(_ int32, cfg AgentConfig, _ map[llm.Provider]llm.ProviderConfig, _ int) (*Agent, error) {
				return fakeAgent(client, cfg), nil
			},
		},
	}
}

func (f costCapFixture) poolConfig(capUsd float64) PoolConfig {
	return PoolConfig{
		Season:               int(waiverRaceSeason),
		NumTeams:             int(waiverRaceAgents),
		WaiverDays:           int(waiverRaceDays),
		DraftRounds:          1,
		MaxLLMCostUsdPerPool: capUsd,
		RosterPositions:      map[RosterSlot]int{SlotC: int(oneBenchRoster.C), SlotBN: int(oneBenchRoster.BN)},
	}
}

func (f costCapFixture) manageInput(agentID int32, date string, capUsd float64) ManageRosterInput {
	cfg := f.poolConfig(capUsd)
	return ManageRosterInput{
		PoolID:      f.poolID,
		AgentID:     agentID,
		SimDate:     mustPgDate(date),
		PoolConfig:  cfg,
		AgentConfig: costCapAgent,
		Roster:      RosterState{Placements: map[int64]RosterSlot{}, Limits: cfg.RosterPositions},
	}
}

func (f costCapFixture) draftInput(agentID int32, capUsd float64) DraftPickInput {
	cfg := f.poolConfig(capUsd)
	return DraftPickInput{
		PoolID:       f.poolID,
		AgentID:      agentID,
		Round:        1,
		Pick:         1,
		SimDate:      mustPgDate("2024-10-08"),
		PoolConfig:   cfg,
		AgentConfig:  costCapAgent,
		DraftPrompt:  DraftPromptInput{SlotsRemaining: computeSlotsRemaining(cfg, nil, nil)},
		AvailableIDs: []int64{costCapPlayer},
	}
}

// runSimActivity runs one attempt of activityFn as a Temporal activity,
// the way a worker runs it.
func runSimActivity[R any](activityFn any, in any) (R, error) {
	var res R
	env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	env.RegisterActivity(activityFn)
	fut, err := env.ExecuteActivity(activityFn, in)
	if err != nil {
		return res, err
	}
	err = fut.Get(&res)
	return res, err
}

// manageAttempt is the outcome of one ManageRoster attempt.
type manageAttempt struct {
	result ManageRosterResult
	err    error
}

// manageRosterAsync runs one ManageRoster attempt in the background, the
// way an overlapping retry runs next to an attempt still in flight.
func manageRosterAsync(acts *Activities, in ManageRosterInput) <-chan manageAttempt {
	done := make(chan manageAttempt, 1)
	go func() {
		res, err := runSimActivity[ManageRosterResult](acts.ManageRoster, in)
		done <- manageAttempt{result: res, err: err}
	}()
	return done
}

func awaitManageAttempt(t *testing.T, done <-chan manageAttempt) manageAttempt {
	t.Helper()
	select {
	case a := <-done:
		return a
	case <-time.After(attemptTimeout):
		t.Fatalf("ManageRoster attempt did not finish within %s", attemptTimeout)
		return manageAttempt{}
	}
}

func setPoolSpend(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, poolID int32, usd float64) {
	t.Helper()
	spent, err := pgNumericFromFloat(usd)
	require.NoError(t, err)
	_, err = pgPool.Exec(ctx, `UPDATE sim_pools SET total_llm_cost_usd = $2 WHERE id = $1`, poolID, spent)
	require.NoError(t, err)
}

func poolSpend(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, poolID int32) float64 {
	t.Helper()
	pool, err := sqlcdb.New(pgPool).GetSimPool(ctx, poolID)
	require.NoError(t, err)
	spent, err := pgFloatFromNumeric(pool.TotalLLMCostUSD)
	require.NoError(t, err)
	return spent
}

// assertPausedOnce checks the committed outcome of one cost-cap
// transition: the pool is paused and holds exactly one cost_cap_reached
// row, written by agentID with the given spend and cap.
func assertPausedOnce(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, poolID, agentID int32, spentUsd, capUsd float64) {
	t.Helper()
	pool, err := sqlcdb.New(pgPool).GetSimPool(ctx, poolID)
	require.NoError(t, err)
	assert.Equal(t, string(PoolStatusPaused), pool.Status)

	rows, err := pgPool.Query(ctx,
		`SELECT agent_id, cost_usd, cap_usd FROM sim_transactions
		 WHERE pool_id = $1 AND type = 'cost_cap_reached'`, poolID)
	require.NoError(t, err)
	defer rows.Close()
	type capRow struct {
		agentID   int32
		cost, cap float64
	}
	var got []capRow
	for rows.Next() {
		var r capRow
		require.NoError(t, rows.Scan(&r.agentID, &r.cost, &r.cap))
		got = append(got, r)
	}
	require.NoError(t, rows.Err())
	require.Len(t, got, 1, "exactly one cost_cap_reached audit row per pool")
	assert.Equal(t, agentID, got[0].agentID)
	assert.InDelta(t, spentUsd, got[0].cost, costDelta)
	assert.InDelta(t, capUsd, got[0].cap, costDelta)
}

// pauseInTx runs attempt A's pause statement inside tx and leaves it
// uncommitted, holding the pool row the way an attempt mid-statement
// does, so the test controls when overlapping attempts may proceed.
func pauseInTx(t *testing.T, ctx context.Context, tx pgx.Tx, poolID, agentID int32) {
	t.Helper()
	spent, err := pgNumericFromFloat(costCapSpentUsd)
	require.NoError(t, err)
	capNum, err := pgNumericFromFloat(costCapUsd)
	require.NoError(t, err)
	written, err := sqlcdb.New(tx).PauseSimPoolForCostCap(ctx, sqlcdb.PauseSimPoolForCostCapParams{
		PoolID: poolID, AgentID: agentID, Date: mustPgDate(costCapDayOne), CostUSD: spent, CapUSD: capNum,
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, written, "attempt A makes the transition")
}

func requireCostCapSkip(t *testing.T, res ManageRosterResult, err error) {
	t.Helper()
	require.NoError(t, err)
	assert.True(t, res.Skipped)
	assert.Equal(t, SkipReasonCostCapReached, res.SkipReason)
	assert.Zero(t, res.CostUsd)
}

// SIM-G3, serial retries: the first attempt that finds the cap reached
// pauses the pool; every later attempt — a retry of the same turn,
// another agent's turn, a draft pick — reports the cap again without
// calling the model and writes no second audit row.
func TestIntegrationCostCapSerialRetriesPauseOnce(t *testing.T) {
	pgPool := requireIntegrationEnv(t)
	ctx := context.Background()
	f := newCostCapFixture(t, ctx, pgPool, "costcap_serial")
	setPoolSpend(t, ctx, pgPool, f.poolID, costCapSpentUsd)

	for range 2 {
		res, err := runSimActivity[ManageRosterResult](f.acts.ManageRoster, f.manageInput(f.agents[0], costCapDayOne, costCapUsd))
		requireCostCapSkip(t, res, err)
	}
	res, err := runSimActivity[ManageRosterResult](f.acts.ManageRoster, f.manageInput(f.agents[1], costCapDayOne, costCapUsd))
	requireCostCapSkip(t, res, err)
	pick, err := runSimActivity[DraftPickResult](f.acts.DraftPick, f.draftInput(f.agents[2], costCapUsd))
	require.NoError(t, err)
	assert.Equal(t, SkipReasonCostCapReached, pick.SkipReason)

	assert.Zero(t, f.llm.calls.Load(), "a reached cap never calls the model")
	assertPausedOnce(t, ctx, pgPool, f.poolID, f.agents[0], costCapSpentUsd, costCapUsd)
}

// SIM-G3, overlapping attempts: attempt A has paused the pool but not
// committed; attempts B and C (a timed-out attempt's retry and another
// turn) reach the cap meanwhile. They must wait on the pool row, then
// see A's committed pause and write nothing: one audit row in total.
func TestIntegrationCostCapOverlappingAttemptsPauseOnce(t *testing.T) {
	pgPool := requireIntegrationEnv(t)
	ctx := context.Background()
	f := newCostCapFixture(t, ctx, pgPool, "costcap_overlap")
	setPoolSpend(t, ctx, pgPool, f.poolID, costCapSpentUsd)

	txA, err := pgPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = txA.Rollback(ctx) }()
	pauseInTx(t, ctx, txA, f.poolID, f.agents[0])

	attemptB := manageRosterAsync(f.acts, f.manageInput(f.agents[1], costCapDayOne, costCapUsd))
	attemptC := manageRosterAsync(f.acts, f.manageInput(f.agents[2], costCapDayOne, costCapUsd))
	waitForLockWaiters(t, ctx, pgPool, 2)
	select {
	case b := <-attemptB:
		t.Fatalf("attempt B finished while A held the pool row: %+v", b)
	case c := <-attemptC:
		t.Fatalf("attempt C finished while A held the pool row: %+v", c)
	default:
	}
	require.NoError(t, txA.Commit(ctx))

	b := awaitManageAttempt(t, attemptB)
	requireCostCapSkip(t, b.result, b.err)
	c := awaitManageAttempt(t, attemptC)
	requireCostCapSkip(t, c.result, c.err)
	assert.Zero(t, f.llm.calls.Load())
	assertPausedOnce(t, ctx, pgPool, f.poolID, f.agents[0], costCapSpentUsd, costCapUsd)
}

// The other order of the overlap: attempt A rolls back (its activity
// failed) while B waits. B must then make the transition itself rather
// than assume A's uncommitted pause: the pool still ends paused, once.
func TestIntegrationCostCapOverlappingAttemptAfterRollbackPauses(t *testing.T) {
	pgPool := requireIntegrationEnv(t)
	ctx := context.Background()
	f := newCostCapFixture(t, ctx, pgPool, "costcap_overlap_rollback")
	setPoolSpend(t, ctx, pgPool, f.poolID, costCapSpentUsd)

	txA, err := pgPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = txA.Rollback(ctx) }()
	pauseInTx(t, ctx, txA, f.poolID, f.agents[0])

	attemptB := manageRosterAsync(f.acts, f.manageInput(f.agents[1], costCapDayOne, costCapUsd))
	waitForLockWaiter(t, ctx, pgPool)
	require.NoError(t, txA.Rollback(ctx))

	b := awaitManageAttempt(t, attemptB)
	requireCostCapSkip(t, b.result, b.err)
	assertPausedOnce(t, ctx, pgPool, f.poolID, f.agents[1], costCapSpentUsd, costCapUsd)
}

// A late attempt on a pool that already completed or was cancelled still
// reports the cap (the workflow is done with the pool anyway) but leaves
// the pool's status as it is and writes no audit row.
func TestIntegrationCostCapLeavesEndedPoolAlone(t *testing.T) {
	pgPool := requireIntegrationEnv(t)
	ctx := context.Background()
	for _, status := range []PoolStatus{PoolStatusComplete, PoolStatusCancelled} {
		t.Run(string(status), func(t *testing.T) {
			f := newCostCapFixture(t, ctx, pgPool, "costcap_ended_"+string(status))
			setPoolSpend(t, ctx, pgPool, f.poolID, costCapSpentUsd)
			require.NoError(t, sqlcdb.New(pgPool).UpdateSimPoolStatus(ctx, sqlcdb.UpdateSimPoolStatusParams{
				ID: f.poolID, Status: string(status),
			}))

			res, err := runSimActivity[ManageRosterResult](f.acts.ManageRoster, f.manageInput(f.agents[0], costCapDayOne, costCapUsd))
			requireCostCapSkip(t, res, err)

			pool, err := sqlcdb.New(pgPool).GetSimPool(ctx, f.poolID)
			require.NoError(t, err)
			assert.Equal(t, string(status), pool.Status)
			var capRows int
			require.NoError(t, pgPool.QueryRow(ctx,
				`SELECT COUNT(*) FROM sim_transactions WHERE pool_id = $1 AND type = 'cost_cap_reached'`,
				f.poolID).Scan(&capRows))
			assert.Zero(t, capRows)
		})
	}
}

// usageRound returns a response carrying every token class, scaled by n
// so each round's usage is distinct.
func usageRound(resp *llm.Response, n int) *llm.Response {
	const (
		prompt        = 1_000
		completion    = 100
		cacheCreation = 2_000
		cacheRead     = 3_000
	)
	resp.Usage = &llm.Usage{
		PromptTokens:             n * prompt,
		CompletionTokens:         n * completion,
		TotalTokens:              n * (prompt + completion),
		CacheCreationInputTokens: n * cacheCreation,
		CacheReadInputTokens:     n * cacheRead,
		CacheReported:            true,
	}
	return resp
}

// turnUsage sums the usage of one turn's scripted rounds.
func turnUsage(rounds ...*llm.Response) llm.Usage {
	var u llm.Usage
	for _, r := range rounds {
		u.Add(r.Usage)
	}
	return u
}

// assertTurnRow checks the phase's sim_agent_turns row: its token
// columns hold want (every token class) and equal the sum of its round
// rows, and cost_usd is the activity's reported cost.
func assertTurnRow(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, poolID int32, phase TurnPhase, want llm.Usage, reportedCost float64) {
	t.Helper()
	var prompt, completion, cacheCreation, cacheRead int
	var roundPrompt, roundCompletion, roundCacheCreation, roundCacheRead int
	var cost float64
	require.NoError(t, pgPool.QueryRow(ctx, `
		SELECT t.prompt_tokens, t.completion_tokens, t.cache_creation_tokens, t.cache_read_tokens,
		       SUM(r.prompt_tokens), SUM(r.completion_tokens), SUM(r.cache_creation_tokens), SUM(r.cache_read_tokens),
		       t.cost_usd
		FROM sim_agent_turns t JOIN sim_agent_turn_rounds r ON r.turn_id = t.id
		WHERE t.pool_id = $1 AND t.phase = $2
		GROUP BY t.id`, poolID, string(phase)).Scan(
		&prompt, &completion, &cacheCreation, &cacheRead,
		&roundPrompt, &roundCompletion, &roundCacheCreation, &roundCacheRead, &cost))

	assert.Equal(t, []int{want.PromptTokens, want.CompletionTokens, want.CacheCreationInputTokens, want.CacheReadInputTokens},
		[]int{prompt, completion, cacheCreation, cacheRead}, "%s turn records every token class", phase)
	assert.Equal(t, []int{prompt, completion, cacheCreation, cacheRead},
		[]int{roundPrompt, roundCompletion, roundCacheCreation, roundCacheRead}, "%s turn totals equal its rounds", phase)
	assert.InDelta(t, EstimateCost(costCapAgent.Provider, costCapAgent.Model, want), cost, costDelta, "%s turn cost", phase)
	assert.InDelta(t, reportedCost, cost, costDelta, "%s turn row holds the cost the activity reported", phase)
}

// SIM-D3 + cap enforcement: one turn of each LLM phase bills every token
// class; the pool total is exactly the sum of the turn rows' cost_usd
// (both written from the one TurnSpend), and once that total reaches the
// cap the next turn is refused before the model runs.
func TestIntegrationTurnCostsMatchPoolTotal(t *testing.T) {
	pgPool := requireIntegrationEnv(t)
	ctx := context.Background()
	f := newCostCapFixture(t, ctx, pgPool, "costcap_spend")
	seedPlayer(t, ctx, pgPool, costCapPlayer, sqlcdb.PlayerPositionC)
	agentID := f.agents[0]

	teamName := []*llm.Response{usageRound(setTeamNameToolCall("Spend Check", "frugal"), 1), usageRound(finalTextResponse("named"), 2)}
	draft := []*llm.Response{usageRound(draftPlayerToolCall(costCapPlayer, "only one left"), 3), usageRound(finalTextResponse("drafted"), 4)}
	daily := []*llm.Response{usageRound(dailyToolResponse(ToolUpdateNotes, `{"notes":"watch the cap"}`, ""), 5), usageRound(finalTextResponse("noted"), 6)}
	for _, turn := range [][]*llm.Response{teamName, draft, daily} {
		f.llm.responses = append(f.llm.responses, turn...)
	}

	named, err := runSimActivity[PickTeamNameResult](f.acts.PickTeamName, PickTeamNameInput{
		PoolID: f.poolID, AgentID: agentID, AgentConfig: costCapAgent, NumTeams: int(waiverRaceAgents),
	})
	require.NoError(t, err)
	require.Equal(t, "Spend Check", named.Name)
	picked, err := runSimActivity[DraftPickResult](f.acts.DraftPick, f.draftInput(agentID, costCapNoLimitUsd))
	require.NoError(t, err)
	require.Equal(t, costCapPlayer, picked.PlayerID)
	require.False(t, picked.UsedFallback)
	managed, err := runSimActivity[ManageRosterResult](f.acts.ManageRoster, f.manageInput(agentID, costCapDayOne, costCapNoLimitUsd))
	require.NoError(t, err)
	require.False(t, managed.Errored)
	require.EqualValues(t, len(teamName)+len(draft)+len(daily), f.llm.calls.Load(), "every scripted round ran")

	assertTurnRow(t, ctx, pgPool, f.poolID, TurnPhaseTeamName, turnUsage(teamName...), named.CostUsd)
	assertTurnRow(t, ctx, pgPool, f.poolID, TurnPhaseDraft, turnUsage(draft...), picked.CostUsd)
	assertTurnRow(t, ctx, pgPool, f.poolID, TurnPhaseDaily, turnUsage(daily...), managed.CostUsd)

	var poolMatchesTurns bool
	require.NoError(t, pgPool.QueryRow(ctx, `
		SELECT p.total_llm_cost_usd = (SELECT SUM(t.cost_usd) FROM sim_agent_turns t WHERE t.pool_id = p.id)
		FROM sim_pools p WHERE p.id = $1`, f.poolID).Scan(&poolMatchesTurns))
	assert.True(t, poolMatchesTurns, "sim_pools.total_llm_cost_usd is exactly the sum of sim_agent_turns.cost_usd")
	spent := poolSpend(t, ctx, pgPool, f.poolID)
	assert.InDelta(t, named.CostUsd+picked.CostUsd+managed.CostUsd, spent, costDelta)

	// The cap is now reached: the next turn pauses the pool instead of
	// calling the model.
	res, err := runSimActivity[ManageRosterResult](f.acts.ManageRoster, f.manageInput(agentID, costCapDayTwo, spent))
	requireCostCapSkip(t, res, err)
	assert.EqualValues(t, len(teamName)+len(draft)+len(daily), f.llm.calls.Load(), "no model call once the cap is reached")
	assertPausedOnce(t, ctx, pgPool, f.poolID, agentID, spent, spent)
	assert.InDelta(t, spent, poolSpend(t, ctx, pgPool, f.poolID), costDelta, "a refused turn adds no spend")
}
