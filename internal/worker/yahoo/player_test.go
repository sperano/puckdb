package yahoo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/httpx"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestFetchYahooPlayer_AlreadyMissing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	downloader := &MockHTTPDownloader{}

	playerID := store.YahooPlayerID(12345)

	// Pre-mark as missing
	missingRes := resource.MissingYahooPlayer{PlayerID: playerID}
	require.NoError(t, mem.Write(context.Background(), missingRes.Path(), []byte("missing")))

	status, err := fetchYahooPlayerImpl(ctx, mem, downloader.Downloader(), playerID, testMissingTTL, time.Now())

	require.NoError(t, err)
	assert.Equal(t, fetchStatusMissing, status)
	downloader.AssertNotCalled(t, "Download")
}

func TestFetchYahooPlayer_AlreadyCached(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	downloader := &MockHTTPDownloader{}

	playerID := store.YahooPlayerID(12345)

	// Pre-populate player file
	playerRes := resource.YahooPlayer{PlayerID: playerID}
	require.NoError(t, mem.Write(context.Background(), playerRes.Path(), []byte("<html>Player Page</html>")))

	status, err := fetchYahooPlayerImpl(ctx, mem, downloader.Downloader(), playerID, testMissingTTL, time.Now())

	require.NoError(t, err)
	assert.Equal(t, fetchStatusCached, status)
	downloader.AssertNotCalled(t, "Download")
}

func TestFetchYahooPlayer_DownloadSuccess(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	downloader := &MockHTTPDownloader{}

	playerID := store.YahooPlayerID(12345)

	// Download succeeds
	content := []byte("<html>Player Page</html>")
	downloader.On("Download", mock.Anything, resource.YahooPlayer{PlayerID: playerID}.URL()).Return(content, nil)

	status, err := fetchYahooPlayerImpl(ctx, mem, downloader.Downloader(), playerID, testMissingTTL, time.Now())

	require.NoError(t, err)
	assert.Equal(t, fetchStatusDownloaded, status)
	downloader.AssertExpectations(t)

	// Verify player was saved
	playerRes := resource.YahooPlayer{PlayerID: playerID}
	assert.True(t, mem.Exists(context.Background(), playerRes.Path()))
}

func TestFetchYahooPlayer_Download404(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	downloader := &MockHTTPDownloader{}

	playerID := store.YahooPlayerID(99999)

	// Download returns 404
	httpErr := &httpx.HTTPError{StatusCode: 404, Status: "404 Not Found"}
	downloader.On("Download", mock.Anything, resource.YahooPlayer{PlayerID: playerID}.URL()).Return(nil, httpErr)

	status, err := fetchYahooPlayerImpl(ctx, mem, downloader.Downloader(), playerID, testMissingTTL, time.Now())

	require.NoError(t, err)
	assert.Equal(t, fetchStatusNotFound, status)
	downloader.AssertExpectations(t)

	// The marker is the batch's to write, once it has ruled out throttling.
	missingRes := resource.MissingYahooPlayer{PlayerID: playerID}
	assert.False(t, mem.Exists(context.Background(), missingRes.Path()))
}

func TestFetchYahooPlayer_DownloadOtherError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	downloader := &MockHTTPDownloader{}

	playerID := store.YahooPlayerID(12345)

	// Download returns 500 error
	httpErr := &httpx.HTTPError{StatusCode: 500, Status: "500 Internal Server Error"}
	downloader.On("Download", mock.Anything, resource.YahooPlayer{PlayerID: playerID}.URL()).Return(nil, httpErr)

	status, err := fetchYahooPlayerImpl(ctx, mem, downloader.Downloader(), playerID, testMissingTTL, time.Now())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "download player")
	assert.Equal(t, fetchStatus(0), status)
	downloader.AssertExpectations(t)
}

func TestFetchYahooPlayer_DownloadNetworkError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mem := store.NewMemStorage()
	downloader := &MockHTTPDownloader{}

	playerID := store.YahooPlayerID(12345)
	// Download returns network error
	wantErr := errors.New("network timeout")
	downloader.On("Download", mock.Anything, resource.YahooPlayer{PlayerID: playerID}.URL()).Return(nil, wantErr)

	status, err := fetchYahooPlayerImpl(ctx, mem, downloader.Downloader(), playerID, testMissingTTL, time.Now())

	require.Error(t, err)
	require.ErrorIs(t, err, wantErr)
	assert.Contains(t, err.Error(), "download player")
	assert.Equal(t, fetchStatus(0), status)
	downloader.AssertExpectations(t)
}

func TestFetchYahooPlayer_ContextCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	mem := store.NewMemStorage()
	downloader := &MockHTTPDownloader{}

	playerID := store.YahooPlayerID(12345)
	status, err := fetchYahooPlayerImpl(ctx, mem, downloader.Downloader(), playerID, testMissingTTL, time.Now())

	require.Error(t, err)
	assert.Equal(t, context.Canceled, err)
	assert.Equal(t, fetchStatus(0), status)
	downloader.AssertNotCalled(t, "Download")
}

func TestFetchYahooPlayer_ExpiredMarkerIsRefetched(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mem := store.NewMemStorage()
	downloader := &MockHTTPDownloader{}
	playerID := store.YahooPlayerID(6743)

	missingRes := resource.MissingYahooPlayer{PlayerID: playerID}
	mem.SetFileWithTime(missingRes.Path(), []byte("missing"), time.Now().Add(-testMissingTTL-time.Hour))
	content := []byte("<html>McDavid</html>")
	downloader.On("Download", mock.Anything, resource.YahooPlayer{PlayerID: playerID}.URL()).Return(content, nil)

	status, err := fetchYahooPlayerImpl(ctx, mem, downloader.Downloader(), playerID, testMissingTTL, time.Now())

	require.NoError(t, err)
	assert.Equal(t, fetchStatusDownloaded, status)
	downloader.AssertExpectations(t)
}

func TestFetchYahooPlayer_FreshMarkerIsTrusted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mem := store.NewMemStorage()
	downloader := &MockHTTPDownloader{}
	playerID := store.YahooPlayerID(12345)

	missingRes := resource.MissingYahooPlayer{PlayerID: playerID}
	mem.SetFileWithTime(missingRes.Path(), []byte("missing"), time.Now().Add(-testMissingTTL+time.Hour))

	status, err := fetchYahooPlayerImpl(ctx, mem, downloader.Downloader(), playerID, testMissingTTL, time.Now())

	require.NoError(t, err)
	assert.Equal(t, fetchStatusMissing, status)
	downloader.AssertNotCalled(t, "Download")
}

// A non-positive TTL fails safe: the marker is ignored and the page refetched,
// never trusted forever (the failure mode of the throttled 404 markers).
func TestFetchYahooPlayer_ZeroTTLIgnoresMarker(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mem := store.NewMemStorage()
	downloader := &MockHTTPDownloader{}
	playerID := store.YahooPlayerID(12345)

	missingRes := resource.MissingYahooPlayer{PlayerID: playerID}
	// A marker written a minute ago: fresh under any positive TTL.
	mem.SetFileWithTime(missingRes.Path(), []byte("missing"), time.Now().Add(-time.Minute))
	downloader.On("Download", mock.Anything, resource.YahooPlayer{PlayerID: playerID}.URL()).Return([]byte("<html>Player Page</html>"), nil)

	status, err := fetchYahooPlayerImpl(ctx, mem, downloader.Downloader(), playerID, 0, time.Now())

	require.NoError(t, err)
	assert.Equal(t, fetchStatusDownloaded, status)
	downloader.AssertExpectations(t)
}
