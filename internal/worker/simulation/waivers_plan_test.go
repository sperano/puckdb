package simulation

import (
	"context"
	"testing"

	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SIM-I7 mutation probe, inverted: replaying a stale resolution of an
// already-won claim (its player now rostered, so the group resolves lost)
// must fail instead of rewriting the claim as lost.
func TestApplyWaiverResolution_StaleClaimIsNotRewritten(t *testing.T) {
	ctx := context.Background()
	in := ProcessWaiversInput{PoolID: 1, SimDate: pgDate(t, "2024-11-15")}
	r := claimResolution{playerID: 8478402, winner: claim(100, 1, 8478402, 0)}
	q := &stubSimQueries{
		existsRosterPlayerByPlayer: map[int64]bool{8478402: true},
		resolveClaimNotPending:     map[int32]bool{100: true},
	}

	won, err := applyWaiverResolution(ctx, q, in, map[RosterSlot]int{SlotBN: testWaiverBenchLimit}, r)
	require.ErrorIs(t, err, errWaiverClaimNotPending)
	assert.False(t, won)
	require.Len(t, q.resolveClaimCalls, 1, "the only update is the pending-guarded one that matched no row")
	assert.Equal(t, string(WaiverClaimStatusLost), q.resolveClaimCalls[0].Status)
}

func TestMarkGroupWon_LoserNoLongerPendingFails(t *testing.T) {
	ctx := context.Background()
	in := ProcessWaiversInput{PoolID: 1, SimDate: pgDate(t, "2024-11-15")}
	r := claimResolution{
		playerID: 8478402,
		winner:   claim(100, 1, 8478402, 0),
		losers:   []sqlcdb.SimWaiverClaim{claim(101, 2, 8478402, 0)},
	}
	q := &stubSimQueries{resolveClaimNotPending: map[int32]bool{101: true}}

	err := markGroupWon(ctx, q, in, r)
	require.ErrorIs(t, err, errWaiverClaimNotPending)
	assert.Contains(t, err.Error(), "claim 101 lost")
	assert.Empty(t, q.cancelClaimsCalls, "nothing else runs after the failed transition")
}

// The activity surfaces the stale transition as a failed attempt, so the
// transaction rolls back and nothing from the group (roster add, priority
// rotation) commits.
func (s *ProcessWaiversTestSuite) TestStaleWinningClaim_FailsAttempt() {
	t := s.T()
	s.queries.listClaimsDueRows = []sqlcdb.SimWaiverClaim{claim(100, 1, 8478402, 0)}
	s.setPriorities(priority(1, 1), priority(2, 2))
	s.queries.resolveClaimNotPending = map[int32]bool{100: true}

	_, err := s.env.ExecuteActivity(s.acts.ProcessWaivers, s.input())
	require.Error(t, err)
	assert.Contains(t, err.Error(), errWaiverClaimNotPending.Error())
	assert.Empty(t, s.queries.insertRosterCalls, "no roster add after a failed won transition")
	assert.Empty(t, s.queries.updatePriorityCalls)
}

// Waiver resolution applies the same capacity policy as add_player and
// claim_player (replacementCases in validate_test.go), against the
// winner's roster as it is on the process date.
func TestPlanWaiverResolution_ReplacementCapacity(t *testing.T) {
	ctx := context.Background()
	const winner int32 = 1
	in := ProcessWaiversInput{PoolID: 1, SimDate: pgDate(t, "2024-11-15")}
	for _, tc := range replacementCases() {
		t.Run(tc.name, func(t *testing.T) {
			rows := make([]sqlcdb.SimRoster, 0, len(tc.placements))
			for id, slot := range tc.placements {
				rows = append(rows, sqlcdb.SimRoster{PoolID: 1, AgentID: winner, PlayerID: id, Slot: string(slot)})
			}
			q := &stubSimQueries{listFullRosterByAgent: map[int32][]sqlcdb.SimRoster{winner: rows}}
			var dropID int64
			if tc.drop != nil {
				dropID = *tc.drop
			}

			plan, err := planWaiverResolution(ctx, q, in, replacementLimits(), claim(100, winner, replacementCandidate, dropID))
			require.NoError(t, err)
			assert.Equal(t, tc.waiverClaimable, plan.claimable)
			_, rostered := tc.placements[dropID]
			assert.Equal(t, tc.drop != nil && rostered, plan.dropPresent)
		})
	}
}
