//go:build integration

package simulation

// DB-backed waiver resolution tests (SIM-I7, SIM-I8): per-pool
// serialization of overlapping ProcessWaivers attempts, pending-only
// claim transitions, and waiver priority initialization from the
// recorded draft order, all against real PostgreSQL. None of them seeds
// sim_waiver_priority: the pools start with an empty priority table, as
// a production pool does.

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

const (
	waiverRaceSeason int32  = 20242025
	waiverRaceDate   string = "2024-11-15"
	// waiverRacePlayer is outside seedScenario's 5001..5004 range.
	waiverRacePlayer int64         = 5101
	waiverRaceAgents int32         = 3
	waiverRaceDays   int32         = 2
	lockWaitTimeout  time.Duration = 10 * time.Second
	lockPollInterval time.Duration = 20 * time.Millisecond
	attemptTimeout   time.Duration = 30 * time.Second
)

// waiverRaceFixture is a pool of three agents with no priority rows, where
// the first two agents (draft positions 1 and 2) claim one player due today.
// Reverse draft order ranks agents[2], agents[1], agents[0], so agents[1]
// wins the claim and rotates to the bottom.
type waiverRaceFixture struct {
	poolID int32
	agents []int32 // index i has draft_position i+1 once recorded
	claims []sqlcdb.SimWaiverClaim
	in     ProcessWaiversInput
}

// waiverTestRoster is the per-slot roster of a waiver test pool; slots
// left zero have no capacity.
type waiverTestRoster struct{ C, BN, IR int32 }

// oneBenchRoster is the default waiver test roster: one active slot and a
// one-player bench, so a single BN player fills the bench.
var oneBenchRoster = waiverTestRoster{C: 1, BN: 1}

// insertWaiverTestPool creates a pool of waiverRaceAgents agents (the given
// roster, one draft round) the way createSimPool does, minus the workflow
// start. Agents have no draft position and the pool no priority rows.
func insertWaiverTestPool(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, name, startDate, endDate string, roster waiverTestRoster) (poolID int32, agents []int32) {
	t.Helper()
	q := sqlcdb.New(pgPool)
	costCap, err := pgNumericFromFloat(200.0)
	require.NoError(t, err)
	pool, err := q.InsertSimPool(ctx, sqlcdb.InsertSimPoolParams{
		Name: name, Season: waiverRaceSeason, Status: string(PoolStatusDraft),
		NumTeams: waiverRaceAgents, WaiverDays: waiverRaceDays, DraftRounds: 1,
		MaxLLMCostUsdPerPool: costCap, Categories: []string{"G", "A"},
		RosterC: roster.C, RosterBN: roster.BN, RosterIR: roster.IR, StopAfter: StopAfterNever.String(),
		StartDate: mustPgDate(startDate), EndDate: mustPgDate(endDate),
	})
	require.NoError(t, err)
	for range waiverRaceAgents {
		a, err := q.InsertSimAgent(ctx, sqlcdb.InsertSimAgentParams{
			PoolID: pool.ID, Provider: "anthropic", Model: "claude-haiku-4-5", Strategy: "balanced",
		})
		require.NoError(t, err)
		agents = append(agents, a.ID)
	}
	return pool.ID, agents
}

func newWaiverRaceFixture(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, name string, recordDraftOrder bool) waiverRaceFixture {
	t.Helper()
	seedSeason(t, ctx, pgPool, waiverRaceSeason)
	seedPlayer(t, ctx, pgPool, waiverRacePlayer, sqlcdb.PlayerPositionC)

	q := sqlcdb.New(pgPool)
	poolID, agents := insertWaiverTestPool(t, ctx, pgPool, name, "2024-10-08", "2024-12-31", oneBenchRoster)
	f := waiverRaceFixture{
		poolID: poolID,
		agents: agents,
		in:     ProcessWaiversInput{PoolID: poolID, SimDate: mustPgDate(waiverRaceDate)},
	}
	if recordDraftOrder {
		acts := &Activities{Queries: q}
		require.NoError(t, acts.RecordDraftOrder(ctx, RecordDraftOrderInput{AgentIDs: f.agents}))
	}
	for _, agentID := range f.agents[:2] {
		c, err := q.InsertSimWaiverClaim(ctx, sqlcdb.InsertSimWaiverClaimParams{
			PoolID: poolID, AgentID: agentID, PlayerID: waiverRacePlayer,
			FiledDate: mustPgDate("2024-11-13"), ProcessDate: f.in.SimDate,
		})
		require.NoError(t, err)
		f.claims = append(f.claims, c)
	}
	require.Empty(t, waiverPriorityOrder(t, ctx, pgPool, f.poolID), "fixture starts with no priority rows")
	return f
}

// assertResolvedOnce checks the committed outcome of one resolution of the
// fixture: agents[1] won and holds the player, agents[0] lost, and the
// winner rotated to the bottom of the reverse draft order exactly once.
func (f waiverRaceFixture) assertResolvedOnce(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool) {
	t.Helper()
	assertWaiverClaimStatus(t, ctx, pgPool, f.poolID, f.agents[1], waiverRacePlayer, string(WaiverClaimStatusWon))
	assertWaiverClaimStatus(t, ctx, pgPool, f.poolID, f.agents[0], waiverRacePlayer, string(WaiverClaimStatusLost))
	assertPlayerOnRoster(t, ctx, pgPool, f.poolID, f.agents[1], waiverRacePlayer)
	var adds int
	require.NoError(t, pgPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM sim_transactions WHERE pool_id=$1 AND type='add' AND player_id=$2`,
		f.poolID, waiverRacePlayer).Scan(&adds))
	assert.Equal(t, 1, adds, "exactly one add for the won claim")
	assert.Equal(t, []int32{f.agents[2], f.agents[0], f.agents[1]}, waiverPriorityOrder(t, ctx, pgPool, f.poolID))
}

// waiverPriorityOrder returns the pool's agent IDs by priority, checking the
// numbers are exactly 1..N.
func waiverPriorityOrder(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, poolID int32) []int32 {
	t.Helper()
	rows, err := sqlcdb.New(pgPool).ListSimWaiverPriorityByPool(ctx, poolID)
	require.NoError(t, err)
	order := make([]int32, 0, len(rows))
	for i, r := range rows {
		require.Equal(t, int32(i+1), r.Priority, "priority numbers are 1..N")
		order = append(order, r.AgentID)
	}
	return order
}

// waiverAttempt is the outcome of one ProcessWaivers activity attempt.
type waiverAttempt struct {
	result ProcessWaiversResult
	err    error
}

// processWaiversAsync runs ProcessWaivers as a Temporal activity on the
// shared pool, the way a second attempt runs, and delivers its outcome.
func processWaiversAsync(pgPool *pgxpool.Pool, in ProcessWaiversInput) <-chan waiverAttempt {
	acts := &Activities{Queries: sqlcdb.New(pgPool), Tx: NewPgxTransactor(pgPool)}
	env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	env.RegisterActivity(acts.ProcessWaivers)
	done := make(chan waiverAttempt, 1)
	go func() {
		fut, err := env.ExecuteActivity(acts.ProcessWaivers, in)
		if err != nil {
			done <- waiverAttempt{err: err}
			return
		}
		var res ProcessWaiversResult
		done <- waiverAttempt{result: res, err: fut.Get(&res)}
	}()
	return done
}

// waitForLockWaiter blocks until some other session is waiting on a lock
// in the test database: the second attempt, parked behind the first
// attempt's open transaction (on LockSimPool, unless that lock is missing).
func waitForLockWaiter(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool) {
	t.Helper()
	deadline := time.Now().Add(lockWaitTimeout)
	for time.Now().Before(deadline) {
		var waiting bool
		require.NoError(t, pgPool.QueryRow(ctx, `
			SELECT EXISTS (
			    SELECT 1 FROM pg_stat_activity
			    WHERE datname = current_database() AND pid <> pg_backend_pid()
			      AND wait_event_type = 'Lock'
			)`).Scan(&waiting))
		if waiting {
			return
		}
		time.Sleep(lockPollInterval)
	}
	t.Fatalf("no session waited on the pool lock within %s", lockWaitTimeout)
}

func awaitAttempt(t *testing.T, done <-chan waiverAttempt) waiverAttempt {
	t.Helper()
	select {
	case a := <-done:
		return a
	case <-time.After(attemptTimeout):
		t.Fatalf("ProcessWaivers attempt did not finish within %s", attemptTimeout)
		return waiverAttempt{}
	}
}

// SIM-I7 + SIM-I8, two-transaction interleaving on an empty priority
// table: attempt A initializes priority and resolves the claim but has
// not committed; attempt B starts meanwhile. B must wait on the pool lock,
// then see A's result (no pending claims, priority rows present) instead
// of re-resolving a stale snapshot: the won claim stays won and the
// priority rows are neither duplicated nor rotated twice.
func TestIntegrationWaiverOverlappingAttemptKeepsWin(t *testing.T) {
	pgPool := requireIntegrationEnv(t)
	ctx := context.Background()
	f := newWaiverRaceFixture(t, ctx, pgPool, "waiver_overlap", true)

	txA, err := pgPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = txA.Rollback(ctx) }()
	resA, err := processDueWaivers(ctx, sqlcdb.New(txA), f.in)
	require.NoError(t, err)
	assert.True(t, resA.PriorityInitialized, "A initializes the empty priority table")
	assert.Equal(t, 1, resA.ClaimsWon)
	assert.Equal(t, 1, resA.ClaimsLost)

	attemptB := processWaiversAsync(pgPool, f.in)
	waitForLockWaiter(t, ctx, pgPool)
	select {
	case b := <-attemptB:
		t.Fatalf("attempt B finished while A held the pool lock: %+v", b)
	default:
	}
	require.NoError(t, txA.Commit(ctx))

	b := awaitAttempt(t, attemptB)
	require.NoError(t, b.err)
	assert.True(t, b.result.Skipped, "B finds no pending claims after A's commit")
	assert.False(t, b.result.PriorityInitialized, "B does not re-initialize priority")
	assert.Zero(t, b.result.ClaimsResolved)
	f.assertResolvedOnce(t, ctx, pgPool)
}

// SIM-I7 mutation probe against PostgreSQL: replaying the resolution with
// the pre-commit claim snapshot (the player is now rostered, so the group
// would resolve lost) fails on the pending-only update and changes nothing.
func TestIntegrationWaiverStaleResolutionCannotRewriteWin(t *testing.T) {
	pgPool := requireIntegrationEnv(t)
	ctx := context.Background()
	f := newWaiverRaceFixture(t, ctx, pgPool, "waiver_stale", true)

	first := awaitAttempt(t, processWaiversAsync(pgPool, f.in))
	require.NoError(t, first.err)
	require.Equal(t, 1, first.result.ClaimsWon)

	stale := claimResolution{playerID: waiverRacePlayer, winner: f.claims[1], losers: f.claims[:1]}
	pool, err := sqlcdb.New(pgPool).GetSimPool(ctx, f.poolID)
	require.NoError(t, err)
	tx, err := pgPool.Begin(ctx)
	require.NoError(t, err)
	won, err := applyWaiverResolution(ctx, sqlcdb.New(tx), f.in, rosterLimitsFromRow(pool), stale)
	require.NoError(t, tx.Rollback(ctx), "roll back before asserting so a failure leaves no open locks")
	require.ErrorIs(t, err, errWaiverClaimNotPending)
	assert.False(t, won)

	f.assertResolvedOnce(t, ctx, pgPool)
}

// Serial retries after a committed resolution: initialization inserts
// nothing, the retry skips, and the rotated order is not reset to the
// reverse draft order.
func TestIntegrationWaiverPriorityRetryKeepsRows(t *testing.T) {
	pgPool := requireIntegrationEnv(t)
	ctx := context.Background()
	f := newWaiverRaceFixture(t, ctx, pgPool, "waiver_retry", true)

	first := awaitAttempt(t, processWaiversAsync(pgPool, f.in))
	require.NoError(t, first.err)
	require.True(t, first.result.PriorityInitialized)

	inserted, err := sqlcdb.New(pgPool).InitSimWaiverPriority(ctx, f.poolID)
	require.NoError(t, err)
	assert.Zero(t, inserted, "a pool with priority rows is not re-initialized")

	retry := awaitAttempt(t, processWaiversAsync(pgPool, f.in))
	require.NoError(t, retry.err)
	assert.True(t, retry.result.Skipped)
	assert.False(t, retry.result.PriorityInitialized)
	f.assertResolvedOnce(t, ctx, pgPool)
}

// Without a recorded draft order there is no reverse draft order to start
// from: resolution refuses to run and leaves the claims pending.
func TestIntegrationWaiverPriorityRequiresDraftOrder(t *testing.T) {
	pgPool := requireIntegrationEnv(t)
	ctx := context.Background()
	f := newWaiverRaceFixture(t, ctx, pgPool, "waiver_no_draft_order", false)

	attempt := awaitAttempt(t, processWaiversAsync(pgPool, f.in))
	require.Error(t, attempt.err)
	assert.Contains(t, attempt.err.Error(), errWaiverPriorityIncomplete.Error())

	assert.Empty(t, waiverPriorityOrder(t, ctx, pgPool, f.poolID))
	for _, agentID := range f.agents[:2] {
		assertWaiverClaimStatus(t, ctx, pgPool, f.poolID, agentID, waiverRacePlayer, string(WaiverClaimStatusPending))
	}
}
