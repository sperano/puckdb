package yahoo

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"net/http"
	"testing"

	"github.com/go-redis/redis/v8"
	"github.com/go-redis/redismock/v8"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/httpx"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
)

const (
	testYahooSeason = 2023
	testGameKey     = 423
	testLeagueID    = 12345
	testLeagueXML   = `<fantasy_content><league><league_key>423.l.12345</league_key><league_id>12345</league_id><game_code>nhl</game_code><season>2023</season><end_week>25</end_week></league></fantasy_content>`
	testTeamXML     = `<fantasy_content><team></team></fantasy_content>`
)

func seedGameKey() {
	SetGameKeyCache(testYahooSeason, testGameKey)
}

func newFetchActivities(mem *store.MemStorage, dl shared.Downloader, gobCache *cache.GobCache) *FetchActivities {
	return &FetchActivities{
		Storage:  mem,
		Download: dl,
		GobCache: gobCache,
	}
}

// anyArgs is a redismock matcher that accepts any arguments.
func anyArgs(expected, actual []any) error { return nil }

// --- FetchLeague tests ---

type FetchLeagueTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestActivityEnvironment
}

func (s *FetchLeagueTestSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
	seedGameKey()
}

func TestFetchLeagueTestSuite(t *testing.T) {
	suite.Run(t, new(FetchLeagueTestSuite))
}

func (s *FetchLeagueTestSuite) TestCacheHit() {
	mem := store.NewMemStorage()
	dl := mockDownloader(nil, nil)
	redisClient, mockRedis := redismock.NewClientMock()

	// Pre-populate the file so ReadParsedCached finds it
	res := resource.League{Season: testYahooSeason, LeagueID: testLeagueID, GameKey: testGameKey}
	require.NoError(s.T(), mem.Write(context.Background(), res.Path(), []byte(testLeagueXML)))

	// Redis miss → falls through to filesystem → populates cache
	mockRedis.ExpectGet(core.RedisKey(res)).SetErr(redis.Nil)
	mockRedis.CustomMatch(anyArgs).ExpectSet(core.RedisKey(res), "x", cache.GobCacheTTL).SetVal("OK")

	act := newFetchActivities(mem, dl, cache.NewGobCache(redisClient))
	s.env.RegisterActivity(act.FetchLeague)
	_, err := s.env.ExecuteActivity(act.FetchLeague, testYahooSeason, testLeagueID)

	require.NoError(s.T(), err)
}

func (s *FetchLeagueTestSuite) TestDownloadAndSave() {
	mem := store.NewMemStorage()
	content := []byte(testLeagueXML)
	dl := mockDownloader(content, nil)
	redisClient, mockRedis := redismock.NewClientMock()

	res := resource.League{Season: testYahooSeason, LeagueID: testLeagueID, GameKey: testGameKey}

	// ReadParsedCached: Redis miss, filesystem miss → download → cache.Set
	mockRedis.ExpectGet(core.RedisKey(res)).SetErr(redis.Nil)
	mockRedis.CustomMatch(anyArgs).ExpectSet(core.RedisKey(res), "x", cache.GobCacheTTL).SetVal("OK")

	act := newFetchActivities(mem, dl, cache.NewGobCache(redisClient))
	s.env.RegisterActivity(act.FetchLeague)
	_, err := s.env.ExecuteActivity(act.FetchLeague, testYahooSeason, testLeagueID)

	require.NoError(s.T(), err)

	// Verify file was saved
	assert.True(s.T(), mem.Exists(context.Background(), res.Path()))
	saved, readErr := mem.Read(context.Background(), res.Path())
	require.NoError(s.T(), readErr)
	assert.Equal(s.T(), content, saved)
}

func (s *FetchLeagueTestSuite) TestDownloadError() {
	mem := store.NewMemStorage()
	dl := mockDownloader(nil, errors.New("network error"))
	redisClient, mockRedis := redismock.NewClientMock()

	res := resource.League{Season: testYahooSeason, LeagueID: testLeagueID, GameKey: testGameKey}

	// ReadParsedCached misses → download fails before cache.Set
	mockRedis.ExpectGet(core.RedisKey(res)).SetErr(redis.Nil)

	act := newFetchActivities(mem, dl, cache.NewGobCache(redisClient))
	s.env.RegisterActivity(act.FetchLeague)
	_, err := s.env.ExecuteActivity(act.FetchLeague, testYahooSeason, testLeagueID)

	require.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "network error")
}

func (s *FetchLeagueTestSuite) TestInvalidRedisIdentityIsEvictedAndReloaded() {
	mem := store.NewMemStorage()
	res := resource.League{Season: testYahooSeason, LeagueID: testLeagueID, GameKey: testGameKey}
	require.NoError(s.T(), mem.Write(context.Background(), res.Path(), []byte(testLeagueXML)))

	invalid := &store.FantasyContent{League: store.League{
		ID: testLeagueID + 1, Key: "423.l.12346", GameCode: "nhl", Season: testYahooSeason,
	}}
	var encoded bytes.Buffer
	require.NoError(s.T(), gob.NewEncoder(&encoded).Encode(invalid))
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.ExpectGet(core.RedisKey(res)).SetVal(encoded.String())
	mockRedis.ExpectDel(core.RedisKey(res)).SetVal(1)
	mockRedis.ExpectGet(core.RedisKey(res)).SetErr(redis.Nil)
	mockRedis.CustomMatch(anyArgs).ExpectSet(core.RedisKey(res), "x", cache.GobCacheTTL).SetVal("OK")

	act := newFetchActivities(mem, mockDownloader(nil, errors.New("must not download")), cache.NewGobCache(redisClient))
	s.env.RegisterActivity(act.FetchLeague)
	value, err := s.env.ExecuteActivity(act.FetchLeague, testYahooSeason, testLeagueID)

	require.NoError(s.T(), err)
	var result FetchLeagueResult
	require.NoError(s.T(), value.Get(&result))
	assert.Equal(s.T(), fixtureLeagueEndWeek, result.EndWeek)
	require.NoError(s.T(), mockRedis.ExpectationsWereMet())
}

func (s *FetchLeagueTestSuite) TestInvalidRedisIdentityThenBadRequestIsNonRetryable() {
	res := resource.League{Season: testYahooSeason, LeagueID: testLeagueID, GameKey: testGameKey}
	invalid := &store.FantasyContent{League: store.League{
		ID: testLeagueID + 1, Key: "423.l.12346", GameCode: "nhl", Season: testYahooSeason,
	}}
	var encoded bytes.Buffer
	require.NoError(s.T(), gob.NewEncoder(&encoded).Encode(invalid))
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.ExpectGet(core.RedisKey(res)).SetVal(encoded.String())
	mockRedis.ExpectDel(core.RedisKey(res)).SetVal(1)
	mockRedis.ExpectGet(core.RedisKey(res)).SetErr(redis.Nil)
	rejection := &httpx.HTTPError{StatusCode: http.StatusBadRequest, Status: "400 Bad Request"}

	act := newFetchActivities(store.NewMemStorage(), failingDownloader(rejection), cache.NewGobCache(redisClient))
	s.env.RegisterActivity(act.FetchLeague)
	_, err := s.env.ExecuteActivity(act.FetchLeague, testYahooSeason, testLeagueID)

	assertRetryability(s.T(), err, false, leagueNotFoundErrorType)
	assert.Contains(s.T(), err.Error(), "check the league IDs")
	require.NoError(s.T(), mockRedis.ExpectationsWereMet())
}

// --- FetchTeams tests ---

type FetchTeamsTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestActivityEnvironment
}

func (s *FetchTeamsTestSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
	seedGameKey()
}

func TestFetchTeamsTestSuite(t *testing.T) {
	suite.Run(t, new(FetchTeamsTestSuite))
}

func (s *FetchTeamsTestSuite) TestSuccess() {
	mem := store.NewMemStorage()
	content := []byte(testTeamXML)
	dl := mockDownloader(content, nil)
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)

	teams := []TeamInfo{
		{LeagueID: testLeagueID, TeamID: 1},
		{LeagueID: testLeagueID, TeamID: 2},
		{LeagueID: testLeagueID, TeamID: 3},
	}
	input := FetchTeamsInput{StartSeason: testYahooSeason, Teams: teams}

	// Each team: Redis miss → download → cache.Set
	for _, team := range teams {
		res := resource.Team{Season: testYahooSeason, LeagueID: team.LeagueID, TeamID: team.TeamID, GameKey: testGameKey}
		mockRedis.ExpectGet(core.RedisKey(res)).SetErr(redis.Nil)
		mockRedis.CustomMatch(anyArgs).ExpectSet(core.RedisKey(res), "x", cache.GobCacheTTL).SetVal("OK")
	}

	act := newFetchActivities(mem, dl, cache.NewGobCache(redisClient))
	s.env.RegisterActivity(act.FetchTeams)
	_, err := s.env.ExecuteActivity(act.FetchTeams, input)

	require.NoError(s.T(), err)

	// Verify all team files were saved
	for _, team := range teams {
		res := resource.Team{Season: testYahooSeason, LeagueID: team.LeagueID, TeamID: team.TeamID, GameKey: testGameKey}
		assert.True(s.T(), mem.Exists(context.Background(), res.Path()), "expected file for team %d", team.TeamID)
	}
}

func (s *FetchTeamsTestSuite) TestEmptyTeams() {
	mem := store.NewMemStorage()
	dl := mockDownloader(nil, errors.New("should not be called"))
	redisClient, _ := redismock.NewClientMock()

	input := FetchTeamsInput{StartSeason: testYahooSeason, Teams: nil}

	act := newFetchActivities(mem, dl, cache.NewGobCache(redisClient))
	s.env.RegisterActivity(act.FetchTeams)
	_, err := s.env.ExecuteActivity(act.FetchTeams, input)

	require.NoError(s.T(), err)
}

func (s *FetchTeamsTestSuite) TestDownloadError() {
	mem := store.NewMemStorage()
	dl := mockDownloader(nil, errors.New("yahoo unavailable"))
	redisClient, mockRedis := redismock.NewClientMock()

	teams := []TeamInfo{
		{LeagueID: testLeagueID, TeamID: 1},
		{LeagueID: testLeagueID, TeamID: 2},
	}
	input := FetchTeamsInput{StartSeason: testYahooSeason, Teams: teams}

	// First team: Redis miss → download fails before cache.Set
	res := resource.Team{Season: testYahooSeason, LeagueID: teams[0].LeagueID, TeamID: teams[0].TeamID, GameKey: testGameKey}
	mockRedis.ExpectGet(core.RedisKey(res)).SetErr(redis.Nil)

	act := newFetchActivities(mem, dl, cache.NewGobCache(redisClient))
	s.env.RegisterActivity(act.FetchTeams)
	_, err := s.env.ExecuteActivity(act.FetchTeams, input)

	require.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "yahoo unavailable")

	// First team should not be saved (download failed)
	assert.False(s.T(), mem.Exists(context.Background(), res.Path()))
}

// fixtureLeagueEndWeek is the <end_week> of the points league fixture.
const fixtureLeagueEndWeek = 25

func (s *FetchLeagueTestSuite) TestReturnsEndWeek() {
	mem := store.NewMemStorage()
	res := resource.League{Season: testYahooSeason, LeagueID: testLeagueID, GameKey: testGameKey}
	require.NoError(s.T(), mem.Write(context.Background(), res.Path(), []byte(testLeagueXML)))
	act := newFetchActivities(mem, mockDownloader(nil, errors.New("must not download")), cache.NewGobCache(nil))
	s.env.RegisterActivity(act.FetchLeague)

	val, err := s.env.ExecuteActivity(act.FetchLeague, testYahooSeason, testLeagueID)

	require.NoError(s.T(), err)
	var result FetchLeagueResult
	require.NoError(s.T(), val.Get(&result))
	assert.Equal(s.T(), fixtureLeagueEndWeek, result.EndWeek)
}
