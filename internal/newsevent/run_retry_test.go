package newsevent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testRetryAfter is the runners' wait before another attempt.
const testRetryAfter = 30 * time.Minute

func TestRun_AttemptInProgressElsewhereIsLeftAlone(t *testing.T) {
	db := newFakeDB()
	row := seedInjuryVersion(db, 1, 100, 1, fixedNow)
	rn := newTestRunner(db, testModelA)
	rn.RetryAfter = testRetryAfter
	db.seedExtraction(sqlcdb.NewsExtraction{
		VersionID: row.ID, ExtractorKey: rn.Extractor.Key(), Status: string(ExtractionPending), Attempts: 1,
		LastAttemptAt: pgtype.Timestamptz{Time: fixedNow.Add(-time.Minute), Valid: true},
	})
	client := newScriptedClient(replyResponse(injuryReply))
	rn.Extractor.Client = client
	budget := &testBudget{remaining: 1}

	result, err := rn.Run(context.Background(), row, budget)

	require.NoError(t, err)
	assert.Equal(t, ExtractionDeferred, result.Status)
	assert.Zero(t, client.callCount(), "another refresh is calling the model for this version")
	assert.Equal(t, 1, budget.remaining, "the reserved call is returned")
	assert.Equal(t, int32(1), db.extractionsFor(row.ID)[0].Attempts)
}

func TestRun_StaleAttemptIsTakenOver(t *testing.T) {
	db := newFakeDB()
	row := seedInjuryVersion(db, 1, 100, 1, fixedNow)
	rn := newTestRunner(db, testModelA)
	rn.RetryAfter = testRetryAfter
	db.seedExtraction(sqlcdb.NewsExtraction{
		VersionID: row.ID, ExtractorKey: rn.Extractor.Key(), Status: string(ExtractionPending), Attempts: 1,
		LastAttemptAt: pgtype.Timestamptz{Time: fixedNow.Add(-2 * testRetryAfter), Valid: true},
	})
	rn.Extractor.Client = newScriptedClient(replyResponse(injuryReply))

	result, err := rn.Run(context.Background(), row, &testBudget{remaining: 1})

	require.NoError(t, err)
	assert.Equal(t, ExtractionSucceeded, result.Status, "a crashed attempt does not hold the version forever")
	assert.Equal(t, int32(2), db.extractionsFor(row.ID)[0].Attempts)
}

func TestRun_ReconcileFailureIsRecordedAndRetriedWithoutACall(t *testing.T) {
	db := newFakeDB()
	row := seedInjuryVersion(db, 1, 100, 1, fixedNow)
	client := newScriptedClient(replyResponse(injuryReply))
	rn := newTestRunner(db, testModelA)
	rn.Extractor.Client = client
	db.txErr = errors.New("constraint violated")

	failed, err := rn.Run(context.Background(), row, &testBudget{remaining: 1})

	require.NoError(t, err, "one version that cannot be reconciled must not stop the batch")
	assert.Equal(t, ExtractionFailed, failed.Status)
	x := db.extractionsFor(row.ID)[0]
	assert.Equal(t, string(ExtractionSucceeded), x.Status, "the model output is kept")
	assert.False(t, x.ReconciledAt.Valid)
	assert.Equal(t, int32(2), x.Attempts, "the failed reconcile counts as an attempt")
	assert.Contains(t, x.LastError, "constraint violated")
	assert.Zero(t, db.eventCount())

	db.txErr = nil
	retried, err := rn.Run(context.Background(), row, &testBudget{})
	require.NoError(t, err)
	assert.Equal(t, ExtractionSucceeded, retried.Status)
	assert.Equal(t, 1, client.callCount(), "the stored output is reconciled without calling the model again")
	assert.Equal(t, 1, db.eventCount())
	assert.Empty(t, db.extractionsFor(row.ID)[0].LastError)
}

func TestRun_ExtractionReconciledElsewhereIsNotAppliedTwice(t *testing.T) {
	db := newFakeDB()
	row := seedInjuryVersion(db, 1, 100, 1, fixedNow)
	rn := newTestRunner(db, testModelA)
	x := db.seedExtraction(sqlcdb.NewsExtraction{
		VersionID: row.ID, ExtractorKey: rn.Extractor.Key(), Status: string(ExtractionSucceeded), Attempts: 1,
		RawOutput: injuryReply, ReconciledAt: pgtype.Timestamptz{Time: fixedNow, Valid: true},
	})
	in, err := BuildInput(context.Background(), db, row, testMaxInputRunes)
	require.NoError(t, err)
	v, err := Interpret(injuryReply, in)
	require.NoError(t, err)
	j := &job{row: row, in: in, extractionID: x.ID}

	require.NoError(t, rn.applyEvents(context.Background(), db, j, reportOf(row, x.ID, true), v))

	assert.Zero(t, db.eventCount())
}
