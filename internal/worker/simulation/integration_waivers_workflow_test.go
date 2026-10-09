//go:build integration

package simulation

// SIM-I8 end to end: a pool created like createSimPool, drafted by the
// real workflow, with two agents claiming the same waiver player. No test
// helper seeds sim_waiver_priority; the order must come from the draft.

import (
	"cmp"
	"context"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/llm"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	contestedWaiverStart   = "2024-10-08"
	contestedWaiverDays    = 5
	contestedWaiverDropDay = 2
	// contestedWaiverClaimDay files both claims; they process
	// waiverRaceDays later, on the pool's last day.
	contestedWaiverClaimDay = 3
	contestedWaiverTimeout  = 90 * time.Second
)

func TestIntegrationContestedWaiverUsesReverseDraftOrder(t *testing.T) {
	pgPool := requireIntegrationEnv(t)
	redisClient := connectRedisForTest(t)
	defer redisClient.Close()
	tc, tcCleanup := startTemporalForTest(t)
	defer tcCleanup()
	ctx := context.Background()

	playerIDs := seedScenario(t, ctx, pgPool, waiverRaceSeason)
	poolID, agents := insertWaiverTestPool(t, ctx, pgPool, "integration_test_contested_waiver",
		contestedWaiverStart, offsetDate(contestedWaiverStart, contestedWaiverDays-1))
	alpha, bravo, charlie := agents[0], agents[1], agents[2]
	contested := playerIDs[2]

	// Each agent drafts one player. Charlie drops theirs on day 2; Alpha
	// and Bravo both claim it on day 3 (each has a free BN spot), so the
	// claims process together on day 5.
	daily := func(day int, resp *llm.Response) []*llm.Response {
		out := make([]*llm.Response, contestedWaiverDays)
		out[day-1] = resp
		return out
	}
	scripts := map[int32]*perAgentScript{
		alpha: {teamName: "Alpha", draftPicks: []int64{playerIDs[0]},
			daily: daily(contestedWaiverClaimDay, claimPlayerResponse(contested, nil, "Alpha claims"))},
		bravo: {teamName: "Bravo", draftPicks: []int64{playerIDs[1]},
			daily: daily(contestedWaiverClaimDay, claimPlayerResponse(contested, nil, "Bravo claims"))},
		charlie: {teamName: "Charlie", draftPicks: []int64{contested},
			daily: daily(contestedWaiverDropDay, dropPlayerResponse(contested, "Charlie drops"))},
	}
	stop := setupIntegrationWorker(t, tc, pgPool, redisClient, perAgentFactory(scripts))
	defer stop()

	runWorkflowToCompletion(t, ctx, tc, poolID, contestedWaiverTimeout)

	// The draft order is a workflow-random shuffle: derive the expected
	// reverse order from what RecordDraftOrder stored.
	reverseDraft := agentsByDraftPositionDesc(t, ctx, pgPool, poolID)
	winner, loser := alpha, bravo
	if slices.Index(reverseDraft, bravo) < slices.Index(reverseDraft, alpha) {
		winner, loser = bravo, alpha
	}
	assertWaiverClaimStatus(t, ctx, pgPool, poolID, winner, contested, string(WaiverClaimStatusWon))
	assertWaiverClaimStatus(t, ctx, pgPool, poolID, loser, contested, string(WaiverClaimStatusLost))
	assertPlayerOnRoster(t, ctx, pgPool, poolID, winner, contested)

	want := slices.DeleteFunc(slices.Clone(reverseDraft), func(id int32) bool { return id == winner })
	want = append(want, winner)
	assert.Equal(t, want, waiverPriorityOrder(t, ctx, pgPool, poolID),
		"reverse draft order with the winner rotated to the bottom")
}

// agentsByDraftPositionDesc returns the pool's agents, last draft position
// first, failing if any agent has none.
func agentsByDraftPositionDesc(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, poolID int32) []int32 {
	t.Helper()
	agents, err := sqlcdb.New(pgPool).ListSimAgentsByPool(ctx, poolID)
	require.NoError(t, err)
	slices.SortFunc(agents, func(a, b sqlcdb.SimAgent) int {
		return cmp.Compare(b.DraftPosition.Int32, a.DraftPosition.Int32)
	})
	ids := make([]int32, len(agents))
	for i, a := range agents {
		require.Truef(t, a.DraftPosition.Valid, "agent %d has a draft position", a.ID)
		ids[i] = a.ID
	}
	return ids
}
