package newsevent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runInjuryExtraction runs rn over one injury version and returns the
// times it recorded on the extraction: the attempt and the reconciliation.
func runInjuryExtraction(t *testing.T, db *fakeDB, rn *Runner) (attempted, reconciled time.Time) {
	t.Helper()
	row := seedInjuryVersion(db, 1, 100, 1, fixedNow)
	rn.Extractor.Client = newScriptedClient(replyResponse(injuryReply))

	result, err := rn.Run(context.Background(), row, &testBudget{remaining: 1})
	require.NoError(t, err)
	require.Equal(t, ExtractionSucceeded, result.Status)

	extractions := db.extractionsFor(row.ID)
	require.Len(t, extractions, 1)
	x := extractions[0]
	require.True(t, x.LastAttemptAt.Valid)
	require.True(t, x.ReconciledAt.Valid)
	return x.LastAttemptAt.Time, x.ReconciledAt.Time
}

func TestRunRecordsTheInjectedClock(t *testing.T) {
	db := newFakeDB()

	attempted, reconciled := runInjuryExtraction(t, db, newTestRunner(db, testModelA))

	assert.Equal(t, fixedNow, attempted)
	assert.Equal(t, fixedNow, reconciled)
}

func TestRunWithoutAClockUsesTimeNow(t *testing.T) {
	db := newFakeDB()
	rn := newTestRunner(db, testModelA)
	rn.Now = nil

	before := time.Now()
	attempted, reconciled := runInjuryExtraction(t, db, rn)
	after := time.Now()

	for _, at := range []time.Time{attempted, reconciled} {
		assert.False(t, at.Before(before), "%s precedes the run", at)
		assert.False(t, at.After(after), "%s follows the run", at)
	}
}
