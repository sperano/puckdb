package worker

import (
	"context"
	"encoding/json"
	"errors"
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
		GameStory:  dl,
	}
}

// setAllGameFilesExist pre-populates all game data files (boxscore, play-by-play, shift chart, game story).
func setAllGameFilesExist(mem *store.MemStorage, day time.Time, gameID nhl.GameID) {
	mem.SetFile(store.BoxscorePath(day, gameID), []byte("{}"))
	mem.SetFile(store.PlayByPlayPath(day, gameID), []byte("{}"))
	mem.SetFile(store.ShiftChartPath(day, gameID), []byte("{}"))
	mem.SetFile(store.GameStoryPath(day, gameID), []byte("{}"))
}

func TestFetchDailyScheduleImpl_ScheduleFromCache(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemStorage()
	repos := store.NewRepos(mem)
	mockClient := &MockNHLClient{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	schedule := &nhl.DailySchedule{
		Games: []nhl.ScheduleGame{
			{ID: nhl.GameID(2024020001), GameType: nhl.GameTypeRegularSeason, GameState: nhl.GameStateFinal},
		},
	}
	scheduleJSON, err := json.Marshal(schedule)
	require.NoError(t, err)
	require.NoError(t, repos.Schedule.Save(day, scheduleJSON))

	// All game data files already cached
	setAllGameFilesExist(mem, day, nhl.GameID(2024020001))

	err = fetchDailyScheduleImpl(ctx, repos, mockClient, day, mockGameDownloaders(nil, nil))

	assert.NoError(t, err)
}

func TestFetchDailyScheduleImpl_DownloadSchedule(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemStorage()
	repos := store.NewRepos(mem)
	mockClient := &MockNHLClient{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	schedule := &nhl.DailySchedule{
		Games: []nhl.ScheduleGame{
			{ID: nhl.GameID(2024020001), GameType: nhl.GameTypeRegularSeason, GameState: nhl.GameStateFinal},
		},
	}
	mockClient.On("DailySchedule", ctx, nhl.FromDate(day)).Return(schedule, nil)

	// All game data files already cached
	setAllGameFilesExist(mem, day, nhl.GameID(2024020001))

	err := fetchDailyScheduleImpl(ctx, repos, mockClient, day, mockGameDownloaders(nil, nil))

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)

	// Verify schedule was saved
	assert.True(t, repos.Schedule.Exists(day))
}

func TestFetchDailyScheduleImpl_DownloadScheduleError(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemStorage()
	repos := store.NewRepos(mem)
	mockClient := &MockNHLClient{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	mockClient.On("DailySchedule", ctx, nhl.FromDate(day)).Return(nil, errors.New("API error"))

	err := fetchDailyScheduleImpl(ctx, repos, mockClient, day, mockGameDownloaders(nil, nil))

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "download schedule")
}

func TestFetchDailyScheduleImpl_ParseScheduleError(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemStorage()
	repos := store.NewRepos(mem)
	mockClient := &MockNHLClient{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	// Pre-populate with invalid JSON
	mem.SetFile(store.DailySchedulePath(day), []byte("invalid json"))

	err := fetchDailyScheduleImpl(ctx, repos, mockClient, day, mockGameDownloaders(nil, nil))

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "read schedule")
}

func TestFetchDailyScheduleImpl_SkipsIncompleteGames(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemStorage()
	repos := store.NewRepos(mem)
	mockClient := &MockNHLClient{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	schedule := &nhl.DailySchedule{
		Games: []nhl.ScheduleGame{
			{ID: nhl.GameID(2024020001), GameType: nhl.GameTypeRegularSeason, GameState: nhl.GameStateLive},  // In progress - skip
			{ID: nhl.GameID(2024020002), GameType: nhl.GameTypeRegularSeason, GameState: nhl.GameStateFinal}, // Final - include
		},
	}
	scheduleJSON, err := json.Marshal(schedule)
	require.NoError(t, err)
	require.NoError(t, repos.Schedule.Save(day, scheduleJSON))

	// Only game 2 should be processed (all game data files cached)
	setAllGameFilesExist(mem, day, nhl.GameID(2024020002))

	err = fetchDailyScheduleImpl(ctx, repos, mockClient, day, mockGameDownloaders(nil, nil))

	assert.NoError(t, err)
}

func TestFetchDailyScheduleImpl_DownloadsGameData(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemStorage()
	repos := store.NewRepos(mem)
	mockClient := &MockNHLClient{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	schedule := &nhl.DailySchedule{
		Games: []nhl.ScheduleGame{
			{ID: nhl.GameID(2024020001), GameType: nhl.GameTypeRegularSeason, GameState: nhl.GameStateFinal},
		},
	}
	scheduleJSON, err := json.Marshal(schedule)
	require.NoError(t, err)
	require.NoError(t, repos.Schedule.Save(day, scheduleJSON))

	// No game data files cached - should download all

	err = fetchDailyScheduleImpl(ctx, repos, mockClient, day, mockGameDownloaders([]byte("game data"), nil))

	assert.NoError(t, err)

	// Verify all files were downloaded
	gameID := nhl.GameID(2024020001)
	assert.True(t, mem.Has(store.BoxscorePath(day, gameID)))
	assert.True(t, mem.Has(store.PlayByPlayPath(day, gameID)))
	assert.True(t, mem.Has(store.ShiftChartPath(day, gameID)))
	assert.True(t, mem.Has(store.GameStoryPath(day, gameID)))
}

func TestFetchDailyScheduleImpl_CachedGameDataSkipped(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemStorage()
	repos := store.NewRepos(mem)
	mockClient := &MockNHLClient{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	schedule := &nhl.DailySchedule{
		Games: []nhl.ScheduleGame{
			{ID: nhl.GameID(2024020001), GameType: nhl.GameTypeRegularSeason, GameState: nhl.GameStateFinal},
			{ID: nhl.GameID(2024020002), GameType: nhl.GameTypeRegularSeason, GameState: nhl.GameStateFinal},
		},
	}
	scheduleJSON, err := json.Marshal(schedule)
	require.NoError(t, err)
	require.NoError(t, repos.Schedule.Save(day, scheduleJSON))

	// All game data files already cached
	setAllGameFilesExist(mem, day, nhl.GameID(2024020001))
	setAllGameFilesExist(mem, day, nhl.GameID(2024020002))

	// Even with a failing downloader, cached files should still work
	err = fetchDailyScheduleImpl(ctx, repos, mockClient, day, mockGameDownloaders(nil, errors.New("download error")))

	assert.NoError(t, err)
}

func TestFetchDailyScheduleImpl_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	mem := store.NewMemStorage()
	repos := store.NewRepos(mem)
	mockClient := &MockNHLClient{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	schedule := &nhl.DailySchedule{
		Games: []nhl.ScheduleGame{
			{ID: nhl.GameID(2024020001), GameType: nhl.GameTypeRegularSeason, GameState: nhl.GameStateFinal},
		},
	}
	scheduleJSON, err := json.Marshal(schedule)
	require.NoError(t, err)
	require.NoError(t, repos.Schedule.Save(day, scheduleJSON))

	// Cancel before processing boxscores
	cancel()

	err = fetchDailyScheduleImpl(ctx, repos, mockClient, day, mockGameDownloaders(nil, nil))

	assert.ErrorIs(t, err, context.Canceled)
}

func TestFetchDailyScheduleImpl_BoxscoreDownloadError(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemStorage()
	repos := store.NewRepos(mem)
	mockClient := &MockNHLClient{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	schedule := &nhl.DailySchedule{
		Games: []nhl.ScheduleGame{
			{ID: nhl.GameID(2024020001), GameType: nhl.GameTypeRegularSeason, GameState: nhl.GameStateFinal},
		},
	}
	scheduleJSON, err := json.Marshal(schedule)
	require.NoError(t, err)
	require.NoError(t, repos.Schedule.Save(day, scheduleJSON))

	// No game data files cached - download will fail
	err = fetchDailyScheduleImpl(ctx, repos, mockClient, day, mockGameDownloaders(nil, errors.New("download error")))

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "download error")
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
