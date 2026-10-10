package draftsession

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	firstSlot  = PickKey{Round: 1, Pick: 1}
	secondSlot = PickKey{Round: 1, Pick: 2}
)

func TestManualAddRejectsPlayerAlreadyOnBoard(t *testing.T) {
	state, _, err := Reconcile(State{}, completeSnapshot(observed(1, 1, 1, 101)))
	require.NoError(t, err)

	next, report, err := ApplyManual(state, ManualOperation{Kind: ManualAdd, Key: secondSlot, Pick: pick(secondSlot, 2, 101)})

	require.ErrorIs(t, err, ErrDuplicatePlayer)
	assert.ErrorIs(t, err, ErrInvalidManual)
	assert.Contains(t, err.Error(), "round 1 pick 1")
	assert.Equal(t, state, next, "a rejected add leaves the board untouched")
	assert.False(t, report.Changed)
}

func TestManualAddRejectsPlayerHeldByAnotherManualEntry(t *testing.T) {
	state, _, err := Reconcile(State{}, completeSnapshot())
	require.NoError(t, err)
	state, _, err = ApplyManual(state, ManualOperation{Kind: ManualAdd, Key: firstSlot, Pick: pick(firstSlot, 1, 101)})
	require.NoError(t, err)

	_, _, err = ApplyManual(state, ManualOperation{Kind: ManualAdd, Key: secondSlot, Pick: pick(secondSlot, 2, 101)})

	assert.ErrorIs(t, err, ErrDuplicatePlayer)
}

func TestManualCorrectionRejectsPlayerInAnotherSlot(t *testing.T) {
	state, _, err := Reconcile(State{}, completeSnapshot(observed(1, 1, 1, 101), observed(1, 2, 2, 102)))
	require.NoError(t, err)

	next, _, err := ApplyManual(state, ManualOperation{Kind: ManualCorrect, Key: secondSlot, Pick: pick(secondSlot, 2, 101)})

	assert.ErrorIs(t, err, ErrDuplicatePlayer)
	assert.Equal(t, state, next)
}

func TestManualCorrectionKeepsPlayerInSameSlot(t *testing.T) {
	state, _, err := Reconcile(State{}, completeSnapshot(observed(1, 1, 1, 101)))
	require.NoError(t, err)

	state, report, err := ApplyManual(state, ManualOperation{Kind: ManualCorrect, Key: firstSlot, Pick: pick(firstSlot, 2, 101)})

	require.NoError(t, err, "changing the team of a slot's own player is not a duplicate")
	assert.Equal(t, 2, EffectiveBoard(state)[0].Pick.TeamID)
	assert.True(t, report.SafeToRecommend)
}

func TestManualMoveIsUndoThenAdd(t *testing.T) {
	state, _, err := Reconcile(State{}, completeSnapshot(observed(1, 1, 1, 101)))
	require.NoError(t, err)
	state, _, err = ApplyManual(state, ManualOperation{Kind: ManualUndo, Key: firstSlot})
	require.NoError(t, err)

	state, report, err := ApplyManual(state, ManualOperation{Kind: ManualAdd, Key: secondSlot, Pick: pick(secondSlot, 2, 101)})

	require.NoError(t, err)
	board := EffectiveBoard(state)
	require.Len(t, board, 1)
	assert.Equal(t, secondSlot, board[0].Pick.Key)
	assert.Empty(t, report.Duplicates)
	assert.True(t, report.SafeToRecommend)
}

func TestUpstreamArrivalInAnotherSlotFlagsManualDuplicate(t *testing.T) {
	state := manualAddThenUpstreamElsewhere(t)

	assert.True(t, state.Manual[secondSlot].Conflict)
	assert.False(t, SafeToRecommend(state))
	assert.Equal(t, []DuplicatePlayer{{PlayerID: 101, Keys: []PickKey{firstSlot, secondSlot}}}, DuplicatePlayers(state))

	again, report, err := Reconcile(state, completeSnapshot(observed(1, 1, 1, 101)))
	require.NoError(t, err)
	assert.False(t, report.Changed, "a repeated poll must not clear the duplicate conflict")
	assert.Equal(t, []PickKey{secondSlot}, report.Conflicts)
	assert.Len(t, report.Duplicates, 1)
	assert.False(t, report.SafeToRecommend)
	assert.Equal(t, state.Version, again.Version)
}

func TestKeepManualRejectsDuplicateAndAcceptUpstreamClearsIt(t *testing.T) {
	state := manualAddThenUpstreamElsewhere(t)

	kept, _, err := ResolveConflict(state, secondSlot, KeepManual)
	assert.ErrorIs(t, err, ErrDuplicatePlayer)
	assert.Equal(t, state, kept)

	accepted, report, err := ResolveConflict(state, secondSlot, AcceptUpstream)
	require.NoError(t, err)
	assert.Empty(t, accepted.Manual)
	assert.Empty(t, report.Duplicates)
	assert.True(t, report.SafeToRecommend)
}

func TestKeepManualAllowedOnceOtherSlotIsCorrected(t *testing.T) {
	state := manualAddThenUpstreamElsewhere(t)
	state, report, err := ApplyManual(state, ManualOperation{Kind: ManualCorrect, Key: firstSlot, Pick: pick(firstSlot, 1, 103)})
	require.NoError(t, err)
	assert.Empty(t, report.Duplicates)
	assert.False(t, report.SafeToRecommend, "the flagged entry still needs an explicit resolution")

	state, report, err = ResolveConflict(state, secondSlot, KeepManual)

	require.NoError(t, err)
	assert.False(t, state.Manual[secondSlot].Conflict)
	assert.True(t, report.SafeToRecommend)
}

func TestAcceptUpstreamThatDuplicatesManualEntryFlagsIt(t *testing.T) {
	state, _, err := Reconcile(State{}, completeSnapshot(observed(1, 1, 1, 100)))
	require.NoError(t, err)
	state, _, err = ApplyManual(state, ManualOperation{Kind: ManualCorrect, Key: firstSlot, Pick: pick(firstSlot, 1, 300)})
	require.NoError(t, err)
	state, _, err = ApplyManual(state, ManualOperation{Kind: ManualAdd, Key: secondSlot, Pick: pick(secondSlot, 2, 101)})
	require.NoError(t, err)
	state, report, err := Reconcile(state, completeSnapshot(observed(1, 1, 1, 101)))
	require.NoError(t, err)
	require.Equal(t, []PickKey{firstSlot}, report.Conflicts, "only the corrected slot contradicts Yahoo so far")

	state, report, err = ResolveConflict(state, firstSlot, AcceptUpstream)

	require.NoError(t, err)
	assert.Equal(t, []PickKey{secondSlot}, report.Conflicts)
	assert.Equal(t, []DuplicatePlayer{{PlayerID: 101, Keys: []PickKey{firstSlot, secondSlot}}}, report.Duplicates)
	assert.False(t, report.SafeToRecommend)
	assert.True(t, state.Manual[secondSlot].Conflict)
}

func TestSafeToRecommendRejectsStoredDuplicateWithoutConflictFlag(t *testing.T) {
	state := State{
		Upstream:         map[PickKey]Pick{firstSlot: *pick(firstSlot, 1, 101)},
		Manual:           map[PickKey]ManualChange{secondSlot: {Kind: ManualAdd, Pick: pick(secondSlot, 2, 101)}},
		UpstreamComplete: true,
	}

	assert.False(t, SafeToRecommend(state))
	assert.Len(t, DuplicatePlayers(state), 1)
}

func TestSafeToRecommendRejectsUpstreamOnlyDuplicate(t *testing.T) {
	state := State{
		Upstream: map[PickKey]Pick{
			firstSlot: *pick(firstSlot, 1, 101), secondSlot: *pick(secondSlot, 2, 101),
		},
		Manual: map[PickKey]ManualChange{}, UpstreamComplete: true,
	}

	assert.False(t, SafeToRecommend(state))
}

func TestDuplicatePlayersIgnoresUndoneSlots(t *testing.T) {
	state := State{
		Upstream: map[PickKey]Pick{
			firstSlot: *pick(firstSlot, 1, 101), secondSlot: *pick(secondSlot, 2, 101),
		},
		Manual: map[PickKey]ManualChange{firstSlot: {Kind: ManualUndo, Base: pick(firstSlot, 1, 101)}},
	}

	assert.Empty(t, DuplicatePlayers(state))
}

// manualAddThenUpstreamElsewhere adds player 101 locally at the second slot
// while Yahoo is empty, then lets a complete Yahoo board place 101 at the
// first slot. The second slot's own Yahoo state is unchanged (still empty),
// so only the duplicate makes it a conflict.
func manualAddThenUpstreamElsewhere(t *testing.T) State {
	t.Helper()
	state, _, err := Reconcile(State{}, completeSnapshot())
	require.NoError(t, err)
	state, _, err = ApplyManual(state, ManualOperation{Kind: ManualAdd, Key: secondSlot, Pick: pick(secondSlot, 2, 101)})
	require.NoError(t, err)
	state, report, err := Reconcile(state, completeSnapshot(observed(1, 1, 1, 101)))
	require.NoError(t, err)
	require.Equal(t, []PickKey{secondSlot}, report.Conflicts)
	require.False(t, report.SafeToRecommend)
	return state
}
