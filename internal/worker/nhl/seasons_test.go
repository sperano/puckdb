package nhl

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/go-redis/redismock/v8"
	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
)

// ptr is a helper to create a pointer to an int for tests.
func ptr(i int) *int {
	return &i
}

// d is a helper to create nhlapi.Date from string for tests.
func d(s string) nhlapi.Date {
	return nhlapi.MustParseDate(s)
}

// marshalSeasonsManifest wraps seasons in the API response format for filesystem storage.
func marshalSeasonsManifest(t *testing.T, seasons []nhlapi.SeasonInfo) []byte {
	t.Helper()
	data, err := json.Marshal(nhlapi.SeasonsResponse{Seasons: seasons})
	require.NoError(t, err)
	return data
}

// gobEncodeSeasonsManifest encodes seasons as gob for Redis cache assertions.
func gobEncodeSeasonsManifest(t *testing.T, seasons []nhlapi.SeasonInfo) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buf).Encode(nhlapi.SeasonsResponse{Seasons: seasons}))
	return buf.String()
}

// anySeasonsArgs is a redismock matcher that accepts any arguments.
func anySeasonsArgs(expected, actual []any) error { return nil }

// failingWriteStorage wraps a storage and fails all Write operations.
type failingWriteStorage struct {
	store.Storage
}

func (f *failingWriteStorage) Write(_ context.Context, _ string, _ []byte) error {
	return errors.New("simulated write failure")
}

// failingStatStorage wraps a storage and fails Stat with a non-NotExist error.
type failingStatStorage struct {
	*store.MemStorage
}

func (f *failingStatStorage) Stat(_ context.Context, _ string) (os.FileInfo, error) {
	return nil, os.ErrPermission
}

// --- filterSeasons tests ---

// filterTestNow is a fixed reference time after every non-future test season.
var filterTestNow = d("2025-06-01").Time

func TestFilterSeasons_NoFilters(t *testing.T) {
	t.Parallel()

	seasons := []nhlapi.SeasonInfo{
		{ID: nhlapi.NewSeason(2022), StandingsStart: d("2022-10-07"), StandingsEnd: d("2023-04-14")},
		{ID: nhlapi.NewSeason(2023), StandingsStart: d("2023-10-10"), StandingsEnd: d("2024-04-18")},
		{ID: nhlapi.NewSeason(2024), StandingsStart: d("2024-10-04"), StandingsEnd: d("2025-04-17")},
	}

	result := filterSeasons(seasons, &model.SeasonsInput{}, filterTestNow)

	assert.Len(t, result, 3)
	assert.Equal(t, 2022, result[0].ID.StartYear())
	assert.Equal(t, 2023, result[1].ID.StartYear())
	assert.Equal(t, 2024, result[2].ID.StartYear())
}

func TestFilterSeasons_StartFilter(t *testing.T) {
	t.Parallel()

	seasons := []nhlapi.SeasonInfo{
		{ID: nhlapi.NewSeason(2020), StandingsStart: d("2020-01-13"), StandingsEnd: d("2020-08-31")},
		{ID: nhlapi.NewSeason(2021), StandingsStart: d("2021-01-13"), StandingsEnd: d("2021-05-19")},
		{ID: nhlapi.NewSeason(2022), StandingsStart: d("2022-10-07"), StandingsEnd: d("2023-04-14")},
		{ID: nhlapi.NewSeason(2023), StandingsStart: d("2023-10-10"), StandingsEnd: d("2024-04-18")},
	}

	result := filterSeasons(seasons, &model.SeasonsInput{StartSeason: ptr(2022)}, filterTestNow)

	assert.Len(t, result, 2)
	assert.Equal(t, 2022, result[0].ID.StartYear())
	assert.Equal(t, 2023, result[1].ID.StartYear())
}

func TestFilterSeasons_EndFilter(t *testing.T) {
	t.Parallel()

	seasons := []nhlapi.SeasonInfo{
		{ID: nhlapi.NewSeason(2020), StandingsStart: d("2020-01-13"), StandingsEnd: d("2020-08-31")},
		{ID: nhlapi.NewSeason(2021), StandingsStart: d("2021-01-13"), StandingsEnd: d("2021-05-19")},
		{ID: nhlapi.NewSeason(2022), StandingsStart: d("2022-10-07"), StandingsEnd: d("2023-04-14")},
		{ID: nhlapi.NewSeason(2023), StandingsStart: d("2023-10-10"), StandingsEnd: d("2024-04-18")},
	}

	result := filterSeasons(seasons, &model.SeasonsInput{EndSeason: ptr(2021)}, filterTestNow)

	assert.Len(t, result, 2)
	assert.Equal(t, 2020, result[0].ID.StartYear())
	assert.Equal(t, 2021, result[1].ID.StartYear())
}

func TestFilterSeasons_BothFilters(t *testing.T) {
	t.Parallel()

	seasons := []nhlapi.SeasonInfo{
		{ID: nhlapi.NewSeason(2020), StandingsStart: d("2020-01-13"), StandingsEnd: d("2020-08-31")},
		{ID: nhlapi.NewSeason(2021), StandingsStart: d("2021-01-13"), StandingsEnd: d("2021-05-19")},
		{ID: nhlapi.NewSeason(2022), StandingsStart: d("2022-10-07"), StandingsEnd: d("2023-04-14")},
		{ID: nhlapi.NewSeason(2023), StandingsStart: d("2023-10-10"), StandingsEnd: d("2024-04-18")},
	}

	result := filterSeasons(seasons, &model.SeasonsInput{
		StartSeason: ptr(2021),
		EndSeason:   ptr(2022),
	}, filterTestNow)

	assert.Len(t, result, 2)
	assert.Equal(t, 2021, result[0].ID.StartYear())
	assert.Equal(t, 2022, result[1].ID.StartYear())
}

func TestFilterSeasons_EmptyResult(t *testing.T) {
	t.Parallel()

	seasons := []nhlapi.SeasonInfo{
		{ID: nhlapi.NewSeason(2022), StandingsStart: d("2022-10-07"), StandingsEnd: d("2023-04-14")},
		{ID: nhlapi.NewSeason(2023), StandingsStart: d("2023-10-10"), StandingsEnd: d("2024-04-18")},
	}

	result := filterSeasons(seasons, &model.SeasonsInput{StartSeason: ptr(2099)}, filterTestNow)

	assert.Empty(t, result)
}

func TestFilterSeasons_SkipsUnstartedSeasons(t *testing.T) {
	t.Parallel()

	seasons := []nhlapi.SeasonInfo{
		{ID: nhlapi.NewSeason(2024), StandingsStart: d("2024-10-04"), StandingsEnd: d("2025-04-17")},
		{ID: nhlapi.NewSeason(2025), StandingsStart: d("2025-10-07"), StandingsEnd: d("2026-04-17")},
		{ID: nhlapi.NewSeason(2026), StandingsStart: d("2026-09-29"), StandingsEnd: d("2027-04-10")},
	}
	offseason := d("2026-07-15").Time

	// Nil input must still drop the not-yet-started season.
	result := filterSeasons(seasons, nil, offseason)
	assert.Len(t, result, 2)
	assert.Equal(t, 2024, result[0].ID.StartYear())
	assert.Equal(t, 2025, result[1].ID.StartYear())

	// An explicit range covering the future season doesn't resurrect it.
	result = filterSeasons(seasons, &model.SeasonsInput{StartSeason: ptr(2025), EndSeason: ptr(2026)}, offseason)
	assert.Len(t, result, 1)
	assert.Equal(t, 2025, result[0].ID.StartYear())

	// On opening day the season is included (StandingsStart is midnight).
	openingDay := d("2026-09-29").Time.Add(12 * time.Hour)
	result = filterSeasons(seasons, nil, openingDay)
	assert.Len(t, result, 3)
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
			s := nhlapi.SeasonInfo{ID: nhlapi.NewSeason(tt.startYear)}
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

	seasons := []nhlapi.SeasonInfo{
		{ID: nhlapi.NewSeason(2022), StandingsStart: d("2022-10-07"), StandingsEnd: d("2023-04-14")},
		{ID: nhlapi.NewSeason(2023), StandingsStart: d("2023-10-10"), StandingsEnd: d("2024-04-18")},
		{ID: nhlapi.NewSeason(2024), StandingsStart: d("2024-10-04"), StandingsEnd: d("2025-04-17")},
	}
	require.NoError(s.T(), resource.WriteParsed(context.Background(), mem, resource.SeasonsManifest{}, nhlapi.SeasonsResponse{Seasons: seasons}))

	mockRedis.ExpectGet(redisSeasonsManifestKey).SetErr(redis.Nil)
	mockRedis.CustomMatch(anySeasonsArgs).ExpectSet(redisSeasonsManifestKey, "x", cache.GobCacheTTL).SetVal("OK")

	activities := &SeasonsActivities{
		Storage:  mem,
		GobCache: cache.NewGobCache(redisClient),
	}
	s.env.RegisterActivity(activities.FetchSeasonsManifest)

	input := &model.SeasonsInput{StartSeason: ptr(2023)}
	future, err := s.env.ExecuteActivity(activities.FetchSeasonsManifest, input)

	require.NoError(s.T(), err)

	var result FetchSeasonsManifestResult
	require.NoError(s.T(), future.Get(&result))
	assert.Len(s.T(), result.Seasons, 2)
	assert.Equal(s.T(), 2023, result.Seasons[0].ID.StartYear())
	assert.Equal(s.T(), 2024, result.Seasons[1].ID.StartYear())
}

// --- FetchSeasonsManifest unit tests ---

func TestDownloadSeasonsManifest_RedisHit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	redisClient, mockRedis := redismock.NewClientMock()
	nhlClient := &MockNHLClient{}

	seasons := []nhlapi.SeasonInfo{
		{ID: nhlapi.NewSeason(2022), StandingsStart: nhlapi.MustParseDate("2022-10-07"), StandingsEnd: nhlapi.MustParseDate("2023-04-14")},
		{ID: nhlapi.NewSeason(2023), StandingsStart: nhlapi.MustParseDate("2023-10-10"), StandingsEnd: nhlapi.MustParseDate("2024-04-18")},
	}

	mockRedis.ExpectGet(redisSeasonsManifestKey).SetVal(gobEncodeSeasonsManifest(t, seasons))

	result, err := (&SeasonsActivities{Storage: mem, GobCache: cache.NewGobCache(redisClient), NHLClient: nhlClient}).FetchSeasonsManifest(ctx, nil)

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

	seasons := []nhlapi.SeasonInfo{
		{ID: nhlapi.NewSeason(2022), StandingsStart: nhlapi.MustParseDate("2022-10-07"), StandingsEnd: nhlapi.MustParseDate("2023-04-14")},
		{ID: nhlapi.NewSeason(2023), StandingsStart: nhlapi.MustParseDate("2023-10-10"), StandingsEnd: nhlapi.MustParseDate("2024-04-18")},
		{ID: nhlapi.NewSeason(2024), StandingsStart: nhlapi.MustParseDate("2024-10-04"), StandingsEnd: nhlapi.MustParseDate("2025-04-17")},
	}

	mockRedis.ExpectGet(redisSeasonsManifestKey).SetErr(redis.Nil)
	nhlClient.On("SeasonStandingManifest", ctx).Return(seasons, nil)
	mockRedis.CustomMatch(anySeasonsArgs).ExpectSet(redisSeasonsManifestKey, "x", cache.GobCacheTTL).SetVal("OK")

	result, err := (&SeasonsActivities{Storage: mem, GobCache: cache.NewGobCache(redisClient), NHLClient: nhlClient}).FetchSeasonsManifest(ctx, nil)

	require.NoError(t, err)
	assert.Equal(t, 3, len(result.Seasons))
	assert.Equal(t, core.OriginRemoteNHLAPI, result.Origin)
	nhlClient.AssertExpectations(t)
	assert.NoError(t, mockRedis.ExpectationsWereMet())

	assert.True(t, mem.Exists(context.Background(), resource.SeasonsManifest{}.Path()))
}

func TestDownloadSeasonsManifest_APIError_NoFallback(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	redisClient, mockRedis := redismock.NewClientMock()
	nhlClient := &MockNHLClient{}

	mockRedis.ExpectGet(redisSeasonsManifestKey).SetErr(redis.Nil)
	nhlClient.On("SeasonStandingManifest", ctx).Return(nil, errors.New("API unavailable"))

	result, err := (&SeasonsActivities{Storage: mem, GobCache: cache.NewGobCache(redisClient), NHLClient: nhlClient}).FetchSeasonsManifest(ctx, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "API unavailable")
	assert.Equal(t, 0, len(result.Seasons))
	nhlClient.AssertExpectations(t)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestDownloadSeasonsManifest_RedisHit_InvalidGob(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	redisClient, mockRedis := redismock.NewClientMock()
	nhlClient := &MockNHLClient{}

	seasons := []nhlapi.SeasonInfo{
		{ID: nhlapi.NewSeason(2022), StandingsStart: nhlapi.MustParseDate("2022-10-07"), StandingsEnd: nhlapi.MustParseDate("2023-04-14")},
	}

	// GobCache auto-evicts corrupted entries (DEL) then returns cache miss
	mockRedis.ExpectGet(redisSeasonsManifestKey).SetVal("invalid gob data")
	mockRedis.ExpectDel(redisSeasonsManifestKey).SetVal(1)
	nhlClient.On("SeasonStandingManifest", ctx).Return(seasons, nil)
	mockRedis.CustomMatch(anySeasonsArgs).ExpectSet(redisSeasonsManifestKey, "x", cache.GobCacheTTL).SetVal("OK")

	result, err := (&SeasonsActivities{Storage: mem, GobCache: cache.NewGobCache(redisClient), NHLClient: nhlClient}).FetchSeasonsManifest(ctx, nil)

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

	seasons := []nhlapi.SeasonInfo{
		{ID: nhlapi.NewSeason(2022), StandingsStart: nhlapi.MustParseDate("2022-10-07"), StandingsEnd: nhlapi.MustParseDate("2023-04-14")},
		{ID: nhlapi.NewSeason(2023), StandingsStart: nhlapi.MustParseDate("2023-10-10"), StandingsEnd: nhlapi.MustParseDate("2024-04-18")},
	}
	seasonsJSON := marshalSeasonsManifest(t, seasons)

	mem.SetFileWithTime(resource.SeasonsManifest{}.Path(), seasonsJSON, time.Now())

	mockRedis.ExpectGet(redisSeasonsManifestKey).SetErr(redis.Nil)
	mockRedis.CustomMatch(anySeasonsArgs).ExpectSet(redisSeasonsManifestKey, "x", cache.GobCacheTTL).SetVal("OK")

	result, err := (&SeasonsActivities{Storage: mem, GobCache: cache.NewGobCache(redisClient), NHLClient: nhlClient}).FetchSeasonsManifest(ctx, nil)

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

	mem.SetFileWithTime(resource.SeasonsManifest{}.Path(), []byte("invalid json"), time.Now())

	mockRedis.ExpectGet(redisSeasonsManifestKey).SetErr(redis.Nil)

	result, err := (&SeasonsActivities{Storage: mem, GobCache: cache.NewGobCache(redisClient), NHLClient: nhlClient}).FetchSeasonsManifest(ctx, nil)

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

	oldSeasons := []nhlapi.SeasonInfo{
		{ID: nhlapi.NewSeason(2022), StandingsStart: nhlapi.MustParseDate("2022-10-07"), StandingsEnd: nhlapi.MustParseDate("2023-04-14")},
	}
	oldSeasonsJSON := marshalSeasonsManifest(t, oldSeasons)

	newSeasons := []nhlapi.SeasonInfo{
		{ID: nhlapi.NewSeason(2022), StandingsStart: nhlapi.MustParseDate("2022-10-07"), StandingsEnd: nhlapi.MustParseDate("2023-04-14")},
		{ID: nhlapi.NewSeason(2023), StandingsStart: nhlapi.MustParseDate("2023-10-10"), StandingsEnd: nhlapi.MustParseDate("2024-04-18")},
	}

	staleTime := time.Now().Add(-config.DefaultSeasonsManifestStaleTTL - time.Hour)
	mem.SetFileWithTime(resource.SeasonsManifest{}.Path(), oldSeasonsJSON, staleTime)

	mockRedis.ExpectGet(redisSeasonsManifestKey).SetErr(redis.Nil)
	nhlClient.On("SeasonStandingManifest", ctx).Return(newSeasons, nil)
	mockRedis.CustomMatch(anySeasonsArgs).ExpectSet(redisSeasonsManifestKey, "x", cache.GobCacheTTL).SetVal("OK")

	result, err := (&SeasonsActivities{Storage: mem, GobCache: cache.NewGobCache(redisClient), NHLClient: nhlClient}).FetchSeasonsManifest(ctx, nil)

	require.NoError(t, err)
	assert.Equal(t, 2, len(result.Seasons))
	assert.Equal(t, core.OriginRemoteNHLAPI, result.Origin)
	nhlClient.AssertExpectations(t)
	assert.NoError(t, mockRedis.ExpectationsWereMet())

	savedData, err := mem.Read(context.Background(), resource.SeasonsManifest{}.Path())
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

	staleSeasons := []nhlapi.SeasonInfo{
		{ID: nhlapi.NewSeason(2022), StandingsStart: nhlapi.MustParseDate("2022-10-07"), StandingsEnd: nhlapi.MustParseDate("2023-04-14")},
		{ID: nhlapi.NewSeason(2023), StandingsStart: nhlapi.MustParseDate("2023-10-10"), StandingsEnd: nhlapi.MustParseDate("2024-04-18")},
	}
	staleSeasonsJSON := marshalSeasonsManifest(t, staleSeasons)

	staleTime := time.Now().Add(-config.DefaultSeasonsManifestStaleTTL - time.Hour)
	mem.SetFileWithTime(resource.SeasonsManifest{}.Path(), staleSeasonsJSON, staleTime)

	mockRedis.ExpectGet(redisSeasonsManifestKey).SetErr(redis.Nil)
	nhlClient.On("SeasonStandingManifest", ctx).Return(nil, errors.New("API unavailable"))
	mockRedis.CustomMatch(anySeasonsArgs).ExpectSet(redisSeasonsManifestKey, "x", cache.GobCacheTTL).SetVal("OK")

	result, err := (&SeasonsActivities{Storage: mem, GobCache: cache.NewGobCache(redisClient), NHLClient: nhlClient}).FetchSeasonsManifest(ctx, nil)

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

	staleTime := time.Now().Add(-config.DefaultSeasonsManifestStaleTTL - time.Hour)
	mem.SetFileWithTime(resource.SeasonsManifest{}.Path(), []byte("invalid json"), staleTime)

	mockRedis.ExpectGet(redisSeasonsManifestKey).SetErr(redis.Nil)
	nhlClient.On("SeasonStandingManifest", ctx).Return(nil, errors.New("API unavailable"))

	result, err := (&SeasonsActivities{Storage: mem, GobCache: cache.NewGobCache(redisClient), NHLClient: nhlClient}).FetchSeasonsManifest(ctx, nil)

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

	seasons := []nhlapi.SeasonInfo{
		{ID: nhlapi.NewSeason(2022), StandingsStart: nhlapi.MustParseDate("2022-10-07"), StandingsEnd: nhlapi.MustParseDate("2023-04-14")},
	}

	mockRedis.ExpectGet(redisSeasonsManifestKey).SetErr(redis.Nil)
	nhlClient.On("SeasonStandingManifest", ctx).Return(seasons, nil)
	mockRedis.CustomMatch(anySeasonsArgs).ExpectSet(redisSeasonsManifestKey, "x", cache.GobCacheTTL).SetVal("OK")

	result, err := (&SeasonsActivities{Storage: failingStorage, GobCache: cache.NewGobCache(redisClient), NHLClient: nhlClient}).FetchSeasonsManifest(ctx, nil)

	require.NoError(t, err)
	assert.Equal(t, 1, len(result.Seasons))
	assert.Equal(t, core.OriginRemoteNHLAPI, result.Origin)
	nhlClient.AssertExpectations(t)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestDownloadSeasonsManifest_GobCacheError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	redisClient, mockRedis := redismock.NewClientMock()
	nhlClient := &MockNHLClient{}

	seasons := []nhlapi.SeasonInfo{
		{ID: nhlapi.NewSeason(2022), StandingsStart: nhlapi.MustParseDate("2022-10-07"), StandingsEnd: nhlapi.MustParseDate("2023-04-14")},
	}

	mockRedis.ExpectGet(redisSeasonsManifestKey).SetErr(redis.Nil)
	nhlClient.On("SeasonStandingManifest", ctx).Return(seasons, nil)
	mockRedis.CustomMatch(anySeasonsArgs).ExpectSet(redisSeasonsManifestKey, "x", cache.GobCacheTTL).SetErr(errors.New("redis error"))

	result, err := (&SeasonsActivities{Storage: mem, GobCache: cache.NewGobCache(redisClient), NHLClient: nhlClient}).FetchSeasonsManifest(ctx, nil)

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

	oldSeasons := []nhlapi.SeasonInfo{
		{ID: nhlapi.NewSeason(2022), StandingsStart: nhlapi.MustParseDate("2022-10-07"), StandingsEnd: nhlapi.MustParseDate("2023-04-14")},
	}
	oldSeasonsJSON := marshalSeasonsManifest(t, oldSeasons)

	newSeasons := []nhlapi.SeasonInfo{
		{ID: nhlapi.NewSeason(2022), StandingsStart: nhlapi.MustParseDate("2022-10-07"), StandingsEnd: nhlapi.MustParseDate("2023-04-14")},
		{ID: nhlapi.NewSeason(2023), StandingsStart: nhlapi.MustParseDate("2023-10-10"), StandingsEnd: nhlapi.MustParseDate("2024-04-18")},
	}

	mem.SetFile(resource.SeasonsManifest{}.Path(), oldSeasonsJSON)

	mockRedis.ExpectGet(redisSeasonsManifestKey).SetErr(redis.Nil)
	nhlClient.On("SeasonStandingManifest", ctx).Return(newSeasons, nil)
	mockRedis.CustomMatch(anySeasonsArgs).ExpectSet(redisSeasonsManifestKey, "x", cache.GobCacheTTL).SetVal("OK")

	result, err := (&SeasonsActivities{Storage: failingStat, GobCache: cache.NewGobCache(redisClient), NHLClient: nhlClient}).FetchSeasonsManifest(ctx, nil)

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
	redisClient, mockRedis := redismock.NewClientMock()
	client := &MockNHLClient{}

	season := nhlapi.NewSeason(2022)
	standingsRes := resource.SeasonStandings{Season: season}
	redisKey := core.RedisKey(standingsRes)

	standings := []nhlapi.Standing{
		{TeamAbbrev: nhlapi.LocalizedString{Default: "MTL"}, TeamName: nhlapi.LocalizedString{Default: "Montreal Canadiens"}},
		{TeamAbbrev: nhlapi.LocalizedString{Default: "TOR"}, TeamName: nhlapi.LocalizedString{Default: "Toronto Maple Leafs"}},
	}
	require.NoError(t, resource.WriteParsed(context.Background(), mem, standingsRes, standings))

	mockRedis.ExpectGet(redisKey).SetErr(redis.Nil)
	mockRedis.CustomMatch(anySeasonsArgs).ExpectSet(redisKey, "x", cache.GobCacheTTL).SetVal("OK")

	a := &SeasonsActivities{Storage: mem, GobCache: cache.NewGobCache(redisClient), NHLClient: client}
	result, err := a.downloadSeasonStandings(ctx, season)

	require.NoError(t, err)
	assert.Equal(t, season, result.Season)
	assert.Equal(t, 2, result.TeamCount)
	assert.True(t, result.FromCache)
	client.AssertNotCalled(t, "LeagueStandingsForSeason")
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestDownloadSeasonStandings_CacheMiss(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	redisClient, mockRedis := redismock.NewClientMock()
	client := &MockNHLClient{}

	season := nhlapi.NewSeason(2022)
	standingsRes := resource.SeasonStandings{Season: season}
	redisKey := core.RedisKey(standingsRes)

	standings := []nhlapi.Standing{
		{TeamAbbrev: nhlapi.LocalizedString{Default: "MTL"}, TeamName: nhlapi.LocalizedString{Default: "Montreal Canadiens"}},
		{TeamAbbrev: nhlapi.LocalizedString{Default: "TOR"}, TeamName: nhlapi.LocalizedString{Default: "Toronto Maple Leafs"}},
		{TeamAbbrev: nhlapi.LocalizedString{Default: "BOS"}, TeamName: nhlapi.LocalizedString{Default: "Boston Bruins"}},
	}
	client.On("LeagueStandingsForSeason", ctx, season).Return(standings, nil)

	mockRedis.ExpectGet(redisKey).SetErr(redis.Nil)
	mockRedis.CustomMatch(anySeasonsArgs).ExpectSet(redisKey, "x", cache.GobCacheTTL).SetVal("OK")

	a := &SeasonsActivities{Storage: mem, GobCache: cache.NewGobCache(redisClient), NHLClient: client}
	result, err := a.downloadSeasonStandings(ctx, season)

	require.NoError(t, err)
	assert.Equal(t, season, result.Season)
	assert.Equal(t, 3, result.TeamCount)
	assert.False(t, result.FromCache)
	client.AssertExpectations(t)
	assert.NoError(t, mockRedis.ExpectationsWereMet())

	assert.True(t, mem.Exists(context.Background(), standingsRes.Path()))
}

func TestDownloadSeasonStandings_APIError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	redisClient, mockRedis := redismock.NewClientMock()
	client := &MockNHLClient{}

	season := nhlapi.NewSeason(2022)
	redisKey := core.RedisKey(resource.SeasonStandings{Season: season})

	client.On("LeagueStandingsForSeason", ctx, season).Return(nil, errors.New("API unavailable"))

	mockRedis.ExpectGet(redisKey).SetErr(redis.Nil)

	a := &SeasonsActivities{Storage: mem, GobCache: cache.NewGobCache(redisClient), NHLClient: client}
	result, err := a.downloadSeasonStandings(ctx, season)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "API unavailable")
	assert.Equal(t, season, result.Season)
	assert.Equal(t, 0, result.TeamCount)
	client.AssertExpectations(t)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestDownloadSeasonStandings_CacheCorrupted(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	redisClient, mockRedis := redismock.NewClientMock()
	client := &MockNHLClient{}

	season := nhlapi.NewSeason(2022)
	standingsRes := resource.SeasonStandings{Season: season}
	redisKey := core.RedisKey(standingsRes)

	mem.SetFile(standingsRes.Path(), []byte("invalid json"))

	standings := []nhlapi.Standing{
		{TeamAbbrev: nhlapi.LocalizedString{Default: "MTL"}, TeamName: nhlapi.LocalizedString{Default: "Montreal Canadiens"}},
	}
	client.On("LeagueStandingsForSeason", ctx, season).Return(standings, nil)

	mockRedis.ExpectGet(redisKey).SetErr(redis.Nil)
	mockRedis.CustomMatch(anySeasonsArgs).ExpectSet(redisKey, "x", cache.GobCacheTTL).SetVal("OK")

	a := &SeasonsActivities{Storage: mem, GobCache: cache.NewGobCache(redisClient), NHLClient: client}
	result, err := a.downloadSeasonStandings(ctx, season)

	require.NoError(t, err)
	assert.Equal(t, season, result.Season)
	assert.Equal(t, 1, result.TeamCount)
	assert.False(t, result.FromCache)
	client.AssertExpectations(t)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestDownloadSeasonStandings_SaveStandingsError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	redisClient, mockRedis := redismock.NewClientMock()
	failingStorage := &failingWriteStorage{Storage: mem}
	client := &MockNHLClient{}

	season := nhlapi.NewSeason(2022)
	redisKey := core.RedisKey(resource.SeasonStandings{Season: season})

	standings := []nhlapi.Standing{
		{TeamAbbrev: nhlapi.LocalizedString{Default: "MTL"}, TeamName: nhlapi.LocalizedString{Default: "Montreal Canadiens"}},
	}
	client.On("LeagueStandingsForSeason", ctx, season).Return(standings, nil)

	mockRedis.ExpectGet(redisKey).SetErr(redis.Nil)

	a := &SeasonsActivities{Storage: failingStorage, GobCache: cache.NewGobCache(redisClient), NHLClient: client}
	_, err := a.downloadSeasonStandings(ctx, season)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "simulated write failure")
	client.AssertExpectations(t)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

// --- UpsertSeasons tests ---

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

	seasons := []nhlapi.SeasonInfo{
		{ID: nhlapi.NewSeason(2022), StandingsStart: nhlapi.MustParseDate("2022-10-07"), StandingsEnd: nhlapi.MustParseDate("2023-04-14")},
		{ID: nhlapi.NewSeason(2023), StandingsStart: nhlapi.MustParseDate("2023-10-10"), StandingsEnd: nhlapi.MustParseDate("2024-04-18")},
	}
	require.NoError(s.T(), resource.WriteParsed(context.Background(), mem, resource.SeasonsManifest{}, nhlapi.SeasonsResponse{Seasons: seasons}))

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

	require.NoError(s.T(), resource.WriteParsed(context.Background(), mem, resource.SeasonsManifest{}, nhlapi.SeasonsResponse{Seasons: []nhlapi.SeasonInfo{}}))

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

	seasons := []nhlapi.SeasonInfo{
		{ID: nhlapi.NewSeason(2022), StandingsStart: nhlapi.MustParseDate("2022-10-07"), StandingsEnd: nhlapi.MustParseDate("2023-04-14")},
	}
	require.NoError(s.T(), resource.WriteParsed(context.Background(), mem, resource.SeasonsManifest{}, nhlapi.SeasonsResponse{Seasons: seasons}))

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

// --- InitializeSeasonTeamsActivity tests ---

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
	redisClient, mockRedis := redismock.NewClientMock()
	nhlClient := &MockNHLClient{}
	upserter := &MockSeasonTeamsUpserter{}

	season := nhlapi.NewSeason(2022)
	confName := "Eastern"
	confAbbrev := "E"
	redisKey := core.RedisKey(resource.SeasonStandings{Season: season})

	standings := []nhlapi.Standing{
		{
			TeamAbbrev:       nhlapi.LocalizedString{Default: "MTL"},
			TeamName:         nhlapi.LocalizedString{Default: "Montreal Canadiens"},
			DivisionName:     "Atlantic",
			DivisionAbbrev:   "A",
			ConferenceName:   &confName,
			ConferenceAbbrev: &confAbbrev,
		},
		{
			TeamAbbrev:       nhlapi.LocalizedString{Default: "TOR"},
			TeamName:         nhlapi.LocalizedString{Default: "Toronto Maple Leafs"},
			DivisionName:     "Atlantic",
			DivisionAbbrev:   "A",
			ConferenceName:   &confName,
			ConferenceAbbrev: &confAbbrev,
		},
	}

	nhlClient.On("LeagueStandingsForSeason", mock.Anything, season).Return(standings, nil)
	upserter.On("UpsertSeasonTeam", mock.Anything, mock.AnythingOfType("sqlcdb.UpsertSeasonTeamParams")).Return(nil).Times(2)

	mockRedis.ExpectGet(redisKey).SetErr(redis.Nil)
	mockRedis.CustomMatch(anySeasonsArgs).ExpectSet(redisKey, "x", cache.GobCacheTTL).SetVal("OK")

	var gobBuf bytes.Buffer
	require.NoError(s.T(), gob.NewEncoder(&gobBuf).Encode(standings))
	mockRedis.ExpectGet(redisKey).SetVal(gobBuf.String())

	activities := &SeasonsActivities{
		Storage:             mem,
		GobCache:            cache.NewGobCache(redisClient),
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
	assert.NoError(s.T(), mockRedis.ExpectationsWereMet())
}

// anyArgs is a redismock matcher that accepts any arguments (used in franchise/daily schedule tests).
func anyArgs(expected, actual []any) error { return nil }

// ensure sqlcdb import is used
var _ sqlcdb.UpsertSeasonParams = sqlcdb.UpsertSeasonParams{}
