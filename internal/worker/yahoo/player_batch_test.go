package yahoo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/httpx"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

const testMissingTTL = 30 * 24 * time.Hour

var errNotFound = &httpx.HTTPError{StatusCode: 404, Status: "404 Not Found"}

func playerURL(id store.YahooPlayerID) string { return resource.YahooPlayer{PlayerID: id}.URL() }

func hasMarker(mem *store.MemStorage, id store.YahooPlayerID) bool {
	return mem.Exists(context.Background(), resource.MissingYahooPlayer{PlayerID: id}.Path())
}

// runBatch executes FetchYahooPlayerBatch through a Temporal activity
// environment (the activity heartbeats).
func runBatch(t *testing.T, mem *store.MemStorage, d *MockHTTPDownloader, start, end store.YahooPlayerID) (shared.FetchStats, error) {
	t.Helper()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	act := &FetchActivities{Storage: mem, PublicDownloader: d.Downloader()}
	env.RegisterActivity(act.FetchYahooPlayerBatch)
	val, err := env.ExecuteActivity(act.FetchYahooPlayerBatch, start, end, testMissingTTL)
	var stats shared.FetchStats
	if err == nil {
		require.NoError(t, val.Get(&stats))
	}
	return stats, err
}

// A throttled Yahoo 404s everything, canary included: no marker may be
// written and the batch must fail so Temporal retries it.
func TestFetchYahooPlayerBatch_ThrottleStormWritesNoMarkers(t *testing.T) {
	t.Parallel()
	mem := store.NewMemStorage()
	d := &MockHTTPDownloader{}
	d.On("Download", mock.Anything, mock.Anything).Return(nil, errNotFound)

	_, err := runBatch(t, mem, d, 1000, 1099)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "throttling")
	for id := store.YahooPlayerID(1000); id <= 1099; id++ {
		assert.False(t, hasMarker(mem, id), "marker written for %d", id)
	}
	// It aborts at the first verification instead of burning the whole batch.
	d.AssertNumberOfCalls(t, "Download", notFoundVerifyThreshold+1)
}

// The empty tail (ids above the highest real player) legitimately all 404
// while the canary still answers 200: markers are written, no error.
func TestFetchYahooPlayerBatch_EmptyTailWritesMarkers(t *testing.T) {
	t.Parallel()
	mem := store.NewMemStorage()
	d := &MockHTTPDownloader{}
	d.On("Download", mock.Anything, playerURL(yahooCanaryPlayerID)).Return([]byte("<html/>"), nil)
	d.On("Download", mock.Anything, mock.Anything).Return(nil, errNotFound)

	const first, last = store.YahooPlayerID(34000), store.YahooPlayerID(34099)
	stats, err := runBatch(t, mem, d, first, last)

	require.NoError(t, err)
	assert.Equal(t, 100, stats.Missing)
	for id := first; id <= last; id++ {
		assert.True(t, hasMarker(mem, id), "no marker for %d", id)
	}
}

// A short run of 404s at the end of a batch (below the verify threshold) is
// verified by the final flush.
func TestFetchYahooPlayerBatch_TrailingNotFoundsAreVerified(t *testing.T) {
	t.Parallel()
	mem := store.NewMemStorage()
	d := &MockHTTPDownloader{}
	d.On("Download", mock.Anything, playerURL(yahooCanaryPlayerID)).Return(nil, errNotFound)
	d.On("Download", mock.Anything, mock.Anything).Return(nil, errNotFound)

	_, err := runBatch(t, mem, d, 50, 52)

	require.Error(t, err)
	assert.False(t, hasMarker(mem, 50))
}

func TestFetchYahooPlayerBatch_CanaryTransportErrorIsReturned(t *testing.T) {
	t.Parallel()
	mem := store.NewMemStorage()
	d := &MockHTTPDownloader{}
	d.On("Download", mock.Anything, playerURL(yahooCanaryPlayerID)).Return(nil, errors.New("connection reset"))
	d.On("Download", mock.Anything, mock.Anything).Return(nil, errNotFound)

	_, err := runBatch(t, mem, d, 50, 52)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "canary")
	assert.False(t, hasMarker(mem, 50))
}

// An expired marker is re-fetched and, on a real 404 (canary healthy), re-marked.
func TestFetchYahooPlayerBatch_ExpiredMarkerRemarkedOn404(t *testing.T) {
	t.Parallel()
	mem := store.NewMemStorage()
	d := &MockHTTPDownloader{}
	d.On("Download", mock.Anything, playerURL(yahooCanaryPlayerID)).Return([]byte("<html/>"), nil)
	d.On("Download", mock.Anything, playerURL(77)).Return(nil, errNotFound)
	old := time.Now().Add(-2 * testMissingTTL)
	mem.SetFileWithTime(resource.MissingYahooPlayer{PlayerID: 77}.Path(), []byte("missing"), old)

	_, err := runBatch(t, mem, d, 77, 77)

	require.NoError(t, err)
	info, err := resource.Stat(context.Background(), mem, resource.MissingYahooPlayer{PlayerID: 77})
	require.NoError(t, err)
	assert.True(t, info.ModTime().After(old), "marker was not refreshed")
}
