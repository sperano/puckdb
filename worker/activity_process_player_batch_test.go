package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/go-redis/redismock/v8"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestProcessPlayerBatch_EmptyPlayers(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}
	redisClient, _ := redismock.NewClientMock()
	upserter := NewMockPlayerUpserter()

	deps := processDeps{
		fs:        fs,
		nhlClient: client,
		redis:     redisClient,
		queries:   upserter,
	}

	result, err := processPlayerBatchImpl(ctx, deps, []BoxscorePlayer{})

	require.NoError(t, err)
	assert.Equal(t, 0, result.Downloaded)
	assert.Equal(t, 0, result.CacheHits)
	assert.Equal(t, 0, result.Missing)
	assert.Equal(t, 0, result.Imported)
	assert.Equal(t, 0, result.Matched)
	assert.Empty(t, result.Errors)
}

func TestProcessPlayerBatch_LoadYahooPoolError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}
	redisClient, mockRedis := redismock.NewClientMock()
	upserter := NewMockPlayerUpserter()

	// Redis HGetAll fails
	mockRedis.ExpectHGetAll(YahooIDPoolKey).SetErr(errors.New("redis connection refused"))

	deps := processDeps{
		fs:        fs,
		nhlClient: client,
		redis:     redisClient,
		queries:   upserter,
	}

	players := []BoxscorePlayer{{ID: 8476453}}
	result, err := processPlayerBatchImpl(ctx, deps, players)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "load yahoo pool")
	assert.Equal(t, ProcessPlayerBatchResult{}, result)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestProcessPlayerBatch_ContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	fs := NewMockFileSystem()
	client := &MockNHLClient{}
	redisClient, mockRedis := redismock.NewClientMock()
	upserter := NewMockPlayerUpserter()

	// Redis HGetAll succeeds
	mockRedis.ExpectHGetAll(YahooIDPoolKey).SetVal(map[string]string{})

	deps := processDeps{
		fs:        fs,
		nhlClient: client,
		redis:     redisClient,
		queries:   upserter,
	}

	// Cancel context before processing
	cancel()

	players := []BoxscorePlayer{{ID: 8476453}}
	result, err := processPlayerBatchImpl(ctx, deps, players)

	require.Error(t, err)
	assert.Equal(t, context.Canceled, err)
	assert.Equal(t, 0, result.Imported)
}

func TestProcessPlayerBatch_CacheHitAndImport(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)
	upserter := NewMockPlayerUpserter()

	playerID := nhl.PlayerID(8476453)

	// Redis HGetAll succeeds (empty pool - no Yahoo matching)
	mockRedis.ExpectHGetAll(YahooIDPoolKey).SetVal(map[string]string{})

	// Player landing file already exists (cache hit)
	landingFile := store.PlayerLandingFile{PlayerID: playerID}
	missingFile := store.MissingPlayerLandingFile{PlayerID: playerID}
	fs.On("Exists", missingFile).Return(false)
	fs.On("Exists", landingFile).Return(true)

	// Read the cached landing file
	landing := &nhl.PlayerLanding{
		PlayerID:  playerID,
		FirstName: nhl.LocalizedString{Default: "Connor"},
		LastName:  nhl.LocalizedString{Default: "McDavid"},
		Position:  "C",
		IsActive:  true,
	}
	landingJSON, _ := json.Marshal(landing)
	fs.On("Read", landingFile).Return(landingJSON, nil)

	// Expect UpsertPlayer call
	upserter.On("UpsertPlayer", ctx, mock.AnythingOfType("sqlcdb.UpsertPlayerParams")).Return(nil)

	deps := processDeps{
		fs:        fs,
		nhlClient: client,
		redis:     redisClient,
		queries:   upserter,
	}

	players := []BoxscorePlayer{{ID: int64(playerID)}}
	result, err := processPlayerBatchImpl(ctx, deps, players)

	require.NoError(t, err)
	assert.Equal(t, 0, result.Downloaded)
	assert.Equal(t, 1, result.CacheHits)
	assert.Equal(t, 0, result.Missing)
	assert.Equal(t, 1, result.Imported)
	assert.Equal(t, 0, result.Matched) // No Yahoo pool entries
	assert.Empty(t, result.Errors)
	fs.AssertExpectations(t)
	upserter.AssertExpectations(t)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestProcessPlayerBatch_MissingPlayer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}
	redisClient, mockRedis := redismock.NewClientMock()
	upserter := NewMockPlayerUpserter()

	playerID := nhl.PlayerID(9999999)

	// Redis HGetAll succeeds
	mockRedis.ExpectHGetAll(YahooIDPoolKey).SetVal(map[string]string{})

	// Missing file exists (player marked as 404)
	missingFile := store.MissingPlayerLandingFile{PlayerID: playerID}
	fs.On("Exists", missingFile).Return(true)

	deps := processDeps{
		fs:        fs,
		nhlClient: client,
		redis:     redisClient,
		queries:   upserter,
	}

	players := []BoxscorePlayer{{ID: int64(playerID)}}
	result, err := processPlayerBatchImpl(ctx, deps, players)

	require.NoError(t, err)
	assert.Equal(t, 0, result.Downloaded)
	assert.Equal(t, 0, result.CacheHits)
	assert.Equal(t, 1, result.Missing)
	assert.Equal(t, 0, result.Imported) // Missing players are not imported
	assert.Empty(t, result.Errors)
	fs.AssertExpectations(t)
	upserter.AssertNotCalled(t, "UpsertPlayer")
}

func TestProcessPlayerBatch_UpsertError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}
	redisClient, mockRedis := redismock.NewClientMock()
	upserter := NewMockPlayerUpserter()

	playerID := nhl.PlayerID(8476453)

	// Redis HGetAll succeeds
	mockRedis.ExpectHGetAll(YahooIDPoolKey).SetVal(map[string]string{})

	// Player landing file exists
	landingFile := store.PlayerLandingFile{PlayerID: playerID}
	missingFile := store.MissingPlayerLandingFile{PlayerID: playerID}
	fs.On("Exists", missingFile).Return(false)
	fs.On("Exists", landingFile).Return(true)

	// Read the cached landing file
	landing := &nhl.PlayerLanding{
		PlayerID:  playerID,
		FirstName: nhl.LocalizedString{Default: "Connor"},
		LastName:  nhl.LocalizedString{Default: "McDavid"},
		Position:  "C",
		IsActive:  true,
	}
	landingJSON, _ := json.Marshal(landing)
	fs.On("Read", landingFile).Return(landingJSON, nil)

	// UpsertPlayer fails
	upserter.On("UpsertPlayer", ctx, mock.AnythingOfType("sqlcdb.UpsertPlayerParams")).
		Return(errors.New("database connection lost"))

	deps := processDeps{
		fs:        fs,
		nhlClient: client,
		redis:     redisClient,
		queries:   upserter,
	}

	players := []BoxscorePlayer{{ID: int64(playerID)}}
	result, err := processPlayerBatchImpl(ctx, deps, players)

	// No error returned - errors are collected in result.Errors
	require.NoError(t, err)
	assert.Equal(t, 1, result.CacheHits)
	assert.Equal(t, 0, result.Imported) // Failed to import
	assert.Len(t, result.Errors, 1)
	assert.Contains(t, result.Errors[0], "upsert error")
	fs.AssertExpectations(t)
	upserter.AssertExpectations(t)
}

func TestProcessPlayerBatch_FileReadError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}
	redisClient, mockRedis := redismock.NewClientMock()
	upserter := NewMockPlayerUpserter()

	playerID := nhl.PlayerID(8476453)

	// Redis HGetAll succeeds
	mockRedis.ExpectHGetAll(YahooIDPoolKey).SetVal(map[string]string{})

	// Player landing file exists
	landingFile := store.PlayerLandingFile{PlayerID: playerID}
	missingFile := store.MissingPlayerLandingFile{PlayerID: playerID}
	fs.On("Exists", missingFile).Return(false)
	fs.On("Exists", landingFile).Return(true)

	// Read fails
	fs.On("Read", landingFile).Return(nil, errors.New("disk read error"))

	deps := processDeps{
		fs:        fs,
		nhlClient: client,
		redis:     redisClient,
		queries:   upserter,
	}

	players := []BoxscorePlayer{{ID: int64(playerID)}}
	result, err := processPlayerBatchImpl(ctx, deps, players)

	// No error returned - errors are collected in result.Errors
	require.NoError(t, err)
	assert.Equal(t, 1, result.CacheHits)
	assert.Equal(t, 0, result.Imported)
	assert.Len(t, result.Errors, 1)
	assert.Contains(t, result.Errors[0], "read error")
	fs.AssertExpectations(t)
	upserter.AssertNotCalled(t, "UpsertPlayer")
}

func TestProcessPlayerBatch_JSONParseError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}
	redisClient, mockRedis := redismock.NewClientMock()
	upserter := NewMockPlayerUpserter()

	playerID := nhl.PlayerID(8476453)

	// Redis HGetAll succeeds
	mockRedis.ExpectHGetAll(YahooIDPoolKey).SetVal(map[string]string{})

	// Player landing file exists
	landingFile := store.PlayerLandingFile{PlayerID: playerID}
	missingFile := store.MissingPlayerLandingFile{PlayerID: playerID}
	fs.On("Exists", missingFile).Return(false)
	fs.On("Exists", landingFile).Return(true)

	// Read returns invalid JSON
	fs.On("Read", landingFile).Return([]byte("not valid json"), nil)

	deps := processDeps{
		fs:        fs,
		nhlClient: client,
		redis:     redisClient,
		queries:   upserter,
	}

	players := []BoxscorePlayer{{ID: int64(playerID)}}
	result, err := processPlayerBatchImpl(ctx, deps, players)

	// No error returned - errors are collected in result.Errors
	require.NoError(t, err)
	assert.Equal(t, 1, result.CacheHits)
	assert.Equal(t, 0, result.Imported)
	assert.Len(t, result.Errors, 1)
	assert.Contains(t, result.Errors[0], "parse error")
	fs.AssertExpectations(t)
	upserter.AssertNotCalled(t, "UpsertPlayer")
}
