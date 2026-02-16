package worker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockBoxscoreDownloader returns a BoxscoreDownloader that returns the given content and error.
func mockBoxscoreDownloader(content []byte, err error) BoxscoreDownloader {
	return func(id nhl.GameID) ([]byte, error) {
		return content, err
	}
}

// mockGameDownloaders creates a GameDataDownloaders with the same mock for all types.
func mockGameDownloaders(content []byte, err error) GameDataDownloaders {
	dl := mockBoxscoreDownloader(content, err)
	return GameDataDownloaders{
		Boxscore:   dl,
		PlayByPlay: dl,
		ShiftChart: dl,
	}
}

// mockAllGameFilesExist sets up mocks for all game data files (boxscore, play-by-play, shift chart) as existing.
func mockAllGameFilesExist(mockFS *MockFileSystem, day time.Time, gameID nhl.GameID) {
	mockFS.On("Exists", store.BoxscoreFile{Date: day, GameID: gameID}).Return(true)
	mockFS.On("Exists", store.PlayByPlayFile{Date: day, GameID: gameID}).Return(true)
	mockFS.On("Exists", store.ShiftChartFile{Date: day, GameID: gameID}).Return(true)
}

func TestFetchDailyScheduleImpl_ScheduleFromCache(t *testing.T) {
	ctx := context.Background()
	mockFS := NewMockFileSystem()
	mockClient := &MockNHLClient{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	scheduleFile := store.DailyScheduleFile{Date: day}
	schedule := &nhl.DailySchedule{
		Games: []nhl.ScheduleGame{
			{ID: nhl.GameID(2024020001), GameType: nhl.GameTypeRegularSeason, GameState: nhl.GameStateFinal},
		},
	}
	scheduleJSON, _ := json.Marshal(schedule)

	mockFS.On("MkdirAll", scheduleFile.Dir(), os.FileMode(0755)).Return(nil)
	mockFS.On("Exists", scheduleFile).Return(true)
	mockFS.On("Read", scheduleFile).Return(scheduleJSON, nil)

	// All game data files already cached
	mockAllGameFilesExist(mockFS, day, nhl.GameID(2024020001))

	err := fetchDailyScheduleImpl(ctx, mockFS, mockClient, day, mockGameDownloaders(nil, nil))

	assert.NoError(t, err)
	mockFS.AssertExpectations(t)
}

func TestFetchDailyScheduleImpl_DownloadSchedule(t *testing.T) {
	ctx := context.Background()
	mockFS := NewMockFileSystem()
	mockClient := &MockNHLClient{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	scheduleFile := store.DailyScheduleFile{Date: day}
	schedule := &nhl.DailySchedule{
		Games: []nhl.ScheduleGame{
			{ID: nhl.GameID(2024020001), GameType: nhl.GameTypeRegularSeason, GameState: nhl.GameStateFinal},
		},
	}
	scheduleJSON, _ := json.Marshal(schedule)

	mockFS.On("MkdirAll", scheduleFile.Dir(), os.FileMode(0755)).Return(nil)
	mockFS.On("Exists", scheduleFile).Return(false)
	mockClient.On("DailySchedule", ctx, nhl.FromDate(day)).Return(schedule, nil)
	mockFS.On("Write", scheduleFile, scheduleJSON).Return(nil)
	mockFS.On("Read", scheduleFile).Return(scheduleJSON, nil)

	// All game data files already cached
	mockAllGameFilesExist(mockFS, day, nhl.GameID(2024020001))

	err := fetchDailyScheduleImpl(ctx, mockFS, mockClient, day, mockGameDownloaders(nil, nil))

	assert.NoError(t, err)
	mockFS.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestFetchDailyScheduleImpl_DownloadScheduleError(t *testing.T) {
	ctx := context.Background()
	mockFS := NewMockFileSystem()
	mockClient := &MockNHLClient{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	scheduleFile := store.DailyScheduleFile{Date: day}

	mockFS.On("MkdirAll", scheduleFile.Dir(), os.FileMode(0755)).Return(nil)
	mockFS.On("Exists", scheduleFile).Return(false)
	mockClient.On("DailySchedule", ctx, nhl.FromDate(day)).Return(nil, errors.New("API error"))

	err := fetchDailyScheduleImpl(ctx, mockFS, mockClient, day, mockGameDownloaders(nil, nil))

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "download schedule")
	mockFS.AssertExpectations(t)
}

func TestFetchDailyScheduleImpl_MkdirError(t *testing.T) {
	ctx := context.Background()
	mockFS := NewMockFileSystem()
	mockClient := &MockNHLClient{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	scheduleFile := store.DailyScheduleFile{Date: day}

	mockFS.On("MkdirAll", scheduleFile.Dir(), os.FileMode(0755)).Return(errors.New("permission denied"))

	err := fetchDailyScheduleImpl(ctx, mockFS, mockClient, day, mockGameDownloaders(nil, nil))

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "mkdir")
	mockFS.AssertExpectations(t)
}

func TestFetchDailyScheduleImpl_WriteScheduleError(t *testing.T) {
	ctx := context.Background()
	mockFS := NewMockFileSystem()
	mockClient := &MockNHLClient{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	scheduleFile := store.DailyScheduleFile{Date: day}
	schedule := &nhl.DailySchedule{Games: []nhl.ScheduleGame{}}
	scheduleJSON, _ := json.Marshal(schedule)

	mockFS.On("MkdirAll", scheduleFile.Dir(), os.FileMode(0755)).Return(nil)
	mockFS.On("Exists", scheduleFile).Return(false)
	mockClient.On("DailySchedule", ctx, nhl.FromDate(day)).Return(schedule, nil)
	mockFS.On("Write", scheduleFile, scheduleJSON).Return(errors.New("disk full"))

	err := fetchDailyScheduleImpl(ctx, mockFS, mockClient, day, mockGameDownloaders(nil, nil))

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "save schedule")
	mockFS.AssertExpectations(t)
}

func TestFetchDailyScheduleImpl_ReadScheduleError(t *testing.T) {
	ctx := context.Background()
	mockFS := NewMockFileSystem()
	mockClient := &MockNHLClient{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	scheduleFile := store.DailyScheduleFile{Date: day}

	mockFS.On("MkdirAll", scheduleFile.Dir(), os.FileMode(0755)).Return(nil)
	mockFS.On("Exists", scheduleFile).Return(true)
	mockFS.On("Read", scheduleFile).Return(nil, errors.New("file corrupted"))

	err := fetchDailyScheduleImpl(ctx, mockFS, mockClient, day, mockGameDownloaders(nil, nil))

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "read schedule")
	mockFS.AssertExpectations(t)
}

func TestFetchDailyScheduleImpl_ParseScheduleError(t *testing.T) {
	ctx := context.Background()
	mockFS := NewMockFileSystem()
	mockClient := &MockNHLClient{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	scheduleFile := store.DailyScheduleFile{Date: day}

	mockFS.On("MkdirAll", scheduleFile.Dir(), os.FileMode(0755)).Return(nil)
	mockFS.On("Exists", scheduleFile).Return(true)
	mockFS.On("Read", scheduleFile).Return([]byte("invalid json"), nil)

	err := fetchDailyScheduleImpl(ctx, mockFS, mockClient, day, mockGameDownloaders(nil, nil))

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse schedule")
	mockFS.AssertExpectations(t)
}

func TestFetchDailyScheduleImpl_SkipsIncompleteGames(t *testing.T) {
	ctx := context.Background()
	mockFS := NewMockFileSystem()
	mockClient := &MockNHLClient{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	scheduleFile := store.DailyScheduleFile{Date: day}
	schedule := &nhl.DailySchedule{
		Games: []nhl.ScheduleGame{
			{ID: nhl.GameID(2024020001), GameType: nhl.GameTypeRegularSeason, GameState: nhl.GameStateLive},  // In progress - skip
			{ID: nhl.GameID(2024020002), GameType: nhl.GameTypeRegularSeason, GameState: nhl.GameStateFinal}, // Final - include
		},
	}
	scheduleJSON, _ := json.Marshal(schedule)

	mockFS.On("MkdirAll", scheduleFile.Dir(), os.FileMode(0755)).Return(nil)
	mockFS.On("Exists", scheduleFile).Return(true)
	mockFS.On("Read", scheduleFile).Return(scheduleJSON, nil)

	// Only game 2 should be processed (all game data files cached)
	mockAllGameFilesExist(mockFS, day, nhl.GameID(2024020002))

	err := fetchDailyScheduleImpl(ctx, mockFS, mockClient, day, mockGameDownloaders(nil, nil))

	assert.NoError(t, err)
	mockFS.AssertExpectations(t)
}

func TestFetchDailyScheduleImpl_DownloadsBoxscore(t *testing.T) {
	ctx := context.Background()
	mockFS := NewMockFileSystem()
	mockClient := &MockNHLClient{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	scheduleFile := store.DailyScheduleFile{Date: day}
	schedule := &nhl.DailySchedule{
		Games: []nhl.ScheduleGame{
			{ID: nhl.GameID(2024020001), GameType: nhl.GameTypeRegularSeason, GameState: nhl.GameStateFinal},
		},
	}
	scheduleJSON, _ := json.Marshal(schedule)

	mockFS.On("MkdirAll", scheduleFile.Dir(), os.FileMode(0755)).Return(nil)
	mockFS.On("Exists", scheduleFile).Return(true)
	mockFS.On("Read", scheduleFile).Return(scheduleJSON, nil)

	// Boxscore not cached - needs download
	gameID := nhl.GameID(2024020001)
	boxscoreFile := store.BoxscoreFile{Date: day, GameID: gameID}
	mockFS.On("Exists", boxscoreFile).Return(false)
	mockFS.On("MkdirAll", boxscoreFile.Dir(), os.FileMode(0755)).Return(nil)
	mockFS.On("Write", boxscoreFile, []byte("boxscore data")).Return(nil)

	// Play-by-play not cached - needs download
	pbpFile := store.PlayByPlayFile{Date: day, GameID: gameID}
	mockFS.On("Exists", pbpFile).Return(false)
	mockFS.On("MkdirAll", pbpFile.Dir(), os.FileMode(0755)).Return(nil)
	mockFS.On("Write", pbpFile, []byte("boxscore data")).Return(nil)

	// Shift chart not cached - needs download
	shiftFile := store.ShiftChartFile{Date: day, GameID: gameID}
	mockFS.On("Exists", shiftFile).Return(false)
	mockFS.On("MkdirAll", shiftFile.Dir(), os.FileMode(0755)).Return(nil)
	mockFS.On("Write", shiftFile, []byte("boxscore data")).Return(nil)

	err := fetchDailyScheduleImpl(ctx, mockFS, mockClient, day, mockGameDownloaders([]byte("boxscore data"), nil))

	assert.NoError(t, err)
	mockFS.AssertExpectations(t)
}

func TestFetchDailyScheduleImpl_BoxscoreDownloadErrorContinues(t *testing.T) {
	ctx := context.Background()
	mockFS := NewMockFileSystem()
	mockClient := &MockNHLClient{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	scheduleFile := store.DailyScheduleFile{Date: day}
	schedule := &nhl.DailySchedule{
		Games: []nhl.ScheduleGame{
			{ID: nhl.GameID(2024020001), GameType: nhl.GameTypeRegularSeason, GameState: nhl.GameStateFinal},
			{ID: nhl.GameID(2024020002), GameType: nhl.GameTypeRegularSeason, GameState: nhl.GameStateFinal},
		},
	}
	scheduleJSON, _ := json.Marshal(schedule)

	mockFS.On("MkdirAll", scheduleFile.Dir(), os.FileMode(0755)).Return(nil)
	mockFS.On("Exists", scheduleFile).Return(true)
	mockFS.On("Read", scheduleFile).Return(scheduleJSON, nil)

	// All game data files already cached
	mockAllGameFilesExist(mockFS, day, nhl.GameID(2024020001))
	mockAllGameFilesExist(mockFS, day, nhl.GameID(2024020002))

	// Even with a failing downloader, cached files should still work
	err := fetchDailyScheduleImpl(ctx, mockFS, mockClient, day, mockGameDownloaders(nil, errors.New("download error")))

	assert.NoError(t, err)
	mockFS.AssertExpectations(t)
}

func TestFetchDailyScheduleImpl_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	mockFS := NewMockFileSystem()
	mockClient := &MockNHLClient{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	scheduleFile := store.DailyScheduleFile{Date: day}
	schedule := &nhl.DailySchedule{
		Games: []nhl.ScheduleGame{
			{ID: nhl.GameID(2024020001), GameType: nhl.GameTypeRegularSeason, GameState: nhl.GameStateFinal},
		},
	}
	scheduleJSON, _ := json.Marshal(schedule)

	mockFS.On("MkdirAll", scheduleFile.Dir(), os.FileMode(0755)).Return(nil)
	mockFS.On("Exists", scheduleFile).Return(true)
	mockFS.On("Read", scheduleFile).Return(scheduleJSON, nil)

	// Cancel before processing boxscores
	cancel()

	err := fetchDailyScheduleImpl(ctx, mockFS, mockClient, day, mockGameDownloaders(nil, nil))

	assert.ErrorIs(t, err, context.Canceled)
}

func TestFetchDailyScheduleImpl_BoxscoreMkdirError(t *testing.T) {
	ctx := context.Background()
	mockFS := NewMockFileSystem()
	mockClient := &MockNHLClient{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	scheduleFile := store.DailyScheduleFile{Date: day}
	schedule := &nhl.DailySchedule{
		Games: []nhl.ScheduleGame{
			{ID: nhl.GameID(2024020001), GameType: nhl.GameTypeRegularSeason, GameState: nhl.GameStateFinal},
		},
	}
	scheduleJSON, _ := json.Marshal(schedule)

	mockFS.On("MkdirAll", scheduleFile.Dir(), os.FileMode(0755)).Return(nil).Once()
	mockFS.On("Exists", scheduleFile).Return(true)
	mockFS.On("Read", scheduleFile).Return(scheduleJSON, nil)

	// Boxscore mkdir fails (second call to same dir path)
	boxscoreFile := store.BoxscoreFile{Date: day, GameID: nhl.GameID(2024020001)}
	mockFS.On("Exists", boxscoreFile).Return(false)
	mockFS.On("MkdirAll", boxscoreFile.Dir(), os.FileMode(0755)).Return(errors.New("mkdir error")).Once()

	// Should return error
	err := fetchDailyScheduleImpl(ctx, mockFS, mockClient, day, mockGameDownloaders(nil, nil))

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "mkdir error")
	mockFS.AssertExpectations(t)
}

func TestFetchDailyScheduleImpl_BoxscoreDownloadError(t *testing.T) {
	ctx := context.Background()
	mockFS := NewMockFileSystem()
	mockClient := &MockNHLClient{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	scheduleFile := store.DailyScheduleFile{Date: day}
	schedule := &nhl.DailySchedule{
		Games: []nhl.ScheduleGame{
			{ID: nhl.GameID(2024020001), GameType: nhl.GameTypeRegularSeason, GameState: nhl.GameStateFinal},
		},
	}
	scheduleJSON, _ := json.Marshal(schedule)

	mockFS.On("MkdirAll", scheduleFile.Dir(), os.FileMode(0755)).Return(nil).Once()
	mockFS.On("Exists", scheduleFile).Return(true)
	mockFS.On("Read", scheduleFile).Return(scheduleJSON, nil)

	// Boxscore download fails (second call to same dir path)
	boxscoreFile := store.BoxscoreFile{Date: day, GameID: nhl.GameID(2024020001)}
	mockFS.On("Exists", boxscoreFile).Return(false)
	mockFS.On("MkdirAll", boxscoreFile.Dir(), os.FileMode(0755)).Return(nil).Once()

	// Should return error
	err := fetchDailyScheduleImpl(ctx, mockFS, mockClient, day, mockGameDownloaders(nil, errors.New("download error")))

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "download error")
	mockFS.AssertExpectations(t)
}

func TestFetchDailyScheduleImpl_BoxscoreWriteError(t *testing.T) {
	ctx := context.Background()
	mockFS := NewMockFileSystem()
	mockClient := &MockNHLClient{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	scheduleFile := store.DailyScheduleFile{Date: day}
	schedule := &nhl.DailySchedule{
		Games: []nhl.ScheduleGame{
			{ID: nhl.GameID(2024020001), GameType: nhl.GameTypeRegularSeason, GameState: nhl.GameStateFinal},
		},
	}
	scheduleJSON, _ := json.Marshal(schedule)

	mockFS.On("MkdirAll", scheduleFile.Dir(), os.FileMode(0755)).Return(nil).Once()
	mockFS.On("Exists", scheduleFile).Return(true)
	mockFS.On("Read", scheduleFile).Return(scheduleJSON, nil)

	// Boxscore write fails (second call to same dir path)
	boxscoreFile := store.BoxscoreFile{Date: day, GameID: nhl.GameID(2024020001)}
	mockFS.On("Exists", boxscoreFile).Return(false)
	mockFS.On("MkdirAll", boxscoreFile.Dir(), os.FileMode(0755)).Return(nil).Once()
	mockFS.On("Write", boxscoreFile, []byte("boxscore data")).Return(errors.New("write error"))

	// Should return error
	err := fetchDailyScheduleImpl(ctx, mockFS, mockClient, day, mockGameDownloaders([]byte("boxscore data"), nil))

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "write error")
	mockFS.AssertExpectations(t)
}

func TestFilterRegularSeasonGames(t *testing.T) {
	tests := []struct {
		name     string
		input    []nhl.GameID
		expected []nhl.GameID
	}{
		{
			name:     "empty input",
			input:    []nhl.GameID{},
			expected: []nhl.GameID{},
		},
		{
			name:     "regular season only",
			input:    []nhl.GameID{nhl.GameID(2024020001), nhl.GameID(2024020002)},
			expected: []nhl.GameID{nhl.GameID(2024020001), nhl.GameID(2024020002)},
		},
		{
			name:     "preseason filtered out",
			input:    []nhl.GameID{nhl.GameID(2024010001), nhl.GameID(2024020001)}, // 01 = preseason, 02 = regular
			expected: []nhl.GameID{nhl.GameID(2024020001)},
		},
		{
			name:     "playoff included",
			input:    []nhl.GameID{nhl.GameID(2024030001)}, // 03 = playoffs
			expected: []nhl.GameID{nhl.GameID(2024030001)},
		},
		{
			name:     "invalid game ID filtered out",
			input:    []nhl.GameID{nhl.GameID(123), nhl.GameID(2024020001)}, // 123 is invalid (too short)
			expected: []nhl.GameID{nhl.GameID(2024020001)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterRegularSeasonGames(tt.input)
			require.Equal(t, tt.expected, result)
		})
	}
}
