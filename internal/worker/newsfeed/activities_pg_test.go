package newsfeed

// PostgreSQL-backed tests of PlanNewsRefresh, FetchNewsSource and
// RecordNewsFetchFailure. See activities_pg_helpers_test.go for fixtures and
// activities_pg_process_test.go for ProcessNewsVersions/PruneNews.

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/news"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

func executePlan(t *testing.T, env *testsuite.TestActivityEnvironment, acts *Activities, src news.Source, force bool) Plan {
	t.Helper()
	val, err := env.ExecuteActivity(acts.PlanNewsRefresh, PlanInput{Sources: []news.Source{src}, Season: pgYahooSeason, Force: force})
	require.NoError(t, err)
	var plan Plan
	require.NoError(t, val.Get(&plan))
	return plan
}

func TestPGPlanNewsRefresh(t *testing.T) {
	pool := openNewsPGTestDB(t)
	acts, env := newNewsActivities(pool, http.DefaultClient, pgFixedNow)
	src := defaultNewsSource(t, "rotowire-nhl", "http://example.invalid/feed")

	plan := executePlan(t, env, acts, src, false)
	assert.Equal(t, []news.Source{src}, plan.Due, "a source never fetched is due")
	assert.Empty(t, plan.NotDue)

	require.NoError(t, acts.Queries.RecordNewsFetchSuccess(context.Background(), sqlcdb.RecordNewsFetchSuccessParams{
		SourceID: src.ID, Scope: news.ScopeFeed, Publisher: src.Publisher, LastAttemptAt: news.Timestamptz(pgFixedNow),
	}))

	plan = executePlan(t, env, acts, src, false)
	assert.Empty(t, plan.Due, "a source fetched inside its refresh interval is not due")
	require.Len(t, plan.NotDue, 1)
	assert.Equal(t, src.ID, plan.NotDue[0].SourceID)

	plan = executePlan(t, env, acts, src, true)
	assert.Equal(t, []news.Source{src}, plan.Due, "Force overrides the refresh interval")
}

func TestPGFetchNewsSourceRSS(t *testing.T) {
	pool := openNewsPGTestDB(t)
	seedNewsDirectory(t, pool)
	fake := newRSSFakeServer(t, readNewsTestdata(t, "rotowire.xml"))
	acts, env := newNewsActivities(pool, fake.srv.Client(), pgFixedNow)
	src := defaultNewsSource(t, "rotowire-nhl", fake.url())

	first := fetchSource(t, env, acts, src)
	assert.False(t, first.NotModified)
	assert.Equal(t, news.IngestResult{Items: 5, NewVersions: 5}, first.Stored)

	second := fetchSource(t, env, acts, src)
	assert.True(t, second.NotModified, "an unchanged body is recognized by its hash alone, with no ETag involved")
	assert.Zero(t, second.Stored)

	fake.setETag(`"v1"`)
	third := fetchSource(t, env, acts, src)
	assert.True(t, third.NotModified, "the body is still unchanged even though the source now advertises an ETag")

	fake.setNotModified(true)
	fourth := fetchSource(t, env, acts, src)
	assert.True(t, fourth.NotModified)
	assert.Equal(t, `"v1"`, fake.requestedIfNoneMatch(), "the ETag recorded from the previous fetch is sent back as If-None-Match")

	fake.setNotModified(false)
	fake.setBody(bytes.Replace(readNewsTestdata(t, "rotowire.xml"),
		[]byte("Lyon sustained an upper-body injury"), []byte("Lyon sustained a concerning upper-body injury"), 1))
	fifth := fetchSource(t, env, acts, src)
	assert.False(t, fifth.NotModified)
	assert.Equal(t, news.IngestResult{Items: 5, NewVersions: 1, Unchanged: 4}, fifth.Stored,
		"only the changed item's article gets a new version")
}

func TestPGFetchNewsSourceYahooStatus(t *testing.T) {
	pool := openNewsPGTestDB(t)
	seedNewsDirectory(t, pool)
	older, newer := pgFixedNow.Add(-2*time.Hour), pgFixedNow.Add(-1*time.Hour)
	seedYahooPool(t, pool, older, newer)
	acts, env := newNewsActivities(pool, http.DefaultClient, pgFixedNow)
	src := defaultNewsSource(t, "yahoo-status", "")

	result := fetchSource(t, env, acts, src)
	assert.False(t, result.NotModified)
	assert.Equal(t, news.IngestResult{Items: 1, NewVersions: 1}, result.Stored,
		"one status item per Yahoo player, deduplicated across the two leagues")

	scope := fmt.Sprintf("season:%d", pgYahooSeason)
	var dataAsOf time.Time
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT data_as_of FROM news_fetch_state WHERE source_id=$1 AND scope=$2`, src.ID, scope).Scan(&dataAsOf))
	assert.True(t, dataAsOf.Equal(older), "data_as_of is the OLDER league's latest pool fetch, not the newer one")

	_, err := env.ExecuteActivity(acts.FetchNewsSource, FetchInput{Source: src, Season: pgOtherYahooSeason})
	require.Error(t, err, "a season with no imported Yahoo pools has no status to read")
	assertApplicationErrorType(t, err, ErrTypeNoCoverage, true)
}

func TestPGRecordNewsFetchFailure(t *testing.T) {
	pool := openNewsPGTestDB(t)
	good := staticServer(t, http.StatusOK, readNewsTestdata(t, "sportsnet.atom"))
	acts, env := newNewsActivities(pool, http.DefaultClient, pgFixedNow)
	src := news.Source{ID: "pg-failing-source", Publisher: "Sportsnet", Kind: news.KindReporting, Adapter: news.AdapterRSS,
		URL: good.URL, RefreshMinutes: 60, Enabled: true}

	first := fetchSource(t, env, acts, src)
	require.False(t, first.NotModified)
	articlesBefore := countRows(t, pool, "news_articles")
	require.Positive(t, articlesBefore)
	before := readFetchState(t, pool, src.ID)

	bad := staticServer(t, http.StatusInternalServerError, nil)
	src.URL = bad.URL
	_, fetchErr := env.ExecuteActivity(acts.FetchNewsSource, FetchInput{Source: src, Season: pgYahooSeason})
	require.Error(t, fetchErr)
	assertApplicationErrorType(t, fetchErr, ErrTypeHTTP, false)

	for _, wantFailures := range []int32{1, 2} {
		_, err := env.ExecuteActivity(acts.RecordNewsFetchFailure,
			FailureInput{Source: src, Season: pgYahooSeason, Error: fetchErr.Error()})
		require.NoError(t, err)
		after := readFetchState(t, pool, src.ID)
		assert.Equal(t, wantFailures, after.consecutiveFailures)
		assert.Equal(t, before.etag, after.etag, "a failed fetch leaves the validators untouched")
		assert.Equal(t, before.bodyHash, after.bodyHash)
		assert.True(t, after.lastSuccessAt.Equal(before.lastSuccessAt), "last_success_at keeps showing the last good fetch")
		assert.Contains(t, after.lastError, "HTTP 500")
	}
	assert.Equal(t, articlesBefore, countRows(t, pool, "news_articles"), "previously stored articles are untouched by a failed fetch")
}
