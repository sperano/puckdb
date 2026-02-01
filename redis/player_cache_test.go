package redis

import (
	"context"
	"errors"
	"testing"

	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestPlayerBatchKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		source     string
		season     int
		batchIndex int
		expected   string
	}{
		{"nhl source", "nhl", 2023, 0, "yfh:players:nhl:2023:0"},
		{"yahoo source", "yahoo", 2022, 5, "yfh:players:yahoo:2022:5"},
		{"large batch index", "nhl", 2024, 100, "yfh:players:nhl:2024:100"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := PlayerBatchKey(tt.source, tt.season, tt.batchIndex)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSeasonResultKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		season   int
		expected string
	}{
		{2023, "yfh:players:season:2023"},
		{2020, "yfh:players:season:2020"},
	}

	for _, tt := range tests {
		t.Run(string(rune(tt.season)), func(t *testing.T) {
			result := SeasonResultKey(tt.season)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestEnrichmentPlayersKey(t *testing.T) {
	t.Parallel()

	result := EnrichmentPlayersKey()
	assert.Equal(t, "yfh:players:enrichment", result)
}

func TestSavePlayerBatch(t *testing.T) {
	t.Parallel()

	t.Run("saves data successfully", func(t *testing.T) {
		mockClient := new(MockClient)
		ctx := context.Background()

		data := []byte("test player data")
		key := "yfh:players:nhl:2023:0"

		statusCmd := redis.NewStatusCmd(ctx)
		statusCmd.SetVal("OK")
		mockClient.On("Set", ctx, key, data, PlayerBatchTTL).Return(statusCmd)

		err := SavePlayerBatch(ctx, mockClient, key, data)
		require.NoError(t, err)
		mockClient.AssertExpectations(t)
	})

	t.Run("returns error on failure", func(t *testing.T) {
		mockClient := new(MockClient)
		ctx := context.Background()

		data := []byte("test player data")
		key := "yfh:players:nhl:2023:0"

		statusCmd := redis.NewStatusCmd(ctx)
		statusCmd.SetErr(errors.New("connection refused"))
		mockClient.On("Set", ctx, key, data, PlayerBatchTTL).Return(statusCmd)

		err := SavePlayerBatch(ctx, mockClient, key, data)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "save player batch")
		mockClient.AssertExpectations(t)
	})
}

func TestLoadPlayerBatch(t *testing.T) {
	t.Parallel()

	t.Run("loads data successfully", func(t *testing.T) {
		mockClient := new(MockClient)
		ctx := context.Background()

		key := "yfh:players:nhl:2023:0"
		expectedData := []byte("test player data")

		stringCmd := redis.NewStringCmd(ctx)
		stringCmd.SetVal(string(expectedData))
		mockClient.On("Get", ctx, key).Return(stringCmd)

		data, err := LoadPlayerBatch(ctx, mockClient, key)
		require.NoError(t, err)
		assert.Equal(t, expectedData, data)
		mockClient.AssertExpectations(t)
	})

	t.Run("returns error when key not found", func(t *testing.T) {
		mockClient := new(MockClient)
		ctx := context.Background()

		key := "yfh:players:nhl:2023:0"

		stringCmd := redis.NewStringCmd(ctx)
		stringCmd.SetErr(redis.Nil)
		mockClient.On("Get", ctx, key).Return(stringCmd)

		_, err := LoadPlayerBatch(ctx, mockClient, key)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "load player batch")
		mockClient.AssertExpectations(t)
	})

	t.Run("returns error on connection failure", func(t *testing.T) {
		mockClient := new(MockClient)
		ctx := context.Background()

		key := "yfh:players:nhl:2023:0"

		stringCmd := redis.NewStringCmd(ctx)
		stringCmd.SetErr(errors.New("connection refused"))
		mockClient.On("Get", ctx, key).Return(stringCmd)

		_, err := LoadPlayerBatch(ctx, mockClient, key)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "load player batch")
		mockClient.AssertExpectations(t)
	})
}

func TestDeletePlayerBatches(t *testing.T) {
	t.Parallel()

	t.Run("deletes multiple keys", func(t *testing.T) {
		mockClient := new(MockClient)
		ctx := context.Background()

		keys := []string{"key1", "key2", "key3"}

		intCmd := redis.NewIntCmd(ctx)
		intCmd.SetVal(3)
		mockClient.On("Del", ctx, "key1", "key2", "key3").Return(intCmd)

		err := DeletePlayerBatches(ctx, mockClient, keys)
		require.NoError(t, err)
		mockClient.AssertExpectations(t)
	})

	t.Run("handles empty key list", func(t *testing.T) {
		mockClient := new(MockClient)
		ctx := context.Background()

		err := DeletePlayerBatches(ctx, mockClient, []string{})
		require.NoError(t, err)
		// Del should not be called
		mockClient.AssertNotCalled(t, "Del", mock.Anything, mock.Anything)
	})

	t.Run("returns error on failure", func(t *testing.T) {
		mockClient := new(MockClient)
		ctx := context.Background()

		keys := []string{"key1"}

		intCmd := redis.NewIntCmd(ctx)
		intCmd.SetErr(errors.New("connection refused"))
		mockClient.On("Del", ctx, "key1").Return(intCmd)

		err := DeletePlayerBatches(ctx, mockClient, keys)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "delete player batches")
		mockClient.AssertExpectations(t)
	})
}
