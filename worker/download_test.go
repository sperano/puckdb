package worker

import (
	"context"
	"errors"
	"os"
	"testing"

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

	mockFS := NewMockFileSystem()
	mockFile := MockFile{DirVal: "test", NameVal: "file", ExtVal: "xml"}

	err := doDownloadImpl(ctx, mockFS, mockFile, "http://example.com", mockDownloader(nil, nil))

	assert.ErrorIs(t, err, context.Canceled)
}

func TestDoDownloadImpl_FileAlreadyExists(t *testing.T) {
	ctx := context.Background()
	mockFS := NewMockFileSystem()
	mockFile := MockFile{DirVal: "test", NameVal: "file", ExtVal: "xml"}

	mockFS.On("MkdirAll", "test", os.FileMode(0755)).Return(nil)
	mockFS.On("Exists", mockFile).Return(true)

	err := doDownloadImpl(ctx, mockFS, mockFile, "http://example.com", mockDownloader(nil, nil))

	assert.NoError(t, err)
	mockFS.AssertExpectations(t)
}

func TestDoDownloadImpl_MkdirAllError(t *testing.T) {
	ctx := context.Background()
	mockFS := NewMockFileSystem()
	mockFile := MockFile{DirVal: "test", NameVal: "file", ExtVal: "xml"}
	expectedErr := errors.New("mkdir failed")

	mockFS.On("MkdirAll", "test", os.FileMode(0755)).Return(expectedErr)

	err := doDownloadImpl(ctx, mockFS, mockFile, "http://example.com", mockDownloader(nil, nil))

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "test")
	mockFS.AssertExpectations(t)
}

func TestDoDownloadImpl_DownloadError(t *testing.T) {
	ctx := context.Background()
	mockFS := NewMockFileSystem()
	mockFile := MockFile{DirVal: "test", NameVal: "file", ExtVal: "xml"}
	downloadErr := errors.New("network error")

	mockFS.On("MkdirAll", "test", os.FileMode(0755)).Return(nil)
	mockFS.On("Exists", mockFile).Return(false)

	err := doDownloadImpl(ctx, mockFS, mockFile, "http://example.com/data", mockDownloader(nil, downloadErr))

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "http://example.com/data")
	assert.Contains(t, err.Error(), "network error")
	mockFS.AssertExpectations(t)
}

func TestDoDownloadImpl_WriteError(t *testing.T) {
	ctx := context.Background()
	mockFS := NewMockFileSystem()
	mockFile := MockFile{DirVal: "test", NameVal: "file", ExtVal: "xml"}
	writeErr := errors.New("disk full")

	mockFS.On("MkdirAll", "test", os.FileMode(0755)).Return(nil)
	mockFS.On("Exists", mockFile).Return(false)
	mockFS.On("Write", mockFile, []byte("content")).Return(writeErr)

	err := doDownloadImpl(ctx, mockFS, mockFile, "http://example.com", mockDownloader([]byte("content"), nil))

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "disk full")
	mockFS.AssertExpectations(t)
}

func TestDoDownloadImpl_Success(t *testing.T) {
	ctx := context.Background()
	mockFS := NewMockFileSystem()
	mockFile := MockFile{DirVal: "test", NameVal: "file", ExtVal: "xml"}

	mockFS.On("MkdirAll", "test", os.FileMode(0755)).Return(nil)
	mockFS.On("Exists", mockFile).Return(false)
	mockFS.On("Write", mockFile, []byte("downloaded content")).Return(nil)

	err := doDownloadImpl(ctx, mockFS, mockFile, "http://example.com", mockDownloader([]byte("downloaded content"), nil))

	assert.NoError(t, err)
	mockFS.AssertExpectations(t)
}
