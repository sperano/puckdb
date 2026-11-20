package player

import (
	"context"
	"errors"
	"testing"

	"github.com/go-redis/redismock/v8"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ////////////////////////////////////////////////////////////////////////////
// saveYahooPlayersToRedisImpl tests
// ////////////////////////////////////////////////////////////////////////////

func TestSaveYahooPlayersToRedisImpl_EmptyPlayers(t *testing.T) {
	ctx := context.Background()
	client, mockRedis := redismock.NewClientMock()
	mockRedis.ExpectHLen(YahooIDPoolKey).SetVal(0)

	result, err := saveYahooPlayersToRedisImpl(ctx, client, []store.YahooPlayer{})

	require.NoError(t, err)
	assert.Equal(t, 0, result.TotalPlayers)
	assert.Equal(t, 0, result.AvailablePlayers)
	assert.Equal(t, 0, result.SkippedNonNHL)
	assert.Equal(t, 0, result.PoolSize)
	require.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestSaveYahooPlayersToRedisImpl_Success(t *testing.T) {
	ctx := context.Background()
	client, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)

	players := []store.YahooPlayer{
		{YahooID: 1, FirstName: "Connor", LastName: "McDavid"},
		{YahooID: 2, FirstName: "Leon", LastName: "Draisaitl"},
	}

	// Mock SMembers for loading verified non-NHL IDs (empty set)
	mockRedis.ExpectSMembers(VerifiedNonNHLKey).SetVal([]string{})

	// Use CustomMatch for flexible matching on gob-encoded data
	// Need to provide placeholder arguments that will be overridden by CustomMatch
	localAnyArgs := func(expected, actual []any) error { return nil }
	mockRedis.CustomMatch(localAnyArgs).ExpectHSet(YahooIDPoolKey, "x", "x").SetVal(1)
	mockRedis.CustomMatch(localAnyArgs).ExpectHSet(YahooIDPoolKey, "x", "x").SetVal(1)
	mockRedis.CustomMatch(localAnyArgs).ExpectSAdd(YahooIDAvailableKey, "x", "x").SetVal(2)
	mockRedis.ExpectExpire(YahooIDPoolKey, ImportPlayersTTL).SetVal(true)
	mockRedis.ExpectExpire(YahooIDAvailableKey, ImportPlayersTTL).SetVal(true)
	mockRedis.ExpectHLen(YahooIDPoolKey).SetVal(2)

	result, err := saveYahooPlayersToRedisImpl(ctx, client, players)

	require.NoError(t, err)
	assert.Equal(t, 2, result.TotalPlayers)
	assert.Equal(t, 2, result.AvailablePlayers)
	assert.Equal(t, 0, result.SkippedNonNHL)
	assert.Equal(t, 2, result.PoolSize, "measured in the same pipeline as the write")
	require.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestSaveYahooPlayersToRedisImpl_WithVerifiedNonNHL(t *testing.T) {
	ctx := context.Background()
	client, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)

	players := []store.YahooPlayer{
		{YahooID: 1, FirstName: "Connor", LastName: "McDavid"},
		{YahooID: 2, FirstName: "Non", LastName: "NHLPlayer"}, // This one is verified non-NHL
		{YahooID: 3, FirstName: "Leon", LastName: "Draisaitl"},
	}

	// Mock SMembers - player 2 is verified non-NHL
	mockRedis.ExpectSMembers(VerifiedNonNHLKey).SetVal([]string{"2"})

	// Expect HSet calls for ALL players (they're all stored in hash)
	localAnyArgs := func(expected, actual []any) error { return nil }
	mockRedis.CustomMatch(localAnyArgs).ExpectHSet(YahooIDPoolKey, "x", "x").SetVal(1)
	mockRedis.CustomMatch(localAnyArgs).ExpectHSet(YahooIDPoolKey, "x", "x").SetVal(1)
	mockRedis.CustomMatch(localAnyArgs).ExpectHSet(YahooIDPoolKey, "x", "x").SetVal(1)

	// Expect SAdd only for available IDs (players 1 and 3, not 2)
	mockRedis.CustomMatch(localAnyArgs).ExpectSAdd(YahooIDAvailableKey, "x", "x").SetVal(2)

	// Expect Expire calls and the pool size measurement
	mockRedis.ExpectExpire(YahooIDPoolKey, ImportPlayersTTL).SetVal(true)
	mockRedis.ExpectExpire(YahooIDAvailableKey, ImportPlayersTTL).SetVal(true)
	mockRedis.ExpectHLen(YahooIDPoolKey).SetVal(3)

	result, err := saveYahooPlayersToRedisImpl(ctx, client, players)

	require.NoError(t, err)
	assert.Equal(t, 3, result.TotalPlayers)
	assert.Equal(t, 2, result.AvailablePlayers)
	assert.Equal(t, 1, result.SkippedNonNHL)
	require.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestSaveYahooPlayersToRedisImpl_PipelineError(t *testing.T) {
	ctx := context.Background()
	client, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)

	players := []store.YahooPlayer{
		{YahooID: 1, FirstName: "Connor", LastName: "McDavid"},
	}

	// Mock SMembers
	mockRedis.ExpectSMembers(VerifiedNonNHLKey).SetVal([]string{})

	// Setup pipeline expectations - one command will fail
	localAnyArgs := func(expected, actual []any) error { return nil }
	mockRedis.CustomMatch(localAnyArgs).ExpectHSet(YahooIDPoolKey, "x", "x").SetVal(1)
	mockRedis.CustomMatch(localAnyArgs).ExpectSAdd(YahooIDAvailableKey, "x").SetVal(1)
	mockRedis.ExpectExpire(YahooIDPoolKey, ImportPlayersTTL).SetVal(true)
	mockRedis.ExpectExpire(YahooIDAvailableKey, ImportPlayersTTL).SetErr(errors.New("redis connection failed"))
	mockRedis.ExpectHLen(YahooIDPoolKey).SetVal(1)

	result, err := saveYahooPlayersToRedisImpl(ctx, client, players)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis connection failed")
	assert.Nil(t, result)
}

func TestSaveYahooPlayersToRedisImpl_LoadVerifiedError(t *testing.T) {
	ctx := context.Background()
	client, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)

	players := []store.YahooPlayer{
		{YahooID: 1, FirstName: "Connor", LastName: "McDavid"},
	}

	// SMembers fails - should log warning but continue with all players available
	mockRedis.ExpectSMembers(VerifiedNonNHLKey).SetErr(errors.New("redis timeout"))

	// Pipeline should still work - player is not excluded since we couldn't load verified set
	localAnyArgs := func(expected, actual []any) error { return nil }
	mockRedis.CustomMatch(localAnyArgs).ExpectHSet(YahooIDPoolKey, "x", "x").SetVal(1)
	mockRedis.CustomMatch(localAnyArgs).ExpectSAdd(YahooIDAvailableKey, "x").SetVal(1)
	mockRedis.ExpectExpire(YahooIDPoolKey, ImportPlayersTTL).SetVal(true)
	mockRedis.ExpectExpire(YahooIDAvailableKey, ImportPlayersTTL).SetVal(true)
	mockRedis.ExpectHLen(YahooIDPoolKey).SetVal(1)

	result, err := saveYahooPlayersToRedisImpl(ctx, client, players)

	require.NoError(t, err)
	assert.Equal(t, 1, result.TotalPlayers)
	assert.Equal(t, 1, result.AvailablePlayers) // All players available since verified set couldn't be loaded
	assert.Equal(t, 0, result.SkippedNonNHL)
}
