package worker

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/sperano/puckdb/cache"
	"github.com/stretchr/testify/assert"
)

// Tests for fetchLeagueImpl

func TestFetchLeagueImpl_FileExists(t *testing.T) {
	ctx := context.Background()
	mockFS := NewMockFileSystem()
	file := cache.LeagueFile{Season: 2023, LeagueID: 12345}

	mockFS.On("MkdirAll", file.Dir(), os.FileMode(0755)).Return(nil)
	mockFS.On("Exists", file).Return(true)

	err := fetchLeagueImpl(ctx, mockFS, 2023, 423, 12345)

	assert.NoError(t, err)
	mockFS.AssertExpectations(t)
}

func TestFetchLeagueImpl_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	mockFS := NewMockFileSystem()

	err := fetchLeagueImpl(ctx, mockFS, 2023, 423, 12345)

	assert.ErrorIs(t, err, context.Canceled)
}

func TestFetchLeagueImpl_MkdirAllError(t *testing.T) {
	ctx := context.Background()
	mockFS := NewMockFileSystem()
	file := cache.LeagueFile{Season: 2023, LeagueID: 12345}
	expectedErr := errors.New("mkdir failed")

	mockFS.On("MkdirAll", file.Dir(), os.FileMode(0755)).Return(expectedErr)

	err := fetchLeagueImpl(ctx, mockFS, 2023, 423, 12345)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), file.Dir())
	mockFS.AssertExpectations(t)
}
