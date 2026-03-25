package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/go-redis/redismock/v8"
	"github.com/sperano/puckdb/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadUnmatchedYahooPlayers_EmptySet(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client, mockRedis := redismock.NewClientMock()

	mockRedis.ExpectSMembers(YahooIDAvailableKey).SetVal([]string{})

	a := &PlayerActivities{RedisClient: client}
	result, err := a.loadUnmatchedYahooPlayersImpl(ctx)

	require.NoError(t, err)
	assert.Empty(t, result)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestLoadUnmatchedYahooPlayers_Success(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)

	// Return two unmatched IDs
	mockRedis.ExpectSMembers(YahooIDAvailableKey).SetVal([]string{"123", "456"})

	// Return player details for each
	player1 := store.YahooPlayer{
		YahooID:      123,
		FirstName:    "Connor",
		LastName:     "McDavid",
		Team:         "EDM",
		JerseyNumber: 97,
	}
	player1JSON, err := json.Marshal(player1)
	require.NoError(t, err)
	mockRedis.ExpectHGet(YahooIDPoolKey, "123").SetVal(string(player1JSON))

	player2 := store.YahooPlayer{
		YahooID:      456,
		FirstName:    "Leon",
		LastName:     "Draisaitl",
		Team:         "EDM",
		JerseyNumber: 29,
	}
	player2JSON, err := json.Marshal(player2)
	require.NoError(t, err)
	mockRedis.ExpectHGet(YahooIDPoolKey, "456").SetVal(string(player2JSON))

	a := &PlayerActivities{RedisClient: client}
	result, err := a.loadUnmatchedYahooPlayersImpl(ctx)

	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestLoadUnmatchedYahooPlayers_SMembersError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client, mockRedis := redismock.NewClientMock()

	mockRedis.ExpectSMembers(YahooIDAvailableKey).SetErr(errors.New("redis connection failed"))

	a := &PlayerActivities{RedisClient: client}
	result, err := a.loadUnmatchedYahooPlayersImpl(ctx)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis connection failed")
	assert.Nil(t, result)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestLoadUnmatchedYahooPlayers_PlayerDetailsMissing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)

	// Return one unmatched ID
	mockRedis.ExpectSMembers(YahooIDAvailableKey).SetVal([]string{"999"})

	// Player details not found
	mockRedis.ExpectHGet(YahooIDPoolKey, "999").SetErr(errors.New("redis: nil"))

	a := &PlayerActivities{RedisClient: client}
	result, err := a.loadUnmatchedYahooPlayersImpl(ctx)

	// Should still return with basic info (YahooID only)
	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, store.YahooPlayerID(999), result[0].YahooID)
	assert.Empty(t, result[0].FirstName)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestCleanupYahooIDPool_Success(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client, mockRedis := redismock.NewClientMock()

	mockRedis.ExpectDel(YahooIDPoolKey, YahooIDAvailableKey).SetVal(2)

	err := CleanupYahooIDPool(ctx, client)

	require.NoError(t, err)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestCleanupYahooIDPool_Error(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client, mockRedis := redismock.NewClientMock()

	mockRedis.ExpectDel(YahooIDPoolKey, YahooIDAvailableKey).SetErr(errors.New("redis error"))

	err := CleanupYahooIDPool(ctx, client)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis error")
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}
