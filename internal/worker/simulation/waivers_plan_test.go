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
	in := ProcessWaiversInput{PoolID: 1, SimDate: pgDate(t, "2024-11-15"), RosterCapacity: testWaiverRosterCapacity}
	r := claimResolution{playerID: 8478402, winner: claim(100, 1, 8478402, 0)}
	q := &stubSimQueries{
		existsRosterPlayerByPlayer: map[int64]bool{8478402: true},
		resolveClaimNotPending:     map[int32]bool{100: true},
	}

	won, err := applyWaiverResolution(ctx, q, in, r)
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
