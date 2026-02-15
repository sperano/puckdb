package worker

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"errors"
	"testing"

	"github.com/go-redis/redismock/v8"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/sqlcdb"
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

func TestProcessPlayerBatch_DownloadAndImport(t *testing.T) {
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

	landingFile := store.PlayerLandingFile{PlayerID: playerID}
	missingFile := store.MissingPlayerLandingFile{PlayerID: playerID}

	// Player landing file does NOT exist - needs download
	fs.On("Exists", missingFile).Return(false)
	fs.On("Exists", landingFile).Return(false).Once() // First check - not cached

	// Client downloads the player landing
	landing := &nhl.PlayerLanding{
		PlayerID:  playerID,
		FirstName: nhl.LocalizedString{Default: "Connor"},
		LastName:  nhl.LocalizedString{Default: "McDavid"},
		Position:  "C",
		IsActive:  true,
	}
	client.On("PlayerLanding", ctx, playerID).Return(landing, nil)

	// Save to cache (ensurePlayerLandingCached directly writes without MkdirAll)
	fs.On("Write", landingFile, mock.Anything).Return(nil)

	// After download, file exists and can be read
	landingJSON, _ := json.Marshal(landing)
	fs.On("Exists", landingFile).Return(true) // Second check - now cached
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
	assert.Equal(t, 1, result.Downloaded)
	assert.Equal(t, 0, result.CacheHits)
	assert.Equal(t, 0, result.Missing)
	assert.Equal(t, 1, result.Imported)
	assert.Equal(t, 0, result.Matched) // No Yahoo pool entries
	assert.Empty(t, result.Errors)
	fs.AssertExpectations(t)
	client.AssertExpectations(t)
	upserter.AssertExpectations(t)
}

func TestProcessPlayerBatch_DownloadAPIError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}
	redisClient, mockRedis := redismock.NewClientMock()
	upserter := NewMockPlayerUpserter()

	playerID := nhl.PlayerID(8476453)

	// Redis HGetAll succeeds
	mockRedis.ExpectHGetAll(YahooIDPoolKey).SetVal(map[string]string{})

	landingFile := store.PlayerLandingFile{PlayerID: playerID}
	missingFile := store.MissingPlayerLandingFile{PlayerID: playerID}

	// File doesn't exist - needs download
	fs.On("Exists", missingFile).Return(false)
	fs.On("Exists", landingFile).Return(false)

	// API returns non-404 error
	client.On("PlayerLanding", ctx, playerID).Return(nil, errors.New("API timeout"))

	deps := processDeps{
		fs:        fs,
		nhlClient: client,
		redis:     redisClient,
		queries:   upserter,
	}

	players := []BoxscorePlayer{{ID: int64(playerID)}}
	result, err := processPlayerBatchImpl(ctx, deps, players)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "API timeout")
	assert.Equal(t, 0, result.Imported)
	fs.AssertExpectations(t)
	client.AssertExpectations(t)
}

func TestProcessPlayerBatch_WriteErrorAfterDownload(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}
	redisClient, mockRedis := redismock.NewClientMock()
	upserter := NewMockPlayerUpserter()

	playerID := nhl.PlayerID(8476453)

	// Redis HGetAll succeeds
	mockRedis.ExpectHGetAll(YahooIDPoolKey).SetVal(map[string]string{})

	landingFile := store.PlayerLandingFile{PlayerID: playerID}
	missingFile := store.MissingPlayerLandingFile{PlayerID: playerID}

	// File doesn't exist - needs download
	fs.On("Exists", missingFile).Return(false)
	fs.On("Exists", landingFile).Return(false)

	// API returns data
	landing := &nhl.PlayerLanding{
		PlayerID:  playerID,
		FirstName: nhl.LocalizedString{Default: "Connor"},
		LastName:  nhl.LocalizedString{Default: "McDavid"},
		Position:  "C",
	}
	client.On("PlayerLanding", ctx, playerID).Return(landing, nil)

	// Write fails
	fs.On("Write", landingFile, mock.Anything).Return(errors.New("disk full"))

	deps := processDeps{
		fs:        fs,
		nhlClient: client,
		redis:     redisClient,
		queries:   upserter,
	}

	players := []BoxscorePlayer{{ID: int64(playerID)}}
	result, err := processPlayerBatchImpl(ctx, deps, players)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "write player")
	assert.Equal(t, 0, result.Imported)
	fs.AssertExpectations(t)
	client.AssertExpectations(t)
}

func TestProcessPlayerBatch_YahooMatchWithClearConflict(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)
	upserter := NewMockPlayerUpserter()

	playerID := nhl.PlayerID(8476453)
	yahooID := store.YahooPlayerID(12345)

	// Create Yahoo pool with a matching player
	yahooPlayer := &store.YahooPlayer{
		YahooID:   yahooID,
		FirstName: "Connor",
		LastName:  "McDavid",
		Team:      "EDM",
	}
	poolData := encodeYahooPlayer(t, yahooPlayer)
	mockRedis.ExpectHGetAll(YahooIDPoolKey).SetVal(map[string]string{
		yahooID.String(): string(poolData),
	})

	// Also expect the removal of the matched ID (SRem)
	mockRedis.ExpectSRem(YahooIDAvailableKey, yahooID).SetVal(1)

	landingFile := store.PlayerLandingFile{PlayerID: playerID}
	missingFile := store.MissingPlayerLandingFile{PlayerID: playerID}

	// File exists (cache hit)
	fs.On("Exists", missingFile).Return(false)
	fs.On("Exists", landingFile).Return(true)

	// Read cached landing with team
	teamAbbrev := "EDM"
	landing := &nhl.PlayerLanding{
		PlayerID:          playerID,
		FirstName:         nhl.LocalizedString{Default: "Connor"},
		LastName:          nhl.LocalizedString{Default: "McDavid"},
		Position:          "C",
		IsActive:          true,
		CurrentTeamAbbrev: &teamAbbrev,
		BirthDate:         "1997-01-13",
	}
	landingJSON, _ := json.Marshal(landing)
	fs.On("Read", landingFile).Return(landingJSON, nil)

	// Expect ClearConflictingYahooID call
	upserter.On("ClearConflictingYahooID", ctx, mock.AnythingOfType("sqlcdb.ClearConflictingYahooIDParams")).Return(nil)

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
	assert.Equal(t, 1, result.CacheHits)
	assert.Equal(t, 1, result.Imported)
	assert.Equal(t, 1, result.Matched) // Yahoo ID matched
	assert.Empty(t, result.Errors)
	fs.AssertExpectations(t)
	upserter.AssertExpectations(t)
}

func TestProcessPlayerBatch_ClearConflictingYahooIDError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)
	upserter := NewMockPlayerUpserter()

	playerID := nhl.PlayerID(8476453)
	yahooID := store.YahooPlayerID(12345)

	// Create Yahoo pool with a matching player
	yahooPlayer := &store.YahooPlayer{
		YahooID:   yahooID,
		FirstName: "Connor",
		LastName:  "McDavid",
	}
	poolData := encodeYahooPlayer(t, yahooPlayer)
	mockRedis.ExpectHGetAll(YahooIDPoolKey).SetVal(map[string]string{
		yahooID.String(): string(poolData),
	})
	mockRedis.ExpectSRem(YahooIDAvailableKey, yahooID).SetVal(1)

	landingFile := store.PlayerLandingFile{PlayerID: playerID}
	missingFile := store.MissingPlayerLandingFile{PlayerID: playerID}

	fs.On("Exists", missingFile).Return(false)
	fs.On("Exists", landingFile).Return(true)

	landing := &nhl.PlayerLanding{
		PlayerID:  playerID,
		FirstName: nhl.LocalizedString{Default: "Connor"},
		LastName:  nhl.LocalizedString{Default: "McDavid"},
		Position:  "C",
	}
	landingJSON, _ := json.Marshal(landing)
	fs.On("Read", landingFile).Return(landingJSON, nil)

	// ClearConflictingYahooID fails - should log warning but continue
	upserter.On("ClearConflictingYahooID", ctx, mock.AnythingOfType("sqlcdb.ClearConflictingYahooIDParams")).
		Return(errors.New("db error"))

	// UpsertPlayer should still be called
	upserter.On("UpsertPlayer", ctx, mock.AnythingOfType("sqlcdb.UpsertPlayerParams")).Return(nil)

	deps := processDeps{
		fs:        fs,
		nhlClient: client,
		redis:     redisClient,
		queries:   upserter,
	}

	players := []BoxscorePlayer{{ID: int64(playerID)}}
	result, err := processPlayerBatchImpl(ctx, deps, players)

	// Should succeed despite ClearConflictingYahooID error
	require.NoError(t, err)
	assert.Equal(t, 1, result.Imported)
	assert.Equal(t, 1, result.Matched)
	upserter.AssertExpectations(t)
}

func TestProcessPlayerBatch_UpsertErrorWithYahooID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)
	upserter := NewMockPlayerUpserter()

	playerID := nhl.PlayerID(8476453)
	yahooID := store.YahooPlayerID(12345)

	// Create Yahoo pool with a matching player
	yahooPlayer := &store.YahooPlayer{
		YahooID:   yahooID,
		FirstName: "Connor",
		LastName:  "McDavid",
	}
	poolData := encodeYahooPlayer(t, yahooPlayer)
	mockRedis.ExpectHGetAll(YahooIDPoolKey).SetVal(map[string]string{
		yahooID.String(): string(poolData),
	})

	landingFile := store.PlayerLandingFile{PlayerID: playerID}
	missingFile := store.MissingPlayerLandingFile{PlayerID: playerID}

	fs.On("Exists", missingFile).Return(false)
	fs.On("Exists", landingFile).Return(true)

	landing := &nhl.PlayerLanding{
		PlayerID:  playerID,
		FirstName: nhl.LocalizedString{Default: "Connor"},
		LastName:  nhl.LocalizedString{Default: "McDavid"},
		Position:  "C",
	}
	landingJSON, _ := json.Marshal(landing)
	fs.On("Read", landingFile).Return(landingJSON, nil)

	upserter.On("ClearConflictingYahooID", ctx, mock.AnythingOfType("sqlcdb.ClearConflictingYahooIDParams")).Return(nil)

	// UpsertPlayer fails
	upserter.On("UpsertPlayer", ctx, mock.AnythingOfType("sqlcdb.UpsertPlayerParams")).
		Return(errors.New("constraint violation"))

	deps := processDeps{
		fs:        fs,
		nhlClient: client,
		redis:     redisClient,
		queries:   upserter,
	}

	players := []BoxscorePlayer{{ID: int64(playerID)}}
	result, err := processPlayerBatchImpl(ctx, deps, players)

	require.NoError(t, err)
	assert.Equal(t, 0, result.Imported)
	assert.Len(t, result.Errors, 1)
	// Error message should include Yahoo ID
	assert.Contains(t, result.Errors[0], "yahoo_id=12345")
	assert.Contains(t, result.Errors[0], "upsert error")
}

func TestProcessPlayerBatch_FileNotFoundAfterDownload(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)
	upserter := NewMockPlayerUpserter()

	playerID := nhl.PlayerID(8476453)

	mockRedis.ExpectHGetAll(YahooIDPoolKey).SetVal(map[string]string{})

	landingFile := store.PlayerLandingFile{PlayerID: playerID}
	missingFile := store.MissingPlayerLandingFile{PlayerID: playerID}

	// First check: file doesn't exist (needs download)
	fs.On("Exists", missingFile).Return(false)
	fs.On("Exists", landingFile).Return(false).Once()

	// API returns data
	landing := &nhl.PlayerLanding{
		PlayerID:  playerID,
		FirstName: nhl.LocalizedString{Default: "Connor"},
		LastName:  nhl.LocalizedString{Default: "McDavid"},
		Position:  "C",
	}
	client.On("PlayerLanding", ctx, playerID).Return(landing, nil)

	// Write succeeds
	fs.On("Write", landingFile, mock.Anything).Return(nil)

	// But then file doesn't exist when we try to read! (edge case)
	fs.On("Exists", landingFile).Return(false)

	deps := processDeps{
		fs:        fs,
		nhlClient: client,
		redis:     redisClient,
		queries:   upserter,
	}

	players := []BoxscorePlayer{{ID: int64(playerID)}}
	result, err := processPlayerBatchImpl(ctx, deps, players)

	require.NoError(t, err)
	assert.Equal(t, 1, result.Downloaded)
	assert.Equal(t, 0, result.Imported)
	assert.Len(t, result.Errors, 1)
	assert.Contains(t, result.Errors[0], "file not found after download")
}

func TestProcessPlayerBatch_FullPlayerLandingWithAllFields(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)
	upserter := NewMockPlayerUpserter()

	playerID := nhl.PlayerID(8476453)

	mockRedis.ExpectHGetAll(YahooIDPoolKey).SetVal(map[string]string{})

	landingFile := store.PlayerLandingFile{PlayerID: playerID}
	missingFile := store.MissingPlayerLandingFile{PlayerID: playerID}

	fs.On("Exists", missingFile).Return(false)
	fs.On("Exists", landingFile).Return(true)

	// Full landing with all optional fields
	teamID := nhl.TeamID(22)
	sweaterNumber := 97
	teamAbbrev := "EDM"
	heroImage := "https://example.com/hero.jpg"
	playerSlug := "connor-mcdavid-8476453"
	birthCity := nhl.LocalizedString{Default: "Richmond Hill"}
	birthProvince := nhl.LocalizedString{Default: "ON"}
	birthCountry := "CAN"

	landing := &nhl.PlayerLanding{
		PlayerID:           playerID,
		FirstName:          nhl.LocalizedString{Default: " Connor "}, // With spaces to test trimming
		LastName:           nhl.LocalizedString{Default: " McDavid "},
		Position:           "C",
		ShootsCatches:      "L",
		HeightInInches:     73,
		WeightInPounds:     193,
		IsActive:           true,
		Headshot:           "https://example.com/headshot.jpg",
		CurrentTeamID:      &teamID,
		CurrentTeamAbbrev:  &teamAbbrev,
		SweaterNumber:      &sweaterNumber,
		BirthDate:          "1997-01-13",
		BirthCity:          &birthCity,
		BirthStateProvince: &birthProvince,
		BirthCountry:       &birthCountry,
		HeroImage:          &heroImage,
		PlayerSlug:         &playerSlug,
		DraftDetails: &nhl.DraftDetails{
			Year:        2015,
			TeamAbbrev:  "EDM",
			Round:       1,
			PickInRound: 1,
			OverallPick: 1,
		},
	}
	landingJSON, _ := json.Marshal(landing)
	fs.On("Read", landingFile).Return(landingJSON, nil)

	// Capture the upsert params to verify all fields
	var capturedParams sqlcdb.UpsertPlayerParams
	upserter.On("UpsertPlayer", ctx, mock.AnythingOfType("sqlcdb.UpsertPlayerParams")).
		Run(func(args mock.Arguments) {
			capturedParams = args.Get(1).(sqlcdb.UpsertPlayerParams)
		}).
		Return(nil)

	deps := processDeps{
		fs:        fs,
		nhlClient: client,
		redis:     redisClient,
		queries:   upserter,
	}

	players := []BoxscorePlayer{{ID: int64(playerID)}}
	result, err := processPlayerBatchImpl(ctx, deps, players)

	require.NoError(t, err)
	assert.Equal(t, 1, result.Imported)
	assert.Empty(t, result.Errors)

	// Verify all fields were set correctly
	assert.Equal(t, "Connor", capturedParams.FirstName)        // Trimmed
	assert.Equal(t, "McDavid", capturedParams.LastName)        // Trimmed
	assert.Equal(t, "connor", capturedParams.FirstNameNormalized)
	assert.Equal(t, "mcdavid", capturedParams.LastNameNormalized)
	assert.Equal(t, "C", capturedParams.Position)
	assert.Equal(t, "L", capturedParams.ShootsCatches)
	assert.Equal(t, int32(73), capturedParams.HeightInches.Int32)
	assert.Equal(t, int32(193), capturedParams.WeightPounds.Int32)
	assert.True(t, capturedParams.IsActive)
	assert.Equal(t, int64(22), capturedParams.TeamID.Int64)
	assert.Equal(t, int32(97), capturedParams.SweaterNumber.Int32)
	assert.Equal(t, "Richmond Hill", capturedParams.BirthCity.String)
	assert.Equal(t, "ON", capturedParams.BirthStateProvince.String)
	assert.Equal(t, "CAN", capturedParams.BirthCountry.String)
	assert.Equal(t, "https://example.com/hero.jpg", capturedParams.HeroImageURL.String)
	assert.Equal(t, "connor-mcdavid-8476453", capturedParams.PlayerSlug.String)
	assert.Equal(t, int32(2015), capturedParams.DraftYear.Int32)
	assert.Equal(t, "EDM", capturedParams.DraftTeamAbbrev.String)
	assert.Equal(t, int32(1), capturedParams.DraftRound.Int32)
	assert.Equal(t, int32(1), capturedParams.DraftPickInRound.Int32)
	assert.Equal(t, int32(1), capturedParams.DraftOverallPick.Int32)
}

// encodeYahooPlayer encodes a YahooPlayer for Redis mock
func encodeYahooPlayer(t *testing.T, player *store.YahooPlayer) []byte {
	t.Helper()
	var buf bytes.Buffer
	err := gob.NewEncoder(&buf).Encode(player)
	require.NoError(t, err)
	return buf.Bytes()
}
