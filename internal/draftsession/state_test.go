package draftsession

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReconcileKeepsCorrectedAndResetPlayersAsUpstreamPlayers(t *testing.T) {
	initial, _, err := Reconcile(State{}, completeSnapshot(observed(1, 1, 3, 102)))
	require.NoError(t, err)
	corrected, _, err := Reconcile(initial, completeSnapshot(observed(1, 1, 3, 103)))
	require.NoError(t, err)
	reset, report, err := Reconcile(corrected, completeSnapshot())
	require.NoError(t, err)

	assert.Equal(t, map[int]bool{102: true}, initial.UpstreamPlayers)
	assert.Equal(t, map[int]bool{102: true, 103: true}, corrected.UpstreamPlayers)
	assert.Empty(t, reset.Upstream)
	assert.Equal(t, map[int]bool{102: true, 103: true}, reset.UpstreamPlayers,
		"a reset board still knows which players Yahoo drafted")
	assert.True(t, report.Changed)
}

func TestReconcileRecordsPriorPicksOfStateWithoutUpstreamPlayers(t *testing.T) {
	legacy := State{Upstream: map[PickKey]Pick{{Round: 1, Pick: 1}: *pick(PickKey{Round: 1, Pick: 1}, 3, 102)}}

	next, _, err := Reconcile(legacy, completeSnapshot(observed(1, 1, 3, 103)))

	require.NoError(t, err)
	assert.Equal(t, map[int]bool{102: true, 103: true}, next.UpstreamPlayers)
	assert.Nil(t, legacy.UpstreamPlayers, "the reducer must not mutate its input")
}

func TestUpstreamPlayersDoNotAdvanceVersionOfIdenticalSnapshot(t *testing.T) {
	snapshot := completeSnapshot(observed(1, 1, 3, 102))
	legacy := State{Upstream: map[PickKey]Pick{{Round: 1, Pick: 1}: *pick(PickKey{Round: 1, Pick: 1}, 3, 102)},
		UpstreamComplete: true, Version: 4}

	next, report, err := Reconcile(legacy, snapshot)

	require.NoError(t, err)
	assert.False(t, report.Changed)
	assert.Equal(t, uint64(4), next.Version)
	assert.Equal(t, map[int]bool{102: true}, next.UpstreamPlayers)
}

func TestDraftDerivedPlayersCountsManualPickOnlyWhileEntryExists(t *testing.T) {
	key := PickKey{Round: 1, Pick: 1}
	added, _, err := ApplyManual(State{}, ManualOperation{Kind: ManualAdd, Key: key, Pick: pick(key, 8, 101)})
	require.NoError(t, err)
	assert.Equal(t, map[int]bool{101: true}, DraftDerivedPlayers(added))

	undone, _, err := ApplyManual(added, ManualOperation{Kind: ManualUndo, Key: key})
	require.NoError(t, err)
	assert.Empty(t, DraftDerivedPlayers(undone), "an undone manual add leaves no draft provenance")
}

func TestDraftDerivedPlayersIncludesManualBaseAndUpstreamHistory(t *testing.T) {
	key := PickKey{Round: 1, Pick: 1}
	state := State{
		Upstream:        map[PickKey]Pick{key: *pick(key, 3, 102)},
		Manual:          map[PickKey]ManualChange{key: {Kind: ManualCorrect, Pick: pick(key, 8, 104), Base: pick(key, 3, 105)}},
		UpstreamPlayers: map[int]bool{106: true},
	}

	assert.Equal(t, map[int]bool{102: true, 104: true, 105: true, 106: true}, DraftDerivedPlayers(state))
}

func TestRecordUpstreamPlayersSeedsFromPicksAndBases(t *testing.T) {
	key := PickKey{Round: 1, Pick: 1}
	other := PickKey{Round: 1, Pick: 2}
	state := State{
		Upstream: map[PickKey]Pick{key: *pick(key, 3, 102)},
		Manual:   map[PickKey]ManualChange{other: {Kind: ManualUndo, Base: pick(other, 8, 105)}},
	}

	RecordUpstreamPlayers(&state)

	assert.Equal(t, map[int]bool{102: true, 105: true}, state.UpstreamPlayers)
}
