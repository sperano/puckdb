package worker

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/sperano/puckdb/cache"
	"github.com/stretchr/testify/assert"
)

// Tests for downloadTeamImpl

func TestDownloadTeamImpl_FileExists(t *testing.T) {
	ctx := context.Background()
	mockFS := NewMockFileSystem()
	file := cache.TeamFile{Season: 2023, LeagueID: 12345, TeamID: 1}

	mockFS.On("MkdirAll", file.Dir(), os.FileMode(0755)).Return(nil)
	mockFS.On("Exists", file).Return(true)

	err := downloadTeamImpl(ctx, mockFS, 2023, 423, 12345, 1)

	assert.NoError(t, err)
	mockFS.AssertExpectations(t)
}

func TestDownloadTeamImpl_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mockFS := NewMockFileSystem()

	err := downloadTeamImpl(ctx, mockFS, 2023, 423, 12345, 1)

	assert.ErrorIs(t, err, context.Canceled)
}

// Tests for downloadRosterForTeamOnDayImpl

func TestDownloadRosterForTeamOnDayImpl_FileExists(t *testing.T) {
	ctx := context.Background()
	mockFS := NewMockFileSystem()
	day := time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC)
	file := cache.RosterFile{Date: day, LeagueID: 12345, TeamID: 1}

	mockFS.On("MkdirAll", file.Dir(), os.FileMode(0755)).Return(nil)
	mockFS.On("Exists", file).Return(true)

	err := downloadRosterForTeamOnDayImpl(ctx, mockFS, 423, 12345, 1, day)

	assert.NoError(t, err)
	mockFS.AssertExpectations(t)
}

func TestDownloadRosterForTeamOnDayImpl_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mockFS := NewMockFileSystem()
	day := time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC)

	err := downloadRosterForTeamOnDayImpl(ctx, mockFS, 423, 12345, 1, day)

	assert.ErrorIs(t, err, context.Canceled)
}

// Tests for downloadTeamSummaryForTeamOnDayImpl

func TestDownloadTeamSummaryForTeamOnDayImpl_FileExists(t *testing.T) {
	ctx := context.Background()
	mockFS := NewMockFileSystem()
	day := time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC)
	file := cache.TeamSummaryFile{Date: day, LeagueID: 12345, TeamID: 1}

	mockFS.On("MkdirAll", file.Dir(), os.FileMode(0755)).Return(nil)
	mockFS.On("Exists", file).Return(true)

	err := downloadTeamSummaryForTeamOnDayImpl(ctx, mockFS, 423, 12345, 1, day)

	assert.NoError(t, err)
	mockFS.AssertExpectations(t)
}

func TestDownloadTeamSummaryForTeamOnDayImpl_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mockFS := NewMockFileSystem()
	day := time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC)

	err := downloadTeamSummaryForTeamOnDayImpl(ctx, mockFS, 423, 12345, 1, day)

	assert.ErrorIs(t, err, context.Canceled)
}

func TestDownloadTeamSummaryForTeamOnDayImpl_MkdirAllError(t *testing.T) {
	ctx := context.Background()
	mockFS := NewMockFileSystem()
	day := time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC)
	file := cache.TeamSummaryFile{Date: day, LeagueID: 12345, TeamID: 1}
	expectedErr := errors.New("mkdir failed")

	mockFS.On("MkdirAll", file.Dir(), os.FileMode(0755)).Return(expectedErr)

	err := downloadTeamSummaryForTeamOnDayImpl(ctx, mockFS, 423, 12345, 1, day)

	assert.Error(t, err)
	mockFS.AssertExpectations(t)
}
