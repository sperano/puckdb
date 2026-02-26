package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/sperano/puckdb/store"
	"github.com/stretchr/testify/assert"
)

// mockDownloader returns a Downloader that returns the given content and error.
func mockDownloader(content []byte, err error) Downloader {
	return func(url string) ([]byte, error) {
		return content, err
	}
}

// Tests for doDownloadImpl

func TestDoDownloadImpl_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	mem := store.NewMemStorage()
	path := "test/file.xml"

	err := doDownloadImpl(ctx, mem, path, "http://example.com", mockDownloader(nil, nil), "TestFile")

	assert.ErrorIs(t, err, context.Canceled)
}

func TestDoDownloadImpl_FileAlreadyExists(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemStorage()
	path := "test/file.xml"

	// Pre-populate the file
	mem.SetFile(path, []byte("existing"))

	err := doDownloadImpl(ctx, mem, path, "http://example.com", mockDownloader(nil, nil), "TestFile")

	assert.NoError(t, err)
}

func TestDoDownloadImpl_DownloadError(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemStorage()
	path := "test/file.xml"
	downloadErr := errors.New("network error")

	err := doDownloadImpl(ctx, mem, path, "http://example.com/data", mockDownloader(nil, downloadErr), "TestFile")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "http://example.com/data")
	assert.Contains(t, err.Error(), "network error")
}

func TestDoDownloadImpl_Success(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemStorage()
	path := "test/file.xml"

	err := doDownloadImpl(ctx, mem, path, "http://example.com", mockDownloader([]byte("downloaded content"), nil), "TestFile")

	assert.NoError(t, err)
	assert.True(t, mem.Has(path))
	content := mem.Get(path)
	assert.Equal(t, []byte("downloaded content"), content)
}
