package yahoo

import (
	"context"
	"errors"
	"fmt"
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
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
)

// ────────────────────────────────────────────────────────────────────────────
// Shared XML fixtures
// ────────────────────────────────────────────────────────────────────────────

const (
	// minimalMatchupXML is a valid two-team matchup for week 1.
	minimalMatchupXML = `<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content>
  <league>
    <scoreboard>
      <week>1</week>
      <matchups count="1">
        <matchup>
          <week>1</week>
          <status>postevent</status>
          <is_playoffs>0</is_playoffs>
          <is_consolation>0</is_consolation>
          <teams count="2">
            <team>
              <team_id>1</team_id>
              <team_points><total>55.5</total></team_points>
            </team>
            <team>
              <team_id>2</team_id>
              <team_points><total>42.0</total></team_points>
            </team>
          </teams>
        </matchup>
      </matchups>
    </scoreboard>
  </league>
</fantasy_content>`
)

// ────────────────────────────────────────────────────────────────────────────
// GetGameKeyForSeason tests
// ────────────────────────────────────────────────────────────────────────────

func TestGetGameKeyForSeason_MemoryCacheHit(t *testing.T) {
	t.Parallel()

	const season = 2020
	const wantKey = 390

	// Seed the in-memory cache so no storage or fetcher is consulted.
	SetGameKeyCache(season, wantKey)

	got, err := GetGameKeyForSeason(
		context.Background(),
		nil, // storage — must not be called
		nil, // gob cache — must not be called
		nil, // fetcher — must not be called
		season,
	)

	require.NoError(t, err)
	assert.Equal(t, wantKey, got)
}

func TestGetGameKeyForSeason_FallsThroughToImpl(t *testing.T) {
	t.Parallel()

	const season = 2018
	mem := store.NewMemStorage()
	gobCache := cache.NewGobCache(nil)

	xmlContent := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content><games><game><game_key>398</game_key><game_id>398</game_id><code>nhl</code><season>2018</season></game></games></fantasy_content>`)

	fetcher := mockDownloader(xmlContent, nil)

	// Ensure the in-memory cache doesn't have a stale entry for this season.
	gameKeyCacheMu.Lock()
	delete(gameKeyCache, season)
	gameKeyCacheMu.Unlock()

	got, err := GetGameKeyForSeason(context.Background(), mem, gobCache, fetcher, season)

	require.NoError(t, err)
	assert.Equal(t, 398, got)
}

func TestGetGameKeyForSeason_FetcherError(t *testing.T) {
	t.Parallel()

	const season = 2017
	mem := store.NewMemStorage()
	gobCache := cache.NewGobCache(nil)

	gameKeyCacheMu.Lock()
	delete(gameKeyCache, season)
	gameKeyCacheMu.Unlock()

	fetcher := mockDownloader(nil, errors.New("network timeout"))

	_, err := GetGameKeyForSeason(context.Background(), mem, gobCache, fetcher, season)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "fetch game key")
}

// ────────────────────────────────────────────────────────────────────────────
// HTTPDownloaderFunc tests
// ────────────────────────────────────────────────────────────────────────────

func TestHTTPDownloaderFunc_DelegatesToFunc(t *testing.T) {
	t.Parallel()

	want := []byte("response body")
	var capturedURL string

	fn := HTTPDownloaderFunc(func(_ context.Context, url string) ([]byte, error) {
		capturedURL = url
		return want, nil
	})

	got, err := fn.Download(context.Background(), "https://example.com/path")

	require.NoError(t, err)
	assert.Equal(t, want, got)
	assert.Equal(t, "https://example.com/path", capturedURL)
}

func TestHTTPDownloaderFunc_PropagatesError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("connection refused")
	fn := HTTPDownloaderFunc(func(_ context.Context, _ string) ([]byte, error) {
		return nil, wantErr
	})

	got, err := fn.Download(context.Background(), "https://example.com/path")

	require.ErrorIs(t, err, wantErr)
	assert.Nil(t, got)
}

// ────────────────────────────────────────────────────────────────────────────
// FetchYahooPlayerBatch tests
// ────────────────────────────────────────────────────────────────────────────

type FetchYahooPlayerBatchSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestActivityEnvironment
}

func (s *FetchYahooPlayerBatchSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
}

func TestFetchYahooPlayerBatchSuite(t *testing.T) {
	suite.Run(t, new(FetchYahooPlayerBatchSuite))
}

func (s *FetchYahooPlayerBatchSuite) TestAllCached() {
	mem := store.NewMemStorage()
	downloader := &MockHTTPDownloader{}

	// Pre-cache two player files.
	for _, id := range []store.YahooPlayerID{100, 101} {
		res := resource.YahooPlayer{PlayerID: id}
		require.NoError(s.T(), mem.Write(context.Background(), res.Path(), []byte("<html>player</html>")))
	}

	act := &FetchActivities{Storage: mem, PublicDownloader: downloader}
	s.env.RegisterActivity(act.FetchYahooPlayerBatch)
	val, err := s.env.ExecuteActivity(act.FetchYahooPlayerBatch, store.YahooPlayerID(100), store.YahooPlayerID(101))

	require.NoError(s.T(), err)

	var stats shared.FetchStats
	require.NoError(s.T(), val.Get(&stats))
	assert.Equal(s.T(), 2, stats.CacheHits)
	assert.Equal(s.T(), 0, stats.Downloaded)
	assert.Equal(s.T(), 0, stats.Missing)
	downloader.AssertNotCalled(s.T(), "Download")
}

func (s *FetchYahooPlayerBatchSuite) TestAllMissing() {
	mem := store.NewMemStorage()
	downloader := &MockHTTPDownloader{}

	// Pre-mark two players as missing.
	for _, id := range []store.YahooPlayerID{200, 201} {
		res := resource.MissingYahooPlayer{PlayerID: id}
		require.NoError(s.T(), mem.Write(context.Background(), res.Path(), []byte("missing")))
	}

	act := &FetchActivities{Storage: mem, PublicDownloader: downloader}
	s.env.RegisterActivity(act.FetchYahooPlayerBatch)
	val, err := s.env.ExecuteActivity(act.FetchYahooPlayerBatch, store.YahooPlayerID(200), store.YahooPlayerID(201))

	require.NoError(s.T(), err)

	var stats shared.FetchStats
	require.NoError(s.T(), val.Get(&stats))
	assert.Equal(s.T(), 2, stats.Missing)
	assert.Equal(s.T(), 0, stats.CacheHits)
	assert.Equal(s.T(), 0, stats.Downloaded)
	downloader.AssertNotCalled(s.T(), "Download")
}

func (s *FetchYahooPlayerBatchSuite) TestDownloadedAndCounted() {
	mem := store.NewMemStorage()
	downloader := &MockHTTPDownloader{}

	playerID := store.YahooPlayerID(300)
	res := resource.YahooPlayer{PlayerID: playerID}
	content := []byte("<html>player page</html>")
	downloader.On("Download", mock.Anything, res.URL()).Return(content, nil)

	act := &FetchActivities{Storage: mem, PublicDownloader: downloader}
	s.env.RegisterActivity(act.FetchYahooPlayerBatch)
	val, err := s.env.ExecuteActivity(act.FetchYahooPlayerBatch, playerID, playerID)

	require.NoError(s.T(), err)

	var stats shared.FetchStats
	require.NoError(s.T(), val.Get(&stats))
	assert.Equal(s.T(), 1, stats.Downloaded)
	assert.Equal(s.T(), 0, stats.CacheHits)
	assert.Equal(s.T(), 0, stats.Missing)
	downloader.AssertExpectations(s.T())
}

func (s *FetchYahooPlayerBatchSuite) TestDownloadError_ReturnsError() {
	mem := store.NewMemStorage()
	downloader := &MockHTTPDownloader{}

	playerID := store.YahooPlayerID(400)
	res := resource.YahooPlayer{PlayerID: playerID}
	downloader.On("Download", mock.Anything, res.URL()).Return(nil, errors.New("connection refused"))

	act := &FetchActivities{Storage: mem, PublicDownloader: downloader}
	s.env.RegisterActivity(act.FetchYahooPlayerBatch)
	_, err := s.env.ExecuteActivity(act.FetchYahooPlayerBatch, playerID, playerID)

	require.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "download player")
}

func (s *FetchYahooPlayerBatchSuite) TestEmptyRange() {
	mem := store.NewMemStorage()
	downloader := &MockHTTPDownloader{}

	// startID > endID → loop body never executes.
	act := &FetchActivities{Storage: mem, PublicDownloader: downloader}
	s.env.RegisterActivity(act.FetchYahooPlayerBatch)
	val, err := s.env.ExecuteActivity(act.FetchYahooPlayerBatch, store.YahooPlayerID(500), store.YahooPlayerID(499))

	require.NoError(s.T(), err)

	var stats shared.FetchStats
	require.NoError(s.T(), val.Get(&stats))
	assert.Equal(s.T(), 0, stats.Downloaded+stats.CacheHits+stats.Missing)
	downloader.AssertNotCalled(s.T(), "Download")
}

// ────────────────────────────────────────────────────────────────────────────
// FetchYahooLeagueData tests
// ────────────────────────────────────────────────────────────────────────────

type FetchYahooLeagueDataSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestActivityEnvironment
}

func (s *FetchYahooLeagueDataSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
	seedGameKey()
}

func TestFetchYahooLeagueDataSuite(t *testing.T) {
	suite.Run(t, new(FetchYahooLeagueDataSuite))
}

// errNoMoreWeeks is how Yahoo answers a scoreboard request past the end of the season.
var errNoMoreWeeks = &httpx.HTTPError{StatusCode: http.StatusBadRequest, Status: "400 Bad Request"}

// buildAct creates a FetchActivities with in-memory storage.
func (s *FetchYahooLeagueDataSuite) buildAct(mem *store.MemStorage, dl shared.Downloader, gobCache *cache.GobCache) *FetchActivities {
	return &FetchActivities{
		Storage:  mem,
		Download: dl,
		GobCache: gobCache,
	}
}

// expectRedisMiss declares the GET every validated read makes before falling through to storage.
func expectRedisMiss(mockRedis redismock.ClientMock, res core.Resource) {
	mockRedis.ExpectGet(core.RedisKey(res)).SetErr(redis.Nil)
}

// expectRedisPopulate declares the SET made once a resource has parsed (from a download or from storage).
func expectRedisPopulate(mockRedis redismock.ClientMock, res core.Resource) {
	mockRedis.CustomMatch(anyArgs).ExpectSet(core.RedisKey(res), "x", cache.GobCacheTTL).SetVal("OK")
}

func (s *FetchYahooLeagueDataSuite) TestSuccess_AllFromDownload() {
	mem := store.NewMemStorage()
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)

	txRes := resource.Transactions{Season: testYahooSeason, LeagueID: testLeagueID, GameKey: testGameKey}
	drRes := resource.DraftResults{Season: testYahooSeason, LeagueID: testLeagueID, GameKey: testGameKey}
	mu1Res := resource.Matchups{Season: testYahooSeason, LeagueID: testLeagueID, Week: 1, GameKey: testGameKey}
	mu2Res := resource.Matchups{Season: testYahooSeason, LeagueID: testLeagueID, Week: 2, GameKey: testGameKey}

	// Each resource: Redis miss → download → Redis SET. Week 2 misses then fails to download.
	for _, res := range []core.Resource{txRes, drRes, mu1Res} {
		expectRedisMiss(mockRedis, res)
		expectRedisPopulate(mockRedis, res)
	}
	expectRedisMiss(mockRedis, mu2Res)

	emptyTxXML := []byte(`<fantasy_content><league><transactions count="0"></transactions></league></fantasy_content>`)
	emptyDrXML := []byte(`<fantasy_content><league><draft_results count="0"></draft_results></league></fantasy_content>`)

	callCount := 0
	dl := shared.Downloader(func(_ context.Context, _ string) ([]byte, error) {
		callCount++
		switch callCount {
		case 1:
			return emptyTxXML, nil // transactions
		case 2:
			return emptyDrXML, nil // draft results
		case 3:
			return []byte(minimalMatchupXML), nil // week 1
		default:
			return nil, errNoMoreWeeks
		}
	})

	act := s.buildAct(mem, dl, cache.NewGobCache(redisClient))
	s.env.RegisterActivity(act.FetchYahooLeagueData)
	input := FetchYahooLeagueDataInput{Season: testYahooSeason, LeagueID: testLeagueID}
	_, err := s.env.ExecuteActivity(act.FetchYahooLeagueData, input)

	require.NoError(s.T(), err)
	require.NoError(s.T(), mockRedis.ExpectationsWereMet())
	assert.True(s.T(), mem.Exists(context.Background(), txRes.Path()), "transactions file must be written")
	assert.True(s.T(), mem.Exists(context.Background(), drRes.Path()), "draft results file must be written")
	assert.True(s.T(), mem.Exists(context.Background(), mu1Res.Path()), "matchup week-1 file must be written")
}

func (s *FetchYahooLeagueDataSuite) TestSuccess_AlreadyCached() {
	mem := store.NewMemStorage()
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)

	// Pre-write all three resources so the validated cache read hits storage.
	txRes := resource.Transactions{Season: testYahooSeason, LeagueID: testLeagueID, GameKey: testGameKey}
	drRes := resource.DraftResults{Season: testYahooSeason, LeagueID: testLeagueID, GameKey: testGameKey}
	mu1Res := resource.Matchups{Season: testYahooSeason, LeagueID: testLeagueID, Week: 1, GameKey: testGameKey}
	mu2Res := resource.Matchups{Season: testYahooSeason, LeagueID: testLeagueID, Week: 2, GameKey: testGameKey}

	emptyTxXML := []byte(`<fantasy_content><league><transactions count="0"></transactions></league></fantasy_content>`)
	emptyDrXML := []byte(`<fantasy_content><league><draft_results count="0"></draft_results></league></fantasy_content>`)

	require.NoError(s.T(), mem.Write(context.Background(), txRes.Path(), emptyTxXML))
	require.NoError(s.T(), mem.Write(context.Background(), drRes.Path(), emptyDrXML))
	require.NoError(s.T(), mem.Write(context.Background(), mu1Res.Path(), []byte(minimalMatchupXML)))

	// Each cached file: Redis miss → storage hit → Redis populated. Week 2: miss → download fails → stop.
	for _, res := range []core.Resource{txRes, drRes, mu1Res} {
		expectRedisMiss(mockRedis, res)
		expectRedisPopulate(mockRedis, res)
	}
	expectRedisMiss(mockRedis, mu2Res)

	dl := mockDownloader(nil, errNoMoreWeeks)

	act := s.buildAct(mem, dl, cache.NewGobCache(redisClient))
	s.env.RegisterActivity(act.FetchYahooLeagueData)
	input := FetchYahooLeagueDataInput{Season: testYahooSeason, LeagueID: testLeagueID}
	_, err := s.env.ExecuteActivity(act.FetchYahooLeagueData, input)

	// No week-2 file → matchup loop stops. No downloads needed → no errors.
	require.NoError(s.T(), err)
	require.NoError(s.T(), mockRedis.ExpectationsWereMet())
}

func (s *FetchYahooLeagueDataSuite) TestTransactionDownloadError() {
	mem := store.NewMemStorage()
	redisClient, mockRedis := redismock.NewClientMock()
	expectRedisMiss(mockRedis, resource.Transactions{Season: testYahooSeason, LeagueID: testLeagueID, GameKey: testGameKey})

	dl := mockDownloader(nil, errors.New("yahoo is down"))

	act := s.buildAct(mem, dl, cache.NewGobCache(redisClient))
	s.env.RegisterActivity(act.FetchYahooLeagueData)
	input := FetchYahooLeagueDataInput{Season: testYahooSeason, LeagueID: testLeagueID}
	_, err := s.env.ExecuteActivity(act.FetchYahooLeagueData, input)

	require.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "fetch transactions")
	assert.Contains(s.T(), err.Error(), "yahoo is down")
}

// TestMatchupsTransientFailureIsAnError proves that a transient
// or unrelated download failure is not mistaken for the end of the season.
func (s *FetchYahooLeagueDataSuite) TestMatchupsTransientFailureIsAnError() {
	cases := map[string]error{
		"server error": &httpx.HTTPError{StatusCode: http.StatusInternalServerError, Status: "500 Internal Server Error"},
		"throttled":    &httpx.HTTPError{StatusCode: http.StatusTooManyRequests, Status: "429 Too Many Requests"},
		"transport":    errors.New("connection reset"),
		"cancelled":    context.Canceled,
	}
	for name, dlErr := range cases {
		s.Run(name, func() {
			mem := store.NewMemStorage()
			redisClient, mockRedis := redismock.NewClientMock()
			mockRedis.MatchExpectationsInOrder(false)

			txRes := resource.Transactions{Season: testYahooSeason, LeagueID: testLeagueID, GameKey: testGameKey}
			drRes := resource.DraftResults{Season: testYahooSeason, LeagueID: testLeagueID, GameKey: testGameKey}
			mu1Res := resource.Matchups{Season: testYahooSeason, LeagueID: testLeagueID, Week: 1, GameKey: testGameKey}
			for _, res := range []core.Resource{txRes, drRes, mu1Res} {
				expectRedisMiss(mockRedis, res)
				expectRedisPopulate(mockRedis, res)
			}

			emptyXML := []byte(`<fantasy_content><league></league></fantasy_content>`)
			callCount := 0
			dl := shared.Downloader(func(_ context.Context, _ string) ([]byte, error) {
				callCount++
				if callCount <= 3 { // transactions, draft results, week 1
					return emptyXML, nil
				}
				return nil, dlErr
			})

			act := s.buildAct(mem, dl, cache.NewGobCache(redisClient))
			env := s.NewTestActivityEnvironment()
			env.RegisterActivity(act.FetchYahooLeagueData)
			_, err := env.ExecuteActivity(act.FetchYahooLeagueData, FetchYahooLeagueDataInput{Season: testYahooSeason, LeagueID: testLeagueID})

			require.Error(s.T(), err)
			assert.Contains(s.T(), err.Error(), "week 2")
		})
	}
}

func TestIsEndOfMatchupWeeks(t *testing.T) {
	t.Parallel()
	wrap := func(err error) error { return fmt.Errorf("%w: Matchups: %w", ErrDownload, err) }
	cases := map[string]struct {
		err  error
		want bool
	}{
		"400 rejection":         {wrap(&httpx.HTTPError{StatusCode: http.StatusBadRequest}), true},
		"404":                   {wrap(&httpx.HTTPError{StatusCode: http.StatusNotFound}), false},
		"429 throttled":         {wrap(&httpx.HTTPError{StatusCode: http.StatusTooManyRequests}), false},
		"500":                   {wrap(&httpx.HTTPError{StatusCode: http.StatusInternalServerError}), false},
		"transport":             {wrap(errors.New("timeout")), false},
		"4xx not from download": {&httpx.HTTPError{StatusCode: http.StatusBadRequest}, false},
		"nil":                   {nil, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, isEndOfMatchupWeeks(tc.err))
		})
	}
}
