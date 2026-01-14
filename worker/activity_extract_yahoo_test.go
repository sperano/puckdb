package worker

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// Tests for extractYahooPlayersForDayBatchImpl

func TestExtractYahooPlayersForDayBatchImpl_EmptyDays(t *testing.T) {
	ctx := context.Background()
	mockRedis := &MockRedisClient{}

	input := BatchInput{
		Days:       []time.Time{},
		Season:     2023,
		BatchIndex: 0,
	}

	result, err := extractYahooPlayersForDayBatchImpl(ctx, mockRedis, input)

	assert.NoError(t, err)
	assert.Equal(t, "", result.RedisKey)
	assert.Equal(t, 0, result.PlayerCount)
}

func TestExtractYahooPlayersForDayBatchImpl_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	mockRedis := &MockRedisClient{}

	// Use a day in the past that won't have any cache files
	day := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	input := BatchInput{
		Days:       []time.Time{day},
		Season:     2020,
		BatchIndex: 0,
	}

	_, err := extractYahooPlayersForDayBatchImpl(ctx, mockRedis, input)

	assert.ErrorIs(t, err, context.Canceled)
}
