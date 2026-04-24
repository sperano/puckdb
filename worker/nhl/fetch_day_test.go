package nhl

import (
	"context"
	"encoding/json"
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
	"github.com/sperano/puckdb/worker/yahoo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
)

const (
	testFetchDayDate = "2024-01-15"
	testYahooSeason  = 2023
	testGameKey      = 423
	testLeagueID     = 12345
)

func seedGameKey() {
	yahoo.SetGameKeyCache(testYahooSeason, testGameKey)
}

func mockDownloader(content []byte, err error) shared.Downloader {
	return func(_ context.Context, _ string) ([]byte, error) {
		return content, err
	}
}

type FetchDayTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestActivityEnvironment
	day time.Time
}

func (s *FetchDayTestSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
	seedGameKey()

	var err error
	s.day, err = time.Parse("2006-01-02", testFetchDayDate)
	s.Require().NoError(err)
}

func TestFetchDayTestSuite(t *testing.T) {
	suite.Run(t, new(FetchDayTestSuite))
}

func (s *FetchDayTestSuite) newFetchDayActivities(mem *store.MemStorage, dl shared.Downloader, gobCache *cache.GobCache) *DailyScheduleActivities {
	return &DailyScheduleActivities{
		Storage:   mem,
		NHLClient: &MockNHLClient{},
		GobCache:  gobCache,
		Download:  dl,
	}
}

func (s *FetchDayTestSuite) newPermissiveGobCache() (*cache.GobCache, redismock.ClientMock) {
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)
	keyPattern := core.RedisResourceKeyPrefix + ".*"
	for range 3 {
		mockRedis.Regexp().ExpectGet(keyPattern).SetErr(redis.Nil)
		mockRedis.Regexp().CustomMatch(anyArgs).ExpectSet(keyPattern, "x", cache.GobCacheTTL).SetVal("OK")
	}
	return cache.NewGobCache(redisClient), mockRedis
}

func (s *FetchDayTestSuite) seedCachedSchedule(mem *store.MemStorage) {
	schedule := &nhlapi.DailySchedule{Games: []nhlapi.ScheduleGame{}}
	data, err := json.Marshal(schedule)
	s.Require().NoError(err)
	s.Require().NoError(mem.Write(context.Background(),resource.DailySchedule{Date: s.day}.Path(), data))
}

func (s *FetchDayTestSuite) seedCachedStandings(mem *store.MemStorage) {
	standings := []nhlapi.Standing{}
	data, err := json.Marshal(standings)
	s.Require().NoError(err)
	s.Require().NoError(mem.Write(context.Background(),resource.DailyStandings{Date: s.day}.Path(), data))
}

func (s *FetchDayTestSuite) TestNoTeams() {
	mem := store.NewMemStorage()
	s.seedCachedSchedule(mem)
	s.seedCachedStandings(mem)
	gobCache, _ := s.newPermissiveGobCache()

	a := s.newFetchDayActivities(mem, mockDownloader(nil, nil), gobCache)
	s.env.RegisterActivity(a.FetchDay)

	input := FetchDayInput{
		Day:         s.day,
		StartSeason: testYahooSeason,
		TeamIDs:     []yahoo.TeamInfo{},
	}

	future, err := s.env.ExecuteActivity(a.FetchDay, input)
	require.NoError(s.T(), err)

	var counts core.OriginCounts
	require.NoError(s.T(), future.Get(&counts))
	assert.NotNil(s.T(), counts)
}

func (s *FetchDayTestSuite) TestWithTeams() {
	mem := store.NewMemStorage()
	s.seedCachedSchedule(mem)
	s.seedCachedStandings(mem)

	teams := []yahoo.TeamInfo{
		{LeagueID: testLeagueID, TeamID: 1},
		{LeagueID: testLeagueID, TeamID: 2},
	}

	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)

	scheduleKeyPattern := core.RedisResourceKeyPrefix + ".*"
	for range 2 {
		mockRedis.Regexp().ExpectGet(scheduleKeyPattern).SetErr(redis.Nil)
		mockRedis.Regexp().CustomMatch(anyArgs).ExpectSet(scheduleKeyPattern, "x", cache.GobCacheTTL).SetVal("OK")
	}

	for _, team := range teams {
		rosterRes := resource.Roster{LeagueID: team.LeagueID, TeamID: team.TeamID, Date: s.day, GameKey: testGameKey}
		summaryRes := resource.TeamSummary{LeagueID: team.LeagueID, TeamID: team.TeamID, Date: s.day, GameKey: testGameKey}
		mockRedis.ExpectGet(core.RedisKey(rosterRes)).SetErr(redis.Nil)
		mockRedis.CustomMatch(anyArgs).ExpectSet(core.RedisKey(rosterRes), "x", cache.GobCacheTTL).SetVal("OK")
		mockRedis.ExpectGet(core.RedisKey(summaryRes)).SetErr(redis.Nil)
		mockRedis.CustomMatch(anyArgs).ExpectSet(core.RedisKey(summaryRes), "x", cache.GobCacheTTL).SetVal("OK")
	}

	validXML := []byte(`<fantasy_content><team></team></fantasy_content>`)
	a := s.newFetchDayActivities(mem, mockDownloader(validXML, nil), cache.NewGobCache(redisClient))
	s.env.RegisterActivity(a.FetchDay)

	input := FetchDayInput{
		Day:         s.day,
		StartSeason: testYahooSeason,
		TeamIDs:     teams,
	}

	future, err := s.env.ExecuteActivity(a.FetchDay, input)
	require.NoError(s.T(), err)

	var counts core.OriginCounts
	require.NoError(s.T(), future.Get(&counts))

	for _, team := range teams {
		rosterRes := resource.Roster{LeagueID: team.LeagueID, TeamID: team.TeamID, Date: s.day, GameKey: testGameKey}
		summaryRes := resource.TeamSummary{LeagueID: team.LeagueID, TeamID: team.TeamID, Date: s.day, GameKey: testGameKey}
		assert.True(s.T(), mem.Exists(context.Background(),rosterRes.Path()), "expected roster file for team %d", team.TeamID)
		assert.True(s.T(), mem.Exists(context.Background(),summaryRes.Path()), "expected summary file for team %d", team.TeamID)
	}
}

func (s *FetchDayTestSuite) TestDailyScheduleError() {
	mem := store.NewMemStorage()
	mockClient := &MockNHLClient{}
	mockClient.On("DailySchedule", mock.Anything, nhlapi.FromDate(s.day)).
		Return(nil, errors.New("schedule fetch failed"))

	gobCache, _ := s.newPermissiveGobCache()

	a := &DailyScheduleActivities{
		Storage:   mem,
		NHLClient: mockClient,
		GobCache:  gobCache,
		Download:  mockDownloader(nil, nil),
	}
	s.env.RegisterActivity(a.FetchDay)

	input := FetchDayInput{
		Day:         s.day,
		StartSeason: testYahooSeason,
		TeamIDs:     []yahoo.TeamInfo{{LeagueID: testLeagueID, TeamID: 1}},
	}

	_, err := s.env.ExecuteActivity(a.FetchDay, input)

	require.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "schedule fetch failed")
}

func (s *FetchDayTestSuite) TestRosterDownloadError() {
	mem := store.NewMemStorage()
	s.seedCachedSchedule(mem)
	s.seedCachedStandings(mem)

	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)

	scheduleKeyPattern := core.RedisResourceKeyPrefix + ".*"
	for range 2 {
		mockRedis.Regexp().ExpectGet(scheduleKeyPattern).SetErr(redis.Nil)
		mockRedis.Regexp().CustomMatch(anyArgs).ExpectSet(scheduleKeyPattern, "x", cache.GobCacheTTL).SetVal("OK")
	}

	team := yahoo.TeamInfo{LeagueID: testLeagueID, TeamID: 1}
	rosterRes := resource.Roster{LeagueID: team.LeagueID, TeamID: team.TeamID, Date: s.day, GameKey: testGameKey}
	mockRedis.ExpectGet(core.RedisKey(rosterRes)).SetErr(redis.Nil)

	a := s.newFetchDayActivities(mem, mockDownloader(nil, errors.New("roster fetch failed")), cache.NewGobCache(redisClient))
	s.env.RegisterActivity(a.FetchDay)

	input := FetchDayInput{
		Day:         s.day,
		StartSeason: testYahooSeason,
		TeamIDs:     []yahoo.TeamInfo{team},
	}

	_, err := s.env.ExecuteActivity(a.FetchDay, input)

	require.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "roster fetch failed")
}

func (s *FetchDayTestSuite) TestTeamSummaryDownloadError() {
	mem := store.NewMemStorage()
	s.seedCachedSchedule(mem)
	s.seedCachedStandings(mem)

	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)

	scheduleKeyPattern := core.RedisResourceKeyPrefix + ".*"
	for range 2 {
		mockRedis.Regexp().ExpectGet(scheduleKeyPattern).SetErr(redis.Nil)
		mockRedis.Regexp().CustomMatch(anyArgs).ExpectSet(scheduleKeyPattern, "x", cache.GobCacheTTL).SetVal("OK")
	}

	team := yahoo.TeamInfo{LeagueID: testLeagueID, TeamID: 1}
	rosterRes := resource.Roster{LeagueID: team.LeagueID, TeamID: team.TeamID, Date: s.day, GameKey: testGameKey}
	summaryRes := resource.TeamSummary{LeagueID: team.LeagueID, TeamID: team.TeamID, Date: s.day, GameKey: testGameKey}

	validXML := []byte(`<fantasy_content><team></team></fantasy_content>`)
	require.NoError(s.T(), mem.Write(context.Background(),rosterRes.Path(), validXML))
	mockRedis.ExpectGet(core.RedisKey(rosterRes)).SetErr(redis.Nil)
	mockRedis.CustomMatch(anyArgs).ExpectSet(core.RedisKey(rosterRes), "x", cache.GobCacheTTL).SetVal("OK")

	mockRedis.ExpectGet(core.RedisKey(summaryRes)).SetErr(redis.Nil)

	a := s.newFetchDayActivities(mem, mockDownloader(nil, errors.New("summary fetch failed")), cache.NewGobCache(redisClient))
	s.env.RegisterActivity(a.FetchDay)

	input := FetchDayInput{
		Day:         s.day,
		StartSeason: testYahooSeason,
		TeamIDs:     []yahoo.TeamInfo{team},
	}

	_, err := s.env.ExecuteActivity(a.FetchDay, input)

	require.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "summary fetch failed")
}

func (s *FetchDayTestSuite) TestSecondTeamRosterError() {
	mem := store.NewMemStorage()
	s.seedCachedSchedule(mem)
	s.seedCachedStandings(mem)

	teams := []yahoo.TeamInfo{
		{LeagueID: testLeagueID, TeamID: 1},
		{LeagueID: testLeagueID, TeamID: 2},
	}

	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)

	scheduleKeyPattern := core.RedisResourceKeyPrefix + ".*"
	for range 2 {
		mockRedis.Regexp().ExpectGet(scheduleKeyPattern).SetErr(redis.Nil)
		mockRedis.Regexp().CustomMatch(anyArgs).ExpectSet(scheduleKeyPattern, "x", cache.GobCacheTTL).SetVal("OK")
	}

	validXML := []byte(`<fantasy_content><team></team></fantasy_content>`)

	roster1 := resource.Roster{LeagueID: testLeagueID, TeamID: 1, Date: s.day, GameKey: testGameKey}
	summary1 := resource.TeamSummary{LeagueID: testLeagueID, TeamID: 1, Date: s.day, GameKey: testGameKey}
	require.NoError(s.T(), mem.Write(context.Background(),roster1.Path(), validXML))
	require.NoError(s.T(), mem.Write(context.Background(),summary1.Path(), validXML))
	mockRedis.ExpectGet(core.RedisKey(roster1)).SetErr(redis.Nil)
	mockRedis.CustomMatch(anyArgs).ExpectSet(core.RedisKey(roster1), "x", cache.GobCacheTTL).SetVal("OK")
	mockRedis.ExpectGet(core.RedisKey(summary1)).SetErr(redis.Nil)
	mockRedis.CustomMatch(anyArgs).ExpectSet(core.RedisKey(summary1), "x", cache.GobCacheTTL).SetVal("OK")

	roster2 := resource.Roster{LeagueID: testLeagueID, TeamID: 2, Date: s.day, GameKey: testGameKey}
	mockRedis.ExpectGet(core.RedisKey(roster2)).SetErr(redis.Nil)

	a := s.newFetchDayActivities(mem, mockDownloader(nil, errors.New("second roster failed")), cache.NewGobCache(redisClient))
	s.env.RegisterActivity(a.FetchDay)

	input := FetchDayInput{
		Day:         s.day,
		StartSeason: testYahooSeason,
		TeamIDs:     teams,
	}

	_, err := s.env.ExecuteActivity(a.FetchDay, input)

	require.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "second roster failed")
}
