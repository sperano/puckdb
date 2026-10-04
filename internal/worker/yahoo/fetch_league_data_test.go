package yahoo

import (
	"context"
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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
)

const (
	emptyTransactionsXML = `<fantasy_content><league><transactions count="0"></transactions></league></fantasy_content>`
	emptyDraftResultsXML = `<fantasy_content><league><draft_results count="0"></draft_results></league></fantasy_content>`
	testMatchupWeek      = 1
	testLaterMatchupWeek = 2
)

// errNoMoreWeeks is how Yahoo answers a scoreboard request past the end of
// the season, and a league-level resource before the season starts.
var errNoMoreWeeks = &httpx.HTTPError{StatusCode: http.StatusBadRequest, Status: "400 Bad Request"}

// expectRedisMiss declares the GET every validated read makes before falling through to storage.
func expectRedisMiss(mockRedis redismock.ClientMock, res core.Resource) {
	if res.Type() == core.YahooDraftResults {
		expectCoherentRedisMiss(mockRedis, res)
		return
	}
	mockRedis.ExpectGet(core.RedisKey(res)).SetErr(redis.Nil)
}

// expectRedisPopulate declares the SET made once a resource has parsed (from a download or from storage).
func expectRedisPopulate(mockRedis redismock.ClientMock, res core.Resource) {
	mockRedis.CustomMatch(anyArgs).ExpectSet(core.RedisKey(res), "x", cache.GobCacheTTL).SetVal("OK")
}

type FetchLeagueDataSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
}

func (s *FetchLeagueDataSuite) SetupTest() {
	seedGameKey()
}

func TestFetchLeagueDataSuite(t *testing.T) {
	suite.Run(t, new(FetchLeagueDataSuite))
}

var testLeagueDataInput = FetchYahooLeagueDataInput{Season: testYahooSeason, LeagueID: testLeagueID}

func (s *FetchLeagueDataSuite) transactionsRes() resource.Transactions {
	return resource.Transactions{Season: testYahooSeason, LeagueID: testLeagueID, GameKey: testGameKey}
}

func (s *FetchLeagueDataSuite) draftResultsRes() resource.DraftResults {
	return resource.DraftResults{Season: testYahooSeason, LeagueID: testLeagueID, GameKey: testGameKey}
}

func (s *FetchLeagueDataSuite) matchupRes(week int) resource.Matchups {
	return resource.Matchups{Season: testYahooSeason, LeagueID: testLeagueID, Week: week, GameKey: testGameKey}
}

// runResource executes one league-resource activity and decodes its result.
func (s *FetchLeagueDataSuite) runResource(activityFn any) (FetchYahooLeagueResourceResult, error) {
	env := s.NewTestActivityEnvironment()
	env.RegisterActivity(activityFn)
	val, err := env.ExecuteActivity(activityFn, testLeagueDataInput)
	var result FetchYahooLeagueResourceResult
	if err == nil {
		require.NoError(s.T(), val.Get(&result))
	}
	return result, err
}

// runMatchupWeek executes FetchYahooMatchupWeek for one week and decodes its result.
func (s *FetchLeagueDataSuite) runMatchupWeek(act *FetchActivities, week int) (FetchYahooMatchupWeekResult, error) {
	env := s.NewTestActivityEnvironment()
	env.RegisterActivity(act.FetchYahooMatchupWeek)
	val, err := env.ExecuteActivity(act.FetchYahooMatchupWeek,
		FetchYahooMatchupWeekInput{Season: testYahooSeason, LeagueID: testLeagueID, Week: week})
	var result FetchYahooMatchupWeekResult
	if err == nil {
		require.NoError(s.T(), val.Get(&result))
	}
	return result, err
}

func (s *FetchLeagueDataSuite) TestTransactions_Downloaded() {
	mem := store.NewMemStorage()
	redisClient, mockRedis := redismock.NewClientMock()
	expectRedisMiss(mockRedis, s.transactionsRes())
	expectRedisPopulate(mockRedis, s.transactionsRes())
	act := newFetchActivities(mem, mockDownloader([]byte(emptyTransactionsXML), nil), cache.NewGobCache(redisClient))

	result, err := s.runResource(act.FetchYahooTransactions)

	require.NoError(s.T(), err)
	assert.False(s.T(), result.Unavailable)
	require.NoError(s.T(), mockRedis.ExpectationsWereMet())
	assert.True(s.T(), mem.Exists(context.Background(), s.transactionsRes().Path()), "transactions file must be written")
}

func (s *FetchLeagueDataSuite) TestDraftResults_AlreadyCached() {
	mem := store.NewMemStorage()
	require.NoError(s.T(), mem.Write(context.Background(), s.draftResultsRes().Path(), []byte(emptyDraftResultsXML)))
	redisClient, mockRedis := redismock.NewClientMock()
	expectRedisMiss(mockRedis, s.draftResultsRes())
	expectRedisPopulate(mockRedis, s.draftResultsRes())
	act := newFetchActivities(mem, mockDownloader(nil, errors.New("must not download")), cache.NewGobCache(redisClient))

	result, err := s.runResource(act.FetchYahooDraftResults)

	require.NoError(s.T(), err)
	assert.False(s.T(), result.Unavailable)
	require.NoError(s.T(), mockRedis.ExpectationsWereMet())
}

// Yahoo answers 400 for league-level resources it has not published before
// the season starts: reported as unavailable, not an error.
func (s *FetchLeagueDataSuite) TestPreseasonRejectionIsUnavailable() {
	for name, pick := range map[string]func(*FetchActivities) any{
		"transactions":  func(a *FetchActivities) any { return a.FetchYahooTransactions },
		"draft results": func(a *FetchActivities) any { return a.FetchYahooDraftResults },
	} {
		s.Run(name, func() {
			act := newFetchActivities(store.NewMemStorage(), mockDownloader(nil, errNoMoreWeeks), cache.NewGobCache(nil))

			result, err := s.runResource(pick(act))

			require.NoError(s.T(), err)
			assert.True(s.T(), result.Unavailable)
		})
	}
}

func (s *FetchLeagueDataSuite) TestTransactionDownloadError() {
	act := newFetchActivities(store.NewMemStorage(), mockDownloader(nil, errors.New("yahoo is down")), cache.NewGobCache(nil))

	_, err := s.runResource(act.FetchYahooTransactions)

	require.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "fetch transactions")
	assert.Contains(s.T(), err.Error(), "yahoo is down")
}

func (s *FetchLeagueDataSuite) TestMatchupWeek_Downloaded() {
	mem := store.NewMemStorage()
	redisClient, mockRedis := redismock.NewClientMock()
	expectRedisMiss(mockRedis, s.matchupRes(testMatchupWeek))
	expectRedisPopulate(mockRedis, s.matchupRes(testMatchupWeek))
	act := newFetchActivities(mem, mockDownloader([]byte(minimalMatchupXML), nil), cache.NewGobCache(redisClient))

	result, err := s.runMatchupWeek(act, testMatchupWeek)

	require.NoError(s.T(), err)
	assert.False(s.T(), result.EndOfSeries)
	require.NoError(s.T(), mockRedis.ExpectationsWereMet())
	assert.True(s.T(), mem.Exists(context.Background(), s.matchupRes(testMatchupWeek).Path()),
		"matchup week-1 file must be written")
}

func (s *FetchLeagueDataSuite) TestMatchupWeek_RejectionEndsSeries() {
	act := newFetchActivities(store.NewMemStorage(), mockDownloader(nil, errNoMoreWeeks), cache.NewGobCache(nil))

	result, err := s.runMatchupWeek(act, testLaterMatchupWeek)

	require.NoError(s.T(), err)
	assert.True(s.T(), result.EndOfSeries)
	assert.False(s.T(), act.Storage.Exists(context.Background(), s.matchupRes(testLaterMatchupWeek).Path()))
}

// TestMatchupWeek_TransientFailureIsAnError proves that a transient or
// unrelated download failure (404 included) is not mistaken for the end of
// the season.
func (s *FetchLeagueDataSuite) TestMatchupWeek_TransientFailureIsAnError() {
	cases := map[string]error{
		"not found":    &httpx.HTTPError{StatusCode: http.StatusNotFound, Status: "404 Not Found"},
		"server error": &httpx.HTTPError{StatusCode: http.StatusInternalServerError, Status: "500 Internal Server Error"},
		"throttled":    &httpx.HTTPError{StatusCode: http.StatusTooManyRequests, Status: "429 Too Many Requests"},
		"transport":    errors.New("connection reset"),
		"cancelled":    context.Canceled,
	}
	for name, dlErr := range cases {
		s.Run(name, func() {
			act := newFetchActivities(store.NewMemStorage(), mockDownloader(nil, dlErr), cache.NewGobCache(nil))

			_, err := s.runMatchupWeek(act, testLaterMatchupWeek)

			require.Error(s.T(), err)
			assert.Contains(s.T(), err.Error(), "week 2")
		})
	}
}
