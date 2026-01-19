package worker

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"testing"

	goredis "github.com/go-redis/redis/v8"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/redis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestPartialToSqlcPlayer_WithoutLanding(t *testing.T) {
	partial := PartialPlayer{
		ID:               12345,
		FirstName:        "John",
		LastName:         "Doe",
		Position:         "C",
		SweaterNumber:    91,
		YahooImageSmall:  "https://yahoo.com/small.jpg",
		YahooImageMedium: "https://yahoo.com/medium.jpg",
		YahooImageLarge:  "https://yahoo.com/large.jpg",
		YahooHomeURL:     "https://yahoo.com/player/12345",
	}

	player := partialToSqlcPlayer(partial, nil)

	assert.Equal(t, int64(12345), player.ID)
	assert.Equal(t, int64(12345), player.YahooID.Int64)
	assert.True(t, player.YahooID.Valid)
	assert.Equal(t, "John", player.FirstName)
	assert.Equal(t, "Doe", player.LastName)
	assert.Equal(t, "C", player.Position)
	assert.Equal(t, int32(91), player.SweaterNumber.Int32)
	assert.True(t, player.SweaterNumber.Valid)
	assert.False(t, player.NhlTeamID.Valid) // No team without landing
	assert.Equal(t, "https://yahoo.com/small.jpg", player.YahooImageSmall)
	assert.Equal(t, "https://yahoo.com/medium.jpg", player.YahooImageMedium)
	assert.Equal(t, "https://yahoo.com/large.jpg", player.YahooImageLarge)
	assert.Equal(t, "https://yahoo.com/player/12345", player.YahooHomeUrl)
	assert.False(t, player.IsActive)
	assert.Empty(t, player.ShootsCatches)
	assert.Empty(t, player.HeadshotUrl)
}

func TestPartialToSqlcPlayer_WithLanding(t *testing.T) {
	partial := PartialPlayer{
		ID:            12345,
		FirstName:     "John",
		LastName:      "Doe",
		Position:      "C",
		SweaterNumber: 91,
	}

	sweaterNum := 91
	teamID := nhl.TeamID(10)
	heroImage := "https://nhl.com/hero.jpg"
	slug := "john-doe-12345"
	birthCity := nhl.LocalizedString{Default: "Toronto"}
	birthProvince := nhl.LocalizedString{Default: "Ontario"}
	birthCountry := "CAN"

	landing := &nhl.PlayerLanding{
		FirstName:          nhl.LocalizedString{Default: "Jonathan"},
		LastName:           nhl.LocalizedString{Default: "Doe"},
		Position:           nhl.Position("C"),
		ShootsCatches:      nhl.Handedness("L"),
		HeightInInches:     73,
		WeightInPounds:     195,
		IsActive:           true,
		Headshot:           "https://nhl.com/headshot.jpg",
		SweaterNumber:      &sweaterNum,
		CurrentTeamID:      &teamID,
		HeroImage:          &heroImage,
		PlayerSlug:         &slug,
		BirthDate:          "1990-05-15",
		BirthCity:          &birthCity,
		BirthStateProvince: &birthProvince,
		BirthCountry:       &birthCountry,
		DraftDetails: &nhl.DraftDetails{
			Year:        2008,
			TeamAbbrev:  "TOR",
			Round:       1,
			PickInRound: 5,
			OverallPick: 5,
		},
	}

	player := partialToSqlcPlayer(partial, landing)

	// NHL data should override partial data
	assert.Equal(t, "Jonathan", player.FirstName)
	assert.Equal(t, "Doe", player.LastName)
	assert.Equal(t, "C", player.Position)
	assert.Equal(t, "L", player.ShootsCatches)
	assert.Equal(t, int32(73), player.HeightInches.Int32)
	assert.Equal(t, int32(195), player.WeightPounds.Int32)
	assert.True(t, player.IsActive)
	assert.Equal(t, "https://nhl.com/headshot.jpg", player.HeadshotUrl)
	assert.Equal(t, int32(91), player.SweaterNumber.Int32)
	assert.Equal(t, int64(10), player.NhlTeamID.Int64)
	assert.True(t, player.NhlTeamID.Valid)
	assert.Equal(t, "https://nhl.com/hero.jpg", player.HeroImageUrl.String)
	assert.Equal(t, "john-doe-12345", player.PlayerSlug.String)

	// Birth info
	assert.True(t, player.BirthDate.Valid)
	assert.Equal(t, "Toronto", player.BirthCity.String)
	assert.Equal(t, "Ontario", player.BirthStateProvince.String)
	assert.Equal(t, "CAN", player.BirthCountry.String)

	// Draft details
	assert.Equal(t, int32(2008), player.DraftYear.Int32)
	assert.Equal(t, "TOR", player.DraftTeamAbbrev.String)
	assert.Equal(t, int32(1), player.DraftRound.Int32)
	assert.Equal(t, int32(5), player.DraftPickInRound.Int32)
	assert.Equal(t, int32(5), player.DraftOverallPick.Int32)
}

func TestPartialToSqlcPlayer_InactivePlayer_NoTeam(t *testing.T) {
	partial := PartialPlayer{
		ID:        12345,
		FirstName: "Retired",
		LastName:  "Player",
	}

	// Player with no current team (nil CurrentTeamID)
	landing := &nhl.PlayerLanding{
		FirstName:     nhl.LocalizedString{Default: "Retired"},
		LastName:      nhl.LocalizedString{Default: "Player"},
		Position:      nhl.Position("C"),
		ShootsCatches: nhl.Handedness("R"),
		IsActive:      false,
		CurrentTeamID: nil, // Not on a team
		BirthDate:     "1985-01-01",
	}

	player := partialToSqlcPlayer(partial, landing)

	assert.False(t, player.NhlTeamID.Valid)
	assert.False(t, player.IsActive)
}

func TestGetPlayerLandingWithCache_CacheHit(t *testing.T) {
	ctx := context.Background()
	mockFS := &MockFileSystem{}
	mockNHL := &MockNHLClient{}

	playerID := nhl.PlayerID(8476453)
	mockFile := MockFile{DirVal: "players", NameVal: "8476453", ExtVal: "json"}

	// Setup cache hit with valid JSON
	cachedData := []byte(`{
		"playerId": 8476453,
		"isActive": true,
		"firstName": {"default": "Connor"},
		"lastName": {"default": "McDavid"},
		"position": "C",
		"shootsCatches": "L",
		"headshot": "https://example.com/headshot.jpg",
		"heightInInches": 73,
		"weightInPounds": 193,
		"birthDate": "1997-01-13"
	}`)

	mockFS.On("New", mock.Anything, mock.Anything).Return(mockFile)
	mockFS.On("Exists", mockFile).Return(true)
	mockFS.On("Read", mockFile).Return(cachedData, nil)

	result, fromCache, err := getPlayerLandingWithCache(ctx, mockFS, mockNHL, playerID)

	require.NoError(t, err)
	assert.True(t, fromCache)
	assert.Equal(t, "Connor", result.FirstName.Default)
	assert.Equal(t, "McDavid", result.LastName.Default)

	// Verify NHL API was NOT called
	mockNHL.AssertNotCalled(t, "PlayerLanding")
}

func TestGetPlayerLandingWithCache_CacheMiss(t *testing.T) {
	ctx := context.Background()
	mockFS := &MockFileSystem{}
	mockNHL := &MockNHLClient{}

	playerID := nhl.PlayerID(8476453)
	mockFile := MockFile{DirVal: "players", NameVal: "8476453", ExtVal: "json"}

	// Setup cache miss
	mockFS.On("New", mock.Anything, mock.Anything).Return(mockFile)
	mockFS.On("Exists", mockFile).Return(false)
	mockFS.On("Write", mockFile, mock.Anything).Return(nil)

	// Setup API response with all required fields for valid JSON marshaling
	landing := &nhl.PlayerLanding{
		FirstName:      nhl.LocalizedString{Default: "Connor"},
		LastName:       nhl.LocalizedString{Default: "McDavid"},
		Position:       nhl.Position("C"),
		ShootsCatches:  nhl.Handedness("L"),
		HeightInInches: 73,
		WeightInPounds: 193,
		BirthDate:      "1997-01-13",
		IsActive:       true,
	}
	mockNHL.On("PlayerLanding", ctx, playerID).Return(landing, nil)

	result, fromCache, err := getPlayerLandingWithCache(ctx, mockFS, mockNHL, playerID)

	require.NoError(t, err)
	assert.False(t, fromCache)
	assert.Equal(t, "Connor", result.FirstName.Default)
	assert.Equal(t, "McDavid", result.LastName.Default)

	// Verify API was called and result was cached
	mockNHL.AssertCalled(t, "PlayerLanding", ctx, playerID)
	mockFS.AssertCalled(t, "Write", mockFile, mock.Anything)
}

func TestGetPlayerLandingWithCache_APIError(t *testing.T) {
	ctx := context.Background()
	mockFS := &MockFileSystem{}
	mockNHL := &MockNHLClient{}

	playerID := nhl.PlayerID(8476453)
	mockFile := MockFile{DirVal: "players", NameVal: "8476453", ExtVal: "json"}

	// Setup cache miss
	mockFS.On("New", mock.Anything, mock.Anything).Return(mockFile)
	mockFS.On("Exists", mockFile).Return(false)

	// Setup API error
	mockNHL.On("PlayerLanding", ctx, playerID).Return(nil, errors.New("API error"))

	result, fromCache, err := getPlayerLandingWithCache(ctx, mockFS, mockNHL, playerID)

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.False(t, fromCache)
}

func TestGetPlayerLandingWithCache_CorruptedCache(t *testing.T) {
	ctx := context.Background()
	mockFS := &MockFileSystem{}
	mockNHL := &MockNHLClient{}

	playerID := nhl.PlayerID(8476453)
	mockFile := MockFile{DirVal: "players", NameVal: "8476453", ExtVal: "json"}

	// Setup corrupted cache (invalid JSON)
	mockFS.On("New", mock.Anything, mock.Anything).Return(mockFile)
	mockFS.On("Exists", mockFile).Return(true)
	mockFS.On("Read", mockFile).Return([]byte("not valid json"), nil)
	mockFS.On("Write", mockFile, mock.Anything).Return(nil)

	// Should fall back to API
	landing := &nhl.PlayerLanding{
		FirstName:      nhl.LocalizedString{Default: "Connor"},
		LastName:       nhl.LocalizedString{Default: "McDavid"},
		Position:       nhl.Position("C"),
		ShootsCatches:  nhl.Handedness("L"),
		HeightInInches: 73,
		WeightInPounds: 193,
		BirthDate:      "1997-01-13",
		IsActive:       true,
	}
	mockNHL.On("PlayerLanding", ctx, playerID).Return(landing, nil)

	result, fromCache, err := getPlayerLandingWithCache(ctx, mockFS, mockNHL, playerID)

	require.NoError(t, err)
	assert.False(t, fromCache)
	assert.Equal(t, "Connor", result.FirstName.Default)
}

func TestEnrichPlayerBatchImpl_EmptyBatch(t *testing.T) {
	ctx := context.Background()

	deps := EnrichDeps{
		FS:      &MockFileSystem{},
		NHL:     &MockNHLClient{},
		Redis:   &redis.MockClient{},
		Queries: NewMockPlayerUpserter(),
	}

	// Empty batch should return 0 immediately
	count, err := enrichPlayerBatchImpl(ctx, deps, []int64{}, "test-key")

	assert.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestEnrichPlayerBatchImpl_SinglePlayer(t *testing.T) {
	ctx := context.Background()

	// Create partial players map and encode to gob
	partialPlayers := map[int64]PartialPlayer{
		8476453: {
			ID:              8476453,
			FirstName:       "Connor",
			LastName:        "McDavid",
			SweaterNumber:   97,
			YahooImageSmall: "https://yahoo.com/mcdavid.jpg",
		},
	}
	var buf bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buf).Encode(partialPlayers))

	// Setup mocks
	mockFS := &MockFileSystem{}
	mockNHL := &MockNHLClient{}
	mockRedis := &redis.MockClient{}
	mockDB := NewMockPlayerUpserter()

	// Mock Redis Get to return the encoded players
	redisCmd := goredis.NewStringCmd(ctx)
	redisCmd.SetVal(buf.String())
	mockRedis.On("Get", ctx, "test-key").Return(redisCmd)

	// Mock cache miss and API call
	mockFile := MockFile{DirVal: "players", NameVal: "8476453", ExtVal: "json"}
	mockFS.On("New", mock.Anything, mock.Anything).Return(mockFile)
	mockFS.On("Exists", mockFile).Return(false)
	mockFS.On("Write", mockFile, mock.Anything).Return(nil)

	landing := &nhl.PlayerLanding{
		FirstName:      nhl.LocalizedString{Default: "Connor"},
		LastName:       nhl.LocalizedString{Default: "McDavid"},
		Position:       nhl.Position("C"),
		ShootsCatches:  nhl.Handedness("L"),
		HeightInInches: 73,
		WeightInPounds: 193,
		BirthDate:      "1997-01-13",
		IsActive:       true,
	}
	mockNHL.On("PlayerLanding", ctx, nhl.PlayerID(8476453)).Return(landing, nil)

	// Mock database upsert
	mockDB.On("UpsertPlayer", ctx, mock.Anything).Return(nil)

	deps := EnrichDeps{
		FS:      mockFS,
		NHL:     mockNHL,
		Redis:   mockRedis,
		Queries: mockDB,
	}

	count, err := enrichPlayerBatchImpl(ctx, deps, []int64{8476453}, "test-key")

	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// Verify player was upserted
	assert.Len(t, mockDB.UpsertedPlayers, 1)
	upserted := mockDB.UpsertedPlayers[0]
	assert.Equal(t, int64(8476453), upserted.ID)
	assert.Equal(t, "Connor", upserted.FirstName)
	assert.Equal(t, "L", upserted.ShootsCatches)
}
