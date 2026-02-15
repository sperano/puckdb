package worker

import (
	"context"
	"os"
	"testing"

	"github.com/sperano/puckdb/store"
	"github.com/stretchr/testify/assert"
)

func TestFetchTeamImpl_FileExists(t *testing.T) {
	ctx := context.Background()
	mockFS := NewMockFileSystem()
	file := store.TeamFile{Season: 2023, LeagueID: 12345, TeamID: 1}

	mockFS.On("MkdirAll", file.Dir(), os.FileMode(0755)).Return(nil)
	mockFS.On("Exists", file).Return(true)

	err := fetchTeamImpl(ctx, mockFS, 2023, 423, 12345, 1, mockDownloader(nil, nil))

	assert.NoError(t, err)
	mockFS.AssertExpectations(t)
}

func TestFetchTeamImpl_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mockFS := NewMockFileSystem()

	err := fetchTeamImpl(ctx, mockFS, 2023, 423, 12345, 1, mockDownloader(nil, nil))

	assert.ErrorIs(t, err, context.Canceled)
}
