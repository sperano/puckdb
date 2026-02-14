package worker

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/sperano/puckdb/store"
	"github.com/stretchr/testify/assert"
)

func TestFetchTeamSummaryForTeamOnDayImpl_FileExists(t *testing.T) {
	ctx := context.Background()
	mockFS := NewMockFileSystem()
	day := time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC)
	file := store.TeamSummaryFile{Date: day, LeagueID: 12345, TeamID: 1}

	mockFS.On("MkdirAll", file.Dir(), os.FileMode(0755)).Return(nil)
	mockFS.On("Exists", file).Return(true)

	err := fetchTeamSummaryForTeamOnDayImpl(ctx, mockFS, 423, 12345, 1, day)

	assert.NoError(t, err)
	mockFS.AssertExpectations(t)
}

func TestFetchTeamSummaryForTeamOnDayImpl_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mockFS := NewMockFileSystem()
	day := time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC)

	err := fetchTeamSummaryForTeamOnDayImpl(ctx, mockFS, 423, 12345, 1, day)

	assert.ErrorIs(t, err, context.Canceled)
}

func TestFetchTeamSummaryForTeamOnDayImpl_MkdirAllError(t *testing.T) {
	ctx := context.Background()
	mockFS := NewMockFileSystem()
	day := time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC)
	file := store.TeamSummaryFile{Date: day, LeagueID: 12345, TeamID: 1}
	expectedErr := errors.New("mkdir failed")

	mockFS.On("MkdirAll", file.Dir(), os.FileMode(0755)).Return(expectedErr)

	err := fetchTeamSummaryForTeamOnDayImpl(ctx, mockFS, 423, 12345, 1, day)

	assert.Error(t, err)
	mockFS.AssertExpectations(t)
}
