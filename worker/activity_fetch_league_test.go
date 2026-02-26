package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/sperano/puckdb/store"
	"github.com/stretchr/testify/assert"
)

// Tests for fetchLeagueImpl

func TestFetchLeagueImpl_FileExists(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemStorage()
	repos := store.NewRepos(mem)

	// Pre-populate the file
	path := store.LeaguePath(2023, 12345)
	mem.SetFile(path, []byte("existing data"))

	err := fetchLeagueImpl(ctx, repos, 2023, 423, 12345, mockDownloader(nil, nil))

	assert.NoError(t, err)
	// Downloader should not have been called (file exists)
}

func TestFetchLeagueImpl_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	mem := store.NewMemStorage()
	repos := store.NewRepos(mem)

	err := fetchLeagueImpl(ctx, repos, 2023, 423, 12345, mockDownloader(nil, nil))

	assert.ErrorIs(t, err, context.Canceled)
}

func TestFetchLeagueImpl_DownloadAndSave(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemStorage()
	repos := store.NewRepos(mem)

	content := []byte("<league>data</league>")

	err := fetchLeagueImpl(ctx, repos, 2023, 423, 12345, mockDownloader(content, nil))

	assert.NoError(t, err)

	// Verify file was saved
	path := store.LeaguePath(2023, 12345)
	assert.True(t, mem.Has(path))
	saved := mem.Get(path)
	assert.Equal(t, content, saved)
}

func TestFetchLeagueImpl_DownloadError(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemStorage()
	repos := store.NewRepos(mem)

	expectedErr := errors.New("network error")

	err := fetchLeagueImpl(ctx, repos, 2023, 423, 12345, mockDownloader(nil, expectedErr))

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "network error")
}
