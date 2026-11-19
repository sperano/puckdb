package yahoo

import (
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

// legacyScanMissingTTL is the TTL a legacy scan history passes.
const legacyScanMissingTTL = 30 * 24 * time.Hour

// TestFetchYahooPlayerBatch_RetiredScanDownloadsNothing checks that a batch a
// legacy scan history schedules neither downloads nor touches storage.
func TestFetchYahooPlayerBatch_RetiredScanDownloadsNothing(t *testing.T) {
	mem := store.NewMemStorage()
	dl, calls := countingDownloader(nil, assert.AnError)
	act := &FetchActivities{Storage: mem, PublicDownloader: dl}
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(act.FetchYahooPlayerBatch)

	val, err := env.ExecuteActivity(act.FetchYahooPlayerBatch, store.YahooPlayerID(1), store.YahooPlayerID(100), legacyScanMissingTTL)

	require.NoError(t, err)
	var stats shared.FetchStats
	require.NoError(t, val.Get(&stats))
	assert.Equal(t, shared.FetchStats{}, stats)
	assert.Zero(t, *calls, "no player page is requested")
	assert.Zero(t, mem.Count(), "no page or missing marker is written")
}
