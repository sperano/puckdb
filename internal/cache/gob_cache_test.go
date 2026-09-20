package cache

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/go-redis/redismock/v8"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testData is a simple struct for testing gob encoding
type testData struct {
	Name  string
	Value int
}

func TestGobCache_GetSet(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	cache := NewGobCache(client)
	ctx := context.Background()
	key := "test:key"

	data := &testData{Name: "test", Value: 42}

	// Use CustomMatch for flexible byte slice matching
	anyArgs := func(expected, actual []any) error { return nil }
	mock.CustomMatch(anyArgs).ExpectSet(key, "x", 10*time.Minute).SetVal("OK")

	err := cache.Set(ctx, key, data)
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGobCache_GetHit(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	cache := NewGobCache(client)
	ctx := context.Background()
	key := "test:hit"

	// Pre-encode the data as gob
	data := &testData{Name: "test", Value: 42}
	gobData := encodeGob(t, data)
	mock.ExpectGet(key).SetVal(string(gobData))

	result, ok, err := cache.Get[*testData](ctx, key)
	require.NoError(t, err)
	assert.True(t, ok)
	require.NotNil(t, result)
	assert.Equal(t, "test", result.Name)
	assert.Equal(t, 42, result.Value)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGobCache_GetMiss(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	cache := NewGobCache(client)
	ctx := context.Background()
	key := "test:miss"

	mock.ExpectGet(key).RedisNil()

	result, ok, err := cache.Get[*testData](ctx, key)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Nil(t, result)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGobCache_NilCache(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var nilCache *GobCache

	// Get should return ErrNilCache
	result, ok, err := nilCache.Get[*testData](ctx, "key")
	assert.ErrorIs(t, err, ErrNilCache)
	assert.False(t, ok)
	assert.Nil(t, result)

	// Set should return ErrNilCache
	err = nilCache.Set[*testData](ctx, "key", nil)
	assert.ErrorIs(t, err, ErrNilCache)
}

func TestGobCache_Delete(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	cache := NewGobCache(client)
	ctx := context.Background()
	key := "test:delete"

	mock.ExpectDel(key).SetVal(1)

	err := cache.Delete(ctx, key)
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGobCache_DeleteNilCache(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	var cache *GobCache
	err := cache.Delete(ctx, "key")
	assert.ErrorIs(t, err, ErrNilCache)
}

func TestNewGobCacheWithTTL(t *testing.T) {
	t.Parallel()
	client, _ := redismock.NewClientMock()

	cache := NewGobCache(client, WithTTL(5*time.Minute))
	assert.Equal(t, 5*time.Minute, cache.ttl)
}

func TestDataOrigin_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		origin   core.DataOrigin
		expected string
	}{
		{core.OriginUnknown, "Unknown"},
		{core.OriginRemoteNHLAPI, "RemoteNHLAPI"},
		{core.OriginRemoteYahooAPI, "RemoteYahooAPI"},
		{core.OriginFileSystem, "FileSystem"},
		{core.OriginMemFileSystem, "MemFileSystem"},
		{core.OriginRedis, "Redis"},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.expected, tt.origin.String())
	}
}

func TestGobCache_GetCorruptData_DeletesAndReturnsMiss(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	cache := NewGobCache(client)
	ctx := context.Background()
	key := "test:corrupt"

	// Return invalid gob data from Redis
	mock.ExpectGet(key).SetVal("this is not valid gob data")
	// Expect the corrupt key to be deleted
	mock.ExpectDel(key).SetVal(1)

	result, ok, err := cache.Get[*testData](ctx, key)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Nil(t, result)

	require.NoError(t, mock.ExpectationsWereMet())
}

// Helper to encode gob for test fixtures
func encodeGob(t *testing.T, obj any) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	require.NoError(t, enc.Encode(obj))
	return buf.Bytes()
}

// testResource implements core.Parseable and core.Formattable for testing.
type testResource struct{}

func (r testResource) Path() string        { return "test/resource.json" }
func (r testResource) Type() core.FileType { return core.Unknown }
func (r testResource) Parse(data []byte) (*testData, error) {
	var d testData
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, err
	}
	return &d, nil
}
func (r testResource) Format(obj *testData) ([]byte, error) {
	return json.Marshal(obj)
}

// redisKey is the expected Redis key for testResource.
const testResourceRedisKey = "puckdb:resource:test/resource.json"

func TestReadParsedCached_CacheHit(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	cache := NewGobCache(client)
	ctx := context.Background()

	data := &testData{Name: "cached", Value: 99}
	gobData := encodeGob(t, data)
	mock.ExpectGet(testResourceRedisKey).SetVal(string(gobData))

	result, origin, err := cache.ReadParsedCached(ctx, store.NewMemStorage(), testResource{})
	require.NoError(t, err)
	assert.Equal(t, core.OriginRedis, origin)
	require.NotNil(t, result)
	assert.Equal(t, "cached", result.Name)
	assert.Equal(t, 99, result.Value)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReadParsedCached_CacheMiss(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	cache := NewGobCache(client)
	ctx := context.Background()

	// Seed JSON in storage so the cache-miss path can read it.
	mem := store.NewMemStorage()
	jsonBytes, err := json.Marshal(&testData{Name: "fromfs", Value: 7})
	require.NoError(t, err)
	mem.SetFile("test/resource.json", jsonBytes)

	mock.ExpectGet(testResourceRedisKey).RedisNil()

	anyArgs := func(expected, actual []any) error { return nil }
	mock.CustomMatch(anyArgs).ExpectSet(testResourceRedisKey, "x", GobCacheTTL).SetVal("OK")

	result, origin, err := cache.ReadParsedCached(ctx, mem, testResource{})
	require.NoError(t, err)
	assert.Equal(t, core.OriginFileSystem, origin)
	require.NotNil(t, result)
	assert.Equal(t, "fromfs", result.Name)
	assert.Equal(t, 7, result.Value)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReadParsedCached_NilCache(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	mem := store.NewMemStorage()
	jsonBytes, err := json.Marshal(&testData{Name: "nocache", Value: 3})
	require.NoError(t, err)
	mem.SetFile("test/resource.json", jsonBytes)

	// A nil cache must degrade gracefully: ErrNilCache from Get is ignored,
	// ErrNilCache from setWithTTL is also ignored.
	var nilCache *GobCache
	result, origin, err := nilCache.ReadParsedCached(ctx, mem, testResource{})
	require.NoError(t, err)
	assert.Equal(t, core.OriginFileSystem, origin)
	require.NotNil(t, result)
	assert.Equal(t, "nocache", result.Name)
	assert.Equal(t, 3, result.Value)
}

func TestWriteParsedCached_Success(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	cache := NewGobCache(client)
	ctx := context.Background()
	mem := store.NewMemStorage()

	anyArgs := func(expected, actual []any) error { return nil }
	mock.CustomMatch(anyArgs).ExpectSet(testResourceRedisKey, "x", GobCacheTTL).SetVal("OK")

	obj := &testData{Name: "written", Value: 42}
	err := cache.WriteParsedCached(ctx, mem, testResource{}, obj)
	require.NoError(t, err)

	// Verify storage was also written.
	stored := mem.Get("test/resource.json")
	require.NotNil(t, stored)
	var decoded testData
	require.NoError(t, json.Unmarshal(stored, &decoded))
	assert.Equal(t, "written", decoded.Name)
	assert.Equal(t, 42, decoded.Value)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSetWithTTL_RedisError(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	cache := NewGobCache(client)
	ctx := context.Background()
	key := "test:setwithttl:err"
	redisErr := errors.New("connection refused")

	anyArgs := func(expected, actual []any) error { return nil }
	mock.CustomMatch(anyArgs).ExpectSet(key, "x", GobCacheTTL).SetErr(redisErr)

	data := &testData{Name: "x", Value: 1}
	err := cache.setWithTTL(ctx, key, data, GobCacheTTL)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis set")
	assert.ErrorIs(t, err, redisErr)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGet_RedisError(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	cache := NewGobCache(client)
	ctx := context.Background()
	key := "test:get:err"
	redisErr := fmt.Errorf("CLUSTERDOWN")

	mock.ExpectGet(key).SetErr(redisErr)

	result, ok, err := cache.Get[*testData](ctx, key)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis get")
	assert.ErrorIs(t, err, redisErr)
	assert.False(t, ok)
	assert.Nil(t, result)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReadParsedCached_SkipRedisConfig(t *testing.T) {
	t.Parallel()
	client, _ := redismock.NewClientMock()
	cfg := &GobCacheConfig{
		Types: map[core.FileType]GobCacheTypeConfig{
			core.Unknown: {SkipRedis: true},
		},
	}
	cache := NewGobCache(client, WithConfig(cfg))
	ctx := context.Background()

	mem := store.NewMemStorage()
	jsonBytes, _ := json.Marshal(&testData{Name: "skipredis", Value: 5})
	mem.SetFile("test/resource.json", jsonBytes)

	result, origin, err := cache.ReadParsedCached(ctx, mem, testResource{})
	require.NoError(t, err)
	assert.Equal(t, core.OriginFileSystem, origin)
	require.NotNil(t, result)
	assert.Equal(t, "skipredis", result.Name)
}

func TestReadParsedCached_SkipRedisConfig_StorageError(t *testing.T) {
	t.Parallel()
	client, _ := redismock.NewClientMock()
	cfg := &GobCacheConfig{
		Types: map[core.FileType]GobCacheTypeConfig{
			core.Unknown: {SkipRedis: true},
		},
	}
	cache := NewGobCache(client, WithConfig(cfg))
	ctx := context.Background()

	_, origin, err := cache.ReadParsedCached(ctx, store.NewMemStorage(), testResource{})
	assert.Error(t, err)
	assert.Equal(t, core.OriginFileSystem, origin)
}

func TestReadParsedCached_StorageErrorOnMiss(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	cache := NewGobCache(client)
	ctx := context.Background()

	mock.ExpectGet(testResourceRedisKey).RedisNil()

	_, origin, err := cache.ReadParsedCached(ctx, store.NewMemStorage(), testResource{})
	assert.Error(t, err)
	assert.Equal(t, core.OriginFileSystem, origin)
}

func TestReadParsedCached_RedisGetError(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	cache := NewGobCache(client)
	ctx := context.Background()

	mock.ExpectGet(testResourceRedisKey).SetErr(errors.New("connection reset"))

	_, origin, err := cache.ReadParsedCached(ctx, store.NewMemStorage(), testResource{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "gob cache read")
	assert.Equal(t, core.OriginUnknown, origin)
}

func TestReadParsedCached_CacheWriteError(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	cache := NewGobCache(client)
	ctx := context.Background()

	mem := store.NewMemStorage()
	jsonBytes, _ := json.Marshal(&testData{Name: "ok", Value: 1})
	mem.SetFile("test/resource.json", jsonBytes)

	mock.ExpectGet(testResourceRedisKey).RedisNil()
	anyArgs := func(expected, actual []any) error { return nil }
	mock.CustomMatch(anyArgs).ExpectSet(testResourceRedisKey, "x", GobCacheTTL).SetErr(errors.New("readonly"))

	// A best-effort cache write failure must NOT fail a successful storage read:
	// the read result is returned with OriginFileSystem and no error.
	result, origin, err := cache.ReadParsedCached(ctx, mem, testResource{})
	require.NoError(t, err)
	assert.Equal(t, core.OriginFileSystem, origin)
	require.NotNil(t, result)
	assert.Equal(t, "ok", result.Name)
	assert.Equal(t, 1, result.Value)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWriteParsedCached_SkipRedisConfig(t *testing.T) {
	t.Parallel()
	client, _ := redismock.NewClientMock()
	cfg := &GobCacheConfig{
		Types: map[core.FileType]GobCacheTypeConfig{
			core.Unknown: {SkipRedis: true},
		},
	}
	cache := NewGobCache(client, WithConfig(cfg))
	ctx := context.Background()
	mem := store.NewMemStorage()

	obj := &testData{Name: "skipwrite", Value: 9}
	err := cache.WriteParsedCached(ctx, mem, testResource{}, obj)
	require.NoError(t, err)

	stored := mem.Get("test/resource.json")
	require.NotNil(t, stored)
}

func TestSetParsed(t *testing.T) {
	t.Parallel()
	anyArgs := func(expected, actual []any) error { return nil }
	obj := &testData{Name: "setparsed", Value: 3}

	t.Run("uses per-type ttl", func(t *testing.T) {
		t.Parallel()
		client, mock := redismock.NewClientMock()
		const typeTTL = 5 * time.Minute
		cfg := &GobCacheConfig{Types: map[core.FileType]GobCacheTypeConfig{core.Unknown: {TTL: typeTTL}}}
		cache := NewGobCache(client, WithConfig(cfg))
		mock.CustomMatch(anyArgs).ExpectSet(testResourceRedisKey, "x", typeTTL).SetVal("OK")

		require.NoError(t, cache.SetParsed(context.Background(), testResource{}, obj))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("skip_redis is a no-op", func(t *testing.T) {
		t.Parallel()
		client, mock := redismock.NewClientMock()
		cfg := &GobCacheConfig{Types: map[core.FileType]GobCacheTypeConfig{core.Unknown: {SkipRedis: true}}}
		cache := NewGobCache(client, WithConfig(cfg))

		require.NoError(t, cache.SetParsed(context.Background(), testResource{}, obj))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("nil cache returns ErrNilCache", func(t *testing.T) {
		t.Parallel()
		var nilCache *GobCache
		err := nilCache.SetParsed(context.Background(), testResource{}, obj)
		assert.ErrorIs(t, err, ErrNilCache)
	})
}

func TestWriteParsedCached_RedisSetError(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	cache := NewGobCache(client)
	ctx := context.Background()
	mem := store.NewMemStorage()

	anyArgs := func(expected, actual []any) error { return nil }
	mock.CustomMatch(anyArgs).ExpectSet(testResourceRedisKey, "x", GobCacheTTL).SetErr(errors.New("disk full"))

	obj := &testData{Name: "fail", Value: 0}
	err := cache.WriteParsedCached(ctx, mem, testResource{}, obj)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "gob cache write")

	stored := mem.Get("test/resource.json")
	require.NotNil(t, stored)
}
