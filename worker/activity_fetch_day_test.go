package worker

import (
	"encoding/json"
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

const testFetchDayDate = "2024-01-15"

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

// newFetchDayActivities builds a DailyScheduleActivities suitable for FetchDay tests.
// The NHLClient mock is used only for FetchDailySchedule; pass nil if the schedule
// will be pre-populated in MemStorage (cache-hit path).
func (s *FetchDayTestSuite) newFetchDayActivities(mem *store.MemStorage, dl Downloader, gobCache *cache.GobCache) *DailyScheduleActivities {
	return &DailyScheduleActivities{
		Storage:   mem,
		NHLClient: &MockNHLClient{},
		GobCache:  gobCache,
		Download:  dl,
	}
}

// newPermissiveGobCache returns a GobCache where Get always misses and Set always succeeds,
// accepting any key pattern. Used for tests that don't need to assert Redis interactions.
func (s *FetchDayTestSuite) newPermissiveGobCache() (*cache.GobCache, redismock.ClientMock) {
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)
	keyPattern := core.RedisResourceKeyPrefix + ".*"
	// FetchDay calls ReadParsedCached multiple times (schedule, standings),
	// each needing a Get miss + Set. Duplicate expectations to cover all calls.
	for range 3 {
		mockRedis.Regexp().ExpectGet(keyPattern).SetErr(redis.Nil)
		mockRedis.Regexp().CustomMatch(anyArgs).ExpectSet(keyPattern, "x", cache.GobCacheTTL).SetVal("OK")
	}
	return cache.NewGobCache(redisClient), mockRedis
}

// seedCachedSchedule writes an empty daily schedule into MemStorage so FetchDailySchedule
// takes the cache-hit path. This lets FetchDay tests focus on the team-fetch loop
// without also needing to mock NHLClient schedule responses.
func (s *FetchDayTestSuite) seedCachedSchedule(mem *store.MemStorage) {
	schedule := &nhl.DailySchedule{Games: []nhl.ScheduleGame{}}
	data, err := json.Marshal(schedule)
	s.Require().NoError(err)
	s.Require().NoError(mem.Write(resource.DailySchedule{Date: s.day}.Path(), data))
}

// seedCachedStandings writes an empty standings slice into MemStorage so FetchOrCache
// takes the cache-hit path, avoiding calls to NHLClient.LeagueStandingsForDate.
func (s *FetchDayTestSuite) seedCachedStandings(mem *store.MemStorage) {
	standings := []nhl.Standing{}
	data, err := json.Marshal(standings)
	s.Require().NoError(err)
	s.Require().NoError(mem.Write(resource.DailyStandings{Date: s.day}.Path(), data))
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
		TeamIDs:     []TeamInfo{},
	}

	future, err := s.env.ExecuteActivity(a.FetchDay, input)
	require.NoError(s.T(), err)

	var counts core.OriginCounts
	require.NoError(s.T(), future.Get(&counts))
	// Empty schedule returns Redis origin from the filesystem cache hit
	assert.NotNil(s.T(), counts)
}

func (s *FetchDayTestSuite) TestWithTeams() {
	mem := store.NewMemStorage()
	s.seedCachedSchedule(mem)
	s.seedCachedStandings(mem)

	teams := []TeamInfo{
		{LeagueID: testLeagueID, TeamID: 1},
		{LeagueID: testLeagueID, TeamID: 2},
	}

	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)

	// Schedule + standings: cache hit path from filesystem (Redis miss → file read → populate cache)
	scheduleKeyPattern := core.RedisResourceKeyPrefix + ".*"
	for range 2 {
		mockRedis.Regexp().ExpectGet(scheduleKeyPattern).SetErr(redis.Nil)
		mockRedis.Regexp().CustomMatch(anyArgs).ExpectSet(scheduleKeyPattern, "x", cache.GobCacheTTL).SetVal("OK")
	}

	// Each team: Roster miss + TeamSummary miss (Redis miss for both, then Set after download)
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

	// Verify roster and summary files were saved for each team
	for _, team := range teams {
		rosterRes := resource.Roster{LeagueID: team.LeagueID, TeamID: team.TeamID, Date: s.day, GameKey: testGameKey}
		summaryRes := resource.TeamSummary{LeagueID: team.LeagueID, TeamID: team.TeamID, Date: s.day, GameKey: testGameKey}
		assert.True(s.T(), mem.Exists(rosterRes.Path()), "expected roster file for team %d", team.TeamID)
		assert.True(s.T(), mem.Exists(summaryRes.Path()), "expected summary file for team %d", team.TeamID)
	}
}

func (s *FetchDayTestSuite) TestDailyScheduleError() {
	mem := store.NewMemStorage()
	// No cached schedule — NHLClient will be called and will fail
	mockClient := &MockNHLClient{}
	mockClient.On("DailySchedule", mock.Anything, nhl.FromDate(s.day)).
		Return(nil, errors.New("schedule fetch failed"))

	gobCache, _ := s.newPermissiveGobCache()

	a := &DailyScheduleActivities{
		Storage:       mem,
		NHLClient:     mockClient,
		GobCache:  gobCache,
		Download:      mockDownloader(nil, nil),
	}
	s.env.RegisterActivity(a.FetchDay)

	input := FetchDayInput{
		Day:         s.day,
		StartSeason: testYahooSeason,
		TeamIDs:     []TeamInfo{{LeagueID: testLeagueID, TeamID: 1}},
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

	// Schedule + standings: cache hit path from filesystem
	scheduleKeyPattern := core.RedisResourceKeyPrefix + ".*"
	for range 2 {
		mockRedis.Regexp().ExpectGet(scheduleKeyPattern).SetErr(redis.Nil)
		mockRedis.Regexp().CustomMatch(anyArgs).ExpectSet(scheduleKeyPattern, "x", cache.GobCacheTTL).SetVal("OK")
	}

	team := TeamInfo{LeagueID: testLeagueID, TeamID: 1}
	rosterRes := resource.Roster{LeagueID: team.LeagueID, TeamID: team.TeamID, Date: s.day, GameKey: testGameKey}
	mockRedis.ExpectGet(core.RedisKey(rosterRes)).SetErr(redis.Nil)

	a := s.newFetchDayActivities(mem, mockDownloader(nil, errors.New("roster fetch failed")), cache.NewGobCache(redisClient))
	s.env.RegisterActivity(a.FetchDay)

	input := FetchDayInput{
		Day:         s.day,
		StartSeason: testYahooSeason,
		TeamIDs:     []TeamInfo{team},
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

	// Schedule + standings: cache hit path from filesystem
	scheduleKeyPattern := core.RedisResourceKeyPrefix + ".*"
	for range 2 {
		mockRedis.Regexp().ExpectGet(scheduleKeyPattern).SetErr(redis.Nil)
		mockRedis.Regexp().CustomMatch(anyArgs).ExpectSet(scheduleKeyPattern, "x", cache.GobCacheTTL).SetVal("OK")
	}

	team := TeamInfo{LeagueID: testLeagueID, TeamID: 1}
	rosterRes := resource.Roster{LeagueID: team.LeagueID, TeamID: team.TeamID, Date: s.day, GameKey: testGameKey}
	summaryRes := resource.TeamSummary{LeagueID: team.LeagueID, TeamID: team.TeamID, Date: s.day, GameKey: testGameKey}

	// Roster succeeds (pre-populate file for cache hit)
	validXML := []byte(`<fantasy_content><team></team></fantasy_content>`)
	require.NoError(s.T(), mem.Write(rosterRes.Path(), validXML))
	mockRedis.ExpectGet(core.RedisKey(rosterRes)).SetErr(redis.Nil)
	mockRedis.CustomMatch(anyArgs).ExpectSet(core.RedisKey(rosterRes), "x", cache.GobCacheTTL).SetVal("OK")

	// TeamSummary misses and download fails
	mockRedis.ExpectGet(core.RedisKey(summaryRes)).SetErr(redis.Nil)

	a := s.newFetchDayActivities(mem, mockDownloader(nil, errors.New("summary fetch failed")), cache.NewGobCache(redisClient))
	s.env.RegisterActivity(a.FetchDay)

	input := FetchDayInput{
		Day:         s.day,
		StartSeason: testYahooSeason,
		TeamIDs:     []TeamInfo{team},
	}

	_, err := s.env.ExecuteActivity(a.FetchDay, input)

	require.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "summary fetch failed")
}

func (s *FetchDayTestSuite) TestSecondTeamRosterError() {
	mem := store.NewMemStorage()
	s.seedCachedSchedule(mem)
	s.seedCachedStandings(mem)

	teams := []TeamInfo{
		{LeagueID: testLeagueID, TeamID: 1},
		{LeagueID: testLeagueID, TeamID: 2},
	}

	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)

	// Schedule + standings: cache hit path from filesystem
	scheduleKeyPattern := core.RedisResourceKeyPrefix + ".*"
	for range 2 {
		mockRedis.Regexp().ExpectGet(scheduleKeyPattern).SetErr(redis.Nil)
		mockRedis.Regexp().CustomMatch(anyArgs).ExpectSet(scheduleKeyPattern, "x", cache.GobCacheTTL).SetVal("OK")
	}

	validXML := []byte(`<fantasy_content><team></team></fantasy_content>`)

	// Team 1: both succeed (pre-populate files for cache hits)
	roster1 := resource.Roster{LeagueID: testLeagueID, TeamID: 1, Date: s.day, GameKey: testGameKey}
	summary1 := resource.TeamSummary{LeagueID: testLeagueID, TeamID: 1, Date: s.day, GameKey: testGameKey}
	require.NoError(s.T(), mem.Write(roster1.Path(), validXML))
	require.NoError(s.T(), mem.Write(summary1.Path(), validXML))
	mockRedis.ExpectGet(core.RedisKey(roster1)).SetErr(redis.Nil)
	mockRedis.CustomMatch(anyArgs).ExpectSet(core.RedisKey(roster1), "x", cache.GobCacheTTL).SetVal("OK")
	mockRedis.ExpectGet(core.RedisKey(summary1)).SetErr(redis.Nil)
	mockRedis.CustomMatch(anyArgs).ExpectSet(core.RedisKey(summary1), "x", cache.GobCacheTTL).SetVal("OK")

	// Team 2: roster misses and download fails
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
