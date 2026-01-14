package worker

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/sperano/yfh/cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// Tests for downloadLeagueImpl

func TestDownloadLeagueImpl_FileExists(t *testing.T) {
	ctx := context.Background()
	mockFS := NewMockFileSystem()
	mockFile := MockFile{DirVal: "leagues/2023", NameVal: "12345", ExtVal: "xml"}

	mockFS.On("New", cache.LeagueFileType, mock.Anything).Return(mockFile)
	mockFS.On("MkdirAll", "leagues/2023", os.FileMode(0755)).Return(nil)
	mockFS.On("Exists", mockFile).Return(true)

	err := downloadLeagueImpl(ctx, mockFS, 2023, 423, 12345)

	assert.NoError(t, err)
	mockFS.AssertExpectations(t)
}

func TestDownloadLeagueImpl_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	mockFS := NewMockFileSystem()
	mockFile := MockFile{DirVal: "leagues/2023", NameVal: "12345", ExtVal: "xml"}

	mockFS.On("New", cache.LeagueFileType, mock.Anything).Return(mockFile)

	err := downloadLeagueImpl(ctx, mockFS, 2023, 423, 12345)

	assert.ErrorIs(t, err, context.Canceled)
}

func TestDownloadLeagueImpl_MkdirAllError(t *testing.T) {
	ctx := context.Background()
	mockFS := NewMockFileSystem()
	mockFile := MockFile{DirVal: "leagues/2023", NameVal: "12345", ExtVal: "xml"}
	expectedErr := errors.New("mkdir failed")

	mockFS.On("New", cache.LeagueFileType, mock.Anything).Return(mockFile)
	mockFS.On("MkdirAll", "leagues/2023", os.FileMode(0755)).Return(expectedErr)

	err := downloadLeagueImpl(ctx, mockFS, 2023, 423, 12345)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "leagues/2023")
	mockFS.AssertExpectations(t)
}
