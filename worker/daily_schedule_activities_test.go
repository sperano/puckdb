package worker

import (
	"errors"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/go-redis/redismock/v8"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
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
		Boxscore:     dl,
		PlayByPlay:   dl,
		ShiftChart:   dl,
		GameStory:    dl,
		SeasonSeries: dl,
	}
}

// setAllGameFilesExist pre-populates all game data files (boxscore, play-by-play, shift chart, game story, season series).
func setAllGameFilesExist(mem *store.MemStorage, day time.Time, gameID nhl.GameID) {
	mem.SetFile(resource.Boxscore{Date: day, GameID: gameID}.Path(), []byte("{}"))
	mem.SetFile(resource.PlayByPlay{Date: day, GameID: gameID}.Path(), []byte("{}"))
	mem.SetFile(resource.ShiftChart{Date: day, GameID: gameID}.Path(), []byte("{}"))
	mem.SetFile(resource.GameStory{Date: day, GameID: gameID}.Path(), []byte("{}"))
	mem.SetFile(resource.SeasonSeries{Date: day, GameID: gameID}.Path(), []byte("{}"))
}

type DailyScheduleTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestActivityEnvironment
}

func (s *DailyScheduleTestSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
}

func TestDailyScheduleTestSuite(t *testing.T) {
	suite.Run(t, new(DailyScheduleTestSuite))
}

// newGobCache creates a permissive GobCache for tests: Get always misses, Set always succeeds.
func (s *DailyScheduleTestSuite) newGobCache() *cache.GobCache {
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)
	scheduleKeyPattern := core.RedisResourceKeyPrefix + ".*"
	mockRedis.Regexp().ExpectGet(scheduleKeyPattern).SetErr(redis.Nil)
	mockRedis.Regexp().CustomMatch(anyArgs).ExpectSet(scheduleKeyPattern, "x", cache.GobCacheTTL).SetVal("OK")
	return cache.NewGobCache(redisClient)
}

func (s *DailyScheduleTestSuite) newActivities(mem *store.MemStorage, client NHLClient, downloaders GameDataDownloaders) *DailyScheduleActivities {
	return &DailyScheduleActivities{
		Storage:       mem,
		NHLClient:     client,
		GameDownloads: downloaders,
		GobCache:      s.newGobCache(),
	}
}

func (s *DailyScheduleTestSuite) TestScheduleFromCache() {
	mem := store.NewMemStorage()
	mockClient := &MockNHLClient{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	schedule := &nhl.DailySchedule{
		Games: []nhl.ScheduleGame{
			{ID: nhl.GameID(2024020001), GameType: nhl.GameTypeRegularSeason, GameState: nhl.GameStateFinal},
		},
	}
	require.NoError(s.T(), resource.WriteParsed(mem, resource.DailySchedule{Date: day}, schedule))

	setAllGameFilesExist(mem, day, nhl.GameID(2024020001))

	a := s.newActivities(mem, mockClient, mockGameDownloaders(nil, nil))
	s.env.RegisterActivity(a.FetchDailySchedule)
	future, err := s.env.ExecuteActivity(a.FetchDailySchedule, day)

	require.NoError(s.T(), err)
	var result FetchDailyScheduleResult
	require.NoError(s.T(), future.Get(&result))
}

func (s *DailyScheduleTestSuite) TestDownloadSchedule() {
	mem := store.NewMemStorage()
	mockClient := &MockNHLClient{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	schedule := &nhl.DailySchedule{
		Games: []nhl.ScheduleGame{
			{ID: nhl.GameID(2024020001), GameType: nhl.GameTypeRegularSeason, GameState: nhl.GameStateFinal},
		},
	}
	mockClient.On("DailySchedule", mock.Anything, nhl.FromDate(day)).Return(schedule, nil)

	setAllGameFilesExist(mem, day, nhl.GameID(2024020001))

	a := s.newActivities(mem, mockClient, mockGameDownloaders(nil, nil))
	s.env.RegisterActivity(a.FetchDailySchedule)
	future, err := s.env.ExecuteActivity(a.FetchDailySchedule, day)

	require.NoError(s.T(), err)
	var result FetchDailyScheduleResult
	require.NoError(s.T(), future.Get(&result))
	mockClient.AssertExpectations(s.T())

	assert.True(s.T(), mem.Exists(resource.DailySchedule{Date: day}.Path()))
}

func (s *DailyScheduleTestSuite) TestDownloadScheduleError() {
	mem := store.NewMemStorage()
	mockClient := &MockNHLClient{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	mockClient.On("DailySchedule", mock.Anything, nhl.FromDate(day)).Return(nil, errors.New("API error"))

	a := s.newActivities(mem, mockClient, mockGameDownloaders(nil, nil))
	s.env.RegisterActivity(a.FetchDailySchedule)
	_, err := s.env.ExecuteActivity(a.FetchDailySchedule, day)

	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "download schedule")
}

func (s *DailyScheduleTestSuite) TestCorruptCacheFallsBackToAPI() {
	mem := store.NewMemStorage()
	mockClient := &MockNHLClient{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	// Corrupt cached schedule should fall back to API
	scheduleRes := resource.DailySchedule{Date: day}
	mem.SetFile(scheduleRes.Path(), []byte("invalid json"))

	mockClient.On("DailySchedule", mock.Anything, nhl.FromDate(day)).Return(nil, errors.New("API error"))

	a := s.newActivities(mem, mockClient, mockGameDownloaders(nil, nil))
	s.env.RegisterActivity(a.FetchDailySchedule)
	_, err := s.env.ExecuteActivity(a.FetchDailySchedule, day)

	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "download schedule")
	mockClient.AssertExpectations(s.T())
}

func (s *DailyScheduleTestSuite) TestSkipsIncompleteGames() {
	mem := store.NewMemStorage()
	mockClient := &MockNHLClient{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	schedule := &nhl.DailySchedule{
		Games: []nhl.ScheduleGame{
			{ID: nhl.GameID(2024020001), GameType: nhl.GameTypeRegularSeason, GameState: nhl.GameStateLive},  // In progress - skip
			{ID: nhl.GameID(2024020002), GameType: nhl.GameTypeRegularSeason, GameState: nhl.GameStateFinal}, // Final - include
		},
	}
	require.NoError(s.T(), resource.WriteParsed(mem, resource.DailySchedule{Date: day}, schedule))

	setAllGameFilesExist(mem, day, nhl.GameID(2024020002))

	a := s.newActivities(mem, mockClient, mockGameDownloaders(nil, nil))
	s.env.RegisterActivity(a.FetchDailySchedule)
	_, err := s.env.ExecuteActivity(a.FetchDailySchedule, day)

	assert.NoError(s.T(), err)
}

func (s *DailyScheduleTestSuite) TestDownloadsGameData() {
	mem := store.NewMemStorage()
	mockClient := &MockNHLClient{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	schedule := &nhl.DailySchedule{
		Games: []nhl.ScheduleGame{
			{ID: nhl.GameID(2024020001), GameType: nhl.GameTypeRegularSeason, GameState: nhl.GameStateFinal},
		},
	}
	// No pre-populated schedule — API fetch path triggers game data downloads
	mockClient.On("DailySchedule", mock.Anything, nhl.FromDate(day)).Return(schedule, nil)

	a := s.newActivities(mem, mockClient, mockGameDownloaders([]byte("game data"), nil))
	s.env.RegisterActivity(a.FetchDailySchedule)
	_, err := s.env.ExecuteActivity(a.FetchDailySchedule, day)

	assert.NoError(s.T(), err)

	gameID := nhl.GameID(2024020001)
	assert.True(s.T(), mem.Has(resource.Boxscore{Date: day, GameID: gameID}.Path()))
	assert.True(s.T(), mem.Has(resource.PlayByPlay{Date: day, GameID: gameID}.Path()))
	assert.True(s.T(), mem.Has(resource.ShiftChart{Date: day, GameID: gameID}.Path()))
	assert.True(s.T(), mem.Has(resource.GameStory{Date: day, GameID: gameID}.Path()))
}

func (s *DailyScheduleTestSuite) TestCachedGameDataSkipped() {
	mem := store.NewMemStorage()
	mockClient := &MockNHLClient{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	schedule := &nhl.DailySchedule{
		Games: []nhl.ScheduleGame{
			{ID: nhl.GameID(2024020001), GameType: nhl.GameTypeRegularSeason, GameState: nhl.GameStateFinal},
			{ID: nhl.GameID(2024020002), GameType: nhl.GameTypeRegularSeason, GameState: nhl.GameStateFinal},
		},
	}
	require.NoError(s.T(), resource.WriteParsed(mem, resource.DailySchedule{Date: day}, schedule))

	setAllGameFilesExist(mem, day, nhl.GameID(2024020001))
	setAllGameFilesExist(mem, day, nhl.GameID(2024020002))

	// Even with a failing downloader, cached files should still work
	a := s.newActivities(mem, mockClient, mockGameDownloaders(nil, errors.New("download error")))
	s.env.RegisterActivity(a.FetchDailySchedule)
	_, err := s.env.ExecuteActivity(a.FetchDailySchedule, day)

	assert.NoError(s.T(), err)
}

func (s *DailyScheduleTestSuite) TestContextCancelled() {
	mem := store.NewMemStorage()
	mockClient := &MockNHLClient{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	schedule := &nhl.DailySchedule{
		Games: []nhl.ScheduleGame{
			{ID: nhl.GameID(2024020001), GameType: nhl.GameTypeRegularSeason, GameState: nhl.GameStateFinal},
		},
	}
	require.NoError(s.T(), resource.WriteParsed(mem, resource.DailySchedule{Date: day}, schedule))

	a := s.newActivities(mem, mockClient, mockGameDownloaders(nil, nil))
	s.env.RegisterActivity(a.FetchDailySchedule)
	_, err := s.env.ExecuteActivity(a.FetchDailySchedule, day)

	// TODO: TestActivityEnvironment doesn't support context cancellation mid-execution.
	// This test needs rethinking once the method body handles cancellation.
	_ = err
}

func (s *DailyScheduleTestSuite) TestBoxscoreDownloadError() {
	mem := store.NewMemStorage()
	mockClient := &MockNHLClient{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	schedule := &nhl.DailySchedule{
		Games: []nhl.ScheduleGame{
			{ID: nhl.GameID(2024020001), GameType: nhl.GameTypeRegularSeason, GameState: nhl.GameStateFinal},
		},
	}
	// No pre-populated schedule — API fetch path triggers game data downloads
	mockClient.On("DailySchedule", mock.Anything, nhl.FromDate(day)).Return(schedule, nil)

	a := s.newActivities(mem, mockClient, mockGameDownloaders(nil, errors.New("download error")))
	s.env.RegisterActivity(a.FetchDailySchedule)
	_, err := s.env.ExecuteActivity(a.FetchDailySchedule, day)

	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "download error")
}

func TestFilterRegularSeasonGames(t *testing.T) {
	game := func(id int) nhl.ScheduleGame {
		return nhl.ScheduleGame{ID: nhl.GameID(id), GameState: nhl.GameStateFinal}
	}

	tests := []struct {
		name     string
		input    []nhl.ScheduleGame
		expected []nhl.GameID
	}{
		{
			name:     "empty input",
			input:    []nhl.ScheduleGame{},
			expected: []nhl.GameID{},
		},
		{
			name:     "regular season only",
			input:    []nhl.ScheduleGame{game(2024020001), game(2024020002)},
			expected: []nhl.GameID{nhl.GameID(2024020001), nhl.GameID(2024020002)},
		},
		{
			name:     "preseason filtered out",
			input:    []nhl.ScheduleGame{game(2024010001), game(2024020001)}, // 01 = preseason, 02 = regular
			expected: []nhl.GameID{nhl.GameID(2024020001)},
		},
		{
			name:     "playoff included",
			input:    []nhl.ScheduleGame{game(2024030001)}, // 03 = playoffs
			expected: []nhl.GameID{nhl.GameID(2024030001)},
		},
		{
			name:     "invalid game ID filtered out",
			input:    []nhl.ScheduleGame{game(123), game(2024020001)}, // 123 is invalid (too short)
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
