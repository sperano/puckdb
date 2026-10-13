package nhl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/go-redis/redismock/v8"
	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/sperano/puckdb/internal/worker/yahoo"
	"github.com/spf13/viper"
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

// downloaderFailingFor returns validXML for every URL except failURL, which
// fails with err.
func downloaderFailingFor(failURL string, validXML []byte, err error) shared.Downloader {
	return func(_ context.Context, url string) ([]byte, error) {
		if url == failURL {
			return nil, err
		}
		return validXML, nil
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
	s.Require().NoError(mem.Write(context.Background(), resource.DailySchedule{Date: s.day}.Path(), data))
}

func (s *FetchDayTestSuite) seedCachedStandings(mem *store.MemStorage) {
	standings := []nhlapi.Standing{}
	data, err := json.Marshal(standings)
	s.Require().NoError(err)
	s.Require().NoError(mem.Write(context.Background(), resource.DailyStandings{Date: s.day}.Path(), data))
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
		assert.True(s.T(), mem.Exists(context.Background(), rosterRes.Path()), "expected roster file for team %d", team.TeamID)
		assert.True(s.T(), mem.Exists(context.Background(), summaryRes.Path()), "expected summary file for team %d", team.TeamID)
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
	summaryRes := resource.TeamSummary{LeagueID: team.LeagueID, TeamID: team.TeamID, Date: s.day, GameKey: testGameKey}
	mockRedis.ExpectGet(core.RedisKey(rosterRes)).SetErr(redis.Nil)
	// The team summary downloads alongside the roster.
	mockRedis.ExpectGet(core.RedisKey(summaryRes)).SetErr(redis.Nil)
	mockRedis.CustomMatch(anyArgs).ExpectSet(core.RedisKey(summaryRes), "x", cache.GobCacheTTL).SetVal("OK")

	validXML := []byte(`<fantasy_content><team></team></fantasy_content>`)
	dl := downloaderFailingFor(rosterRes.URL(), validXML, errors.New("roster fetch failed"))
	a := s.newFetchDayActivities(mem, dl, cache.NewGobCache(redisClient))
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
	require.NoError(s.T(), mem.Write(context.Background(), rosterRes.Path(), validXML))
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
	require.NoError(s.T(), mem.Write(context.Background(), roster1.Path(), validXML))
	require.NoError(s.T(), mem.Write(context.Background(), summary1.Path(), validXML))
	mockRedis.ExpectGet(core.RedisKey(roster1)).SetErr(redis.Nil)
	mockRedis.CustomMatch(anyArgs).ExpectSet(core.RedisKey(roster1), "x", cache.GobCacheTTL).SetVal("OK")
	mockRedis.ExpectGet(core.RedisKey(summary1)).SetErr(redis.Nil)
	mockRedis.CustomMatch(anyArgs).ExpectSet(core.RedisKey(summary1), "x", cache.GobCacheTTL).SetVal("OK")

	roster2 := resource.Roster{LeagueID: testLeagueID, TeamID: 2, Date: s.day, GameKey: testGameKey}
	summary2 := resource.TeamSummary{LeagueID: testLeagueID, TeamID: 2, Date: s.day, GameKey: testGameKey}
	mockRedis.ExpectGet(core.RedisKey(roster2)).SetErr(redis.Nil)
	// Team 2's summary downloads alongside its roster.
	mockRedis.ExpectGet(core.RedisKey(summary2)).SetErr(redis.Nil)
	mockRedis.CustomMatch(anyArgs).ExpectSet(core.RedisKey(summary2), "x", cache.GobCacheTTL).SetVal("OK")

	dl := downloaderFailingFor(roster2.URL(), validXML, errors.New("second roster failed"))
	a := s.newFetchDayActivities(mem, dl, cache.NewGobCache(redisClient))
	s.env.RegisterActivity(a.FetchDay)

	input := FetchDayInput{
		Day:         s.day,
		StartSeason: testYahooSeason,
		TeamIDs:     teams,
	}

	_, err := s.env.ExecuteActivity(a.FetchDay, input)

	require.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "second roster failed")
	assert.Contains(s.T(), err.Error(), fmt.Sprintf("league %d team 2", testLeagueID))
}

const (
	// testYahooTeamConcurrency is the Yahoo team download limit the
	// concurrency tests set; testOverlapTeams teams give twice as many
	// downloads, so the limit is reached.
	testYahooTeamConcurrency = 3
	testOverlapTeams         = 3
	// testOverlapWait bounds how long a fake download waits for the others to
	// start; a sequential FetchDay would wait this long on every download.
	testOverlapWait = 5 * time.Second
	// testDownloadLatency and testRateLimitPerSecond make one download slow
	// next to the rate limiter's spacing (10 ms), so FetchDay's duration
	// shows whether the limiter or the loop paces the downloads.
	testDownloadLatency    = 200 * time.Millisecond
	testRateLimitPerSecond = 100
	testFailingTeamID      = 2
)

var testTeamXML = []byte(`<fantasy_content><team></team></fantasy_content>`)

// setYahooTeamConcurrency sets --yahoo-team-download-concurrency for one test.
func setYahooTeamConcurrency(t *testing.T, n int) {
	t.Helper()
	viper.Set(config.FlagYahooTeamDownloadConcurrency, n)
	t.Cleanup(func() { viper.Set(config.FlagYahooTeamDownloadConcurrency, 0) })
}

func testTeams(n int) []yahoo.TeamInfo {
	teams := make([]yahoo.TeamInfo, n)
	for i := range teams {
		teams[i] = yahoo.TeamInfo{LeagueID: testLeagueID, TeamID: i + 1}
	}
	return teams
}

// overlapDownloader is a fake Yahoo client whose downloads block until limit
// of them are in flight at once (or testOverlapWait passes), recording the
// highest number seen in flight.
type overlapDownloader struct {
	limit       int64
	inFlight    atomic.Int64
	maxInFlight atomic.Int64
	reached     chan struct{}
	once        sync.Once
}

func newOverlapDownloader(limit int) *overlapDownloader {
	return &overlapDownloader{limit: int64(limit), reached: make(chan struct{})}
}

func (d *overlapDownloader) download(ctx context.Context, _ string) ([]byte, error) {
	n := d.inFlight.Add(1)
	defer d.inFlight.Add(-1)
	for {
		m := d.maxInFlight.Load()
		if n <= m || d.maxInFlight.CompareAndSwap(m, n) {
			break
		}
	}
	if n >= d.limit {
		d.once.Do(func() { close(d.reached) })
	}
	select {
	case <-d.reached:
	case <-time.After(testOverlapWait):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return testTeamXML, nil
}

func (s *FetchDayTestSuite) newSeededMemStorage() *store.MemStorage {
	mem := store.NewMemStorage()
	s.seedCachedSchedule(mem)
	s.seedCachedStandings(mem)
	return mem
}

func (s *FetchDayTestSuite) TestYahooTeamDownloadsOverlap() {
	setYahooTeamConcurrency(s.T(), testYahooTeamConcurrency)
	mem := s.newSeededMemStorage()
	dl := newOverlapDownloader(testYahooTeamConcurrency)
	a := s.newFetchDayActivities(mem, dl.download, cache.NewGobCache(nil))
	s.env.RegisterActivity(a.FetchDay)
	teams := testTeams(testOverlapTeams)

	_, err := s.env.ExecuteActivity(a.FetchDay, FetchDayInput{Day: s.day, StartSeason: testYahooSeason, TeamIDs: teams})
	s.Require().NoError(err)

	s.Equal(int64(testYahooTeamConcurrency), dl.maxInFlight.Load(), "downloads of one day should overlap up to the limit")
	for _, team := range teams {
		for _, res := range yahooTeamResources(team, s.day, testGameKey) {
			s.True(mem.Exists(context.Background(), res.Path()), "expected %s", res.Path())
		}
	}
}

func (s *FetchDayTestSuite) TestYahooTeamFailureNamesTeam() {
	setYahooTeamConcurrency(s.T(), testYahooTeamConcurrency)
	failing := resource.Roster{LeagueID: testLeagueID, TeamID: testFailingTeamID, Date: s.day, GameKey: testGameKey}
	dl := func(_ context.Context, url string) ([]byte, error) {
		if url == failing.URL() {
			return nil, errors.New("roster fetch failed")
		}
		return testTeamXML, nil
	}
	a := s.newFetchDayActivities(s.newSeededMemStorage(), dl, cache.NewGobCache(nil))
	s.env.RegisterActivity(a.FetchDay)

	_, err := s.env.ExecuteActivity(a.FetchDay, FetchDayInput{Day: s.day, StartSeason: testYahooSeason, TeamIDs: testTeams(testOverlapTeams)})

	s.Require().Error(err)
	s.Contains(err.Error(), fmt.Sprintf("league %d team %d", testLeagueID, testFailingTeamID))
	s.Contains(err.Error(), "roster fetch failed")
}

// TestYahooTeamDownloadsPacedByRateLimiter checks that, behind the shared
// rate limiter, a day's downloads take about one download's latency plus the
// limiter's spacing instead of the sum of every download's latency.
func (s *FetchDayTestSuite) TestYahooTeamDownloadsPacedByRateLimiter() {
	teams := testTeams(testOverlapTeams)
	downloads := len(teams) * len(yahooTeamResources(teams[0], s.day, testGameKey))
	setYahooTeamConcurrency(s.T(), downloads)
	limiter, err := shared.NewYahooRateLimiter(shared.YahooRateLimit{PerSecond: testRateLimitPerSecond, Burst: 1})
	s.Require().NoError(err)
	slow := func(ctx context.Context, _ string) ([]byte, error) {
		select {
		case <-time.After(testDownloadLatency):
			return testTeamXML, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	a := s.newFetchDayActivities(s.newSeededMemStorage(), shared.RateLimited(limiter, slow), cache.NewGobCache(nil))
	s.env.RegisterActivity(a.FetchDay)

	start := time.Now()
	_, err = s.env.ExecuteActivity(a.FetchDay, FetchDayInput{Day: s.day, StartSeason: testYahooSeason, TeamIDs: teams})
	elapsed := time.Since(start)

	s.Require().NoError(err)
	sequential := time.Duration(downloads) * testDownloadLatency
	s.Less(elapsed, sequential/2, "a day's %d downloads should not run one after another", downloads)
}

func TestFetchYahooTeamsReportsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var calls atomic.Int64
	dl := func(context.Context, string) ([]byte, error) {
		calls.Add(1)
		return testTeamXML, nil
	}
	a := &DailyScheduleActivities{Storage: store.NewMemStorage(), GobCache: cache.NewGobCache(nil), Download: dl}

	err := a.fetchYahooTeams(ctx, time.Now(), testGameKey, testTeams(testOverlapTeams))

	require.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, calls.Load(), "no download should start once the activity is canceled")
}

func TestGetYahooTeamDownloadConcurrency(t *testing.T) {
	setYahooTeamConcurrency(t, 0)
	assert.Equal(t, config.DefaultYahooTeamDownloadConcurrency, getYahooTeamDownloadConcurrency(), "unset uses the default")
	setYahooTeamConcurrency(t, testYahooTeamConcurrency)
	assert.Equal(t, testYahooTeamConcurrency, getYahooTeamDownloadConcurrency())
}
