package yahoo

import (
	"context"
	"errors"
	"testing"

	"github.com/go-redis/redismock/v8"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/store"
	"github.com/sperano/puckdb/worker/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
)

// ────────────────────────────────────────────────────────────────────────────
// Shared XML fixtures
// ────────────────────────────────────────────────────────────────────────────

const (
	// minimalTransactionsXML is a well-formed Yahoo transactions response with
	// a single transaction containing one player.
	minimalTransactionsXML = `<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content>
  <league>
    <transactions count="1">
      <transaction>
        <transaction_key>423.l.12345.tr.1</transaction_key>
        <type>add/drop</type>
        <timestamp>1700000000</timestamp>
        <status>successful</status>
        <players count="0"></players>
      </transaction>
    </transactions>
  </league>
</fantasy_content>`

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
<fantasy_content><games><game><game_key>398</game_key><game_id>398</game_id></game></games></fantasy_content>`)

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

	fn := HTTPDownloaderFunc(func(url string) ([]byte, error) {
		capturedURL = url
		return want, nil
	})

	got, err := fn.Download("https://example.com/path")

	require.NoError(t, err)
	assert.Equal(t, want, got)
	assert.Equal(t, "https://example.com/path", capturedURL)
}

func TestHTTPDownloaderFunc_PropagatesError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("connection refused")
	fn := HTTPDownloaderFunc(func(url string) ([]byte, error) {
		return nil, wantErr
	})

	got, err := fn.Download("https://example.com/path")

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
		require.NoError(s.T(), mem.Write(res.Path(), []byte("<html>player</html>")))
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
		require.NoError(s.T(), mem.Write(res.Path(), []byte("missing")))
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
	downloader.On("Download", res.URL()).Return(content, nil)

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
	downloader.On("Download", res.URL()).Return(nil, errors.New("connection refused"))

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

// buildFetchYahooLeagueDataAct creates a FetchActivities with in-memory storage.
// It seeds the storage with a transactions file and draft results file so that
// fetchYahooResource finds them on the first call.
func (s *FetchYahooLeagueDataSuite) buildAct(mem *store.MemStorage, dl func(string) ([]byte, error), gobCache *cache.GobCache) *FetchActivities {
	return &FetchActivities{
		Storage:  mem,
		Download: dl,
		GobCache: gobCache,
	}
}

func (s *FetchYahooLeagueDataSuite) TestSuccess_AllFromDownload() {
	mem := store.NewMemStorage()
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)

	txRes := resource.Transactions{Season: testYahooSeason, LeagueID: testLeagueID, GameKey: testGameKey}
	drRes := resource.DraftResults{Season: testYahooSeason, LeagueID: testLeagueID, GameKey: testGameKey}
	mu1Res := resource.Matchups{Season: testYahooSeason, LeagueID: testLeagueID, Week: 1, GameKey: testGameKey}

	// Expect Redis SET for each parsed resource.
	mockRedis.CustomMatch(anyArgs).ExpectSet(core.RedisKey(txRes), "x", cache.GobCacheTTL).SetVal("OK")
	mockRedis.CustomMatch(anyArgs).ExpectSet(core.RedisKey(drRes), "x", cache.GobCacheTTL).SetVal("OK")
	mockRedis.CustomMatch(anyArgs).ExpectSet(core.RedisKey(mu1Res), "x", cache.GobCacheTTL).SetVal("OK")

	emptyTxXML := []byte(`<fantasy_content><league><transactions count="0"></transactions></league></fantasy_content>`)
	emptyDrXML := []byte(`<fantasy_content><league><draft_results count="0"></draft_results></league></fantasy_content>`)

	callCount := 0
	dl := func(url string) ([]byte, error) {
		callCount++
		switch callCount {
		case 1:
			return emptyTxXML, nil // transactions
		case 2:
			return emptyDrXML, nil // draft results
		case 3:
			return []byte(minimalMatchupXML), nil // week 1
		default:
			return nil, errors.New("no more weeks")
		}
	}

	act := s.buildAct(mem, dl, cache.NewGobCache(redisClient))
	s.env.RegisterActivity(act.FetchYahooLeagueData)
	input := FetchYahooLeagueDataInput{Season: testYahooSeason, LeagueID: testLeagueID}
	_, err := s.env.ExecuteActivity(act.FetchYahooLeagueData, input)

	require.NoError(s.T(), err)
	assert.True(s.T(), mem.Exists(txRes.Path()), "transactions file must be written")
	assert.True(s.T(), mem.Exists(drRes.Path()), "draft results file must be written")
	assert.True(s.T(), mem.Exists(mu1Res.Path()), "matchup week-1 file must be written")
}

func (s *FetchYahooLeagueDataSuite) TestSuccess_AlreadyCached() {
	mem := store.NewMemStorage()
	redisClient, _ := redismock.NewClientMock()

	// Pre-write all three resources so fetchYahooResource short-circuits.
	txRes := resource.Transactions{Season: testYahooSeason, LeagueID: testLeagueID, GameKey: testGameKey}
	drRes := resource.DraftResults{Season: testYahooSeason, LeagueID: testLeagueID, GameKey: testGameKey}
	mu1Res := resource.Matchups{Season: testYahooSeason, LeagueID: testLeagueID, Week: 1, GameKey: testGameKey}

	emptyTxXML := []byte(`<fantasy_content><league><transactions count="0"></transactions></league></fantasy_content>`)
	emptyDrXML := []byte(`<fantasy_content><league><draft_results count="0"></draft_results></league></fantasy_content>`)

	require.NoError(s.T(), mem.Write(txRes.Path(), emptyTxXML))
	require.NoError(s.T(), mem.Write(drRes.Path(), emptyDrXML))
	require.NoError(s.T(), mem.Write(mu1Res.Path(), []byte(minimalMatchupXML)))

	dl := mockDownloader(nil, errors.New("should not be called"))

	act := s.buildAct(mem, dl, cache.NewGobCache(redisClient))
	s.env.RegisterActivity(act.FetchYahooLeagueData)
	input := FetchYahooLeagueDataInput{Season: testYahooSeason, LeagueID: testLeagueID}
	_, err := s.env.ExecuteActivity(act.FetchYahooLeagueData, input)

	// No week-2 file → matchup loop stops. No downloads needed → no errors.
	require.NoError(s.T(), err)
}

func (s *FetchYahooLeagueDataSuite) TestTransactionDownloadError() {
	mem := store.NewMemStorage()
	redisClient, _ := redismock.NewClientMock()

	dl := mockDownloader(nil, errors.New("yahoo is down"))

	act := s.buildAct(mem, dl, cache.NewGobCache(redisClient))
	s.env.RegisterActivity(act.FetchYahooLeagueData)
	input := FetchYahooLeagueDataInput{Season: testYahooSeason, LeagueID: testLeagueID}
	_, err := s.env.ExecuteActivity(act.FetchYahooLeagueData, input)

	require.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "fetch transactions")
}

// ────────────────────────────────────────────────────────────────────────────
// fetchYahooResource tests (called directly since it's unexported)
// ────────────────────────────────────────────────────────────────────────────

func TestFetchYahooResource_CacheHit(t *testing.T) {
	t.Parallel()

	mem := store.NewMemStorage()
	redisClient, _ := redismock.NewClientMock()

	res := resource.Transactions{Season: 2023, LeagueID: 99, GameKey: 423}
	require.NoError(t, mem.Write(res.Path(), []byte(`<fantasy_content><league></league></fantasy_content>`)))

	dl := mockDownloader(nil, errors.New("should not be called"))
	act := &FetchActivities{Storage: mem, Download: dl, GobCache: cache.NewGobCache(redisClient)}

	err := act.fetchYahooResource(context.Background(), res)
	require.NoError(t, err)
}

func TestFetchYahooResource_DownloadAndWrite(t *testing.T) {
	t.Parallel()

	mem := store.NewMemStorage()
	redisClient, mockRedis := redismock.NewClientMock()

	res := resource.Transactions{Season: 2023, LeagueID: 99, GameKey: 423}
	xml := []byte(`<fantasy_content><league><transactions count="0"></transactions></league></fantasy_content>`)

	mockRedis.CustomMatch(anyArgs).ExpectSet(core.RedisKey(res), "x", cache.GobCacheTTL).SetVal("OK")

	dl := mockDownloader(xml, nil)
	act := &FetchActivities{Storage: mem, Download: dl, GobCache: cache.NewGobCache(redisClient)}

	err := act.fetchYahooResource(context.Background(), res)
	require.NoError(t, err)
	assert.True(t, mem.Exists(res.Path()))
}

func TestFetchYahooResource_DownloadError(t *testing.T) {
	t.Parallel()

	mem := store.NewMemStorage()
	redisClient, _ := redismock.NewClientMock()

	res := resource.Transactions{Season: 2023, LeagueID: 99, GameKey: 423}
	act := &FetchActivities{
		Storage:  mem,
		Download: mockDownloader(nil, errors.New("timeout")),
		GobCache: cache.NewGobCache(redisClient),
	}

	err := act.fetchYahooResource(context.Background(), res)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timeout")
}
