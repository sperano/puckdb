package yahoo

import (
	"context"
	"errors"
	"testing"

	puckhttp "github.com/sperano/puckdb/http"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/store"
	"github.com/stretchr/testify/assert"
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
	require.NoError(t, mem.Write(missingRes.Path(), []byte("missing")))

	status, err := fetchYahooPlayerImpl(ctx, mem, downloader, playerID)

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
	require.NoError(t, mem.Write(playerRes.Path(), []byte("<html>Player Page</html>")))

	status, err := fetchYahooPlayerImpl(ctx, mem, downloader, playerID)

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
	downloader.On("Download", resource.YahooPlayer{PlayerID: playerID}.URL()).Return(content, nil)

	status, err := fetchYahooPlayerImpl(ctx, mem, downloader, playerID)

	require.NoError(t, err)
	assert.Equal(t, fetchStatusDownloaded, status)
	downloader.AssertExpectations(t)

	// Verify player was saved
	playerRes := resource.YahooPlayer{PlayerID: playerID}
	assert.True(t, mem.Exists(playerRes.Path()))
}

func TestFetchYahooPlayer_Download404(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	downloader := &MockHTTPDownloader{}

	playerID := store.YahooPlayerID(99999)

	// Download returns 404
	httpErr := &puckhttp.HTTPError{StatusCode: 404, Status: "404 Not Found"}
	downloader.On("Download", resource.YahooPlayer{PlayerID: playerID}.URL()).Return(nil, httpErr)

	status, err := fetchYahooPlayerImpl(ctx, mem, downloader, playerID)

	require.NoError(t, err)
	assert.Equal(t, fetchStatusMissing, status)
	downloader.AssertExpectations(t)

	// Verify player was marked as missing
	missingRes := resource.MissingYahooPlayer{PlayerID: playerID}
	assert.True(t, mem.Exists(missingRes.Path()))
}

func TestFetchYahooPlayer_DownloadOtherError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	downloader := &MockHTTPDownloader{}

	playerID := store.YahooPlayerID(12345)

	// Download returns 500 error
	httpErr := &puckhttp.HTTPError{StatusCode: 500, Status: "500 Internal Server Error"}
	downloader.On("Download", resource.YahooPlayer{PlayerID: playerID}.URL()).Return(nil, httpErr)

	status, err := fetchYahooPlayerImpl(ctx, mem, downloader, playerID)

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
	downloader.On("Download", resource.YahooPlayer{PlayerID: playerID}.URL()).Return(nil, errors.New("network timeout"))

	status, err := fetchYahooPlayerImpl(ctx, mem, downloader, playerID)

	require.Error(t, err)
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
	status, err := fetchYahooPlayerImpl(ctx, mem, downloader, playerID)

	require.Error(t, err)
	assert.Equal(t, context.Canceled, err)
	assert.Equal(t, fetchStatus(0), status)
	downloader.AssertNotCalled(t, "Download")
}
