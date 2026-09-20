package yahoo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/go-redis/redismock/v8"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	validFetchXML     = `<fantasy_content><league></league></fantasy_content>`
	malformedFetchXML = `<fantasy_content><league>`
)

// fetchResources lists every Yahoo resource type routed through Fetcher, so the
// behavioral tests below run identically for each of them.
func fetchResources() map[string]Resource {
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	return map[string]Resource{
		"league":       resource.League{Season: testYahooSeason, LeagueID: testLeagueID, GameKey: testGameKey},
		"team":         resource.Team{Season: testYahooSeason, LeagueID: testLeagueID, TeamID: 1, GameKey: testGameKey},
		"transactions": resource.Transactions{Season: testYahooSeason, LeagueID: testLeagueID, GameKey: testGameKey},
		"draft":        resource.DraftResults{Season: testYahooSeason, LeagueID: testLeagueID, GameKey: testGameKey},
		"matchups":     resource.Matchups{Season: testYahooSeason, LeagueID: testLeagueID, Week: 1, GameKey: testGameKey},
		"roster":       resource.Roster{LeagueID: testLeagueID, TeamID: 1, Date: day, GameKey: testGameKey},
		"team-summary": resource.TeamSummary{LeagueID: testLeagueID, TeamID: 1, Date: day, GameKey: testGameKey},
		"game-key":     resource.GameKey{Season: testYahooSeason},
	}
}

// fetchHarness wires a Fetcher to in-memory storage, a nil-client gob cache
// (degrades to storage reads) and a scripted downloader.
type fetchHarness struct {
	mem       *store.MemStorage
	fetcher   Fetcher
	downloads int
	throttled int
}

// newFetchHarness scripts the downloader to return each response in turn; the
// last one is repeated once exhausted.
func newFetchHarness(responses ...[]byte) *fetchHarness {
	h := &fetchHarness{mem: store.NewMemStorage()}
	h.fetcher = Fetcher{
		Storage:  h.mem,
		GobCache: cache.NewGobCache(nil),
		Download: func(_ context.Context, _ string) ([]byte, error) {
			h.downloads++
			i := min(h.downloads-1, len(responses)-1)
			return responses[i], nil
		},
		Throttle: func() { h.throttled++ },
	}
	return h
}

func (h *fetchHarness) stored(t *testing.T, res Resource) []byte {
	t.Helper()
	data, err := h.mem.Read(context.Background(), res.Path())
	require.NoError(t, err)
	return data
}

func forEachResource(t *testing.T, fn func(t *testing.T, res Resource)) {
	t.Helper()
	for name, res := range fetchResources() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fn(t, res)
		})
	}
}

func TestFetcher_CacheHitSkipsDownload(t *testing.T) {
	t.Parallel()
	forEachResource(t, func(t *testing.T, res Resource) {
		h := newFetchHarness([]byte(validFetchXML))
		require.NoError(t, h.mem.Write(context.Background(), res.Path(), []byte(validFetchXML)))

		content, origin, err := h.fetcher.Fetch(context.Background(), res)

		require.NoError(t, err)
		assert.NotNil(t, content)
		assert.Equal(t, core.OriginFileSystem, origin)
		assert.Equal(t, 0, h.downloads)
		assert.Equal(t, 0, h.throttled, "cache hits must not throttle")
	})
}

func TestFetcher_MissDownloadsAndStoresRawXML(t *testing.T) {
	t.Parallel()
	forEachResource(t, func(t *testing.T, res Resource) {
		h := newFetchHarness([]byte(validFetchXML))

		content, origin, err := h.fetcher.Fetch(context.Background(), res)

		require.NoError(t, err)
		assert.NotNil(t, content)
		assert.Equal(t, core.OriginRemoteYahooAPI, origin)
		assert.Equal(t, 1, h.downloads)
		assert.Equal(t, 1, h.throttled)
		assert.Equal(t, []byte(validFetchXML), h.stored(t, res), "raw XML must be stored verbatim")
	})
}

func TestFetcher_MalformedDownloadIsNeverStored(t *testing.T) {
	t.Parallel()
	forEachResource(t, func(t *testing.T, res Resource) {
		h := newFetchHarness([]byte(malformedFetchXML))

		_, _, err := h.fetcher.Fetch(context.Background(), res)

		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrDownload, "a parse failure is not a download failure")
		assert.False(t, h.mem.Exists(context.Background(), res.Path()), "malformed XML must not be written")
		assert.Equal(t, 0, h.throttled, "failed fetches must not throttle")
	})
}

func TestFetcher_MalformedCachedFileIsReplacedByValidDownload(t *testing.T) {
	t.Parallel()
	forEachResource(t, func(t *testing.T, res Resource) {
		h := newFetchHarness([]byte(validFetchXML))
		require.NoError(t, h.mem.Write(context.Background(), res.Path(), []byte(malformedFetchXML)))

		content, origin, err := h.fetcher.Fetch(context.Background(), res)

		require.NoError(t, err)
		assert.NotNil(t, content)
		assert.Equal(t, core.OriginRemoteYahooAPI, origin)
		assert.Equal(t, 1, h.downloads)
		assert.Equal(t, []byte(validFetchXML), h.stored(t, res), "valid download must overwrite the corrupt file")
	})
}

func TestFetcher_RepeatedMalformedDownloadRecoversOnValidResponse(t *testing.T) {
	t.Parallel()
	forEachResource(t, func(t *testing.T, res Resource) {
		// Corrupt file on disk, first re-download also malformed, second one valid.
		h := newFetchHarness([]byte(malformedFetchXML), []byte(validFetchXML))
		require.NoError(t, h.mem.Write(context.Background(), res.Path(), []byte(malformedFetchXML)))

		_, _, err := h.fetcher.Fetch(context.Background(), res)
		require.Error(t, err, "first attempt: malformed response must fail")
		assert.Equal(t, []byte(malformedFetchXML), h.stored(t, res), "failed attempt must not touch the file")

		_, origin, err := h.fetcher.Fetch(context.Background(), res)
		require.NoError(t, err, "second attempt: valid response must recover")
		assert.Equal(t, core.OriginRemoteYahooAPI, origin)
		assert.Equal(t, 2, h.downloads)
		assert.Equal(t, []byte(validFetchXML), h.stored(t, res))
	})
}

func TestFetcher_DownloadErrorIsErrDownload(t *testing.T) {
	t.Parallel()
	forEachResource(t, func(t *testing.T, res Resource) {
		h := newFetchHarness()
		netErr := errors.New("network error")
		h.fetcher.Download = func(_ context.Context, _ string) ([]byte, error) { return nil, netErr }

		_, _, err := h.fetcher.Fetch(context.Background(), res)

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrDownload)
		assert.ErrorIs(t, err, netErr)
		assert.False(t, h.mem.Exists(context.Background(), res.Path()))
		assert.Equal(t, 0, h.throttled)
	})
}

func TestFetcher_Redis(t *testing.T) {
	t.Parallel()
	res := resource.League{Season: testYahooSeason, LeagueID: testLeagueID, GameKey: testGameKey}

	t.Run("valid download populates redis", func(t *testing.T) {
		t.Parallel()
		redisClient, mockRedis := redismock.NewClientMock()
		mockRedis.ExpectGet(core.RedisKey(res)).SetErr(redis.Nil)
		mockRedis.CustomMatch(anyArgs).ExpectSet(core.RedisKey(res), "x", cache.GobCacheTTL).SetVal("OK")

		f := Fetcher{
			Storage:  store.NewMemStorage(),
			GobCache: cache.NewGobCache(redisClient),
			Download: mockDownloader([]byte(validFetchXML), nil),
			Throttle: func() {},
		}
		_, _, err := f.Fetch(context.Background(), res)

		require.NoError(t, err)
		require.NoError(t, mockRedis.ExpectationsWereMet())
	})

	t.Run("redis read failure is an error, not a re-download", func(t *testing.T) {
		t.Parallel()
		redisClient, mockRedis := redismock.NewClientMock()
		mockRedis.ExpectGet(core.RedisKey(res)).SetErr(errors.New("CLUSTERDOWN"))

		downloads := 0
		f := Fetcher{
			Storage:  store.NewMemStorage(),
			GobCache: cache.NewGobCache(redisClient),
			Download: shared.Downloader(func(_ context.Context, _ string) ([]byte, error) {
				downloads++
				return []byte(validFetchXML), nil
			}),
		}
		_, _, err := f.Fetch(context.Background(), res)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "CLUSTERDOWN")
		assert.Equal(t, 0, downloads, "a Redis outage must not fan out into Yahoo downloads")
	})
}
