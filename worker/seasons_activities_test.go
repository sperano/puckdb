package worker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/go-redis/redismock/v8"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/graph/model"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
)

// --- Test helpers ---

func ptr(i int) *int {
	return &i
}

// d is a helper to create nhl.Date from string for tests.
func d(s string) nhl.Date {
	return nhl.MustParseDate(s)
}

// marshalSeasonsManifest wraps seasons in the API response format for filesystem storage.
func marshalSeasonsManifest(t *testing.T, seasons []nhl.SeasonInfo) []byte {
	data, err := json.Marshal(nhl.SeasonsResponse{Seasons: seasons})
	require.NoError(t, err)
	return data
}

// anyArgs is a redismock matcher that accepts any arguments.
func anySeasonsArgs(expected, actual []interface{}) error { return nil }

// --- Test storage helpers ---

// failingWriteStorage wraps a storage and fails all Write operations.
type failingWriteStorage struct {
	store.Storage
}

func (f *failingWriteStorage) Write(path string, data []byte) error {
	return errors.New("simulated write failure")
}

// failingStatStorage wraps a storage and fails Stat with a non-NotExist error.
type failingStatStorage struct {
	*store.MemStorage
}

func (f *failingStatStorage) Stat(path string) (os.FileInfo, error) {
	// Return permission denied (not NotExist) to trigger the else branch
	return nil, os.ErrPermission
}

// --- filterSeasons tests ---

func TestFilterSeasons_NoFilters(t *testing.T) {
	t.Parallel()

	seasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2022), StandingsStart: d("2022-10-07"), StandingsEnd: d("2023-04-14")},
		{ID: nhl.NewSeason(2023), StandingsStart: d("2023-10-10"), StandingsEnd: d("2024-04-18")},
		{ID: nhl.NewSeason(2024), StandingsStart: d("2024-10-04"), StandingsEnd: d("2025-04-17")},
	}

	result := filterSeasons(seasons, &model.SeasonsInput{})

	assert.Len(t, result, 3)
	assert.Equal(t, 2022, result[0].ID.StartYear())
	assert.Equal(t, 2023, result[1].ID.StartYear())
	assert.Equal(t, 2024, result[2].ID.StartYear())
}

func TestFilterSeasons_StartFilter(t *testing.T) {
	t.Parallel()

	seasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2020), StandingsStart: d("2020-01-13"), StandingsEnd: d("2020-08-31")},
		{ID: nhl.NewSeason(2021), StandingsStart: d("2021-01-13"), StandingsEnd: d("2021-05-19")},
		{ID: nhl.NewSeason(2022), StandingsStart: d("2022-10-07"), StandingsEnd: d("2023-04-14")},
		{ID: nhl.NewSeason(2023), StandingsStart: d("2023-10-10"), StandingsEnd: d("2024-04-18")},
	}

	result := filterSeasons(seasons, &model.SeasonsInput{StartSeason: ptr(2022)})

	assert.Len(t, result, 2)
	assert.Equal(t, 2022, result[0].ID.StartYear())
	assert.Equal(t, 2023, result[1].ID.StartYear())
}

func TestFilterSeasons_EndFilter(t *testing.T) {
	t.Parallel()

	seasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2020), StandingsStart: d("2020-01-13"), StandingsEnd: d("2020-08-31")},
		{ID: nhl.NewSeason(2021), StandingsStart: d("2021-01-13"), StandingsEnd: d("2021-05-19")},
		{ID: nhl.NewSeason(2022), StandingsStart: d("2022-10-07"), StandingsEnd: d("2023-04-14")},
		{ID: nhl.NewSeason(2023), StandingsStart: d("2023-10-10"), StandingsEnd: d("2024-04-18")},
	}

	result := filterSeasons(seasons, &model.SeasonsInput{EndSeason: ptr(2021)})

	assert.Len(t, result, 2)
	assert.Equal(t, 2020, result[0].ID.StartYear())
	assert.Equal(t, 2021, result[1].ID.StartYear())
}

func TestFilterSeasons_BothFilters(t *testing.T) {
	t.Parallel()

	seasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2020), StandingsStart: d("2020-01-13"), StandingsEnd: d("2020-08-31")},
		{ID: nhl.NewSeason(2021), StandingsStart: d("2021-01-13"), StandingsEnd: d("2021-05-19")},
		{ID: nhl.NewSeason(2022), StandingsStart: d("2022-10-07"), StandingsEnd: d("2023-04-14")},
		{ID: nhl.NewSeason(2023), StandingsStart: d("2023-10-10"), StandingsEnd: d("2024-04-18")},
	}

	result := filterSeasons(seasons, &model.SeasonsInput{
		StartSeason: ptr(2021),
		EndSeason:   ptr(2022),
	})

	assert.Len(t, result, 2)
	assert.Equal(t, 2021, result[0].ID.StartYear())
	assert.Equal(t, 2022, result[1].ID.StartYear())
}

func TestFilterSeasons_EmptyResult(t *testing.T) {
	t.Parallel()

	seasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2022), StandingsStart: d("2022-10-07"), StandingsEnd: d("2023-04-14")},
		{ID: nhl.NewSeason(2023), StandingsStart: d("2023-10-10"), StandingsEnd: d("2024-04-18")},
	}

	result := filterSeasons(seasons, &model.SeasonsInput{StartSeason: ptr(2099)})

	assert.Empty(t, result)
}

func TestSeasonInfo_Label(t *testing.T) {
	t.Parallel()

	tests := []struct {
		startYear int
		expected  string
	}{
		{2022, "2022-23"},
		{2023, "2023-24"},
		{2024, "2024-25"},
		{1999, "1999-00"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			s := nhl.SeasonInfo{ID: nhl.NewSeason(tt.startYear)}
			assert.Equal(t, tt.expected, s.Label())
		})
	}
}

// --- FetchSeasonsManifest tests (struct-based) ---

type FetchSeasonsManifestTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestActivityEnvironment
}

func (s *FetchSeasonsManifestTestSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
}

func TestFetchSeasonsManifestTestSuite(t *testing.T) {
	suite.Run(t, new(FetchSeasonsManifestTestSuite))
}

func (s *FetchSeasonsManifestTestSuite) TestFetchSeasonsManifest_Success() {
	mem := store.NewMemStorage()
	redisClient, mockRedis := redismock.NewClientMock()

	seasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2022), StandingsStart: d("2022-10-07"), StandingsEnd: d("2023-04-14")},
		{ID: nhl.NewSeason(2023), StandingsStart: d("2023-10-10"), StandingsEnd: d("2024-04-18")},
		{ID: nhl.NewSeason(2024), StandingsStart: d("2024-10-04"), StandingsEnd: d("2025-04-17")},
	}
	// Pre-populate filesystem
	require.NoError(s.T(), resource.WriteParsed(mem, resource.SeasonsManifest{}, nhl.SeasonsResponse{Seasons: seasons}))

	// Redis miss - will read from filesystem
	mockRedis.ExpectGet(redisSeasonsManifestKey).SetErr(redis.Nil)
	mockRedis.CustomMatch(anySeasonsArgs).ExpectSet(redisSeasonsManifestKey, "x", config.DefaultSeasonsManifestCacheTTL).SetVal("OK")

	activities := &SeasonsActivities{
		Storage:  mem,
		GobCache: cache.NewGobCache(redisClient),
	}
	s.env.RegisterActivity(activities.FetchSeasonsManifest)

	// Filter to only seasons starting 2023+
	input := &model.SeasonsInput{StartSeason: ptr(2023)}
	future, err := s.env.ExecuteActivity(activities.FetchSeasonsManifest, input)

	require.NoError(s.T(), err)

	var result FetchSeasonsManifestResult
	require.NoError(s.T(), future.Get(&result))
	assert.Len(s.T(), result.Seasons, 2)
	assert.Equal(s.T(), 2023, result.Seasons[0].ID.StartYear())
	assert.Equal(s.T(), 2024, result.Seasons[1].ID.StartYear())
}

// --- FetchSeasonsManifest tests ---

func TestDownloadSeasonsManifest_RedisHit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	redisClient, mockRedis := redismock.NewClientMock()
	nhlClient := &MockNHLClient{}

	seasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2022), StandingsStart: nhl.MustParseDate("2022-10-07"), StandingsEnd: nhl.MustParseDate("2023-04-14")},
		{ID: nhl.NewSeason(2023), StandingsStart: nhl.MustParseDate("2023-10-10"), StandingsEnd: nhl.MustParseDate("2024-04-18")},
	}
	seasonsJSON := marshalSeasonsManifest(t, seasons)

	mockRedis.ExpectGet(redisSeasonsManifestKey).SetVal(string(seasonsJSON))

	result, err := (&SeasonsActivities{Storage: mem, RedisClient: redisClient, NHLClient: nhlClient}).FetchSeasonsManifest(ctx, nil)

	require.NoError(t, err)
	assert.Equal(t, 2, len(result.Seasons))
	assert.Equal(t, core.OriginRedis, result.Origin)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
	nhlClient.AssertNotCalled(t, "SeasonStandingManifest")
}

func TestDownloadSeasonsManifest_AllMiss_APIFetch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	redisClient, mockRedis := redismock.NewClientMock()
	nhlClient := &MockNHLClient{}

	seasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2022), StandingsStart: nhl.MustParseDate("2022-10-07"), StandingsEnd: nhl.MustParseDate("2023-04-14")},
		{ID: nhl.NewSeason(2023), StandingsStart: nhl.MustParseDate("2023-10-10"), StandingsEnd: nhl.MustParseDate("2024-04-18")},
		{ID: nhl.NewSeason(2024), StandingsStart: nhl.MustParseDate("2024-10-04"), StandingsEnd: nhl.MustParseDate("2025-04-17")},
	}
	seasonsJSON := marshalSeasonsManifest(t, seasons)

	// Redis miss
	mockRedis.ExpectGet(redisSeasonsManifestKey).SetErr(redis.Nil)
	// API fetch
	nhlClient.On("SeasonStandingManifest", ctx).Return(seasons, nil)
	// Cache in Redis
	mockRedis.ExpectSet(redisSeasonsManifestKey, seasonsJSON, config.DefaultSeasonsManifestCacheTTL).SetVal("OK")

	result, err := (&SeasonsActivities{Storage: mem, RedisClient: redisClient, NHLClient: nhlClient}).FetchSeasonsManifest(ctx, nil)

	require.NoError(t, err)
	assert.Equal(t, 3, len(result.Seasons))
	assert.Equal(t, core.OriginRemoteNHLAPI, result.Origin)
	nhlClient.AssertExpectations(t)
	assert.NoError(t, mockRedis.ExpectationsWereMet())

	// Verify saved to filesystem
	assert.True(t, mem.Exists(resource.SeasonsManifest{}.Path()))
}

func TestDownloadSeasonsManifest_APIError_NoFallback(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	redisClient, mockRedis := redismock.NewClientMock()
	nhlClient := &MockNHLClient{}

	// Redis miss
	mockRedis.ExpectGet(redisSeasonsManifestKey).SetErr(redis.Nil)
	// API fails
	nhlClient.On("SeasonStandingManifest", ctx).Return(nil, errors.New("API unavailable"))

	result, err := (&SeasonsActivities{Storage: mem, RedisClient: redisClient, NHLClient: nhlClient}).FetchSeasonsManifest(ctx, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "API unavailable")
	assert.Equal(t, 0, len(result.Seasons))
	nhlClient.AssertExpectations(t)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestDownloadSeasonsManifest_RedisHit_InvalidJSON(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	redisClient, mockRedis := redismock.NewClientMock()
	nhlClient := &MockNHLClient{}

	seasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2022), StandingsStart: nhl.MustParseDate("2022-10-07"), StandingsEnd: nhl.MustParseDate("2023-04-14")},
	}
	seasonsJSON := marshalSeasonsManifest(t, seasons)

	// Redis returns invalid JSON - should log warning and fall through
	mockRedis.ExpectGet(redisSeasonsManifestKey).SetVal("invalid json {{{")
	// API fetch
	nhlClient.On("SeasonStandingManifest", ctx).Return(seasons, nil)
	// Cache in Redis
	mockRedis.ExpectSet(redisSeasonsManifestKey, seasonsJSON, config.DefaultSeasonsManifestCacheTTL).SetVal("OK")

	result, err := (&SeasonsActivities{Storage: mem, RedisClient: redisClient, NHLClient: nhlClient}).FetchSeasonsManifest(ctx, nil)

	require.NoError(t, err)
	assert.Equal(t, 1, len(result.Seasons))
	assert.Equal(t, core.OriginRemoteNHLAPI, result.Origin)
	nhlClient.AssertExpectations(t)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestDownloadSeasonsManifest_FreshFilesystemHit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	redisClient, mockRedis := redismock.NewClientMock()
	nhlClient := &MockNHLClient{}

	seasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2022), StandingsStart: nhl.MustParseDate("2022-10-07"), StandingsEnd: nhl.MustParseDate("2023-04-14")},
		{ID: nhl.NewSeason(2023), StandingsStart: nhl.MustParseDate("2023-10-10"), StandingsEnd: nhl.MustParseDate("2024-04-18")},
	}
	seasonsJSON := marshalSeasonsManifest(t, seasons)

	// Pre-populate filesystem with fresh data (recent modtime)
	mem.SetFileWithTime(resource.SeasonsManifest{}.Path(), seasonsJSON, time.Now())

	// Redis miss
	mockRedis.ExpectGet(redisSeasonsManifestKey).SetErr(redis.Nil)
	// Backfill Redis from FS
	mockRedis.ExpectSet(redisSeasonsManifestKey, seasonsJSON, config.DefaultSeasonsManifestCacheTTL).SetVal("OK")

	result, err := (&SeasonsActivities{Storage: mem, RedisClient: redisClient, NHLClient: nhlClient}).FetchSeasonsManifest(ctx, nil)

	require.NoError(t, err)
	assert.Equal(t, 2, len(result.Seasons))
	assert.Equal(t, core.OriginFileSystem, result.Origin)
	nhlClient.AssertNotCalled(t, "SeasonStandingManifest")
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestDownloadSeasonsManifest_FreshFilesystemHit_GetManifestError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	redisClient, mockRedis := redismock.NewClientMock()
	nhlClient := &MockNHLClient{}

	// Pre-populate filesystem with invalid JSON (fresh modtime)
	// Read will succeed but Parse will fail to unmarshal
	mem.SetFileWithTime(resource.SeasonsManifest{}.Path(), []byte("invalid json"), time.Now())

	// Redis miss
	mockRedis.ExpectGet(redisSeasonsManifestKey).SetErr(redis.Nil)

	result, err := (&SeasonsActivities{Storage: mem, RedisClient: redisClient, NHLClient: nhlClient}).FetchSeasonsManifest(ctx, nil)

	require.Error(t, err)
	assert.Equal(t, 0, len(result.Seasons))
	nhlClient.AssertNotCalled(t, "SeasonStandingManifest")
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestDownloadSeasonsManifest_StaleFilesystem_APISuccess(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	redisClient, mockRedis := redismock.NewClientMock()
	nhlClient := &MockNHLClient{}

	oldSeasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2022), StandingsStart: nhl.MustParseDate("2022-10-07"), StandingsEnd: nhl.MustParseDate("2023-04-14")},
	}
	oldSeasonsJSON := marshalSeasonsManifest(t, oldSeasons)

	newSeasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2022), StandingsStart: nhl.MustParseDate("2022-10-07"), StandingsEnd: nhl.MustParseDate("2023-04-14")},
		{ID: nhl.NewSeason(2023), StandingsStart: nhl.MustParseDate("2023-10-10"), StandingsEnd: nhl.MustParseDate("2024-04-18")},
	}
	newSeasonsJSON := marshalSeasonsManifest(t, newSeasons)

	// Pre-populate filesystem with stale data (old modtime)
	staleTime := time.Now().Add(-config.DefaultSeasonsManifestStaleTTL - time.Hour)
	mem.SetFileWithTime(resource.SeasonsManifest{}.Path(), oldSeasonsJSON, staleTime)

	// Redis miss
	mockRedis.ExpectGet(redisSeasonsManifestKey).SetErr(redis.Nil)
	// API fetch (returns new data)
	nhlClient.On("SeasonStandingManifest", ctx).Return(newSeasons, nil)
	// Cache in Redis
	mockRedis.ExpectSet(redisSeasonsManifestKey, newSeasonsJSON, config.DefaultSeasonsManifestCacheTTL).SetVal("OK")

	result, err := (&SeasonsActivities{Storage: mem, RedisClient: redisClient, NHLClient: nhlClient}).FetchSeasonsManifest(ctx, nil)

	require.NoError(t, err)
	assert.Equal(t, 2, len(result.Seasons))
	assert.Equal(t, core.OriginRemoteNHLAPI, result.Origin)
	nhlClient.AssertExpectations(t)
	assert.NoError(t, mockRedis.ExpectationsWereMet())

	// Verify new data was saved to filesystem
	savedData, err := mem.Read(resource.SeasonsManifest{}.Path())
	require.NoError(t, err)
	savedResponse, err := resource.SeasonsManifest{}.Parse(savedData)
	require.NoError(t, err)
	assert.Equal(t, 2, len(savedResponse.Seasons))
}

func TestDownloadSeasonsManifest_APIError_StaleFallback(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	redisClient, mockRedis := redismock.NewClientMock()
	nhlClient := &MockNHLClient{}

	staleSeasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2022), StandingsStart: nhl.MustParseDate("2022-10-07"), StandingsEnd: nhl.MustParseDate("2023-04-14")},
		{ID: nhl.NewSeason(2023), StandingsStart: nhl.MustParseDate("2023-10-10"), StandingsEnd: nhl.MustParseDate("2024-04-18")},
	}
	staleSeasonsJSON := marshalSeasonsManifest(t, staleSeasons)

	// Pre-populate filesystem with stale but valid data
	staleTime := time.Now().Add(-config.DefaultSeasonsManifestStaleTTL - time.Hour)
	mem.SetFileWithTime(resource.SeasonsManifest{}.Path(), staleSeasonsJSON, staleTime)

	// Redis miss
	mockRedis.ExpectGet(redisSeasonsManifestKey).SetErr(redis.Nil)
	// API fails
	nhlClient.On("SeasonStandingManifest", ctx).Return(nil, errors.New("API unavailable"))
	// Stale data backfilled to Redis
	mockRedis.ExpectSet(redisSeasonsManifestKey, staleSeasonsJSON, config.DefaultSeasonsManifestCacheTTL).SetVal("OK")

	result, err := (&SeasonsActivities{Storage: mem, RedisClient: redisClient, NHLClient: nhlClient}).FetchSeasonsManifest(ctx, nil)

	// Should succeed using stale fallback
	require.NoError(t, err)
	assert.Equal(t, 2, len(result.Seasons))
	assert.Equal(t, core.OriginFileSystem, result.Origin)
	nhlClient.AssertExpectations(t)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestDownloadSeasonsManifest_APIError_StaleFallbackUnmarshalFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	redisClient, mockRedis := redismock.NewClientMock()
	nhlClient := &MockNHLClient{}

	// Pre-populate filesystem with stale invalid JSON
	staleTime := time.Now().Add(-config.DefaultSeasonsManifestStaleTTL - time.Hour)
	mem.SetFileWithTime(resource.SeasonsManifest{}.Path(), []byte("invalid json"), staleTime)

	// Redis miss
	mockRedis.ExpectGet(redisSeasonsManifestKey).SetErr(redis.Nil)
	// API fails
	nhlClient.On("SeasonStandingManifest", ctx).Return(nil, errors.New("API unavailable"))

	result, err := (&SeasonsActivities{Storage: mem, RedisClient: redisClient, NHLClient: nhlClient}).FetchSeasonsManifest(ctx, nil)

	// Should fail - API error and stale data is invalid
	require.Error(t, err)
	assert.Contains(t, err.Error(), "API unavailable")
	assert.Equal(t, 0, len(result.Seasons))
	nhlClient.AssertExpectations(t)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestDownloadSeasonsManifest_SaveManifestError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	failingStorage := &failingWriteStorage{Storage: mem}
	redisClient, mockRedis := redismock.NewClientMock()
	nhlClient := &MockNHLClient{}

	seasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2022), StandingsStart: nhl.MustParseDate("2022-10-07"), StandingsEnd: nhl.MustParseDate("2023-04-14")},
	}
	seasonsJSON := marshalSeasonsManifest(t, seasons)

	// Redis miss
	mockRedis.ExpectGet(redisSeasonsManifestKey).SetErr(redis.Nil)
	// API succeeds
	nhlClient.On("SeasonStandingManifest", ctx).Return(seasons, nil)
	// Cache in Redis (this still succeeds)
	mockRedis.ExpectSet(redisSeasonsManifestKey, seasonsJSON, config.DefaultSeasonsManifestCacheTTL).SetVal("OK")

	result, err := (&SeasonsActivities{Storage: failingStorage, RedisClient: redisClient, NHLClient: nhlClient}).FetchSeasonsManifest(ctx, nil)

	// Should still succeed - SaveManifest failure only logs warning
	require.NoError(t, err)
	assert.Equal(t, 1, len(result.Seasons))
	assert.Equal(t, core.OriginRemoteNHLAPI, result.Origin)
	nhlClient.AssertExpectations(t)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestDownloadSeasonsManifest_CacheInRedisError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	redisClient, mockRedis := redismock.NewClientMock()
	nhlClient := &MockNHLClient{}

	seasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2022), StandingsStart: nhl.MustParseDate("2022-10-07"), StandingsEnd: nhl.MustParseDate("2023-04-14")},
	}
	seasonsJSON := marshalSeasonsManifest(t, seasons)

	// Redis miss
	mockRedis.ExpectGet(redisSeasonsManifestKey).SetErr(redis.Nil)
	// API succeeds
	nhlClient.On("SeasonStandingManifest", ctx).Return(seasons, nil)
	// Cache in Redis fails
	mockRedis.ExpectSet(redisSeasonsManifestKey, seasonsJSON, config.DefaultSeasonsManifestCacheTTL).SetErr(errors.New("redis error"))

	result, err := (&SeasonsActivities{Storage: mem, RedisClient: redisClient, NHLClient: nhlClient}).FetchSeasonsManifest(ctx, nil)

	// Should still succeed - cacheInRedis failure only logs warning
	require.NoError(t, err)
	assert.Equal(t, 1, len(result.Seasons))
	nhlClient.AssertExpectations(t)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestDownloadSeasonsManifest_StatError_TreatedAsStale(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	failingStat := &failingStatStorage{MemStorage: mem}
	redisClient, mockRedis := redismock.NewClientMock()
	nhlClient := &MockNHLClient{}

	oldSeasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2022), StandingsStart: nhl.MustParseDate("2022-10-07"), StandingsEnd: nhl.MustParseDate("2023-04-14")},
	}
	oldSeasonsJSON := marshalSeasonsManifest(t, oldSeasons)

	newSeasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2022), StandingsStart: nhl.MustParseDate("2022-10-07"), StandingsEnd: nhl.MustParseDate("2023-04-14")},
		{ID: nhl.NewSeason(2023), StandingsStart: nhl.MustParseDate("2023-10-10"), StandingsEnd: nhl.MustParseDate("2024-04-18")},
	}
	newSeasonsJSON := marshalSeasonsManifest(t, newSeasons)

	// Pre-populate filesystem with data (Stat will fail with permission error)
	mem.SetFile(resource.SeasonsManifest{}.Path(), oldSeasonsJSON)

	// Redis miss
	mockRedis.ExpectGet(redisSeasonsManifestKey).SetErr(redis.Nil)
	// API succeeds (stat error treated as stale, so we fetch from API)
	nhlClient.On("SeasonStandingManifest", ctx).Return(newSeasons, nil)
	// Cache in Redis
	mockRedis.ExpectSet(redisSeasonsManifestKey, newSeasonsJSON, config.DefaultSeasonsManifestCacheTTL).SetVal("OK")

	result, err := (&SeasonsActivities{Storage: failingStat, RedisClient: redisClient, NHLClient: nhlClient}).FetchSeasonsManifest(ctx, nil)

	require.NoError(t, err)
	assert.Equal(t, 2, len(result.Seasons))
	assert.Equal(t, core.OriginRemoteNHLAPI, result.Origin)
	nhlClient.AssertExpectations(t)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

// --- downloadSeasonStandings tests ---

func TestDownloadSeasonStandings_CacheHit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	client := &MockNHLClient{}

	season := nhl.NewSeason(2022)

	standings := []nhl.Standing{
		{TeamAbbrev: nhl.LocalizedString{Default: "MTL"}, TeamName: nhl.LocalizedString{Default: "Montreal Canadiens"}},
		{TeamAbbrev: nhl.LocalizedString{Default: "TOR"}, TeamName: nhl.LocalizedString{Default: "Toronto Maple Leafs"}},
	}
	require.NoError(t, resource.WriteParsed(mem, resource.SeasonStandings{Season: season}, standings))

	a := &SeasonsActivities{Storage: mem, NHLClient: client}
	result, err := a.downloadSeasonStandings(ctx, season)

	require.NoError(t, err)
	assert.Equal(t, season, result.Season)
	assert.Equal(t, 2, result.TeamCount)
	assert.True(t, result.FromCache)
	client.AssertNotCalled(t, "LeagueStandingsForSeason")
}

func TestDownloadSeasonStandings_CacheMiss(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	client := &MockNHLClient{}

	season := nhl.NewSeason(2022)

	standings := []nhl.Standing{
		{TeamAbbrev: nhl.LocalizedString{Default: "MTL"}, TeamName: nhl.LocalizedString{Default: "Montreal Canadiens"}},
		{TeamAbbrev: nhl.LocalizedString{Default: "TOR"}, TeamName: nhl.LocalizedString{Default: "Toronto Maple Leafs"}},
		{TeamAbbrev: nhl.LocalizedString{Default: "BOS"}, TeamName: nhl.LocalizedString{Default: "Boston Bruins"}},
	}
	client.On("LeagueStandingsForSeason", ctx, season).Return(standings, nil)

	a := &SeasonsActivities{Storage: mem, NHLClient: client}
	result, err := a.downloadSeasonStandings(ctx, season)

	require.NoError(t, err)
	assert.Equal(t, season, result.Season)
	assert.Equal(t, 3, result.TeamCount)
	assert.False(t, result.FromCache)
	client.AssertExpectations(t)

	// Verify saved
	assert.True(t, mem.Exists(resource.SeasonStandings{Season: season}.Path()))
}

func TestDownloadSeasonStandings_APIError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	client := &MockNHLClient{}

	season := nhl.NewSeason(2022)

	client.On("LeagueStandingsForSeason", ctx, season).Return(nil, errors.New("API unavailable"))

	a := &SeasonsActivities{Storage: mem, NHLClient: client}
	result, err := a.downloadSeasonStandings(ctx, season)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "API unavailable")
	assert.Equal(t, season, result.Season)
	assert.Equal(t, 0, result.TeamCount)
	client.AssertExpectations(t)
}

func TestDownloadSeasonStandings_CacheCorrupted(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	client := &MockNHLClient{}

	season := nhl.NewSeason(2022)

	// Pre-populate cache with invalid JSON
	mem.SetFile(resource.SeasonStandings{Season: season}.Path(), []byte("invalid json"))

	standings := []nhl.Standing{
		{TeamAbbrev: nhl.LocalizedString{Default: "MTL"}, TeamName: nhl.LocalizedString{Default: "Montreal Canadiens"}},
	}
	client.On("LeagueStandingsForSeason", ctx, season).Return(standings, nil)

	a := &SeasonsActivities{Storage: mem, NHLClient: client}
	result, err := a.downloadSeasonStandings(ctx, season)

	// Should succeed by falling back to API
	require.NoError(t, err)
	assert.Equal(t, season, result.Season)
	assert.Equal(t, 1, result.TeamCount)
	assert.False(t, result.FromCache)
	client.AssertExpectations(t)
}

func TestDownloadSeasonStandings_SaveStandingsError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	failingStorage := &failingWriteStorage{Storage: mem}
	client := &MockNHLClient{}

	season := nhl.NewSeason(2022)

	standings := []nhl.Standing{
		{TeamAbbrev: nhl.LocalizedString{Default: "MTL"}, TeamName: nhl.LocalizedString{Default: "Montreal Canadiens"}},
	}
	client.On("LeagueStandingsForSeason", ctx, season).Return(standings, nil)

	a := &SeasonsActivities{Storage: failingStorage, NHLClient: client}
	result, err := a.downloadSeasonStandings(ctx, season)

	// Should still succeed - SaveStandings failure only logs warning
	require.NoError(t, err)
	assert.Equal(t, season, result.Season)
	assert.Equal(t, 1, result.TeamCount)
	assert.False(t, result.FromCache)
	client.AssertExpectations(t)
}

// --- UpsertSeasons tests (struct-based via Temporal suite) ---

type UpsertSeasonsTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestActivityEnvironment
}

func (s *UpsertSeasonsTestSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
}

func TestUpsertSeasonsTestSuite(t *testing.T) {
	suite.Run(t, new(UpsertSeasonsTestSuite))
}

func (s *UpsertSeasonsTestSuite) TestUpsertSeasons_Success() {
	mem := store.NewMemStorage()
	redisClient, mockRedis := redismock.NewClientMock()
	upserter := &MockSeasonsUpserter{}

	seasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2022), StandingsStart: nhl.MustParseDate("2022-10-07"), StandingsEnd: nhl.MustParseDate("2023-04-14")},
		{ID: nhl.NewSeason(2023), StandingsStart: nhl.MustParseDate("2023-10-10"), StandingsEnd: nhl.MustParseDate("2024-04-18")},
	}
	// Pre-populate filesystem cache
	require.NoError(s.T(), resource.WriteParsed(mem, resource.SeasonsManifest{}, nhl.SeasonsResponse{Seasons: seasons}))

	// Redis miss - will read from filesystem, then populate cache
	seasonsManifestKey := core.RedisKey(resource.SeasonsManifest{})
	mockRedis.ExpectGet(seasonsManifestKey).SetErr(redis.Nil)
	mockRedis.CustomMatch(anySeasonsArgs).ExpectSet(seasonsManifestKey, "x", 10*time.Minute).SetVal("OK")

	upserter.On("UpsertSeason", mock.Anything, mock.AnythingOfType("sqlcdb.UpsertSeasonParams")).Return(nil).Times(2)

	activities := &SeasonsActivities{
		Storage:         mem,
		GobCache:        cache.NewGobCache(redisClient),
		SeasonsUpserter: upserter,
	}
	s.env.RegisterActivity(activities.UpsertSeasons)
	future, err := s.env.ExecuteActivity(activities.UpsertSeasons)

	require.NoError(s.T(), err)

	var result UpsertSeasonsResult
	require.NoError(s.T(), future.Get(&result))
	assert.Equal(s.T(), 2, result.SeasonsUpserted)
	upserter.AssertExpectations(s.T())
}

func (s *UpsertSeasonsTestSuite) TestUpsertSeasons_EmptyInput() {
	mem := store.NewMemStorage()
	redisClient, mockRedis := redismock.NewClientMock()
	upserter := &MockSeasonsUpserter{}

	// Empty seasons manifest
	require.NoError(s.T(), resource.WriteParsed(mem, resource.SeasonsManifest{}, nhl.SeasonsResponse{Seasons: []nhl.SeasonInfo{}}))

	seasonsManifestKey := core.RedisKey(resource.SeasonsManifest{})
	mockRedis.ExpectGet(seasonsManifestKey).SetErr(redis.Nil)
	mockRedis.CustomMatch(anySeasonsArgs).ExpectSet(seasonsManifestKey, "x", 10*time.Minute).SetVal("OK")

	activities := &SeasonsActivities{
		Storage:         mem,
		GobCache:        cache.NewGobCache(redisClient),
		SeasonsUpserter: upserter,
	}
	s.env.RegisterActivity(activities.UpsertSeasons)
	future, err := s.env.ExecuteActivity(activities.UpsertSeasons)

	require.NoError(s.T(), err)

	var result UpsertSeasonsResult
	require.NoError(s.T(), future.Get(&result))
	assert.Equal(s.T(), 0, result.SeasonsUpserted)
	upserter.AssertNotCalled(s.T(), "UpsertSeason")
}

func (s *UpsertSeasonsTestSuite) TestUpsertSeasons_UpsertError() {
	mem := store.NewMemStorage()
	redisClient, mockRedis := redismock.NewClientMock()
	upserter := &MockSeasonsUpserter{}

	seasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2022), StandingsStart: nhl.MustParseDate("2022-10-07"), StandingsEnd: nhl.MustParseDate("2023-04-14")},
	}
	require.NoError(s.T(), resource.WriteParsed(mem, resource.SeasonsManifest{}, nhl.SeasonsResponse{Seasons: seasons}))

	seasonsManifestKey := core.RedisKey(resource.SeasonsManifest{})
	mockRedis.ExpectGet(seasonsManifestKey).SetErr(redis.Nil)
	mockRedis.CustomMatch(anySeasonsArgs).ExpectSet(seasonsManifestKey, "x", 10*time.Minute).SetVal("OK")

	upserter.On("UpsertSeason", mock.Anything, mock.AnythingOfType("sqlcdb.UpsertSeasonParams")).Return(errors.New("database error"))

	activities := &SeasonsActivities{
		Storage:         mem,
		GobCache:        cache.NewGobCache(redisClient),
		SeasonsUpserter: upserter,
	}
	s.env.RegisterActivity(activities.UpsertSeasons)
	_, err := s.env.ExecuteActivity(activities.UpsertSeasons)

	require.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "upsert season")
	assert.Contains(s.T(), err.Error(), "database error")
	upserter.AssertExpectations(s.T())
}

// --- InitializeSeasonTeamsActivity tests (struct-based via Temporal suite) ---

type InitializeSeasonTeamsTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestActivityEnvironment
}

func (s *InitializeSeasonTeamsTestSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
}

func TestInitializeSeasonTeamsTestSuite(t *testing.T) {
	suite.Run(t, new(InitializeSeasonTeamsTestSuite))
}

func (s *InitializeSeasonTeamsTestSuite) TestInitializeSeasonTeams_Success() {
	mem := store.NewMemStorage()
	nhlClient := &MockNHLClient{}
	upserter := &MockSeasonTeamsUpserter{}

	season := nhl.NewSeason(2022)
	confName := "Eastern"
	confAbbrev := "E"

	standings := []nhl.Standing{
		{
			TeamAbbrev:       nhl.LocalizedString{Default: "MTL"},
			TeamName:         nhl.LocalizedString{Default: "Montreal Canadiens"},
			DivisionName:     "Atlantic",
			DivisionAbbrev:   "A",
			ConferenceName:   &confName,
			ConferenceAbbrev: &confAbbrev,
		},
		{
			TeamAbbrev:       nhl.LocalizedString{Default: "TOR"},
			TeamName:         nhl.LocalizedString{Default: "Toronto Maple Leafs"},
			DivisionName:     "Atlantic",
			DivisionAbbrev:   "A",
			ConferenceName:   &confName,
			ConferenceAbbrev: &confAbbrev,
		},
	}

	nhlClient.On("LeagueStandingsForSeason", mock.Anything, season).Return(standings, nil)
	upserter.On("UpsertSeasonTeam", mock.Anything, mock.AnythingOfType("sqlcdb.UpsertSeasonTeamParams")).Return(nil).Times(2)

	activities := &SeasonsActivities{
		Storage:             mem,
		NHLClient:           nhlClient,
		SeasonTeamsUpserter: upserter,
	}
	s.env.RegisterActivity(activities.InitializeSeasonTeamsActivity)
	future, err := s.env.ExecuteActivity(activities.InitializeSeasonTeamsActivity, season)

	require.NoError(s.T(), err)

	var result InitializeSeasonTeamsResult
	require.NoError(s.T(), future.Get(&result))
	assert.Equal(s.T(), season, result.Season)
	assert.Equal(s.T(), 2, result.DownloadResult.TeamCount)
	assert.Equal(s.T(), 2, result.UpsertResult.TeamsUpserted)
	nhlClient.AssertExpectations(s.T())
	upserter.AssertExpectations(s.T())
}
