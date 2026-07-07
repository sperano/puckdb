package player

import (
	"context"
	"errors"
	"testing"

	"github.com/go-redis/redismock/v8"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Sample Yahoo player HTML for testing
const sampleYahooPlayerHTML = `<html>
<head><title>Connor McDavid (C) Stats, News, Bio - Edmonton Oilers - Yahoo Sports</title></head>
<body><span">#97<</span></body>
</html>`

const sampleYahooPlayerHTMLNoJersey = `<html>
<head><title>Wayne Gretzky (C) Stats, News, Bio - Yahoo Sports</title></head>
<body></body>
</html>`

// ////////////////////////////////////////////////////////////////////////////
// ListYahooPlayers tests (via listYahooPlayers helper)
// ////////////////////////////////////////////////////////////////////////////

func TestListYahooPlayers_Success(t *testing.T) {
	mem := store.NewMemStorage()

	// Pre-populate player files
	require.NoError(t, mem.Write(context.Background(), resource.YahooPlayer{PlayerID: 1}.Path(), []byte(sampleYahooPlayerHTML)))
	require.NoError(t, mem.Write(context.Background(), resource.YahooPlayer{PlayerID: 100}.Path(), []byte(sampleYahooPlayerHTML)))
	require.NoError(t, mem.Write(context.Background(), resource.YahooPlayer{PlayerID: 1000}.Path(), []byte(sampleYahooPlayerHTML)))

	ids, err := listYahooPlayers(context.Background(), mem)

	require.NoError(t, err)
	assert.Len(t, ids, 3)
	// Note: order may vary, just check all present
	idSet := make(map[store.YahooPlayerID]bool)
	for _, id := range ids {
		idSet[id] = true
	}
	assert.True(t, idSet[1])
	assert.True(t, idSet[100])
	assert.True(t, idSet[1000])
}

func TestListYahooPlayers_Empty(t *testing.T) {
	mem := store.NewMemStorage()

	ids, err := listYahooPlayers(context.Background(), mem)

	require.NoError(t, err)
	assert.Empty(t, ids)
}

// ////////////////////////////////////////////////////////////////////////////
// parseYahooPlayerBatchImpl tests
// ////////////////////////////////////////////////////////////////////////////

func TestParseYahooPlayerBatchImpl_Success(t *testing.T) {
	mem := store.NewMemStorage()

	playerIDs := []store.YahooPlayerID{97, 99}

	// Pre-populate player files
	require.NoError(t, mem.Write(context.Background(), resource.YahooPlayer{PlayerID: 97}.Path(), []byte(sampleYahooPlayerHTML)))
	require.NoError(t, mem.Write(context.Background(), resource.YahooPlayer{PlayerID: 99}.Path(), []byte(sampleYahooPlayerHTMLNoJersey)))

	result := parseYahooPlayerBatchImpl(context.Background(), mem, nil, playerIDs)

	assert.Len(t, result.Players, 2)
	assert.Equal(t, 0, result.ReadErrors)
	assert.Equal(t, 0, result.ParseErrors)

	// Verify first player
	assert.Equal(t, store.YahooPlayerID(97), result.Players[0].YahooID)
	assert.Equal(t, "Connor", result.Players[0].FirstName)
	assert.Equal(t, "McDavid", result.Players[0].LastName)
	assert.Equal(t, 97, result.Players[0].JerseyNumber)

	// Verify second player (no jersey number)
	assert.Equal(t, store.YahooPlayerID(99), result.Players[1].YahooID)
	assert.Equal(t, "Wayne", result.Players[1].FirstName)
	assert.Equal(t, "Gretzky", result.Players[1].LastName)
}

func TestParseYahooPlayerBatchImpl_ReadError(t *testing.T) {
	mem := store.NewMemStorage()

	playerIDs := []store.YahooPlayerID{1, 2}

	// Only player 2 exists
	require.NoError(t, mem.Write(context.Background(), resource.YahooPlayer{PlayerID: 2}.Path(), []byte(sampleYahooPlayerHTML)))

	result := parseYahooPlayerBatchImpl(context.Background(), mem, nil, playerIDs)

	assert.Len(t, result.Players, 1)
	assert.Equal(t, 1, result.ReadErrors)
	assert.Equal(t, 0, result.ParseErrors)
}

func TestParseYahooPlayerBatchImpl_ParseError(t *testing.T) {
	mem := store.NewMemStorage()

	playerIDs := []store.YahooPlayerID{1}

	// Invalid HTML that won't parse
	require.NoError(t, mem.Write(context.Background(), resource.YahooPlayer{PlayerID: 1}.Path(), []byte("<html><title>Invalid Page</title></html>")))

	result := parseYahooPlayerBatchImpl(context.Background(), mem, nil, playerIDs)

	assert.Empty(t, result.Players)
	assert.Equal(t, 0, result.ReadErrors)
	assert.Equal(t, 1, result.ParseErrors)
}

func TestParseYahooPlayerBatchImpl_EmptyBatch(t *testing.T) {
	mem := store.NewMemStorage()

	result := parseYahooPlayerBatchImpl(context.Background(), mem, nil, []store.YahooPlayerID{})

	assert.Empty(t, result.Players)
	assert.Equal(t, 0, result.ReadErrors)
	assert.Equal(t, 0, result.ParseErrors)
}

func TestParseYahooPlayerBatchImpl_MixedErrors(t *testing.T) {
	mem := store.NewMemStorage()

	playerIDs := []store.YahooPlayerID{1, 2, 3, 4}

	// Player 1: doesn't exist (read error)
	// Player 2: success
	require.NoError(t, mem.Write(context.Background(), resource.YahooPlayer{PlayerID: 2}.Path(), []byte(sampleYahooPlayerHTML)))
	// Player 3: parse error
	require.NoError(t, mem.Write(context.Background(), resource.YahooPlayer{PlayerID: 3}.Path(), []byte("<html>bad</html>")))
	// Player 4: success
	require.NoError(t, mem.Write(context.Background(), resource.YahooPlayer{PlayerID: 4}.Path(), []byte(sampleYahooPlayerHTMLNoJersey)))

	result := parseYahooPlayerBatchImpl(context.Background(), mem, nil, playerIDs)

	assert.Len(t, result.Players, 2)
	assert.Equal(t, 1, result.ReadErrors)
	assert.Equal(t, 1, result.ParseErrors)
}

// ////////////////////////////////////////////////////////////////////////////
// saveYahooPlayersToRedisImpl tests
// ////////////////////////////////////////////////////////////////////////////

func TestSaveYahooPlayersToRedisImpl_EmptyPlayers(t *testing.T) {
	ctx := context.Background()
	client, _ := redismock.NewClientMock()

	result, err := saveYahooPlayersToRedisImpl(ctx, client, []store.YahooPlayer{})

	require.NoError(t, err)
	assert.Equal(t, 0, result.TotalPlayers)
	assert.Equal(t, 0, result.AvailablePlayers)
	assert.Equal(t, 0, result.SkippedNonNHL)
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

	result, err := saveYahooPlayersToRedisImpl(ctx, client, players)

	require.NoError(t, err)
	assert.Equal(t, 2, result.TotalPlayers)
	assert.Equal(t, 2, result.AvailablePlayers)
	assert.Equal(t, 0, result.SkippedNonNHL)
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

	// Expect Expire calls
	mockRedis.ExpectExpire(YahooIDPoolKey, ImportPlayersTTL).SetVal(true)
	mockRedis.ExpectExpire(YahooIDAvailableKey, ImportPlayersTTL).SetVal(true)

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

	result, err := saveYahooPlayersToRedisImpl(ctx, client, players)

	require.NoError(t, err)
	assert.Equal(t, 1, result.TotalPlayers)
	assert.Equal(t, 1, result.AvailablePlayers) // All players available since verified set couldn't be loaded
	assert.Equal(t, 0, result.SkippedNonNHL)
}
