package cache

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/go-redis/redismock/v8"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setValueArg is the position of the value in a Redis SET command's args.
const setValueArg = 2

func TestStorageFirst_ReadsStorageAndRefreshesRedis(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	cache := NewGobCache(client)
	mem := store.NewMemStorage()
	jsonBytes, err := json.Marshal(&testData{Name: "rewritten", Value: 2})
	require.NoError(t, err)
	mem.SetFile("test/resource.json", jsonBytes)

	// No GET is expected: the stale entry Redis would return is never read,
	// and the SET must carry the object parsed from storage.
	mock.CustomMatch(func(_, actual []any) error {
		raw, ok := actual[setValueArg].([]byte)
		if !ok {
			return fmt.Errorf("SET value is %T, want []byte", actual[setValueArg])
		}
		var stored *testData
		if err := gob.NewDecoder(bytes.NewReader(raw)).Decode(&stored); err != nil {
			return err
		}
		if stored.Name != "rewritten" {
			return fmt.Errorf("SET stored %q, want the storage object", stored.Name)
		}
		return nil
	}).ExpectSet(testResourceRedisKey, "x", GobCacheTTL).SetVal("OK")

	result, origin, err := cache.StorageFirst().ReadParsedCached(context.Background(), mem, testResource{})
	require.NoError(t, err)
	assert.Equal(t, core.OriginFileSystem, origin)
	require.NotNil(t, result)
	assert.Equal(t, "rewritten", result.Name)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStorageFirst_LeavesOriginalCacheReadingRedis(t *testing.T) {
	t.Parallel()
	client, mock := redismock.NewClientMock()
	cache := NewGobCache(client)
	_ = cache.StorageFirst()

	mock.ExpectGet(testResourceRedisKey).SetVal(string(encodeGob(t, &testData{Name: "cached"})))

	result, origin, err := cache.ReadParsedCached(context.Background(), store.NewMemStorage(), testResource{})
	require.NoError(t, err)
	assert.Equal(t, core.OriginRedis, origin)
	assert.Equal(t, "cached", result.Name)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStorageFirst_NilCacheStaysNil(t *testing.T) {
	t.Parallel()
	var nilCache *GobCache
	assert.Nil(t, nilCache.StorageFirst())
}
