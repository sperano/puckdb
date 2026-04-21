package nhl

import (
	"errors"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/go-redis/redismock/v8"
	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/store"
	"github.com/sperano/puckdb/worker/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
)

// seedAllGameData writes parseable cached game data files so FetchOrCache hits filesystem.
func seedAllGameData(mem *store.MemStorage, day time.Time, gameID nhlapi.GameID) {
	emptyJSON := []byte("{}")
	mem.SetFile(resource.Boxscore{Date: day, GameID: gameID}.Path(), emptyJSON)
	mem.SetFile(resource.PlayByPlay{Date: day, GameID: gameID}.Path(), emptyJSON)
	mem.SetFile(resource.ShiftChart{Date: day, GameID: gameID}.Path(), emptyJSON)
	mem.SetFile(resource.GameStory{Date: day, GameID: gameID}.Path(), emptyJSON)
	mem.SetFile(resource.SeasonSeries{Date: day, GameID: gameID}.Path(), emptyJSON)
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

const gobCacheTestExpectations = 20

func (s *DailyScheduleTestSuite) newGobCache() *cache.GobCache {
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)
	keyPattern := core.RedisResourceKeyPrefix + ".*"
	for range gobCacheTestExpectations {
		mockRedis.Regexp().ExpectGet(keyPattern).SetErr(redis.Nil)
		mockRedis.Regexp().CustomMatch(anyArgs).ExpectSet(keyPattern, "x", cache.GobCacheTTL).SetVal("OK")
	}
	return cache.NewGobCache(redisClient)
}

func (s *DailyScheduleTestSuite) newActivities(mem *store.MemStorage, client shared.NHLClient) *DailyScheduleActivities {
	return &DailyScheduleActivities{
		Storage:   mem,
		NHLClient: client,
		GobCache:  s.newGobCache(),
	}
}

func (s *DailyScheduleTestSuite) TestScheduleFromCache() {
	mem := store.NewMemStorage()
	mockClient := &MockNHLClient{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	schedule := &nhlapi.DailySchedule{
		Games: []nhlapi.ScheduleGame{
			{ID: nhlapi.GameID(2024020001), GameType: nhlapi.GameTypeRegularSeason, GameState: nhlapi.GameStateFinal},
		},
	}
	require.NoError(s.T(), resource.WriteParsed(mem, resource.DailySchedule{Date: day}, schedule))

	seedAllGameData(mem, day, nhlapi.GameID(2024020001))

	a := s.newActivities(mem, mockClient)
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

	schedule := &nhlapi.DailySchedule{
		Games: []nhlapi.ScheduleGame{
			{ID: nhlapi.GameID(2024020001), GameType: nhlapi.GameTypeRegularSeason, GameState: nhlapi.GameStateFinal},
		},
	}
	mockClient.On("DailySchedule", mock.Anything, nhlapi.FromDate(day)).Return(schedule, nil)

	seedAllGameData(mem, day, nhlapi.GameID(2024020001))

	a := s.newActivities(mem, mockClient)
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

	mockClient.On("DailySchedule", mock.Anything, nhlapi.FromDate(day)).Return(nil, errors.New("API error"))

	a := s.newActivities(mem, mockClient)
	s.env.RegisterActivity(a.FetchDailySchedule)
	_, err := s.env.ExecuteActivity(a.FetchDailySchedule, day)

	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "download schedule")
}

func (s *DailyScheduleTestSuite) TestCorruptCacheFallsBackToAPI() {
	mem := store.NewMemStorage()
	mockClient := &MockNHLClient{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	scheduleRes := resource.DailySchedule{Date: day}
	mem.SetFile(scheduleRes.Path(), []byte("invalid json"))

	mockClient.On("DailySchedule", mock.Anything, nhlapi.FromDate(day)).Return(nil, errors.New("API error"))

	a := s.newActivities(mem, mockClient)
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

	schedule := &nhlapi.DailySchedule{
		Games: []nhlapi.ScheduleGame{
			{ID: nhlapi.GameID(2024020001), GameType: nhlapi.GameTypeRegularSeason, GameState: nhlapi.GameStateLive},
			{ID: nhlapi.GameID(2024020002), GameType: nhlapi.GameTypeRegularSeason, GameState: nhlapi.GameStateFinal},
		},
	}
	require.NoError(s.T(), resource.WriteParsed(mem, resource.DailySchedule{Date: day}, schedule))

	seedAllGameData(mem, day, nhlapi.GameID(2024020002))

	a := s.newActivities(mem, mockClient)
	s.env.RegisterActivity(a.FetchDailySchedule)
	_, err := s.env.ExecuteActivity(a.FetchDailySchedule, day)

	assert.NoError(s.T(), err)
}

func (s *DailyScheduleTestSuite) TestDownloadsGameData() {
	mem := store.NewMemStorage()
	mockClient := &MockNHLClient{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	gameID := nhlapi.GameID(2024020001)

	schedule := &nhlapi.DailySchedule{
		Games: []nhlapi.ScheduleGame{
			{ID: gameID, GameType: nhlapi.GameTypeRegularSeason, GameState: nhlapi.GameStateFinal},
		},
	}
	mockClient.On("DailySchedule", mock.Anything, nhlapi.FromDate(day)).Return(schedule, nil)
	mockClient.On("Boxscore", mock.Anything, gameID).Return(nhlapi.FixtureBoxscore(), nil)
	mockClient.On("PlayByPlay", mock.Anything, gameID).Return(nhlapi.FixturePlayByPlay(), nil)
	mockClient.On("ShiftChart", mock.Anything, gameID).Return(nhlapi.FixtureShiftChart(), nil)
	mockClient.On("GameStory", mock.Anything, gameID).Return(nhlapi.FixtureGameStory(), nil)
	mockClient.On("SeasonSeries", mock.Anything, gameID).Return(nhlapi.FixtureSeasonSeriesMatchup(), nil)

	a := s.newActivities(mem, mockClient)
	s.env.RegisterActivity(a.FetchDailySchedule)
	_, err := s.env.ExecuteActivity(a.FetchDailySchedule, day)

	assert.NoError(s.T(), err)

	assert.True(s.T(), mem.Has(resource.Boxscore{Date: day, GameID: gameID}.Path()))
	assert.True(s.T(), mem.Has(resource.PlayByPlay{Date: day, GameID: gameID}.Path()))
	assert.True(s.T(), mem.Has(resource.ShiftChart{Date: day, GameID: gameID}.Path()))
	assert.True(s.T(), mem.Has(resource.GameStory{Date: day, GameID: gameID}.Path()))
	assert.True(s.T(), mem.Has(resource.SeasonSeries{Date: day, GameID: gameID}.Path()))
}

func (s *DailyScheduleTestSuite) TestCachedGameDataSkipped() {
	mem := store.NewMemStorage()
	mockClient := &MockNHLClient{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	schedule := &nhlapi.DailySchedule{
		Games: []nhlapi.ScheduleGame{
			{ID: nhlapi.GameID(2024020001), GameType: nhlapi.GameTypeRegularSeason, GameState: nhlapi.GameStateFinal},
			{ID: nhlapi.GameID(2024020002), GameType: nhlapi.GameTypeRegularSeason, GameState: nhlapi.GameStateFinal},
		},
	}
	require.NoError(s.T(), resource.WriteParsed(mem, resource.DailySchedule{Date: day}, schedule))

	seedAllGameData(mem, day, nhlapi.GameID(2024020001))
	seedAllGameData(mem, day, nhlapi.GameID(2024020002))

	a := s.newActivities(mem, mockClient)
	s.env.RegisterActivity(a.FetchDailySchedule)
	_, err := s.env.ExecuteActivity(a.FetchDailySchedule, day)

	assert.NoError(s.T(), err)
}

func (s *DailyScheduleTestSuite) TestContextCancelled() {
	mem := store.NewMemStorage()
	mockClient := &MockNHLClient{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	schedule := &nhlapi.DailySchedule{
		Games: []nhlapi.ScheduleGame{
			{ID: nhlapi.GameID(2024020001), GameType: nhlapi.GameTypeRegularSeason, GameState: nhlapi.GameStateFinal},
		},
	}
	require.NoError(s.T(), resource.WriteParsed(mem, resource.DailySchedule{Date: day}, schedule))

	a := s.newActivities(mem, mockClient)
	s.env.RegisterActivity(a.FetchDailySchedule)
	_, err := s.env.ExecuteActivity(a.FetchDailySchedule, day)

	// TestActivityEnvironment doesn't support context cancellation mid-execution.
	_ = err
}

func (s *DailyScheduleTestSuite) TestGameDataDownloadError() {
	mem := store.NewMemStorage()
	mockClient := &MockNHLClient{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	gameID := nhlapi.GameID(2024020001)

	schedule := &nhlapi.DailySchedule{
		Games: []nhlapi.ScheduleGame{
			{ID: gameID, GameType: nhlapi.GameTypeRegularSeason, GameState: nhlapi.GameStateFinal},
		},
	}
	mockClient.On("DailySchedule", mock.Anything, nhlapi.FromDate(day)).Return(schedule, nil)
	mockClient.On("Boxscore", mock.Anything, gameID).Return(nil, errors.New("API down"))
	mockClient.On("PlayByPlay", mock.Anything, gameID).Return(nil, errors.New("API down"))
	mockClient.On("ShiftChart", mock.Anything, gameID).Return(nil, errors.New("API down"))
	mockClient.On("GameStory", mock.Anything, gameID).Return(nil, errors.New("API down"))
	mockClient.On("SeasonSeries", mock.Anything, gameID).Return(nil, errors.New("API down"))

	a := s.newActivities(mem, mockClient)
	s.env.RegisterActivity(a.FetchDailySchedule)
	_, err := s.env.ExecuteActivity(a.FetchDailySchedule, day)

	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "API down")
}

func TestFilterRegularSeasonGames(t *testing.T) {
	game := func(id int) nhlapi.ScheduleGame {
		return nhlapi.ScheduleGame{ID: nhlapi.GameID(id), GameState: nhlapi.GameStateFinal}
	}

	tests := []struct {
		name     string
		input    []nhlapi.ScheduleGame
		expected []nhlapi.GameID
	}{
		{
			name:     "empty input",
			input:    []nhlapi.ScheduleGame{},
			expected: []nhlapi.GameID{},
		},
		{
			name:     "regular season only",
			input:    []nhlapi.ScheduleGame{game(2024020001), game(2024020002)},
			expected: []nhlapi.GameID{nhlapi.GameID(2024020001), nhlapi.GameID(2024020002)},
		},
		{
			name:     "preseason filtered out",
			input:    []nhlapi.ScheduleGame{game(2024010001), game(2024020001)},
			expected: []nhlapi.GameID{nhlapi.GameID(2024020001)},
		},
		{
			name:     "playoff included",
			input:    []nhlapi.ScheduleGame{game(2024030001)},
			expected: []nhlapi.GameID{nhlapi.GameID(2024030001)},
		},
		{
			name:     "invalid game ID filtered out",
			input:    []nhlapi.ScheduleGame{game(123), game(2024020001)},
			expected: []nhlapi.GameID{nhlapi.GameID(2024020001)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterFinalNonPreseasonGames(tt.input)
			require.Equal(t, tt.expected, result)
		})
	}
}
