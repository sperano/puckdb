package draftsession

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManualAddIsVisibleDuringUpstreamOutage(t *testing.T) {
	state, _, err := Reconcile(State{}, completeSnapshot())
	require.NoError(t, err)
	key := PickKey{Round: 1, Pick: 1}
	state, report, err := ApplyManual(state, ManualOperation{Kind: ManualAdd, Key: key, Pick: pick(key, 1, 101)})
	require.NoError(t, err)
	board := EffectiveBoard(state)
	require.Len(t, board, 1)
	assert.Equal(t, SourceManual, board[0].Source)
	assert.Equal(t, 101, board[0].Pick.PlayerID)
	assert.True(t, report.SafeToRecommend)
}

func TestManualUndoCreatesTombstoneAndSnapshotCanReconcileIt(t *testing.T) {
	key := PickKey{Round: 1, Pick: 1}
	state, _, err := Reconcile(State{}, completeSnapshot(observed(1, 1, 1, 101)))
	require.NoError(t, err)
	state, _, err = ApplyManual(state, ManualOperation{Kind: ManualUndo, Key: key})
	require.NoError(t, err)
	assert.Empty(t, EffectiveBoard(state))
	assert.Equal(t, ManualUndo, state.Manual[key].Kind)

	state, report, err := Reconcile(state, completeSnapshot())
	require.NoError(t, err)
	assert.Empty(t, state.Manual)
	assert.True(t, report.SafeToRecommend)
}

func TestManualCorrectionConflictRequiresExplicitResolution(t *testing.T) {
	key := PickKey{Round: 1, Pick: 1}
	state, _, err := Reconcile(State{}, completeSnapshot(observed(1, 1, 1, 101)))
	require.NoError(t, err)
	state, _, err = ApplyManual(state, ManualOperation{Kind: ManualCorrect, Key: key, Pick: pick(key, 1, 201)})
	require.NoError(t, err)
	state, report, err := Reconcile(state, completeSnapshot(observed(1, 1, 2, 102)))
	require.NoError(t, err)
	assert.True(t, state.Manual[key].Conflict)
	assert.Equal(t, []PickKey{key}, report.Conflicts)
	assert.False(t, report.SafeToRecommend)

	_, _, err = ResolveConflict(state, key, ConflictChoice("unknown"))
	assert.ErrorIs(t, err, ErrInvalidManual)
	state, report, err = ResolveConflict(state, key, KeepManual)
	require.NoError(t, err)
	assert.False(t, state.Manual[key].Conflict)
	assert.True(t, report.SafeToRecommend)
	board := EffectiveBoard(state)
	assert.Equal(t, 201, board[0].Pick.PlayerID)
	assert.Equal(t, SourceManual, board[0].Source)
}

func TestAcceptUpstreamConflictDiscardsManualChange(t *testing.T) {
	key := PickKey{Round: 1, Pick: 1}
	state, _, err := Reconcile(State{}, completeSnapshot(observed(1, 1, 1, 101)))
	require.NoError(t, err)
	state, _, err = ApplyManual(state, ManualOperation{Kind: ManualCorrect, Key: key, Pick: pick(key, 1, 201)})
	require.NoError(t, err)
	state, _, err = Reconcile(state, completeSnapshot(observed(1, 1, 2, 102)))
	require.NoError(t, err)
	state, report, err := ResolveConflict(state, key, AcceptUpstream)
	require.NoError(t, err)
	assert.Empty(t, state.Manual)
	assert.Equal(t, 102, EffectiveBoard(state)[0].Pick.PlayerID)
	assert.Equal(t, SourceYahoo, EffectiveBoard(state)[0].Source)
	assert.True(t, report.SafeToRecommend)
}

func TestManualEditOnSameSlotIsReconciledAndIdempotent(t *testing.T) {
	key := PickKey{Round: 1, Pick: 1}
	state, _, err := Reconcile(State{}, completeSnapshot(observed(1, 1, 1, 101)))
	require.NoError(t, err)
	state, _, err = ApplyManual(state, ManualOperation{Kind: ManualCorrect, Key: key, Pick: pick(key, 2, 102)})
	require.NoError(t, err)
	state, report, err := Reconcile(state, completeSnapshot(observed(1, 1, 2, 102)))
	require.NoError(t, err)
	assert.Empty(t, state.Manual)
	assert.True(t, report.Changed)
	assert.True(t, report.SafeToRecommend)
	version := state.Version
	state, report, err = Reconcile(state, completeSnapshot(observed(1, 1, 2, 102)))
	require.NoError(t, err)
	assert.False(t, report.Changed)
	assert.Equal(t, version, state.Version)
}

func TestManualOperationValidation(t *testing.T) {
	key := PickKey{Round: 1, Pick: 1}
	_, _, err := ApplyManual(State{}, ManualOperation{Kind: ManualAdd, Key: key})
	assert.ErrorIs(t, err, ErrInvalidManual)
	_, _, err = ApplyManual(State{}, ManualOperation{Kind: ManualUndo, Key: key})
	assert.ErrorIs(t, err, ErrInvalidManual)
}

func pick(key PickKey, team, player int) *Pick {
	return &Pick{Key: key, TeamID: team, PlayerID: player}
}
