package worker

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/sperano/yfh/cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// Tests for downloadTeamImpl

func TestDownloadTeamImpl_FileExists(t *testing.T) {
	ctx := context.Background()
	mockFS := NewMockFileSystem()
	mockFile := MockFile{DirVal: "teams/2023/12345", NameVal: "1", ExtVal: "xml"}

	mockFS.On("New", cache.TeamFileType, mock.Anything).Return(mockFile)
	mockFS.On("MkdirAll", "teams/2023/12345", os.FileMode(0755)).Return(nil)
	mockFS.On("Exists", mockFile).Return(true)

	err := downloadTeamImpl(ctx, mockFS, 2023, 423, 12345, 1)

	assert.NoError(t, err)
	mockFS.AssertExpectations(t)
}

func TestDownloadTeamImpl_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mockFS := NewMockFileSystem()
	mockFile := MockFile{DirVal: "teams/2023/12345", NameVal: "1", ExtVal: "xml"}

	mockFS.On("New", cache.TeamFileType, mock.Anything).Return(mockFile)

	err := downloadTeamImpl(ctx, mockFS, 2023, 423, 12345, 1)

	assert.ErrorIs(t, err, context.Canceled)
}

// Tests for downloadRosterForTeamOnDayImpl

func TestDownloadRosterForTeamOnDayImpl_FileExists(t *testing.T) {
	ctx := context.Background()
	mockFS := NewMockFileSystem()
	mockFile := MockFile{DirVal: "rosters/12345/1", NameVal: "2023-11-15", ExtVal: "xml"}
	day := time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC)

	mockFS.On("New", cache.RosterFileType, mock.Anything).Return(mockFile)
	mockFS.On("MkdirAll", "rosters/12345/1", os.FileMode(0755)).Return(nil)
	mockFS.On("Exists", mockFile).Return(true)

	err := downloadRosterForTeamOnDayImpl(ctx, mockFS, 423, 12345, 1, day)

	assert.NoError(t, err)
	mockFS.AssertExpectations(t)
}

func TestDownloadRosterForTeamOnDayImpl_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mockFS := NewMockFileSystem()
	mockFile := MockFile{DirVal: "rosters/12345/1", NameVal: "2023-11-15", ExtVal: "xml"}
	day := time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC)

	mockFS.On("New", cache.RosterFileType, mock.Anything).Return(mockFile)

	err := downloadRosterForTeamOnDayImpl(ctx, mockFS, 423, 12345, 1, day)

	assert.ErrorIs(t, err, context.Canceled)
}

// Tests for downloadTeamSummaryForTeamOnDayImpl

func TestDownloadTeamSummaryForTeamOnDayImpl_FileExists(t *testing.T) {
	ctx := context.Background()
	mockFS := NewMockFileSystem()
	mockFile := MockFile{DirVal: "summaries/12345/1", NameVal: "2023-11-15", ExtVal: "xml"}
	day := time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC)

	mockFS.On("New", cache.TeamSummaryFileType, mock.Anything).Return(mockFile)
	mockFS.On("MkdirAll", "summaries/12345/1", os.FileMode(0755)).Return(nil)
	mockFS.On("Exists", mockFile).Return(true)

	err := downloadTeamSummaryForTeamOnDayImpl(ctx, mockFS, 423, 12345, 1, day)

	assert.NoError(t, err)
	mockFS.AssertExpectations(t)
}

func TestDownloadTeamSummaryForTeamOnDayImpl_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mockFS := NewMockFileSystem()
	mockFile := MockFile{DirVal: "summaries/12345/1", NameVal: "2023-11-15", ExtVal: "xml"}
	day := time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC)

	mockFS.On("New", cache.TeamSummaryFileType, mock.Anything).Return(mockFile)

	err := downloadTeamSummaryForTeamOnDayImpl(ctx, mockFS, 423, 12345, 1, day)

	assert.ErrorIs(t, err, context.Canceled)
}

func TestDownloadTeamSummaryForTeamOnDayImpl_MkdirAllError(t *testing.T) {
	ctx := context.Background()
	mockFS := NewMockFileSystem()
	mockFile := MockFile{DirVal: "summaries/12345/1", NameVal: "2023-11-15", ExtVal: "xml"}
	day := time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC)
	expectedErr := errors.New("mkdir failed")

	mockFS.On("New", cache.TeamSummaryFileType, mock.Anything).Return(mockFile)
	mockFS.On("MkdirAll", "summaries/12345/1", os.FileMode(0755)).Return(expectedErr)

	err := downloadTeamSummaryForTeamOnDayImpl(ctx, mockFS, 423, 12345, 1, day)

	assert.Error(t, err)
	mockFS.AssertExpectations(t)
}
