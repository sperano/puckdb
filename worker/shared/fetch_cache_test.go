package shared

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"errors"
	"testing"

	"github.com/go-redis/redismock/v8"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fetchCacheTestData is the value type for FetchOrCache tests.
type fetchCacheTestData struct {
	Name  string
	Value int
}

// fetchCacheResource implements core.ReadWritable[*fetchCacheTestData].
// It uses JSON for on-disk storage, matching the pattern in gob_cache_test.go.
type fetchCacheResource struct{}

func (fetchCacheResource) Path() string        { return "shared/fetch_cache_test.json" }
func (fetchCacheResource) Type() core.FileType { return core.Unknown }

func (fetchCacheResource) Parse(data []byte) (*fetchCacheTestData, error) {
	var d fetchCacheTestData
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

func (fetchCacheResource) Format(obj *fetchCacheTestData) ([]byte, error) {
	return json.Marshal(obj)
}

const fetchCacheRedisKey = core.RedisResourceKeyPrefix + "shared/fetch_cache_test.json"

// anyFetchCacheArgs is a custom redismock matcher that ignores argument values.
// Used when the exact gob bytes differ between test runs (timestamps, etc.).
var anyFetchCacheArgs = func(_, _ []any) error { return nil }

// gobEncode encodes v using encoding/gob, the same way cache.Set does.
func gobEncode(t *testing.T, v *fetchCacheTestData) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buf).Encode(v))
	return buf.Bytes()
}

func TestFetchOrCache_CacheHit(t *testing.T) {
	t.Parallel()

	client, mock := redismock.NewClientMock()
	gobCache := cache.NewGobCache(client)
	mem := store.NewMemStorage()
	ctx := context.Background()

	data := &fetchCacheTestData{Name: "cached", Value: 42}
	mock.ExpectGet(fetchCacheRedisKey).SetVal(string(gobEncode(t, data)))

	fetchCalled := false
	fetch := func(_ context.Context) (*fetchCacheTestData, error) {
		fetchCalled = true
		return nil, errors.New("should not be called on cache hit")
	}

	result, origin, err := FetchOrCache(ctx, mem, gobCache, fetchCacheResource{}, fetch)

	require.NoError(t, err)
	assert.Equal(t, core.OriginRedis, origin)
	require.NotNil(t, result)
	assert.Equal(t, "cached", result.Name)
	assert.Equal(t, 42, result.Value)
	assert.False(t, fetchCalled, "fetch must not be called on cache hit")

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFetchOrCache_CacheMiss_FetchSucceeds(t *testing.T) {
	t.Parallel()

	client, mock := redismock.NewClientMock()
	gobCache := cache.NewGobCache(client)
	mem := store.NewMemStorage()
	ctx := context.Background()

	// Redis returns miss on read; allow any bytes on the write-back.
	mock.ExpectGet(fetchCacheRedisKey).RedisNil()
	mock.CustomMatch(anyFetchCacheArgs).ExpectSet(fetchCacheRedisKey, "x", cache.GobCacheTTL).SetVal("OK")

	fetched := &fetchCacheTestData{Name: "fetched", Value: 7}
	fetchCalled := false
	fetch := func(_ context.Context) (*fetchCacheTestData, error) {
		fetchCalled = true
		return fetched, nil
	}

	result, origin, err := FetchOrCache(ctx, mem, gobCache, fetchCacheResource{}, fetch)

	require.NoError(t, err)
	assert.Equal(t, core.OriginRemoteNHLAPI, origin)
	require.NotNil(t, result)
	assert.Equal(t, "fetched", result.Name)
	assert.Equal(t, 7, result.Value)
	assert.True(t, fetchCalled, "fetch must be called on cache miss")

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFetchOrCache_FetchError(t *testing.T) {
	t.Parallel()

	client, mock := redismock.NewClientMock()
	gobCache := cache.NewGobCache(client)
	mem := store.NewMemStorage()
	ctx := context.Background()

	mock.ExpectGet(fetchCacheRedisKey).RedisNil()

	fetchErr := errors.New("NHL API unavailable")
	fetch := func(_ context.Context) (*fetchCacheTestData, error) {
		return nil, fetchErr
	}

	result, origin, err := FetchOrCache(ctx, mem, gobCache, fetchCacheResource{}, fetch)

	require.Error(t, err)
	assert.ErrorIs(t, err, fetchErr)
	assert.Nil(t, result)
	assert.Equal(t, core.OriginUnknown, origin)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFetchOrCache_NilCache_HitFromStorage(t *testing.T) {
	t.Parallel()

	// When GobCache is nil, ReadParsedCached degrades to filesystem only.
	// Seed the storage so the read succeeds without touching Redis.
	mem := store.NewMemStorage()
	ctx := context.Background()

	seeded := &fetchCacheTestData{Name: "from-storage", Value: 55}
	seededJSON, err := json.Marshal(seeded)
	require.NoError(t, err)
	mem.SetFile(fetchCacheResource{}.Path(), seededJSON)

	fetchCalled := false
	fetch := func(_ context.Context) (*fetchCacheTestData, error) {
		fetchCalled = true
		return nil, errors.New("should not be called when storage has data")
	}

	result, origin, err := FetchOrCache(ctx, mem, nil, fetchCacheResource{}, fetch)

	require.NoError(t, err)
	// With nil cache, ReadParsedCached reads from storage and returns OriginFileSystem,
	// which causes a cache hit in FetchOrCache (no fetch call, origin is OriginFileSystem).
	assert.Equal(t, core.OriginFileSystem, origin)
	require.NotNil(t, result)
	assert.Equal(t, "from-storage", result.Name)
	assert.Equal(t, 55, result.Value)
	assert.False(t, fetchCalled, "fetch must not be called when storage has data")
}
