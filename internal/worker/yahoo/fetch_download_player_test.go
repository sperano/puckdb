package yahoo

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/sperano/puckdb/internal/cache"
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
