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

// Tests for fetchTeamImpl

func TestFetchTeamImpl_FileExists(t *testing.T) {
	ctx := context.Background()
	mockFS := NewMockFileSystem()
	file := cache.TeamFile{Season: 2023, LeagueID: 12345, TeamID: 1}

	mockFS.On("MkdirAll", file.Dir(), os.FileMode(0755)).Return(nil)
	mockFS.On("Exists", file).Return(true)

	err := fetchTeamImpl(ctx, mockFS, 2023, 423, 12345, 1)

	assert.NoError(t, err)
	mockFS.AssertExpectations(t)
}

func TestFetchTeamImpl_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mockFS := NewMockFileSystem()

	err := fetchTeamImpl(ctx, mockFS, 2023, 423, 12345, 1)

	assert.ErrorIs(t, err, context.Canceled)
}

// Tests for fetchRosterForTeamOnDayImpl

func TestFetchRosterForTeamOnDayImpl_FileExists(t *testing.T) {
	ctx := context.Background()
	mockFS := NewMockFileSystem()
	day := time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC)
	file := cache.RosterFile{Date: day, LeagueID: 12345, TeamID: 1}

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

// Tests for fetchTeamSummaryForTeamOnDayImpl

func TestFetchTeamSummaryForTeamOnDayImpl_FileExists(t *testing.T) {
	ctx := context.Background()
	mockFS := NewMockFileSystem()
	day := time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC)
	file := cache.TeamSummaryFile{Date: day, LeagueID: 12345, TeamID: 1}

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
	file := cache.TeamSummaryFile{Date: day, LeagueID: 12345, TeamID: 1}
	expectedErr := errors.New("mkdir failed")

	mockFS.On("MkdirAll", file.Dir(), os.FileMode(0755)).Return(expectedErr)

	err := fetchTeamSummaryForTeamOnDayImpl(ctx, mockFS, 423, 12345, 1, day)

	assert.Error(t, err)
	mockFS.AssertExpectations(t)
}
