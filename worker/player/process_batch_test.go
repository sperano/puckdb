package player

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"errors"
	"testing"

	"github.com/go-redis/redismock/v8"
	"github.com/go-redis/redis/v8"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/sperano/puckdb/store"
	"github.com/sperano/puckdb/worker/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// newTestGobCache creates a permissive GobCache: Get always misses, Set always succeeds.
func newTestGobCache() *cache.GobCache {
	client, mockClient := redismock.NewClientMock()
	mockClient.MatchExpectationsInOrder(false)
	keyPattern := core.RedisResourceKeyPrefix + ".*"
	mockClient.Regexp().ExpectGet(keyPattern).SetErr(redis.Nil)
	mockClient.Regexp().CustomMatch(anyArgs).ExpectSet(keyPattern, "x", cache.GobCacheTTL).SetVal("OK")
	return cache.NewGobCache(client)
}

// newTestProcessActivities creates an Activities with all dependencies needed for ProcessPlayerBatch tests.
func newTestProcessActivities(
	mem store.Storage,
	client shared.NHLClient,
	redisClient cache.Client,
	gobCache *cache.GobCache,
	upserter PlayerUpserter,
	careerUpserter PlayerCareerUpserter,
) *Activities {
	return &Activities{
		Storage:       mem,
		NHLClient:     client,
		RedisClient:   redisClient,
		GobCache:      gobCache,
		Queries:       upserter,
		CareerQueries: careerUpserter,
	}
}

func TestProcessPlayerBatch_EmptyPlayers(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	client := &MockNHLClient{}
	redisClient, _ := redismock.NewClientMock()
	upserter := NewMockPlayerUpserter()

	a := newTestProcessActivities(mem, client, redisClient, newTestGobCache(), upserter, nil)
	result, err := a.ProcessPlayerBatch(ctx, []store.BoxscorePlayer{})

	require.NoError(t, err)
	assert.Equal(t, 0, result.Downloaded)
	assert.Equal(t, 0, result.Origins[core.OriginFileSystem])
	assert.Equal(t, 0, result.Missing)
	assert.Equal(t, 0, result.Imported)
	assert.Equal(t, 0, result.Matched)
	assert.Empty(t, result.Errors)
}

func TestProcessPlayerBatch_LoadYahooPoolError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	client := &MockNHLClient{}
	redisClient, mockRedis := redismock.NewClientMock()
	upserter := NewMockPlayerUpserter()

	// Redis HGetAll fails
	mockRedis.ExpectHGetAll(YahooIDPoolKey).SetErr(errors.New("redis connection refused"))

	a := newTestProcessActivities(mem, client, redisClient, newTestGobCache(), upserter, nil)
	players := []store.BoxscorePlayer{{ID: 8476453}}
	result, err := a.ProcessPlayerBatch(ctx, players)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "load yahoo pool")
	assert.Equal(t, 0, result.Imported)
	assert.Equal(t, 0, result.Downloaded)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestProcessPlayerBatch_ContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	mem := store.NewMemStorage()
	client := &MockNHLClient{}
	redisClient, mockRedis := redismock.NewClientMock()
	upserter := NewMockPlayerUpserter()

	// Redis HGetAll succeeds
	mockRedis.ExpectHGetAll(YahooIDPoolKey).SetVal(map[string]string{})

	a := newTestProcessActivities(mem, client, redisClient, newTestGobCache(), upserter, nil)

	// Cancel context before processing
	cancel()

	players := []store.BoxscorePlayer{{ID: 8476453}}
	result, err := a.ProcessPlayerBatch(ctx, players)

	require.Error(t, err)
	assert.Equal(t, context.Canceled, err)
	assert.Equal(t, 0, result.Imported)
}

func TestProcessPlayerBatch_CacheHitAndImport(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	client := &MockNHLClient{}
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)
	upserter := NewMockPlayerUpserter()

	playerID := nhl.PlayerID(8476453)

	// Redis HGetAll succeeds (empty pool - no Yahoo matching)
	mockRedis.ExpectHGetAll(YahooIDPoolKey).SetVal(map[string]string{})

	// Player landing file already exists (cache hit)
	landing := &nhl.PlayerLanding{
		PlayerID:  playerID,
		FirstName: nhl.LocalizedString{Default: "Connor"},
		LastName:  nhl.LocalizedString{Default: "McDavid"},
		Position:  "C",
		IsActive:  true,
	}
	landingJSON, err := json.Marshal(landing)
	require.NoError(t, err)
	require.NoError(t, mem.Write(resource.PlayerLanding{PlayerID: playerID}.Path(), landingJSON))

	// Expect UpsertPlayer call
	upserter.On("UpsertPlayer", ctx, mock.AnythingOfType("sqlcdb.UpsertPlayerParams")).Return(nil)

	a := newTestProcessActivities(mem, client, redisClient, newTestGobCache(), upserter, nil)
	players := []store.BoxscorePlayer{{ID: int64(playerID)}}
	result, err := a.ProcessPlayerBatch(ctx, players)

	require.NoError(t, err)
	assert.Equal(t, 0, result.Downloaded)
	assert.Equal(t, 1, result.Origins[core.OriginFileSystem])
	assert.Equal(t, 0, result.Missing)
	assert.Equal(t, 1, result.Imported)
	assert.Equal(t, 0, result.Matched) // No Yahoo pool entries
	assert.Empty(t, result.Errors)
	upserter.AssertExpectations(t)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestProcessPlayerBatch_MissingPlayer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	client := &MockNHLClient{}
	redisClient, mockRedis := redismock.NewClientMock()
	upserter := NewMockPlayerUpserter()

	playerID := nhl.PlayerID(9999999)

	// Redis HGetAll succeeds
	mockRedis.ExpectHGetAll(YahooIDPoolKey).SetVal(map[string]string{})

	// Mark player as missing (player marked as 404)
	missingData, _ := json.Marshal(store.MissingPlayerLandingData{
		FirstName: "John",
		LastName:  "Doe",
		Position:  "C",
	})
	require.NoError(t, mem.Write(resource.MissingPlayerLanding{PlayerID: playerID}.Path(), missingData))

	// Expect UpsertPlayer call with minimal info from boxscore data
	upserter.On("UpsertPlayer", ctx, mock.AnythingOfType("sqlcdb.UpsertPlayerParams")).Return(nil)

	a := newTestProcessActivities(mem, client, redisClient, newTestGobCache(), upserter, nil)

	// BoxscorePlayer contains the minimal player info
	players := []store.BoxscorePlayer{{
		ID:        int64(playerID),
		FirstName: "John",
		LastName:  "Doe",
		Position:  "C",
	}}
	result, err := a.ProcessPlayerBatch(ctx, players)

	require.NoError(t, err)
	assert.Equal(t, 0, result.Downloaded)
	assert.Equal(t, 0, result.Origins[core.OriginFileSystem])
	assert.Equal(t, 1, result.Missing)
	assert.Equal(t, 1, result.Imported) // Missing players ARE imported with minimal info
	assert.Empty(t, result.Errors)
	upserter.AssertExpectations(t)
}

func TestProcessPlayerBatch_UpsertError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	client := &MockNHLClient{}
	redisClient, mockRedis := redismock.NewClientMock()
	upserter := NewMockPlayerUpserter()

	playerID := nhl.PlayerID(8476453)

	// Redis HGetAll succeeds
	mockRedis.ExpectHGetAll(YahooIDPoolKey).SetVal(map[string]string{})

	// Player landing file exists
	landing := &nhl.PlayerLanding{
		PlayerID:  playerID,
		FirstName: nhl.LocalizedString{Default: "Connor"},
		LastName:  nhl.LocalizedString{Default: "McDavid"},
		Position:  "C",
		IsActive:  true,
	}
	landingJSON, err := json.Marshal(landing)
	require.NoError(t, err)
	require.NoError(t, mem.Write(resource.PlayerLanding{PlayerID: playerID}.Path(), landingJSON))

	// UpsertPlayer fails
	upserter.On("UpsertPlayer", ctx, mock.AnythingOfType("sqlcdb.UpsertPlayerParams")).
		Return(errors.New("database connection lost"))

	a := newTestProcessActivities(mem, client, redisClient, newTestGobCache(), upserter, nil)
	players := []store.BoxscorePlayer{{ID: int64(playerID)}}
	result, err := a.ProcessPlayerBatch(ctx, players)

	// No error returned - errors are collected in result.Errors
	require.NoError(t, err)
	assert.Equal(t, 1, result.Origins[core.OriginFileSystem])
	assert.Equal(t, 0, result.Imported) // Failed to import
	assert.Len(t, result.Errors, 1)
	assert.Contains(t, result.Errors[0], "upsert error")
	upserter.AssertExpectations(t)
}

func TestProcessPlayerBatch_FileReadError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	client := &MockNHLClient{}
	redisClient, mockRedis := redismock.NewClientMock()
	upserter := NewMockPlayerUpserter()

	playerID := nhl.PlayerID(8476453)

	// Redis HGetAll succeeds
	mockRedis.ExpectHGetAll(YahooIDPoolKey).SetVal(map[string]string{})

	// Player landing file exists but contains invalid JSON (will cause read error in GetLanding)
	landingRes := resource.PlayerLanding{PlayerID: playerID}
	mem.SetFile(landingRes.Path(), []byte("invalid json"))

	a := newTestProcessActivities(mem, client, redisClient, newTestGobCache(), upserter, nil)
	players := []store.BoxscorePlayer{{ID: int64(playerID)}}
	result, err := a.ProcessPlayerBatch(ctx, players)

	// No error returned - errors are collected in result.Errors
	require.NoError(t, err)
	assert.Equal(t, 0, result.Origins[core.OriginFileSystem]) // No origin recorded for failed reads
	assert.Equal(t, 0, result.Imported)
	assert.Len(t, result.Errors, 1)
	assert.Contains(t, result.Errors[0], "read error")
	upserter.AssertNotCalled(t, "UpsertPlayer")
}

func TestProcessPlayerBatch_DownloadAndImport(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	client := &MockNHLClient{}
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)
	upserter := NewMockPlayerUpserter()

	playerID := nhl.PlayerID(8476453)

	// Redis HGetAll succeeds (empty pool - no Yahoo matching)
	mockRedis.ExpectHGetAll(YahooIDPoolKey).SetVal(map[string]string{})

	// No landing file exists - needs download
	// Client downloads the player landing
	landing := &nhl.PlayerLanding{
		PlayerID:  playerID,
		FirstName: nhl.LocalizedString{Default: "Connor"},
		LastName:  nhl.LocalizedString{Default: "McDavid"},
		Position:  "C",
		IsActive:  true,
	}
	client.On("PlayerLanding", ctx, playerID).Return(landing, nil)

	// Expect UpsertPlayer call
	upserter.On("UpsertPlayer", ctx, mock.AnythingOfType("sqlcdb.UpsertPlayerParams")).Return(nil)

	a := newTestProcessActivities(mem, client, redisClient, newTestGobCache(), upserter, nil)
	players := []store.BoxscorePlayer{{ID: int64(playerID)}}
	result, err := a.ProcessPlayerBatch(ctx, players)

	require.NoError(t, err)
	assert.Equal(t, 1, result.Downloaded)
	assert.Equal(t, 0, result.Origins[core.OriginFileSystem])
	assert.Equal(t, 0, result.Missing)
	assert.Equal(t, 1, result.Imported)
	assert.Equal(t, 0, result.Matched) // No Yahoo pool entries
	assert.Empty(t, result.Errors)
	client.AssertExpectations(t)
	upserter.AssertExpectations(t)

	// Verify landing was saved
	assert.True(t, mem.Exists(resource.PlayerLanding{PlayerID: playerID}.Path()))
}

func TestProcessPlayerBatch_DownloadAPIError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	client := &MockNHLClient{}
	redisClient, mockRedis := redismock.NewClientMock()
	upserter := NewMockPlayerUpserter()

	playerID := nhl.PlayerID(8476453)

	// Redis HGetAll succeeds
	mockRedis.ExpectHGetAll(YahooIDPoolKey).SetVal(map[string]string{})

	// API returns non-404 error
	client.On("PlayerLanding", ctx, playerID).Return(nil, errors.New("API timeout"))

	a := newTestProcessActivities(mem, client, redisClient, newTestGobCache(), upserter, nil)
	players := []store.BoxscorePlayer{{ID: int64(playerID)}}
	result, err := a.ProcessPlayerBatch(ctx, players)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "API timeout")
	assert.Equal(t, 0, result.Imported)
	client.AssertExpectations(t)
}

func TestProcessPlayerBatch_YahooMatchWithClearConflict(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
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

	// File exists (cache hit) - with team info for matching
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
	landingJSON, err := json.Marshal(landing)
	require.NoError(t, err)
	require.NoError(t, mem.Write(resource.PlayerLanding{PlayerID: playerID}.Path(), landingJSON))

	// Expect ClearConflictingYahooID call
	upserter.On("ClearConflictingYahooID", ctx, mock.AnythingOfType("sqlcdb.ClearConflictingYahooIDParams")).Return(nil)

	// Expect UpsertPlayer call
	upserter.On("UpsertPlayer", ctx, mock.AnythingOfType("sqlcdb.UpsertPlayerParams")).Return(nil)

	a := newTestProcessActivities(mem, client, redisClient, newTestGobCache(), upserter, nil)
	players := []store.BoxscorePlayer{{ID: int64(playerID)}}
	result, err := a.ProcessPlayerBatch(ctx, players)

	require.NoError(t, err)
	assert.Equal(t, 1, result.Origins[core.OriginFileSystem])
	assert.Equal(t, 1, result.Imported)
	assert.Equal(t, 1, result.Matched) // Yahoo ID matched
	assert.Empty(t, result.Errors)
	upserter.AssertExpectations(t)
}

func TestProcessPlayerBatch_ClearConflictingYahooIDError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
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

	landing := &nhl.PlayerLanding{
		PlayerID:  playerID,
		FirstName: nhl.LocalizedString{Default: "Connor"},
		LastName:  nhl.LocalizedString{Default: "McDavid"},
		Position:  "C",
	}
	landingJSON, err := json.Marshal(landing)
	require.NoError(t, err)
	require.NoError(t, mem.Write(resource.PlayerLanding{PlayerID: playerID}.Path(), landingJSON))

	// ClearConflictingYahooID fails - should log warning but continue
	upserter.On("ClearConflictingYahooID", ctx, mock.AnythingOfType("sqlcdb.ClearConflictingYahooIDParams")).
		Return(errors.New("db error"))

	// UpsertPlayer should still be called
	upserter.On("UpsertPlayer", ctx, mock.AnythingOfType("sqlcdb.UpsertPlayerParams")).Return(nil)

	a := newTestProcessActivities(mem, client, redisClient, newTestGobCache(), upserter, nil)
	players := []store.BoxscorePlayer{{ID: int64(playerID)}}
	result, err := a.ProcessPlayerBatch(ctx, players)

	// Should succeed despite ClearConflictingYahooID error
	require.NoError(t, err)
	assert.Equal(t, 1, result.Imported)
	assert.Equal(t, 1, result.Matched)
	upserter.AssertExpectations(t)
}

func TestProcessPlayerBatch_UpsertErrorWithYahooID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
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

	landing := &nhl.PlayerLanding{
		PlayerID:  playerID,
		FirstName: nhl.LocalizedString{Default: "Connor"},
		LastName:  nhl.LocalizedString{Default: "McDavid"},
		Position:  "C",
	}
	landingJSON, err := json.Marshal(landing)
	require.NoError(t, err)
	require.NoError(t, mem.Write(resource.PlayerLanding{PlayerID: playerID}.Path(), landingJSON))

	upserter.On("ClearConflictingYahooID", ctx, mock.AnythingOfType("sqlcdb.ClearConflictingYahooIDParams")).Return(nil)

	// UpsertPlayer fails
	upserter.On("UpsertPlayer", ctx, mock.AnythingOfType("sqlcdb.UpsertPlayerParams")).
		Return(errors.New("constraint violation"))

	a := newTestProcessActivities(mem, client, redisClient, newTestGobCache(), upserter, nil)
	players := []store.BoxscorePlayer{{ID: int64(playerID)}}
	result, err := a.ProcessPlayerBatch(ctx, players)

	require.NoError(t, err)
	assert.Equal(t, 0, result.Imported)
	assert.Len(t, result.Errors, 1)
	// Error message should include Yahoo ID
	assert.Contains(t, result.Errors[0], "yahoo_id=12345")
	assert.Contains(t, result.Errors[0], "upsert error")
}

func TestProcessPlayerBatch_FullPlayerLandingWithAllFields(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	client := &MockNHLClient{}
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)
	upserter := NewMockPlayerUpserter()

	playerID := nhl.PlayerID(8476453)

	mockRedis.ExpectHGetAll(YahooIDPoolKey).SetVal(map[string]string{})

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
	landingJSON, err := json.Marshal(landing)
	require.NoError(t, err)
	require.NoError(t, mem.Write(resource.PlayerLanding{PlayerID: playerID}.Path(), landingJSON))

	// Capture the upsert params to verify all fields
	var capturedParams sqlcdb.UpsertPlayerParams
	upserter.On("UpsertPlayer", ctx, mock.AnythingOfType("sqlcdb.UpsertPlayerParams")).
		Run(func(args mock.Arguments) {
			capturedParams = args.Get(1).(sqlcdb.UpsertPlayerParams)
		}).
		Return(nil)

	a := newTestProcessActivities(mem, client, redisClient, newTestGobCache(), upserter, nil)
	players := []store.BoxscorePlayer{{ID: int64(playerID)}}
	result, err := a.ProcessPlayerBatch(ctx, players)

	require.NoError(t, err)
	assert.Equal(t, 1, result.Imported)
	assert.Empty(t, result.Errors)

	// Verify all fields were set correctly
	assert.Equal(t, "Connor", capturedParams.FirstName)  // Trimmed
	assert.Equal(t, "McDavid", capturedParams.LastName) // Trimmed
	assert.Equal(t, "connor", capturedParams.FirstNameNormalized)
	assert.Equal(t, "mcdavid", capturedParams.LastNameNormalized)
	assert.Equal(t, sqlcdb.NullPlayerPosition{PlayerPosition: "C", Valid: true}, capturedParams.Position)
	assert.Equal(t, sqlcdb.NullHandSide{HandSide: "L", Valid: true}, capturedParams.ShootsCatches)
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

func TestProcessPlayerBatch_CareerDataUpserted(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := store.NewMemStorage()
	client := &MockNHLClient{}
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)
	upserter := NewMockPlayerUpserter()
	careerUpserter := &MockPlayerCareerUpserter{}

	playerID := nhl.PlayerID(8476453)

	mockRedis.ExpectHGetAll(YahooIDPoolKey).SetVal(map[string]string{})

	goals := 92
	assists := 100
	points := 192
	pm := 10
	pim := 32
	seq := 1

	landing := &nhl.PlayerLanding{
		PlayerID:  playerID,
		FirstName: nhl.LocalizedString{Default: "Connor"},
		LastName:  nhl.LocalizedString{Default: "McDavid"},
		Position:  "C",
		IsActive:  true,
		Awards: []nhl.Award{
			{
				Trophy:  nhl.LocalizedString{Default: "Hart Trophy"},
				Seasons: []nhl.AwardSeason{{SeasonID: nhl.NewSeason(2023)}},
			},
		},
		SeasonTotals: []nhl.SeasonTotal{
			{
				Season:       nhl.NewSeason(2023),
				GameType:     nhl.GameTypeRegularSeason,
				LeagueAbbrev: "NHL",
				TeamName:     nhl.LocalizedString{Default: "Edmonton Oilers"},
				Sequence:     &seq,
				GamesPlayed:  82,
				Goals:        &goals,
				Assists:      &assists,
				Points:       &points,
				PlusMinus:    &pm,
				PIM:          &pim,
			},
		},
	}
	landingJSON, err := json.Marshal(landing)
	require.NoError(t, err)
	require.NoError(t, mem.Write(resource.PlayerLanding{PlayerID: playerID}.Path(), landingJSON))

	upserter.On("UpsertPlayer", ctx, mock.AnythingOfType("sqlcdb.UpsertPlayerParams")).Return(nil)

	careerUpserter.On("UpsertPlayerAwardBatch", ctx, mock.AnythingOfType("[]sqlcdb.UpsertPlayerAwardBatchParams")).
		Return(sqlcdb.NewUpsertPlayerAwardBatchBatchResults(&mockBatchResults{count: 1}, 1))
	careerUpserter.On("UpsertPlayerSeasonTotalBatch", ctx, mock.AnythingOfType("[]sqlcdb.UpsertPlayerSeasonTotalBatchParams")).
		Return(sqlcdb.NewUpsertPlayerSeasonTotalBatchBatchResults(&mockBatchResults{count: 1}, 1))

	a := newTestProcessActivities(mem, client, redisClient, newTestGobCache(), upserter, careerUpserter)
	players := []store.BoxscorePlayer{{ID: int64(playerID)}}
	result, err := a.ProcessPlayerBatch(ctx, players)

	require.NoError(t, err)
	assert.Equal(t, 1, result.Imported)
	assert.Equal(t, 1, result.AwardsImported)
	assert.Equal(t, 1, result.TotalsImported)
	assert.Empty(t, result.Errors)
	upserter.AssertExpectations(t)
	careerUpserter.AssertExpectations(t)
}

// encodeYahooPlayer encodes a YahooPlayer for Redis mock
func encodeYahooPlayer(t *testing.T, player *store.YahooPlayer) []byte {
	t.Helper()
	var buf bytes.Buffer
	err := gob.NewEncoder(&buf).Encode(player)
	require.NoError(t, err)
	return buf.Bytes()
}
