package draftsession

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReconcileIdenticalAuthoritativeSnapshotIsIdempotent(t *testing.T) {
	snapshot := completeSnapshot(observed(1, 1, 1, 101), observed(1, 2, 2, 102))
	first, firstReport, err := Reconcile(State{}, snapshot)
	require.NoError(t, err)
	second, secondReport, err := Reconcile(first, snapshot)
	require.NoError(t, err)

	assert.True(t, firstReport.Changed)
	assert.Equal(t, 2, firstReport.Applied)
	assert.False(t, secondReport.Changed)
	assert.Equal(t, first.Version, second.Version)
	assert.Zero(t, secondReport.Applied)
	assert.True(t, secondReport.SafeToRecommend)
}

func TestReconcilePartialSnapshotAddsWithoutDeletingAndSuppressesRecommendations(t *testing.T) {
	initial, _, err := Reconcile(State{}, completeSnapshot(observed(1, 1, 1, 101), observed(1, 2, 2, 102)))
	require.NoError(t, err)
	partial := Snapshot{Authoritative: true, HasExpectedCount: true, ExpectedCount: 3, RawCount: 2,
		Picks: []ObservedPick{observed(1, 1, 1, 201)}}

	next, report, err := Reconcile(initial, partial)
	require.NoError(t, err)
	assert.Equal(t, 2, len(next.Upstream))
	assert.Equal(t, 201, next.Upstream[PickKey{Round: 1, Pick: 1}].PlayerID)
	assert.False(t, report.Complete)
	assert.False(t, report.SafeToRecommend)
	assert.Equal(t, 1, report.Applied)
	assert.Zero(t, report.Removed)
}

func TestReconcileDoesNotTreatUnverifiedEmptyPayloadAsReset(t *testing.T) {
	initial, _, err := Reconcile(State{}, completeSnapshot(observed(1, 1, 1, 101)))
	require.NoError(t, err)
	for i, empty := range []Snapshot{
		{Authoritative: false, HasExpectedCount: true, ExpectedCount: 0, RawCount: 0},
		{Authoritative: true, HasExpectedCount: false, RawCount: 0},
		{Authoritative: true, HasExpectedCount: true, ExpectedCount: 1, RawCount: 0},
	} {
		next, report, reconcileErr := Reconcile(initial, empty)
		require.NoError(t, reconcileErr)
		assert.Equal(t, initial.Upstream, next.Upstream)
		assert.True(t, report.Changed, "case %d must mark the board incomplete", i)
		assert.False(t, report.SafeToRecommend)
		assert.Zero(t, report.Removed)
	}
}

func TestReconcileAuthoritativeEmptySnapshotResetsBoard(t *testing.T) {
	initial, _, err := Reconcile(State{}, completeSnapshot(observed(1, 1, 1, 101)))
	require.NoError(t, err)
	reset, report, err := Reconcile(initial, completeSnapshot())
	require.NoError(t, err)
	assert.Empty(t, reset.Upstream)
	assert.Equal(t, 1, report.Removed)
	assert.True(t, report.Complete)
	assert.True(t, report.SafeToRecommend)
}

func TestReconcileReportsMalformedAndUnmappedPicks(t *testing.T) {
	snapshot := completeSnapshot(
		observed(1, 1, 1, 101),
		ObservedPick{Key: PickKey{Round: 1, Pick: 2}, TeamID: 2, Issue: "unmapped Yahoo player key"},
		ObservedPick{Key: PickKey{Round: 0, Pick: 3}, TeamID: 3, PlayerID: 103},
	)
	state, report, err := Reconcile(State{}, snapshot)
	require.NoError(t, err)
	assert.Len(t, report.Skipped, 2)
	assert.Contains(t, report.Skipped[0].Reason, "unmapped")
	assert.False(t, report.Complete)
	assert.False(t, report.SafeToRecommend)
	assert.Len(t, state.Upstream, 1)
	assert.Zero(t, report.Removed)
}

func TestReconcileCorrectedAndUndonePicks(t *testing.T) {
	initial, _, err := Reconcile(State{}, completeSnapshot(observed(1, 1, 1, 101), observed(1, 2, 2, 102)))
	require.NoError(t, err)
	corrected, correctionReport, err := Reconcile(initial, completeSnapshot(observed(1, 1, 3, 103)))
	require.NoError(t, err)
	assert.Equal(t, 103, corrected.Upstream[PickKey{Round: 1, Pick: 1}].PlayerID)
	assert.Empty(t, corrected.Upstream[PickKey{Round: 1, Pick: 2}])
	assert.Equal(t, 1, correctionReport.Removed)
	assert.True(t, correctionReport.SafeToRecommend)
}

func TestReconcileRejectsNegativeSnapshotCounts(t *testing.T) {
	_, _, err := Reconcile(State{}, Snapshot{Authoritative: true, RawCount: -1})
	assert.ErrorIs(t, err, ErrInvalidSnapshot)
}

func completeSnapshot(rows ...ObservedPick) Snapshot {
	return Snapshot{Authoritative: true, HasExpectedCount: true, ExpectedCount: len(rows), RawCount: len(rows), Picks: rows}
}

func observed(round, pick, team, player int) ObservedPick {
	return ObservedPick{Key: PickKey{Round: round, Pick: pick}, TeamID: team, PlayerID: player}
}
