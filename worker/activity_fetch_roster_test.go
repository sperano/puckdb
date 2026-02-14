package worker

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/sperano/puckdb/store"
	"github.com/stretchr/testify/assert"
)

func TestFetchRosterForTeamOnDayImpl_FileExists(t *testing.T) {
	ctx := context.Background()
	mockFS := NewMockFileSystem()
	day := time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC)
	file := store.RosterFile{Date: day, LeagueID: 12345, TeamID: 1}

	mockFS.On("MkdirAll", file.Dir(), os.FileMode(0755)).Return(nil)
	mockFS.On("Exists", file).Return(true)

	err := fetchRosterForTeamOnDayImpl(ctx, mockFS, 423, 12345, 1, day)

	assert.NoError(t, err)
	mockFS.AssertExpectations(t)
}

func TestFetchRosterForTeamOnDayImpl_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mockFS := NewMockFileSystem()
	day := time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC)

	err := fetchRosterForTeamOnDayImpl(ctx, mockFS, 423, 12345, 1, day)

	assert.ErrorIs(t, err, context.Canceled)
}
