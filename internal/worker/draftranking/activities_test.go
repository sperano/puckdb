package draftranking

// PostgreSQL-backed tests of the refresh activities. They skip unless
// PUCKDB_TEST_PG_URL names a test database (see CLAUDE.md "Database-backed
// tests").

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/fixtures/pgtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

const (
	testSeason   = 2026
	testLeagueID = 424242
	testRunID    = "activity-run"
)

var testNow = time.Date(2026, time.September, 20, 12, 0, 0, 0, time.UTC)

func openTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := pgtest.Open(t)
	_, err := pool.Exec(context.Background(), `DELETE FROM draft_ranking_refreshes WHERE league_id = $1`, testLeagueID)
	require.NoError(t, err)
	return pool
}

func TestRefreshDraftRanking_KnownFailureIsAnOutcomeNotARetry(t *testing.T) {
	pool := openTestDB(t)
	activities := &Activities{Pool: pool, Now: func() time.Time { return testNow }}
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(activities.RefreshDraftRanking)

	encoded, err := env.ExecuteActivity(activities.RefreshDraftRanking, RefreshInput{RunID: testRunID, Season: testSeason, LeagueID: testLeagueID})
	require.NoError(t, err, "missing rules is recorded, not retried")
	var outcome draftrank.Refresh
	require.NoError(t, encoded.Get(&outcome))
	assert.Equal(t, draftrank.RefreshFailed, outcome.State)
	assert.Equal(t, draftrank.IssueMissingRules, outcome.Code)
}

func TestRefreshDraftRanking_InternalFailureIsRetried(t *testing.T) {
	pool := openTestDB(t)
	pool.Close()
	activities := &Activities{Pool: pool, Now: func() time.Time { return testNow }}
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(activities.RefreshDraftRanking)
	_, err := env.ExecuteActivity(activities.RefreshDraftRanking, RefreshInput{RunID: testRunID, Season: testSeason, LeagueID: testLeagueID})
	assert.Error(t, err, "a database failure is returned so Temporal retries it")
}

func TestCancelDraftRankingRefreshes(t *testing.T) {
	pool := openTestDB(t)
	store := draftrank.NewPGStore(pool)
	_, err := store.StartRefresh(context.Background(), testRunID, testSeason, testLeagueID, testNow)
	require.NoError(t, err)
	activities := &Activities{Pool: pool, Now: func() time.Time { return testNow.Add(time.Minute) }}
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(activities.CancelDraftRankingRefreshes)

	encoded, err := env.ExecuteActivity(activities.CancelDraftRankingRefreshes, CancelInput{RunID: testRunID})
	require.NoError(t, err)
	var canceled int64
	require.NoError(t, encoded.Get(&canceled))
	assert.EqualValues(t, 1, canceled)
	latest, err := store.LatestRefresh(context.Background(), testSeason, testLeagueID)
	require.NoError(t, err)
	assert.Equal(t, draftrank.RefreshCanceled, latest.State)
}
