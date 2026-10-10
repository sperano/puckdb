//go:build integration

package simulation

// DB contract tests for SIM-I5 and SIM-I6 against real PostgreSQL:
//   - every way a player leaves a roster (drop_player, add_player's
//     drop_player_id replacement, a won waiver claim's drop) puts that
//     player on waivers and out of the free-agent pool for the pool's
//     waiver_days, then back into the free-agent pool;
//   - waiver resolution applies the per-slot bench policy, not total
//     roster capacity.

import (
	"context"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Fixture IDs are disjoint from every other integration fixture in this
// package. The season and waiver window come from insertWaiverTestPool.
const (
	replDropDate        = "2024-11-15"
	replGameDate        = "2024-10-10"
	replTeamHome  int64 = 9500
	replTeamAway  int64 = 9501
	replGameID    int64 = 95001
	replExplicit  int64 = 9510 // dropped with drop_player
	replAddDrop   int64 = 9511 // dropped by add_player's drop_player_id
	replWaiver    int64 = 9512 // dropped by a won waiver claim
	replAddTarget int64 = 9513 // the free agent add_player picks up
	replClaimed   int64 = 9514 // the waiver player the claim wins
)

// seedActiveSkaters gives each player a regular-season game line before
// replDropDate, which is what makes ListSimFreeAgentCandidates consider
// them at all.
func seedActiveSkaters(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, players ...int64) {
	t.Helper()
	seedTeam(t, ctx, pgPool, waiverRaceSeason, replTeamHome, "RPH")
	seedTeam(t, ctx, pgPool, waiverRaceSeason, replTeamAway, "RPA")
	seedGame(t, ctx, pgPool, replGameID, waiverRaceSeason, replGameDate, replTeamHome, replTeamAway)
	for _, id := range players {
		seedPlayer(t, ctx, pgPool, id, sqlcdb.PlayerPositionC)
		seedGameSkaterStats(t, ctx, pgPool, replGameID, id, replTeamHome, 0, 0)
	}
}

func seedRosterPlayer(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, poolID, agentID int32, playerID int64, slot RosterSlot) {
	t.Helper()
	require.NoError(t, sqlcdb.New(pgPool).InsertSimRoster(ctx, sqlcdb.InsertSimRosterParams{
		PoolID: poolID, AgentID: agentID, PlayerID: playerID, Slot: string(slot),
		AcquiredAt: mustPgDate(replGameDate), AcquiredVia: string(AcquiredViaDraft),
	}))
}

func seedDueClaim(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, poolID, agentID int32, playerID int64, drop *int64) {
	t.Helper()
	_, err := sqlcdb.New(pgPool).InsertSimWaiverClaim(ctx, sqlcdb.InsertSimWaiverClaimParams{
		PoolID: poolID, AgentID: agentID, PlayerID: playerID, DropPlayerID: nullableInt8(drop),
		FiledDate: mustPgDate(offsetDate(replDropDate, -int(waiverRaceDays))), ProcessDate: mustPgDate(replDropDate),
	})
	require.NoError(t, err)
}

// commitTurnAction applies one daily-turn action in its own transaction,
// through the same dispatcher commitDailyTurn uses.
func commitTurnAction(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, poolID, agentID int32, args Action) {
	t.Helper()
	in := ManageRosterInput{PoolID: poolID, AgentID: agentID, SimDate: mustPgDate(replDropDate)}
	require.NoError(t, NewPgxTransactor(pgPool).InTx(ctx, func(q SimQueries) error {
		_, err := applyAction(ctx, q, in, recordedAction{args: args})
		return err
	}))
}

// waiverAvailability is what claim_player and add_player see on one day.
func waiverAvailability(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, poolID int32, day string) (onWaivers, freeAgents []int64) {
	t.Helper()
	q := sqlcdb.New(pgPool)
	rows, err := q.ListSimPlayersOnWaivers(ctx, sqlcdb.ListSimPlayersOnWaiversParams{
		WaiverDays: waiverRaceDays, PoolID: poolID, SimDate: mustPgDate(day),
	})
	require.NoError(t, err)
	for _, r := range rows {
		onWaivers = append(onWaivers, r.PlayerID.Int64)
	}
	freeAgents, err = q.ListSimFreeAgentCandidates(ctx, sqlcdb.ListSimFreeAgentCandidatesParams{
		Season: waiverRaceSeason, GameDate: mustPgDate(day), PoolID: poolID, Column4: waiverRaceDays,
	})
	require.NoError(t, err)
	return onWaivers, freeAgents
}

func countTransactions(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, poolID int32, txType TransactionType, playerID int64) int {
	t.Helper()
	var n int
	require.NoError(t, pgPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM sim_transactions WHERE pool_id=$1 AND type=$2 AND player_id=$3`,
		poolID, string(txType), playerID).Scan(&n))
	return n
}

// addDropRelation returns the drop_player_id of the add row for playerID.
func addDropRelation(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, poolID int32, playerID int64) int64 {
	t.Helper()
	var drop int64
	require.NoError(t, pgPool.QueryRow(ctx,
		`SELECT drop_player_id FROM sim_transactions WHERE pool_id=$1 AND type='add' AND player_id=$2`,
		poolID, playerID).Scan(&drop))
	return drop
}

// SIM-I5: each removal path opens the same waiver window. Before the fix
// an add_player replacement wrote only an add row, so replAddDrop was a
// free agent from the next day on and never on waivers.
func TestIntegrationReplacementDropsEnterWaiverWindow(t *testing.T) {
	pgPool := requireIntegrationEnv(t)
	ctx := context.Background()
	seedSeason(t, ctx, pgPool, waiverRaceSeason)
	seedActiveSkaters(t, ctx, pgPool, replExplicit, replAddDrop, replWaiver, replAddTarget, replClaimed)
	poolID, agents := insertWaiverTestPool(t, ctx, pgPool, "replacement_drops", "2024-10-08", "2024-12-31", oneBenchRoster)
	acts := &Activities{Queries: sqlcdb.New(pgPool)}
	require.NoError(t, acts.RecordDraftOrder(ctx, RecordDraftOrderInput{AgentIDs: agents}))
	seedRosterPlayer(t, ctx, pgPool, poolID, agents[0], replExplicit, SlotBN)
	seedRosterPlayer(t, ctx, pgPool, poolID, agents[1], replAddDrop, SlotBN)
	seedRosterPlayer(t, ctx, pgPool, poolID, agents[2], replWaiver, SlotBN)
	seedDueClaim(t, ctx, pgPool, poolID, agents[2], replClaimed, intp(replWaiver))

	// Day loop order: waivers first, then the agents' turns.
	res := awaitAttempt(t, processWaiversAsync(pgPool, ProcessWaiversInput{PoolID: poolID, SimDate: mustPgDate(replDropDate)}))
	require.NoError(t, res.err)
	require.Equal(t, 1, res.result.ClaimsWon)
	commitTurnAction(t, ctx, pgPool, poolID, agents[0], DropPlayerArgs{PlayerID: replExplicit})
	commitTurnAction(t, ctx, pgPool, poolID, agents[1], AddPlayerArgs{PlayerID: replAddTarget, DropPlayerID: intp(replAddDrop)})

	dropped := []int64{replExplicit, replAddDrop, replWaiver}
	for _, id := range dropped {
		assert.Equalf(t, 1, countTransactions(t, ctx, pgPool, poolID, TransactionTypeDrop, id),
			"player %d: exactly one drop row (no double marker for replacements)", id)
	}
	assert.Equal(t, replAddDrop, addDropRelation(t, ctx, pgPool, poolID, replAddTarget), "add row keeps the add/drop relation")
	assert.Equal(t, replWaiver, addDropRelation(t, ctx, pgPool, poolID, replClaimed), "waiver add row keeps the relation")

	for day := 0; day <= int(waiverRaceDays); day++ {
		date := offsetDate(replDropDate, day)
		onWaivers, freeAgents := waiverAvailability(t, ctx, pgPool, poolID, date)
		inWindow := day < int(waiverRaceDays)
		for _, id := range dropped {
			assert.Equalf(t, inWindow, slices.Contains(onWaivers, id), "%s: player %d on waivers", date, id)
			assert.Equalf(t, !inWindow, slices.Contains(freeAgents, id), "%s: player %d a free agent", date, id)
		}
		assert.Equalf(t, len(onWaivers), len(slices.Compact(slices.Sorted(slices.Values(onWaivers)))),
			"%s: each waiver player listed once", date)
	}
}

// SIM-I6 at resolution: one pool (C=1, BN=1, IR=1), three winners whose
// claims each fit total roster capacity. Only the one whose drop vacates
// BN wins; no bench ends above its limit. Before the fix all three won
// and two benches held two players.
func TestIntegrationWaiverResolutionKeepsBenchLimit(t *testing.T) {
	pgPool := requireIntegrationEnv(t)
	ctx := context.Background()
	const (
		activeDropBench, activeDropC int64 = 9520, 9521
		benchDropBench, benchDropC   int64 = 9522, 9523
		noDropBench                  int64 = 9524
		claimActive, claimBench      int64 = 9525, 9526
		claimNoDrop                  int64 = 9527
	)
	seedSeason(t, ctx, pgPool, waiverRaceSeason)
	seedActiveSkaters(t, ctx, pgPool, activeDropBench, activeDropC, benchDropBench, benchDropC, noDropBench,
		claimActive, claimBench, claimNoDrop)
	// The empty IR slot is total-capacity headroom the old check counted.
	poolID, agents := insertWaiverTestPool(t, ctx, pgPool, "replacement_bench_limit", "2024-10-08", "2024-12-31",
		waiverTestRoster{C: 1, BN: 1, IR: 1})
	acts := &Activities{Queries: sqlcdb.New(pgPool)}
	require.NoError(t, acts.RecordDraftOrder(ctx, RecordDraftOrderInput{AgentIDs: agents}))

	seedRosterPlayer(t, ctx, pgPool, poolID, agents[0], activeDropBench, SlotBN)
	seedRosterPlayer(t, ctx, pgPool, poolID, agents[0], activeDropC, SlotC)
	seedDueClaim(t, ctx, pgPool, poolID, agents[0], claimActive, intp(activeDropC))
	seedRosterPlayer(t, ctx, pgPool, poolID, agents[1], benchDropBench, SlotBN)
	seedRosterPlayer(t, ctx, pgPool, poolID, agents[1], benchDropC, SlotC)
	seedDueClaim(t, ctx, pgPool, poolID, agents[1], claimBench, intp(benchDropBench))
	seedRosterPlayer(t, ctx, pgPool, poolID, agents[2], noDropBench, SlotBN)
	seedDueClaim(t, ctx, pgPool, poolID, agents[2], claimNoDrop, nil)

	res := awaitAttempt(t, processWaiversAsync(pgPool, ProcessWaiversInput{PoolID: poolID, SimDate: mustPgDate(replDropDate)}))
	require.NoError(t, res.err)
	assert.Equal(t, 1, res.result.ClaimsWon)
	assert.Equal(t, 2, res.result.ClaimsLost)

	assertWaiverClaimStatus(t, ctx, pgPool, poolID, agents[0], claimActive, string(WaiverClaimStatusLost))
	assertWaiverClaimStatus(t, ctx, pgPool, poolID, agents[1], claimBench, string(WaiverClaimStatusWon))
	assertWaiverClaimStatus(t, ctx, pgPool, poolID, agents[2], claimNoDrop, string(WaiverClaimStatusLost))
	assertPlayerOnRoster(t, ctx, pgPool, poolID, agents[0], activeDropC)
	assertPlayerOnRoster(t, ctx, pgPool, poolID, agents[1], claimBench)
	assert.Zero(t, countTransactions(t, ctx, pgPool, poolID, TransactionTypeDrop, activeDropC), "lost claim drops nobody")
	assert.Equal(t, 1, countTransactions(t, ctx, pgPool, poolID, TransactionTypeDrop, benchDropBench))

	q := sqlcdb.New(pgPool)
	for _, agentID := range agents {
		rows, err := q.ListSimRosterByAgent(ctx, sqlcdb.ListSimRosterByAgentParams{PoolID: poolID, AgentID: agentID})
		require.NoError(t, err)
		bench := 0
		for _, r := range rows {
			if r.Slot == string(SlotBN) {
				bench++
			}
		}
		assert.LessOrEqualf(t, bench, 1, "agent %d bench within its limit", agentID)
	}
}
