package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/go-redis/redismock/v8"
	"github.com/sperano/puckdb/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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
// listYahooPlayerFilesImpl tests
// ////////////////////////////////////////////////////////////////////////////

func TestListYahooPlayerFilesImpl_Success(t *testing.T) {
	fs := NewMockFileSystem()

	expectedFiles := []store.File{
		store.YahooPlayerFile{PlayerID: 1},
		store.YahooPlayerFile{PlayerID: 100},
		store.YahooPlayerFile{PlayerID: 1000},
	}

	fs.On("ListFiles", mock.Anything, mock.Anything).Return(expectedFiles, nil)

	ids, err := listYahooPlayerFilesImpl(fs)

	require.NoError(t, err)
	assert.Equal(t, []store.YahooPlayerID{1, 100, 1000}, ids)
	fs.AssertExpectations(t)
}

func TestListYahooPlayerFilesImpl_Empty(t *testing.T) {
	fs := NewMockFileSystem()

	fs.On("ListFiles", mock.Anything, mock.Anything).Return([]store.File{}, nil)

	ids, err := listYahooPlayerFilesImpl(fs)

	require.NoError(t, err)
	assert.Empty(t, ids)
}

func TestListYahooPlayerFilesImpl_Error(t *testing.T) {
	fs := NewMockFileSystem()
	expectedErr := errors.New("failed to list files")

	fs.On("ListFiles", mock.Anything, mock.Anything).Return(nil, expectedErr)

	ids, err := listYahooPlayerFilesImpl(fs)

	require.Error(t, err)
	assert.Equal(t, expectedErr, err)
	assert.Nil(t, ids)
}

// ////////////////////////////////////////////////////////////////////////////
// parseYahooPlayerBatchImpl tests
// ////////////////////////////////////////////////////////////////////////////

func TestParseYahooPlayerBatchImpl_Success(t *testing.T) {
	fs := NewMockFileSystem()

	playerIDs := []store.YahooPlayerID{97, 99}

	fs.On("Read", store.YahooPlayerFile{PlayerID: 97}).
		Return([]byte(sampleYahooPlayerHTML), nil)
	fs.On("Read", store.YahooPlayerFile{PlayerID: 99}).
		Return([]byte(sampleYahooPlayerHTMLNoJersey), nil)

	result := parseYahooPlayerBatchImpl(fs, playerIDs)

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
	fs := NewMockFileSystem()

	playerIDs := []store.YahooPlayerID{1, 2}

	fs.On("Read", store.YahooPlayerFile{PlayerID: 1}).
		Return(nil, errors.New("file not found"))
	fs.On("Read", store.YahooPlayerFile{PlayerID: 2}).
		Return([]byte(sampleYahooPlayerHTML), nil)

	result := parseYahooPlayerBatchImpl(fs, playerIDs)

	assert.Len(t, result.Players, 1)
	assert.Equal(t, 1, result.ReadErrors)
	assert.Equal(t, 0, result.ParseErrors)
}

func TestParseYahooPlayerBatchImpl_ParseError(t *testing.T) {
	fs := NewMockFileSystem()

	playerIDs := []store.YahooPlayerID{1}

	// Invalid HTML that won't parse
	fs.On("Read", store.YahooPlayerFile{PlayerID: 1}).
		Return([]byte("<html><title>Invalid Page</title></html>"), nil)

	result := parseYahooPlayerBatchImpl(fs, playerIDs)

	assert.Empty(t, result.Players)
	assert.Equal(t, 0, result.ReadErrors)
	assert.Equal(t, 1, result.ParseErrors)
}

func TestParseYahooPlayerBatchImpl_EmptyBatch(t *testing.T) {
	fs := NewMockFileSystem()

	result := parseYahooPlayerBatchImpl(fs, []store.YahooPlayerID{})

	assert.Empty(t, result.Players)
	assert.Equal(t, 0, result.ReadErrors)
	assert.Equal(t, 0, result.ParseErrors)
}

func TestParseYahooPlayerBatchImpl_MixedErrors(t *testing.T) {
	fs := NewMockFileSystem()

	playerIDs := []store.YahooPlayerID{1, 2, 3, 4}

	// Player 1: read error
	fs.On("Read", store.YahooPlayerFile{PlayerID: 1}).
		Return(nil, errors.New("file not found"))
	// Player 2: success
	fs.On("Read", store.YahooPlayerFile{PlayerID: 2}).
		Return([]byte(sampleYahooPlayerHTML), nil)
	// Player 3: parse error
	fs.On("Read", store.YahooPlayerFile{PlayerID: 3}).
		Return([]byte("<html>bad</html>"), nil)
	// Player 4: success
	fs.On("Read", store.YahooPlayerFile{PlayerID: 4}).
		Return([]byte(sampleYahooPlayerHTMLNoJersey), nil)

	result := parseYahooPlayerBatchImpl(fs, playerIDs)

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
	anyArgs := func(expected, actual []interface{}) error { return nil }
	mockRedis.CustomMatch(anyArgs).ExpectHSet(YahooIDPoolKey, "x", "x").SetVal(1)
	mockRedis.CustomMatch(anyArgs).ExpectHSet(YahooIDPoolKey, "x", "x").SetVal(1)
	mockRedis.CustomMatch(anyArgs).ExpectSAdd(YahooIDAvailableKey, "x", "x").SetVal(2)
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
	anyArgs := func(expected, actual []interface{}) error { return nil }
	mockRedis.CustomMatch(anyArgs).ExpectHSet(YahooIDPoolKey, "x", "x").SetVal(1)
	mockRedis.CustomMatch(anyArgs).ExpectHSet(YahooIDPoolKey, "x", "x").SetVal(1)
	mockRedis.CustomMatch(anyArgs).ExpectHSet(YahooIDPoolKey, "x", "x").SetVal(1)

	// Expect SAdd only for available IDs (players 1 and 3, not 2)
	mockRedis.CustomMatch(anyArgs).ExpectSAdd(YahooIDAvailableKey, "x", "x").SetVal(2)

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
	anyArgs := func(expected, actual []interface{}) error { return nil }
	mockRedis.CustomMatch(anyArgs).ExpectHSet(YahooIDPoolKey, "x", "x").SetVal(1)
	mockRedis.CustomMatch(anyArgs).ExpectSAdd(YahooIDAvailableKey, "x").SetVal(1)
	mockRedis.ExpectExpire(YahooIDPoolKey, ImportPlayersTTL).SetVal(true)
	mockRedis.ExpectExpire(YahooIDAvailableKey, ImportPlayersTTL).SetErr(errors.New("redis connection failed"))

	result, err := saveYahooPlayersToRedisImpl(ctx, client, players)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis connection failed")
	assert.Nil(t, result)
}
