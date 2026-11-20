package player

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/go-redis/redismock/v8"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadUnmatchedYahooPlayers_EmptySet(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client, mockRedis := redismock.NewClientMock()

	mockRedis.ExpectSMembers(YahooIDAvailableKey).SetVal([]string{})

	a := &Activities{RedisClient: client}
	result, skipped, err := a.loadUnmatchedYahooPlayersImpl(ctx, LoadUnmatchedInput{})

	require.NoError(t, err)
	assert.Empty(t, result)
	assert.Zero(t, skipped)
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

	a := &Activities{RedisClient: client}
	result, skipped, err := a.loadUnmatchedYahooPlayersImpl(ctx, LoadUnmatchedInput{})

	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Zero(t, skipped)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestLoadUnmatchedYahooPlayers_SMembersError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client, mockRedis := redismock.NewClientMock()

	mockRedis.ExpectSMembers(YahooIDAvailableKey).SetErr(errors.New("redis connection failed"))

	a := &Activities{RedisClient: client}
	result, skipped, err := a.loadUnmatchedYahooPlayersImpl(ctx, LoadUnmatchedInput{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis connection failed")
	assert.Nil(t, result)
	assert.Zero(t, skipped)
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

	a := &Activities{RedisClient: client}
	result, skipped, err := a.loadUnmatchedYahooPlayersImpl(ctx, LoadUnmatchedInput{})

	// Should still return with basic info (YahooID only)
	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, store.YahooPlayerID(999), result[0].YahooID)
	assert.Empty(t, result[0].FirstName)
	assert.Zero(t, skipped)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestLoadUnmatchedYahooPlayers_SeasonsSkipAPlayerWithoutDetails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client, mockRedis := redismock.NewClientMock()
	mockRedis.ExpectSMembers(YahooIDAvailableKey).SetVal([]string{"999"})
	mockRedis.ExpectHGet(YahooIDPoolKey, "999").SetErr(errors.New("redis: nil"))

	a := &Activities{RedisClient: client}
	result, skipped, err := a.loadUnmatchedYahooPlayersImpl(ctx, LoadUnmatchedInput{Seasons: []int{2025}})

	require.NoError(t, err)
	assert.Empty(t, result)
	assert.Equal(t, 1, skipped)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

// A single-season run over the pool of every season's game player lists
// verifies only the Yahoo IDs its season's list names.
func TestLoadUnmatchedYahooPlayers_SingleSeasonRunOverAMultiSeasonPool(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr()})
	t.Cleanup(func() { _ = client.Close() })
	_, err := saveYahooPlayersToRedisImpl(ctx, client, []store.YahooPlayer{
		{YahooID: 1, FirstName: "Current", LastName: "Only", Seasons: []int{2025}},
		{YahooID: 2, FirstName: "Long", LastName: "Career", Seasons: []int{2010, 2025}},
		{YahooID: 3, FirstName: "Retired", LastName: "Before", Seasons: []int{2010, 2011}},
		{YahooID: 4, FirstName: "League", LastName: "Only"},
	})
	require.NoError(t, err)

	a := &Activities{RedisClient: client}
	single, skipped, err := a.loadUnmatchedYahooPlayersImpl(ctx, LoadUnmatchedInput{Seasons: []int{2025}})
	require.NoError(t, err)
	assert.ElementsMatch(t, []UnmatchedYahooPlayer{
		{YahooID: 1, FirstName: "Current", LastName: "Only"},
		{YahooID: 2, FirstName: "Long", LastName: "Career"},
	}, single)
	assert.Equal(t, 2, skipped)

	all, skipped, err := a.loadUnmatchedYahooPlayersImpl(ctx, LoadUnmatchedInput{})
	require.NoError(t, err)
	assert.Len(t, all, 4, "unknown seasons verify every unmatched Yahoo ID")
	assert.Zero(t, skipped)
}

func TestListedInSeasons(t *testing.T) {
	t.Parallel()

	player := &store.YahooPlayer{Seasons: []int{2010, 2012}}
	assert.True(t, listedInSeasons(player, nil), "nil seasons: unknown, every player")
	assert.True(t, listedInSeasons(player, []int{2012, 2025}))
	assert.False(t, listedInSeasons(player, []int{2011}))
	assert.False(t, listedInSeasons(player, []int{}), "no season lists anyone")
	assert.False(t, listedInSeasons(&store.YahooPlayer{}, []int{2012}), "a player no list names")
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
