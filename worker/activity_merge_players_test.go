package worker

import (
	"bytes"
	"context"
	"encoding/gob"
	"testing"

	goredis "github.com/go-redis/redis/v8"
	"github.com/sperano/puckdb/redis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMergePlayerBatchesFromRedisImpl_EmptyKeys(t *testing.T) {
	ctx := context.Background()
	mockRedis := &redis.MockClient{}

	result, err := mergePlayerBatchesFromRedisImpl(ctx, mockRedis, []string{}, "yahoo")

	assert.NoError(t, err)
	assert.Len(t, result, 0)
}

func TestMergePlayerBatchesFromRedisImpl_YahooSource(t *testing.T) {
	ctx := context.Background()
	mockRedis := &redis.MockClient{}

	// Create test player data
	players := map[int64]PartialPlayer{
		8476453: {
			ID:           8476453,
			FirstName:    "Connor",
			LastName:     "McDavid",
			HasYahooData: true,
		},
	}
	var buf bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buf).Encode(players))

	// Mock Redis Get
	redisCmd := goredis.NewStringCmd(ctx)
	redisCmd.SetVal(buf.String())
	mockRedis.On("Get", ctx, "test-key").Return(redisCmd)

	// Mock Redis Del
	delCmd := goredis.NewIntCmd(ctx)
	delCmd.SetVal(1)
	mockRedis.On("Del", ctx, "test-key").Return(delCmd)

	result, err := mergePlayerBatchesFromRedisImpl(ctx, mockRedis, []string{"test-key"}, "yahoo")

	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "Connor", result[8476453].FirstName)
}

func TestMergePlayerBatchesFromRedisImpl_BoxscoreSource(t *testing.T) {
	ctx := context.Background()
	mockRedis := &redis.MockClient{}

	// Create test player data
	players := map[int64]PartialPlayer{
		8476453: {
			ID:              8476453,
			FirstName:       "Connor",
			HasBoxscoreData: true,
		},
	}
	var buf bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buf).Encode(players))

	// Mock Redis Get
	redisCmd := goredis.NewStringCmd(ctx)
	redisCmd.SetVal(buf.String())
	mockRedis.On("Get", ctx, "test-key").Return(redisCmd)

	// Mock Redis Del
	delCmd := goredis.NewIntCmd(ctx)
	delCmd.SetVal(1)
	mockRedis.On("Del", ctx, "test-key").Return(delCmd)

	result, err := mergePlayerBatchesFromRedisImpl(ctx, mockRedis, []string{"test-key"}, "boxscore")

	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.True(t, result[8476453].HasBoxscoreData)
}
