package redis

import (
	"context"
	"errors"
	"testing"

	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFlushDB(t *testing.T) {
	t.Parallel()

	t.Run("flushes successfully", func(t *testing.T) {
		mockClient := new(MockClient)
		ctx := context.Background()

		statusCmd := redis.NewStatusCmd(ctx)
		statusCmd.SetVal("OK")
		mockClient.On("FlushDB", ctx).Return(statusCmd)

		err := FlushDB(ctx, mockClient)
		require.NoError(t, err)
		mockClient.AssertExpectations(t)
	})

	t.Run("returns error on failure", func(t *testing.T) {
		mockClient := new(MockClient)
		ctx := context.Background()

		statusCmd := redis.NewStatusCmd(ctx)
		statusCmd.SetErr(errors.New("permission denied"))
		mockClient.On("FlushDB", ctx).Return(statusCmd)

		err := FlushDB(ctx, mockClient)
		require.Error(t, err)
		mockClient.AssertExpectations(t)
	})
}

func TestClearCacheWithFilter(t *testing.T) {
	t.Parallel()

	t.Run("clears matching keys", func(t *testing.T) {
		mockClient := new(MockClient)
		ctx := context.Background()

		filter := "player:*"
		matchingKeys := []string{"player:1", "player:2", "player:3"}

		stringSliceCmd := redis.NewStringSliceCmd(ctx)
		stringSliceCmd.SetVal(matchingKeys)
		mockClient.On("Keys", ctx, filter).Return(stringSliceCmd)

		intCmd := redis.NewIntCmd(ctx)
		intCmd.SetVal(3)
		mockClient.On("Del", ctx, "player:1", "player:2", "player:3").Return(intCmd)

		err := clearCacheWithFilter(ctx, mockClient, filter)
		require.NoError(t, err)
		mockClient.AssertExpectations(t)
	})

	t.Run("handles empty result", func(t *testing.T) {
		mockClient := new(MockClient)
		ctx := context.Background()

		filter := "nonexistent:*"

		stringSliceCmd := redis.NewStringSliceCmd(ctx)
		stringSliceCmd.SetVal([]string{})
		mockClient.On("Keys", ctx, filter).Return(stringSliceCmd)

		intCmd := redis.NewIntCmd(ctx)
		intCmd.SetVal(0)
		mockClient.On("Del", ctx).Return(intCmd)

		err := clearCacheWithFilter(ctx, mockClient, filter)
		require.NoError(t, err)
		mockClient.AssertExpectations(t)
	})

	t.Run("returns error on keys failure", func(t *testing.T) {
		mockClient := new(MockClient)
		ctx := context.Background()

		filter := "player:*"

		stringSliceCmd := redis.NewStringSliceCmd(ctx)
		stringSliceCmd.SetErr(errors.New("connection refused"))
		mockClient.On("Keys", ctx, filter).Return(stringSliceCmd)

		err := clearCacheWithFilter(ctx, mockClient, filter)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "error clearing cache")
		mockClient.AssertExpectations(t)
	})

	t.Run("returns error on del failure", func(t *testing.T) {
		mockClient := new(MockClient)
		ctx := context.Background()

		filter := "player:*"
		matchingKeys := []string{"player:1"}

		stringSliceCmd := redis.NewStringSliceCmd(ctx)
		stringSliceCmd.SetVal(matchingKeys)
		mockClient.On("Keys", ctx, filter).Return(stringSliceCmd)

		intCmd := redis.NewIntCmd(ctx)
		intCmd.SetErr(errors.New("connection refused"))
		mockClient.On("Del", ctx, "player:1").Return(intCmd)

		err := clearCacheWithFilter(ctx, mockClient, filter)
		require.Error(t, err)
		mockClient.AssertExpectations(t)
	})
}
