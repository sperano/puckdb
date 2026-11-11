package nhl

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/go-redis/redismock/v8"
	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/shared"
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
	require.NoError(s.T(), resource.WriteParsed(context.Background(), mem, resource.DailySchedule{Date: day}, schedule))

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

	assert.True(s.T(), mem.Exists(context.Background(), resource.DailySchedule{Date: day}.Path()))
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
	require.NoError(s.T(), resource.WriteParsed(context.Background(), mem, resource.DailySchedule{Date: day}, schedule))
	// The live game makes the cached schedule pending: it is downloaded
	// again, and the game is still live, so none of its files is fetched.
	mockClient.On("DailySchedule", mock.Anything, nhlapi.FromDate(day)).Return(schedule, nil).Once()

	seedAllGameData(mem, day, nhlapi.GameID(2024020002))

	a := s.newActivities(mem, mockClient)
	s.env.RegisterActivity(a.FetchDailySchedule)
	_, err := s.env.ExecuteActivity(a.FetchDailySchedule, day)

	assert.NoError(s.T(), err)
	mockClient.AssertExpectations(s.T())
	assert.False(s.T(), mem.Has(resource.Boxscore{Date: day, GameID: nhlapi.GameID(2024020001)}.Path()))
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
	require.NoError(s.T(), resource.WriteParsed(context.Background(), mem, resource.DailySchedule{Date: day}, schedule))

	seedAllGameData(mem, day, nhlapi.GameID(2024020001))
	seedAllGameData(mem, day, nhlapi.GameID(2024020002))

	a := s.newActivities(mem, mockClient)
	s.env.RegisterActivity(a.FetchDailySchedule)
	_, err := s.env.ExecuteActivity(a.FetchDailySchedule, day)

	assert.NoError(s.T(), err)
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

// assertGameFilesCached fails unless every cached file of gameID on day exists.
func assertGameFilesCached(t *testing.T, mem *store.MemStorage, day time.Time, gameID nhlapi.GameID) {
	t.Helper()
	for _, r := range []core.Resource{
		resource.Boxscore{Date: day, GameID: gameID},
		resource.PlayByPlay{Date: day, GameID: gameID},
		resource.ShiftChart{Date: day, GameID: gameID},
		resource.GameStory{Date: day, GameID: gameID},
		resource.SeasonSeries{Date: day, GameID: gameID},
	} {
		assert.True(t, mem.Has(r.Path()), "missing %s", r.Path())
	}
}

// expectGameDownloads makes every game file download of gameID succeed once.
func expectGameDownloads(client *MockNHLClient, gameID nhlapi.GameID) {
	client.On("Boxscore", mock.Anything, gameID).Return(nhlapi.FixtureBoxscore(), nil).Once()
	client.On("PlayByPlay", mock.Anything, gameID).Return(nhlapi.FixturePlayByPlay(), nil).Once()
	client.On("ShiftChart", mock.Anything, gameID).Return(nhlapi.FixtureShiftChart(), nil).Once()
	client.On("GameStory", mock.Anything, gameID).Return(nhlapi.FixtureGameStory(), nil).Once()
	client.On("SeasonSeries", mock.Anything, gameID).Return(nhlapi.FixtureSeasonSeriesMatchup(), nil).Once()
}

// An attempt that writes the schedule and then fails one game download must
// not leave the retry, which finds the schedule cached, without that file.
func (s *DailyScheduleTestSuite) TestRetryAfterPartialFailureFetchesMissingGameFiles() {
	mem := store.NewMemStorage()
	mockClient := &MockNHLClient{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	gameID := nhlapi.GameID(2024020001)

	schedule := &nhlapi.DailySchedule{
		Games: []nhlapi.ScheduleGame{
			{ID: gameID, GameType: nhlapi.GameTypeRegularSeason, GameState: nhlapi.GameStateFinal},
		},
	}
	mockClient.On("DailySchedule", mock.Anything, nhlapi.FromDate(day)).Return(schedule, nil).Once()
	mockClient.On("Boxscore", mock.Anything, gameID).Return(nil, errors.New("API down")).Once()
	mockClient.On("PlayByPlay", mock.Anything, gameID).Return(nhlapi.FixturePlayByPlay(), nil).Once()
	mockClient.On("ShiftChart", mock.Anything, gameID).Return(nhlapi.FixtureShiftChart(), nil).Once()
	mockClient.On("GameStory", mock.Anything, gameID).Return(nhlapi.FixtureGameStory(), nil).Once()
	mockClient.On("SeasonSeries", mock.Anything, gameID).Return(nhlapi.FixtureSeasonSeriesMatchup(), nil).Once()

	a := s.newActivities(mem, mockClient)
	s.env.RegisterActivity(a.FetchDailySchedule)
	_, err := s.env.ExecuteActivity(a.FetchDailySchedule, day)
	require.ErrorContains(s.T(), err, "API down")
	require.True(s.T(), mem.Has(resource.DailySchedule{Date: day}.Path()), "the failed attempt must have cached the schedule")
	require.False(s.T(), mem.Has(resource.Boxscore{Date: day, GameID: gameID}.Path()))

	// The retry downloads only the missing boxscore: the schedule and the
	// other files come from the cache (each expectation above is Once).
	mockClient.On("Boxscore", mock.Anything, gameID).Return(nhlapi.FixtureBoxscore(), nil).Once()
	future, err := s.env.ExecuteActivity(a.FetchDailySchedule, day)
	require.NoError(s.T(), err)
	var result FetchDailyScheduleResult
	require.NoError(s.T(), future.Get(&result))
	assert.Equal(s.T(), core.OriginFileSystem, result.Origin)

	mockClient.AssertExpectations(s.T())
	assertGameFilesCached(s.T(), mem, day, gameID)
}

// A schedule cached before its game ended is downloaded again, and the game's
// files are fetched once the new schedule shows it final.
func (s *DailyScheduleTestSuite) TestPendingCachedScheduleIsDownloadedAgain() {
	mem := store.NewMemStorage()
	mockClient := &MockNHLClient{}
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	gameID := nhlapi.GameID(2024020001)

	pending := &nhlapi.DailySchedule{
		Games: []nhlapi.ScheduleGame{
			{ID: gameID, GameType: nhlapi.GameTypeRegularSeason, GameState: nhlapi.GameStateFuture},
		},
	}
	require.NoError(s.T(), resource.WriteParsed(context.Background(), mem, resource.DailySchedule{Date: day}, pending))

	final := &nhlapi.DailySchedule{
		Games: []nhlapi.ScheduleGame{
			{ID: gameID, GameType: nhlapi.GameTypeRegularSeason, GameState: nhlapi.GameStateOff},
		},
	}
	mockClient.On("DailySchedule", mock.Anything, nhlapi.FromDate(day)).Return(final, nil).Once()
	expectGameDownloads(mockClient, gameID)

	a := s.newActivities(mem, mockClient)
	s.env.RegisterActivity(a.FetchDailySchedule)
	future, err := s.env.ExecuteActivity(a.FetchDailySchedule, day)
	require.NoError(s.T(), err)
	var result FetchDailyScheduleResult
	require.NoError(s.T(), future.Get(&result))
	assert.Equal(s.T(), core.OriginRemoteNHLAPI, result.Origin)

	mockClient.AssertExpectations(s.T())
	assertGameFilesCached(s.T(), mem, day, gameID)
	cached, err := resource.ReadParsed(context.Background(), mem, resource.DailySchedule{Date: day})
	require.NoError(s.T(), err)
	assert.Equal(s.T(), nhlapi.GameStateOff, cached.Games[0].GameState, "the final schedule must replace the pending one")
}

// A cached schedule whose only non-final games are preseason games or have
// invalid IDs is not pending: it is not downloaded again.
func TestHasPendingGame(t *testing.T) {
	game := func(id int, state nhlapi.GameState) nhlapi.ScheduleGame {
		return nhlapi.ScheduleGame{ID: nhlapi.GameID(id), GameState: state}
	}
	tests := []struct {
		name  string
		games []nhlapi.ScheduleGame
		want  bool
	}{
		{"no games", nil, false},
		{"all final", []nhlapi.ScheduleGame{game(2024020001, nhlapi.GameStateFinal), game(2024020002, nhlapi.GameStateOff)}, false},
		{"future regular season", []nhlapi.ScheduleGame{game(2024020001, nhlapi.GameStateFinal), game(2024020002, nhlapi.GameStateFuture)}, true},
		{"live playoff", []nhlapi.ScheduleGame{game(2024030001, nhlapi.GameStateLive)}, true},
		{"future preseason", []nhlapi.ScheduleGame{game(2024010001, nhlapi.GameStateFuture)}, false},
		{"future invalid ID", []nhlapi.ScheduleGame{game(123, nhlapi.GameStateFuture)}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, hasPendingGame(tt.games))
		})
	}
}
