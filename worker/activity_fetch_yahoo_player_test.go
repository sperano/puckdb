package worker

import (
	"context"
	"errors"
	"testing"

	puckhttp "github.com/sperano/puckdb/http"
	"github.com/sperano/puckdb/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestFetchYahooPlayer_AlreadyMissing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	downloader := &MockHTTPDownloader{}

	playerID := store.YahooPlayerID(12345)
	missingFile := store.MissingYahooPlayerFile{PlayerID: playerID}

	// Missing file exists
	fs.On("Exists", missingFile).Return(true)

	status, err := fetchYahooPlayerImpl(ctx, fs, downloader, playerID)

	require.NoError(t, err)
	assert.Equal(t, fetchStatusMissing, status)
	fs.AssertExpectations(t)
	downloader.AssertNotCalled(t, "Download")
}

func TestFetchYahooPlayer_AlreadyCached(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	downloader := &MockHTTPDownloader{}

	playerID := store.YahooPlayerID(12345)
	missingFile := store.MissingYahooPlayerFile{PlayerID: playerID}
	playerFile := store.YahooPlayerFile{PlayerID: playerID}

	// Missing file doesn't exist, but player file does
	fs.On("Exists", missingFile).Return(false)
	fs.On("Exists", playerFile).Return(true)

	status, err := fetchYahooPlayerImpl(ctx, fs, downloader, playerID)

	require.NoError(t, err)
	assert.Equal(t, fetchStatusCached, status)
	fs.AssertExpectations(t)
	downloader.AssertNotCalled(t, "Download")
}

func TestFetchYahooPlayer_DownloadSuccess(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	downloader := &MockHTTPDownloader{}

	playerID := store.YahooPlayerID(12345)
	missingFile := store.MissingYahooPlayerFile{PlayerID: playerID}
	playerFile := store.YahooPlayerFile{PlayerID: playerID}

	// Neither file exists
	fs.On("Exists", missingFile).Return(false)
	fs.On("Exists", playerFile).Return(false)

	// Directories created successfully
	fs.On("MkdirAll", playerFile.Dir(), mock.Anything).Return(nil)
	fs.On("MkdirAll", missingFile.Dir(), mock.Anything).Return(nil)

	// Download succeeds
	content := []byte("<html>Player Page</html>")
	downloader.On("Download", puckhttp.YahooPlayerURL(int(playerID))).Return(content, nil)

	// Write succeeds
	fs.On("Write", playerFile, content).Return(nil)

	status, err := fetchYahooPlayerImpl(ctx, fs, downloader, playerID)

	require.NoError(t, err)
	assert.Equal(t, fetchStatusDownloaded, status)
	fs.AssertExpectations(t)
	downloader.AssertExpectations(t)
}

func TestFetchYahooPlayer_Download404(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	downloader := &MockHTTPDownloader{}

	playerID := store.YahooPlayerID(99999)
	missingFile := store.MissingYahooPlayerFile{PlayerID: playerID}
	playerFile := store.YahooPlayerFile{PlayerID: playerID}

	// Neither file exists
	fs.On("Exists", missingFile).Return(false)
	fs.On("Exists", playerFile).Return(false)

	// Directories created successfully
	fs.On("MkdirAll", playerFile.Dir(), mock.Anything).Return(nil)
	fs.On("MkdirAll", missingFile.Dir(), mock.Anything).Return(nil)

	// Download returns 404
	httpErr := &puckhttp.HTTPError{StatusCode: 404, Status: "404 Not Found"}
	downloader.On("Download", puckhttp.YahooPlayerURL(int(playerID))).Return(nil, httpErr)

	// Write missing file
	fs.On("Write", missingFile, []byte("404 Not Found")).Return(nil)

	status, err := fetchYahooPlayerImpl(ctx, fs, downloader, playerID)

	require.NoError(t, err)
	assert.Equal(t, fetchStatusMissing, status)
	fs.AssertExpectations(t)
	downloader.AssertExpectations(t)
}

func TestFetchYahooPlayer_DownloadOtherError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	downloader := &MockHTTPDownloader{}

	playerID := store.YahooPlayerID(12345)
	missingFile := store.MissingYahooPlayerFile{PlayerID: playerID}
	playerFile := store.YahooPlayerFile{PlayerID: playerID}

	// Neither file exists
	fs.On("Exists", missingFile).Return(false)
	fs.On("Exists", playerFile).Return(false)

	// Directories created successfully
	fs.On("MkdirAll", playerFile.Dir(), mock.Anything).Return(nil)
	fs.On("MkdirAll", missingFile.Dir(), mock.Anything).Return(nil)

	// Download returns 500 error
	httpErr := &puckhttp.HTTPError{StatusCode: 500, Status: "500 Internal Server Error"}
	downloader.On("Download", puckhttp.YahooPlayerURL(int(playerID))).Return(nil, httpErr)

	status, err := fetchYahooPlayerImpl(ctx, fs, downloader, playerID)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "download player")
	assert.Equal(t, fetchStatus(0), status)
	fs.AssertExpectations(t)
	downloader.AssertExpectations(t)
}

func TestFetchYahooPlayer_MkdirError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	downloader := &MockHTTPDownloader{}

	playerID := store.YahooPlayerID(12345)
	missingFile := store.MissingYahooPlayerFile{PlayerID: playerID}
	playerFile := store.YahooPlayerFile{PlayerID: playerID}

	// Neither file exists
	fs.On("Exists", missingFile).Return(false)
	fs.On("Exists", playerFile).Return(false)

	// First MkdirAll fails
	fs.On("MkdirAll", playerFile.Dir(), mock.Anything).Return(errors.New("permission denied"))

	status, err := fetchYahooPlayerImpl(ctx, fs, downloader, playerID)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "mkdir player dir")
	assert.Equal(t, fetchStatus(0), status)
	fs.AssertExpectations(t)
	downloader.AssertNotCalled(t, "Download")
}

func TestFetchYahooPlayer_WriteError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	downloader := &MockHTTPDownloader{}

	playerID := store.YahooPlayerID(12345)
	missingFile := store.MissingYahooPlayerFile{PlayerID: playerID}
	playerFile := store.YahooPlayerFile{PlayerID: playerID}

	// Neither file exists
	fs.On("Exists", missingFile).Return(false)
	fs.On("Exists", playerFile).Return(false)

	// Directories created successfully
	fs.On("MkdirAll", playerFile.Dir(), mock.Anything).Return(nil)
	fs.On("MkdirAll", missingFile.Dir(), mock.Anything).Return(nil)

	// Download succeeds
	content := []byte("<html>Player Page</html>")
	downloader.On("Download", puckhttp.YahooPlayerURL(int(playerID))).Return(content, nil)

	// Write fails
	fs.On("Write", playerFile, content).Return(errors.New("disk full"))

	status, err := fetchYahooPlayerImpl(ctx, fs, downloader, playerID)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "save player")
	assert.Equal(t, fetchStatus(0), status)
	fs.AssertExpectations(t)
	downloader.AssertExpectations(t)
}

func TestFetchYahooPlayer_ContextCancelled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	fs := NewMockFileSystem()
	downloader := &MockHTTPDownloader{}

	playerID := store.YahooPlayerID(12345)

	status, err := fetchYahooPlayerImpl(ctx, fs, downloader, playerID)

	require.Error(t, err)
	assert.Equal(t, context.Canceled, err)
	assert.Equal(t, fetchStatus(0), status)
	fs.AssertNotCalled(t, "Exists")
	downloader.AssertNotCalled(t, "Download")
}

func TestFetchYahooPlayer_Write404MissingFileError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	downloader := &MockHTTPDownloader{}

	playerID := store.YahooPlayerID(99999)
	missingFile := store.MissingYahooPlayerFile{PlayerID: playerID}
	playerFile := store.YahooPlayerFile{PlayerID: playerID}

	// Neither file exists
	fs.On("Exists", missingFile).Return(false)
	fs.On("Exists", playerFile).Return(false)

	// Directories created successfully
	fs.On("MkdirAll", playerFile.Dir(), mock.Anything).Return(nil)
	fs.On("MkdirAll", missingFile.Dir(), mock.Anything).Return(nil)

	// Download returns 404
	httpErr := &puckhttp.HTTPError{StatusCode: 404, Status: "404 Not Found"}
	downloader.On("Download", puckhttp.YahooPlayerURL(int(playerID))).Return(nil, httpErr)

	// Write missing file fails
	fs.On("Write", missingFile, []byte("404 Not Found")).Return(errors.New("disk full"))

	status, err := fetchYahooPlayerImpl(ctx, fs, downloader, playerID)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "save missing player")
	assert.Equal(t, fetchStatus(0), status)
	fs.AssertExpectations(t)
}
