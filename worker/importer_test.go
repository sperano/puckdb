package worker

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Tests for doDownloadImpl

func TestDoDownloadImpl_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	mockFS := NewMockFileSystem()
	mockFile := MockFile{DirVal: "test", NameVal: "file", ExtVal: "xml"}

	err := doDownloadImpl(ctx, mockFS, mockFile, "http://example.com")

	assert.ErrorIs(t, err, context.Canceled)
}

func TestDoDownloadImpl_FileAlreadyExists(t *testing.T) {
	ctx := context.Background()
	mockFS := NewMockFileSystem()
	mockFile := MockFile{DirVal: "test", NameVal: "file", ExtVal: "xml"}

	mockFS.On("MkdirAll", "test", os.FileMode(0755)).Return(nil)
	mockFS.On("Exists", mockFile).Return(true)

	err := doDownloadImpl(ctx, mockFS, mockFile, "http://example.com")

	assert.NoError(t, err)
	mockFS.AssertExpectations(t)
}

func TestDoDownloadImpl_MkdirAllError(t *testing.T) {
	ctx := context.Background()
	mockFS := NewMockFileSystem()
	mockFile := MockFile{DirVal: "test", NameVal: "file", ExtVal: "xml"}
	expectedErr := errors.New("mkdir failed")

	mockFS.On("MkdirAll", "test", os.FileMode(0755)).Return(expectedErr)

	err := doDownloadImpl(ctx, mockFS, mockFile, "http://example.com")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "test")
	mockFS.AssertExpectations(t)
}

// Tests for saveToCache

func TestSaveToCache_Success(t *testing.T) {
	mockFS := NewMockFileSystem()
	mockFile := MockFile{DirVal: "test", NameVal: "file", ExtVal: "xml"}
	content := []byte("test content")

	mockFS.On("Write", mockFile, content).Return(nil)

	err := saveToCache(mockFS, mockFile, content)

	assert.NoError(t, err)
	mockFS.AssertExpectations(t)
}

func TestSaveToCache_WriteError(t *testing.T) {
	mockFS := NewMockFileSystem()
	mockFile := MockFile{DirVal: "test", NameVal: "file", ExtVal: "xml"}
	content := []byte("test content")
	expectedErr := errors.New("write failed")

	mockFS.On("Write", mockFile, content).Return(expectedErr)

	err := saveToCache(mockFS, mockFile, content)

	assert.Equal(t, expectedErr, err)
	mockFS.AssertExpectations(t)
}

