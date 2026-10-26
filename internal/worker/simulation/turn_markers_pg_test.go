package simulation

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/fixtures/simfixtures"
	"github.com/sperano/puckdb/internal/llm"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

// These tests run two attempts of one turn against real PostgreSQL so
// that they overlap the way a timed-out attempt and its retry can: the
// winner's commit has written its marker but not committed when the
// loser, past its own already-done probe, reaches its commit. The
// marker index must make the loser wait, then fail, so exactly one
// marker and one turn's writes land.

const (
	// heldCommitWaitTimeout bounds how long the held commit waits for the
	// other attempt to block on its marker.
	heldCommitWaitTimeout = 30 * time.Second
	// heldCommitPollInterval is how often the held commit checks.
	heldCommitPollInterval = 10 * time.Millisecond
	// spendDelta absorbs the NUMERIC round trip of the pool spend.
	spendDelta = 1e-6

	raceSeason   = 20242025
	raceModel    = "claude-haiku-4-5"
	raceWinnerID = 8478402
	raceLoserID  = 8480039
)

// heldCommitTransactor is PgxTransactor, except that its first commit
// whose writes succeeded stays open until another backend waits on that
// transaction (an insert waiting on its uncommitted index entry), and
// closes held when it starts waiting.
type heldCommitTransactor struct {
	pool *pgxpool.Pool
	held chan struct{}
	once sync.Once
}

func newHeldCommitTransactor(pool *pgxpool.Pool) *heldCommitTransactor {
	return &heldCommitTransactor{pool: pool, held: make(chan struct{})}
}

func (h *heldCommitTransactor) InTx(ctx context.Context, fn func(SimQueries) error) error {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(sqlcdb.New(tx)); err != nil {
		return err
	}
	var waitErr error
	h.once.Do(func() {
		close(h.held)
		waitErr = waitForBlockedBackend(ctx, h.pool, tx)
	})
	if waitErr != nil {
		return waitErr
	}
	return tx.Commit(ctx)
}

// waitForBlockedBackend returns once another backend waits on blocker.
func waitForBlockedBackend(ctx context.Context, pool *pgxpool.Pool, blocker pgx.Tx) error {
	ctx, cancel := context.WithTimeout(ctx, heldCommitWaitTimeout)
	defer cancel()
	var blockerPID int32
	if err := blocker.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&blockerPID); err != nil {
		return err
	}
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM pg_stat_activity WHERE $1::int = ANY (pg_blocking_pids(pid)))`,
			blockerPID).Scan(&waiting); err != nil {
			return err
		}
		if waiting {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(heldCommitPollInterval):
		}
	}
}

// gatedLLMClient answers through next once gate is closed, so its
// attempt has passed its already-done probe before the other attempt
// commits, but reaches its own commit only after the other attempt's
// marker is written.
type gatedLLMClient struct {
	gate <-chan struct{}
	next llm.Client
}

func (c gatedLLMClient) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	select {
	case <-c.gate:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return c.next.Complete(ctx, req)
}

// raceFixture is one pool with one agent in a private database.
type raceFixture struct {
	pool    *pgxpool.Pool
	poolID  int32
	agentID int32
}

func newRaceFixture(t *testing.T) raceFixture {
	t.Helper()
	pool := simfixtures.NewPool(t, simfixtures.NewDatabase(t))
	seed := simfixtures.NewSeeder(t, pool)
	poolID := seed.Pool("overlapping attempts")
	agentID := seed.Agent(simfixtures.Agent{PoolID: poolID, TeamName: "Racers", Model: raceModel})
	seed.Player(raceWinnerID, "Connor", "McDavid", nil)
	seed.Player(raceLoserID, "Nathan", "MacKinnon", nil)
	return raceFixture{pool: pool, poolID: poolID, agentID: agentID}
}

// activities is one worker's Activities: its own agent cache, so each
// attempt talks to its own model client.
func (f raceFixture) activities(client llm.Client, tx Transactor[SimQueries]) *Activities {
	return &Activities{
		Queries: sqlcdb.New(f.pool),
		Tx:      tx,
		AgentFactory: func(_ int32, cfg AgentConfig, _ map[llm.Provider]llm.ProviderConfig, _ int) (*Agent, error) {
			return fakeAgent(client, cfg), nil
		},
	}
}

func (f raceFixture) scope(t *testing.T, date string) TurnScope {
	return TurnScope{
		PoolID:  f.poolID,
		AgentID: f.agentID,
		SimDate: pgDate(t, date),
		PoolConfig: PoolConfig{
			Season:   raceSeason,
			NumTeams: 2,
			RosterPositions: map[RosterSlot]int{
				SlotC: 2, SlotLW: 2, SlotRW: 2, SlotD: 3, SlotG: 2, SlotUtil: 2, SlotBN: 5, SlotIR: 2,
			},
		},
		AgentConfig: AgentConfig{Provider: "anthropic", Model: raceModel, Strategy: "balanced"},
	}
}

func (f raceFixture) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, f.pool.QueryRow(context.Background(), sql, args...).Scan(&n))
	return n
}

func (f raceFixture) poolSpend(t *testing.T) float64 {
	t.Helper()
	var spend float64
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`SELECT total_llm_cost_usd::float8 FROM sim_pools WHERE id = $1`, f.poolID).Scan(&spend))
	return spend
}

// runActivity executes fn(in) as an activity of a, decoding its result.
func runActivity[In, Out any](a *Activities, fn func(*Activities) func(context.Context, In) (Out, error), in In) (Out, error) {
	var out Out
	env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	act := fn(a)
	env.RegisterActivity(act)
	future, err := env.ExecuteActivity(act, in)
	if err != nil {
		return out, err
	}
	return out, future.Get(&out)
}

// runOverlapping runs winner and loser concurrently and returns their
// results once both finished.
func runOverlapping[Out any](winner, loser func() (Out, error)) (wonRes, lostRes Out, wonErr, lostErr error) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); wonRes, wonErr = winner() }()
	go func() { defer wg.Done(); lostRes, lostErr = loser() }()
	wg.Wait()
	return wonRes, lostRes, wonErr, lostErr
}

func draftPickOf(a *Activities) func(context.Context, DraftPickInput) (DraftPickResult, error) {
	return a.DraftPick
}

func manageRosterOf(a *Activities) func(context.Context, ManageRosterInput) (ManageRosterResult, error) {
	return a.ManageRoster
}

func TestOverlappingDraftPickAttemptsCommitOnePick(t *testing.T) {
	t.Parallel()
	f := newRaceFixture(t)
	in := DraftPickInput{
		TurnScope:    f.scope(t, "2024-10-08"),
		Round:        1,
		Pick:         1,
		DraftPrompt:  DraftPromptInput{SlotsRemaining: v1Need()},
		AvailableIDs: []int64{raceWinnerID, raceLoserID},
	}
	held := newHeldCommitTransactor(f.pool)
	winner := f.activities(&scriptedLLMClient{t: t, responses: []*llm.Response{
		draftPlayerToolCall(raceWinnerID, "winner"),
	}}, held)
	loser := f.activities(gatedLLMClient{gate: held.held, next: &scriptedLLMClient{t: t, responses: []*llm.Response{
		draftPlayerToolCall(raceLoserID, "loser"),
	}}}, NewPgxTransactor(f.pool))

	won, lost, wonErr, lostErr := runOverlapping(
		func() (DraftPickResult, error) { return runActivity(winner, draftPickOf, in) },
		func() (DraftPickResult, error) { return runActivity(loser, draftPickOf, in) },
	)
	require.NoError(t, wonErr)
	require.NoError(t, lostErr)

	assert.Equal(t, DraftPickResult{PlayerID: raceWinnerID, CostUsd: won.CostUsd}, won)
	assert.Equal(t, DraftPickResult{
		Skipped: true, SkipReason: SkipReasonAlreadyDrafted, PlayerID: raceWinnerID, CostUsd: lost.CostUsd,
	}, lost)
	assert.Greater(t, lost.CostUsd, 0.0)

	var pickID int32
	var pickPlayer int64
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`SELECT id, player_id FROM sim_transactions
		 WHERE pool_id = $1 AND agent_id = $2 AND type = 'draft_pick' AND round = 1 AND pick = 1`,
		f.poolID, f.agentID).Scan(&pickID, &pickPlayer), "exactly one draft pick")
	assert.Equal(t, int64(raceWinnerID), pickPlayer)
	assert.Equal(t, 1, f.count(t, `SELECT count(*) FROM sim_rosters WHERE pool_id = $1`, f.poolID))
	assert.Equal(t, 1, f.count(t, `SELECT count(*) FROM sim_rosters WHERE pool_id = $1 AND player_id = $2`,
		f.poolID, raceWinnerID))

	// The winner's turn stays, with the link to its pick.
	assert.Equal(t, 1, f.count(t, `SELECT count(*) FROM sim_agent_turns WHERE pool_id = $1`, f.poolID))
	assert.Equal(t, 1, f.count(t, `SELECT count(*) FROM sim_agent_turns t
		JOIN sim_agent_tool_calls c ON c.turn_id = t.id
		WHERE t.pool_id = $1 AND t.status = 'ok' AND c.applied_transaction_id = $2`, f.poolID, pickID))
	assert.InDelta(t, won.CostUsd+lost.CostUsd, f.poolSpend(t), spendDelta, "both attempts were billed")

	// A serial retry after both reads the pick without calling the model.
	never := &scriptedLLMClient{t: t}
	retry, err := runActivity(f.activities(never, NewPgxTransactor(f.pool)), draftPickOf, in)
	require.NoError(t, err)
	assert.Equal(t, DraftPickResult{Skipped: true, SkipReason: SkipReasonAlreadyDrafted, PlayerID: raceWinnerID}, retry)
	assert.Zero(t, never.calls.Load())
}

// passTurn is a daily turn that takes no action, billed.
func passTurn(text string) []*llm.Response {
	return []*llm.Response{{
		Content: text,
		Usage:   &llm.Usage{PromptTokens: 1000, CompletionTokens: 50, TotalTokens: 1050},
	}}
}

func TestOverlappingDailyTurnAttemptsCommitOneMarker(t *testing.T) {
	t.Parallel()
	f := newRaceFixture(t)
	scope := f.scope(t, "2024-11-15")
	in := ManageRosterInput{
		TurnScope: scope,
		Roster: RosterState{
			Placements: map[int64]RosterSlot{},
			Limits:     scope.PoolConfig.RosterPositions,
		},
		Positions: map[int64]sqlcdb.PlayerPosition{},
	}
	held := newHeldCommitTransactor(f.pool)
	winner := f.activities(&scriptedLLMClient{t: t, responses: passTurn("winner passes")}, held)
	loser := f.activities(gatedLLMClient{gate: held.held, next: &scriptedLLMClient{t: t, responses: passTurn("loser passes")}},
		NewPgxTransactor(f.pool))

	won, lost, wonErr, lostErr := runOverlapping(
		func() (ManageRosterResult, error) { return runActivity(winner, manageRosterOf, in) },
		func() (ManageRosterResult, error) { return runActivity(loser, manageRosterOf, in) },
	)
	require.NoError(t, wonErr)
	require.NoError(t, lostErr)

	assert.False(t, won.Skipped)
	assert.Equal(t, ManageRosterResult{Skipped: true, SkipReason: SkipReasonAlreadyManaged, CostUsd: lost.CostUsd}, lost)
	assert.Greater(t, lost.CostUsd, 0.0)

	date := pgtype.Date{Time: scope.SimDate.Time, Valid: true}
	assert.Equal(t, 1, f.count(t, `SELECT count(*) FROM sim_transactions
		WHERE pool_id = $1 AND agent_id = $2 AND date = $3 AND type = 'daily_turn_done'`,
		f.poolID, f.agentID, date), "exactly one day marker")
	assert.Equal(t, 1, f.count(t, `SELECT count(*) FROM sim_transactions
		WHERE pool_id = $1 AND type <> 'daily_turn_done'`, f.poolID), "only the winner's pass row")

	// The winner's turn stays.
	assert.Equal(t, 1, f.count(t, `SELECT count(*) FROM sim_agent_turns WHERE pool_id = $1`, f.poolID))
	assert.Equal(t, 1, f.count(t, `SELECT count(*) FROM sim_agent_turns
		WHERE pool_id = $1 AND status = 'ok' AND final_text = 'winner passes'`, f.poolID))
	assert.InDelta(t, won.CostUsd+lost.CostUsd, f.poolSpend(t), spendDelta, "both attempts were billed")

	// A serial retry after both skips without calling the model.
	never := &scriptedLLMClient{t: t}
	retry, err := runActivity(f.activities(never, NewPgxTransactor(f.pool)), manageRosterOf, in)
	require.NoError(t, err)
	assert.Equal(t, ManageRosterResult{Skipped: true, SkipReason: SkipReasonAlreadyManaged}, retry)
	assert.Zero(t, never.calls.Load())
}
