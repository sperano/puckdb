package worker

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/sperano/yfh/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestPlayerMatchKey(t *testing.T) {
	tests := []struct {
		name          string
		firstName     string
		lastName      string
		sweaterNumber int
		expected      string
	}{
		{
			name:          "normal names",
			firstName:     "Connor",
			lastName:      "McDavid",
			sweaterNumber: 97,
			expected:      "connor|mcdavid|97",
		},
		{
			name:          "mixed case",
			firstName:     "LEON",
			lastName:      "Draisaitl",
			sweaterNumber: 29,
			expected:      "leon|draisaitl|29",
		},
		{
			name:          "with extra spaces",
			firstName:     "  Zach  ",
			lastName:      "  Hyman  ",
			sweaterNumber: 18,
			expected:      "zach|hyman|18",
		},
		{
			name:          "hyphenated name",
			firstName:     "Pierre-Luc",
			lastName:      "Dubois",
			sweaterNumber: 80,
			expected:      "pierre-luc|dubois|80",
		},
		{
			name:          "zero sweater number",
			firstName:     "Test",
			lastName:      "Player",
			sweaterNumber: 0,
			expected:      "test|player|0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := playerMatchKey(tt.firstName, tt.lastName, tt.sweaterNumber)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMergeSeasonPlayersActivity_MatchByNameNumber(t *testing.T) {
	ctx := context.Background()

	yahoo := map[int64]PartialPlayer{
		// Yahoo uses different IDs
		1001: {
			ID:               1001,
			FirstName:        "Connor",
			LastName:         "McDavid",
			SweaterNumber:    97,
			YahooImageSmall:  "https://yahoo.com/small/mcdavid.jpg",
			YahooImageMedium: "https://yahoo.com/medium/mcdavid.jpg",
			YahooImageLarge:  "https://yahoo.com/large/mcdavid.jpg",
			YahooHomeURL:     "https://yahoo.com/player/mcdavid",
			HasYahooData:     true,
		},
		1002: {
			ID:               1002,
			FirstName:        "Leon",
			LastName:         "Draisaitl",
			SweaterNumber:    29,
			YahooImageSmall:  "https://yahoo.com/small/drai.jpg",
			YahooImageMedium: "https://yahoo.com/medium/drai.jpg",
			YahooImageLarge:  "https://yahoo.com/large/drai.jpg",
			YahooHomeURL:     "https://yahoo.com/player/drai",
			HasYahooData:     true,
		},
	}

	boxscore := map[int64]PartialPlayer{
		// NHL API uses real player IDs
		8476453: {
			ID:              8476453,
			FirstName:       "Connor",
			LastName:        "McDavid",
			SweaterNumber:   97,
			Position:        "C",
			HasBoxscoreData: true,
		},
		8477934: {
			ID:              8477934,
			FirstName:       "Leon",
			LastName:        "Draisaitl",
			SweaterNumber:   29,
			Position:        "C",
			HasBoxscoreData: true,
		},
		8478402: {
			ID:              8478402,
			FirstName:       "Zach",
			LastName:        "Hyman",
			SweaterNumber:   18,
			Position:        "LW",
			HasBoxscoreData: true,
		},
	}

	result, err := MergeSeasonPlayersActivity(ctx, yahoo, boxscore)

	assert.NoError(t, err)
	assert.Len(t, result, 3)

	// McDavid should be merged with Yahoo data
	mcdavid := result[8476453]
	assert.Equal(t, "Connor", mcdavid.FirstName)
	assert.Equal(t, "https://yahoo.com/small/mcdavid.jpg", mcdavid.YahooImageSmall)
	assert.True(t, mcdavid.HasYahooData)

	// Draisaitl should be merged with Yahoo data
	drai := result[8477934]
	assert.Equal(t, "Leon", drai.FirstName)
	assert.Equal(t, "https://yahoo.com/small/drai.jpg", drai.YahooImageSmall)
	assert.True(t, drai.HasYahooData)

	// Hyman has no Yahoo match (not in Yahoo map)
	hyman := result[8478402]
	assert.Equal(t, "Zach", hyman.FirstName)
	assert.Empty(t, hyman.YahooImageSmall)
	assert.False(t, hyman.HasYahooData)
}

func TestMergeSeasonPlayersActivity_CaseInsensitive(t *testing.T) {
	ctx := context.Background()

	yahoo := map[int64]PartialPlayer{
		1001: {
			ID:              1001,
			FirstName:       "CONNOR", // uppercase
			LastName:        "MCDAVID",
			SweaterNumber:   97,
			YahooImageSmall: "https://yahoo.com/mcdavid.jpg",
			HasYahooData:    true,
		},
	}

	boxscore := map[int64]PartialPlayer{
		8476453: {
			ID:              8476453,
			FirstName:       "Connor", // proper case
			LastName:        "McDavid",
			SweaterNumber:   97,
			HasBoxscoreData: true,
		},
	}

	result, err := MergeSeasonPlayersActivity(ctx, yahoo, boxscore)

	assert.NoError(t, err)
	assert.Len(t, result, 1)

	// Should match despite case difference
	player := result[8476453]
	assert.Equal(t, "https://yahoo.com/mcdavid.jpg", player.YahooImageSmall)
	assert.True(t, player.HasYahooData)
}

func TestMergeSeasonPlayersActivity_EmptyInputs(t *testing.T) {
	ctx := context.Background()

	// Empty Yahoo, some boxscore
	result1, err := MergeSeasonPlayersActivity(ctx, nil, map[int64]PartialPlayer{
		123: {ID: 123, FirstName: "Test"},
	})
	assert.NoError(t, err)
	assert.Len(t, result1, 1)

	// Some Yahoo, empty boxscore
	result2, err := MergeSeasonPlayersActivity(ctx, map[int64]PartialPlayer{
		456: {ID: 456, FirstName: "Test"},
	}, nil)
	assert.NoError(t, err)
	assert.Len(t, result2, 0) // Result is based on boxscore

	// Both empty
	result3, err := MergeSeasonPlayersActivity(ctx, nil, nil)
	assert.NoError(t, err)
	assert.Len(t, result3, 0)
}

func TestMergeAllSeasonsActivity(t *testing.T) {
	ctx := context.Background()

	season1 := map[int64]PartialPlayer{
		8476453: {
			ID:              8476453,
			FirstName:       "Connor",
			LastName:        "McDavid",
			HasBoxscoreData: true,
			HasYahooData:    false,
		},
	}

	season2 := map[int64]PartialPlayer{
		8476453: {
			ID:              8476453,
			FirstName:       "Connor",
			LastName:        "McDavid",
			YahooImageSmall: "https://yahoo.com/mcdavid.jpg",
			HasBoxscoreData: true,
			HasYahooData:    true, // More complete
		},
		8477934: {
			ID:              8477934,
			FirstName:       "Leon",
			LastName:        "Draisaitl",
			HasBoxscoreData: true,
		},
	}

	result, err := MergeAllSeasonsActivity(ctx, []map[int64]PartialPlayer{season1, season2})

	assert.NoError(t, err)
	assert.Len(t, result, 2)

	// McDavid should have the more complete data from season2
	mcdavid := result[8476453]
	assert.True(t, mcdavid.HasYahooData)
	assert.Equal(t, "https://yahoo.com/mcdavid.jpg", mcdavid.YahooImageSmall)

	// Draisaitl should exist from season2
	_, hasDrai := result[8477934]
	assert.True(t, hasDrai)
}

func TestMergeAllSeasonsActivity_PreservesYahooImages(t *testing.T) {
	ctx := context.Background()

	// Season 1 has Yahoo data
	season1 := map[int64]PartialPlayer{
		8476453: {
			ID:              8476453,
			FirstName:       "Connor",
			YahooImageSmall: "https://yahoo.com/connor.jpg",
			HasYahooData:    true,
			HasBoxscoreData: false,
		},
	}

	// Season 2 has boxscore data but no Yahoo
	season2 := map[int64]PartialPlayer{
		8476453: {
			ID:              8476453,
			FirstName:       "Connor",
			Position:        "C",
			HasBoxscoreData: true,
			HasYahooData:    false,
		},
	}

	result, err := MergeAllSeasonsActivity(ctx, []map[int64]PartialPlayer{season1, season2})

	assert.NoError(t, err)

	// Should have both boxscore data AND preserved Yahoo images
	player := result[8476453]
	assert.True(t, player.HasBoxscoreData)
	assert.True(t, player.HasYahooData)
	assert.Equal(t, "https://yahoo.com/connor.jpg", player.YahooImageSmall)
	assert.Equal(t, "C", player.Position)
}

func TestMergeAllSeasonsActivity_Empty(t *testing.T) {
	ctx := context.Background()

	result, err := MergeAllSeasonsActivity(ctx, nil)
	assert.NoError(t, err)
	assert.Len(t, result, 0)

	result2, err := MergeAllSeasonsActivity(ctx, []map[int64]PartialPlayer{})
	assert.NoError(t, err)
	assert.Len(t, result2, 0)
}

func TestMergeAllSeasonsActivity_AddsYahooToExistingBoxscore(t *testing.T) {
	ctx := context.Background()

	// Season 1 has boxscore data only
	season1 := map[int64]PartialPlayer{
		8476453: {
			ID:              8476453,
			FirstName:       "Connor",
			LastName:        "McDavid",
			Position:        "C",
			HasBoxscoreData: true,
			HasYahooData:    false,
		},
	}

	// Season 2 has Yahoo data only
	season2 := map[int64]PartialPlayer{
		8476453: {
			ID:               8476453,
			FirstName:        "Connor",
			LastName:         "McDavid",
			YahooHomeURL:     "https://yahoo.com/mcdavid",
			YahooImageSmall:  "https://yahoo.com/small.jpg",
			YahooImageMedium: "https://yahoo.com/medium.jpg",
			YahooImageLarge:  "https://yahoo.com/large.jpg",
			HasBoxscoreData:  false,
			HasYahooData:     true,
		},
	}

	result, err := MergeAllSeasonsActivity(ctx, []map[int64]PartialPlayer{season1, season2})

	assert.NoError(t, err)

	// Should have boxscore data AND Yahoo images merged
	player := result[8476453]
	assert.True(t, player.HasBoxscoreData)
	assert.True(t, player.HasYahooData)
	assert.Equal(t, "C", player.Position)
	assert.Equal(t, "https://yahoo.com/mcdavid", player.YahooHomeURL)
	assert.Equal(t, "https://yahoo.com/small.jpg", player.YahooImageSmall)
	assert.Equal(t, "https://yahoo.com/medium.jpg", player.YahooImageMedium)
	assert.Equal(t, "https://yahoo.com/large.jpg", player.YahooImageLarge)
}

func TestMergeAllSeasonsActivity_KeepsExistingWhenBothComplete(t *testing.T) {
	ctx := context.Background()

	// Season 1 has both boxscore and Yahoo data
	season1 := map[int64]PartialPlayer{
		8476453: {
			ID:              8476453,
			FirstName:       "Connor",
			LastName:        "McDavid",
			Position:        "C",
			YahooImageSmall: "https://yahoo.com/season1.jpg",
			HasBoxscoreData: true,
			HasYahooData:    true,
		},
	}

	// Season 2 has only boxscore data (less complete)
	season2 := map[int64]PartialPlayer{
		8476453: {
			ID:              8476453,
			FirstName:       "Connor",
			LastName:        "McDavid",
			Position:        "RW", // Different position
			HasBoxscoreData: true,
			HasYahooData:    false,
		},
	}

	result, err := MergeAllSeasonsActivity(ctx, []map[int64]PartialPlayer{season1, season2})

	assert.NoError(t, err)

	// Should keep season1 data since it has both sources
	player := result[8476453]
	assert.True(t, player.HasBoxscoreData)
	assert.True(t, player.HasYahooData)
	assert.Equal(t, "C", player.Position)
	assert.Equal(t, "https://yahoo.com/season1.jpg", player.YahooImageSmall)
}

// Tests for impl functions with mocked Redis

func TestMergePlayerBatchesFromRedisImpl_EmptyKeys(t *testing.T) {
	ctx := context.Background()
	mockRedis := &MockRedisClient{}

	result, err := mergePlayerBatchesFromRedisImpl(ctx, mockRedis, []string{}, "yahoo")

	assert.NoError(t, err)
	assert.Len(t, result, 0)
}

func TestMergePlayerBatchesFromRedisImpl_YahooSource(t *testing.T) {
	ctx := context.Background()
	mockRedis := &MockRedisClient{}

	// Create test player data
	players := map[int64]PartialPlayer{
		8476453: {
			ID:           8476453,
			FirstName:    "Connor",
			LastName:     "McDavid",
			HasYahooData: true,
		},
	}
	var buf bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buf).Encode(players))

	// Mock Redis Get
	redisCmd := redis.NewStringCmd(ctx)
	redisCmd.SetVal(buf.String())
	mockRedis.On("Get", ctx, "test-key").Return(redisCmd)

	// Mock Redis Del
	delCmd := redis.NewIntCmd(ctx)
	delCmd.SetVal(1)
	mockRedis.On("Del", ctx, "test-key").Return(delCmd)

	result, err := mergePlayerBatchesFromRedisImpl(ctx, mockRedis, []string{"test-key"}, "yahoo")

	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "Connor", result[8476453].FirstName)
}

func TestMergePlayerBatchesFromRedisImpl_BoxscoreSource(t *testing.T) {
	ctx := context.Background()
	mockRedis := &MockRedisClient{}

	// Create test player data
	players := map[int64]PartialPlayer{
		8476453: {
			ID:              8476453,
			FirstName:       "Connor",
			HasBoxscoreData: true,
		},
	}
	var buf bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buf).Encode(players))

	// Mock Redis Get
	redisCmd := redis.NewStringCmd(ctx)
	redisCmd.SetVal(buf.String())
	mockRedis.On("Get", ctx, "test-key").Return(redisCmd)

	// Mock Redis Del
	delCmd := redis.NewIntCmd(ctx)
	delCmd.SetVal(1)
	mockRedis.On("Del", ctx, "test-key").Return(delCmd)

	result, err := mergePlayerBatchesFromRedisImpl(ctx, mockRedis, []string{"test-key"}, "boxscore")

	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.True(t, result[8476453].HasBoxscoreData)
}

func TestStoreSeasonResultImpl(t *testing.T) {
	ctx := context.Background()
	mockRedis := &MockRedisClient{}

	season := config.Season{
		Start:   time.Date(2023, 10, 1, 0, 0, 0, 0, time.UTC),
		End:     time.Date(2024, 4, 30, 0, 0, 0, 0, time.UTC),
		GameKey: 423,
	}

	players := map[int64]PartialPlayer{
		8476453: {ID: 8476453, FirstName: "Connor"},
	}

	// Mock Redis Set
	statusCmd := redis.NewStatusCmd(ctx)
	statusCmd.SetVal("OK")
	mockRedis.On("Set", ctx, mock.AnythingOfType("string"), mock.Anything, mock.AnythingOfType("time.Duration")).Return(statusCmd)

	result, err := storeSeasonResultImpl(ctx, mockRedis, season, players)

	require.NoError(t, err)
	assert.Contains(t, result.RedisKey, "2023")
	assert.Equal(t, 1, result.PlayerCount)
}

func TestStoreEnrichmentPlayersImpl(t *testing.T) {
	ctx := context.Background()
	mockRedis := &MockRedisClient{}

	players := map[int64]PartialPlayer{
		8476453: {ID: 8476453, FirstName: "Connor"},
		8477934: {ID: 8477934, FirstName: "Leon"},
	}

	// Mock Redis Set
	statusCmd := redis.NewStatusCmd(ctx)
	statusCmd.SetVal("OK")
	mockRedis.On("Set", ctx, mock.AnythingOfType("string"), mock.Anything, mock.AnythingOfType("time.Duration")).Return(statusCmd)

	key, err := storeEnrichmentPlayersImpl(ctx, mockRedis, players)

	require.NoError(t, err)
	assert.NotEmpty(t, key)
}

func TestLoadEnrichmentPlayersImpl(t *testing.T) {
	ctx := context.Background()
	mockRedis := &MockRedisClient{}

	// Create test player data
	players := map[int64]PartialPlayer{
		8476453: {ID: 8476453, FirstName: "Connor"},
	}
	var buf bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buf).Encode(players))

	// Mock Redis Get
	redisCmd := redis.NewStringCmd(ctx)
	redisCmd.SetVal(buf.String())
	mockRedis.On("Get", ctx, "test-key").Return(redisCmd)

	result, err := loadEnrichmentPlayersImpl(ctx, mockRedis, "test-key")

	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "Connor", result[8476453].FirstName)
}

func TestStoreSeasonResultImpl_SaveError(t *testing.T) {
	ctx := context.Background()
	mockRedis := &MockRedisClient{}

	season := config.Season{
		Start:   time.Date(2023, 10, 1, 0, 0, 0, 0, time.UTC),
		End:     time.Date(2024, 4, 30, 0, 0, 0, 0, time.UTC),
		GameKey: 423,
	}

	players := map[int64]PartialPlayer{
		8476453: {ID: 8476453, FirstName: "Connor"},
	}

	// Mock Redis Set with error
	statusCmd := redis.NewStatusCmd(ctx)
	statusCmd.SetErr(errors.New("redis connection error"))
	mockRedis.On("Set", ctx, mock.AnythingOfType("string"), mock.Anything, mock.AnythingOfType("time.Duration")).Return(statusCmd)

	result, err := storeSeasonResultImpl(ctx, mockRedis, season, players)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "redis connection error")
	assert.Empty(t, result.RedisKey)
}

func TestStoreEnrichmentPlayersImpl_SaveError(t *testing.T) {
	ctx := context.Background()
	mockRedis := &MockRedisClient{}

	players := map[int64]PartialPlayer{
		8476453: {ID: 8476453, FirstName: "Connor"},
	}

	// Mock Redis Set with error
	statusCmd := redis.NewStatusCmd(ctx)
	statusCmd.SetErr(errors.New("redis connection error"))
	mockRedis.On("Set", ctx, mock.AnythingOfType("string"), mock.Anything, mock.AnythingOfType("time.Duration")).Return(statusCmd)

	key, err := storeEnrichmentPlayersImpl(ctx, mockRedis, players)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "redis connection error")
	assert.Empty(t, key)
}

func TestLoadEnrichmentPlayersImpl_GetError(t *testing.T) {
	ctx := context.Background()
	mockRedis := &MockRedisClient{}

	// Mock Redis Get with error
	redisCmd := redis.NewStringCmd(ctx)
	redisCmd.SetErr(errors.New("key not found"))
	mockRedis.On("Get", ctx, "missing-key").Return(redisCmd)

	result, err := loadEnrichmentPlayersImpl(ctx, mockRedis, "missing-key")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "key not found")
	assert.Nil(t, result)
}

func TestLoadEnrichmentPlayersImpl_DecodeError(t *testing.T) {
	ctx := context.Background()
	mockRedis := &MockRedisClient{}

	// Mock Redis Get with invalid gob data
	redisCmd := redis.NewStringCmd(ctx)
	redisCmd.SetVal("invalid gob data")
	mockRedis.On("Get", ctx, "bad-data-key").Return(redisCmd)

	result, err := loadEnrichmentPlayersImpl(ctx, mockRedis, "bad-data-key")

	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestMergeAllSeasonsFromRedisImpl_EmptyKeys(t *testing.T) {
	ctx := context.Background()
	mockRedis := &MockRedisClient{}

	result, err := mergeAllSeasonsFromRedisImpl(ctx, mockRedis, []string{})

	assert.NoError(t, err)
	assert.Len(t, result, 0)
}

func TestMergeAllSeasonsFromRedisImpl_MergesTwoSeasons(t *testing.T) {
	ctx := context.Background()
	mockRedis := &MockRedisClient{}

	// Season 1: Connor with boxscore data only
	season1Players := map[int64]PartialPlayer{
		8476453: {
			ID:              8476453,
			FirstName:       "Connor",
			HasBoxscoreData: true,
			HasYahooData:    false,
		},
	}
	var buf1 bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buf1).Encode(season1Players))

	// Season 2: Connor with both data sources
	season2Players := map[int64]PartialPlayer{
		8476453: {
			ID:              8476453,
			FirstName:       "Connor",
			YahooImageSmall: "https://yahoo.com/connor.jpg",
			HasBoxscoreData: true,
			HasYahooData:    true,
		},
		8477934: {
			ID:              8477934,
			FirstName:       "Leon",
			HasBoxscoreData: true,
		},
	}
	var buf2 bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buf2).Encode(season2Players))

	// Mock Redis Get for both keys
	redisCmd1 := redis.NewStringCmd(ctx)
	redisCmd1.SetVal(buf1.String())
	mockRedis.On("Get", ctx, "season-1").Return(redisCmd1)

	redisCmd2 := redis.NewStringCmd(ctx)
	redisCmd2.SetVal(buf2.String())
	mockRedis.On("Get", ctx, "season-2").Return(redisCmd2)

	// Mock Redis Del
	delCmd := redis.NewIntCmd(ctx)
	delCmd.SetVal(2)
	mockRedis.On("Del", ctx, "season-1", "season-2").Return(delCmd)

	result, err := mergeAllSeasonsFromRedisImpl(ctx, mockRedis, []string{"season-1", "season-2"})

	require.NoError(t, err)
	assert.Len(t, result, 2)

	// Connor should have the more complete data from season2
	connor := result[8476453]
	assert.True(t, connor.HasYahooData)
	assert.True(t, connor.HasBoxscoreData)
	assert.Equal(t, "https://yahoo.com/connor.jpg", connor.YahooImageSmall)

	// Leon should exist from season2
	leon := result[8477934]
	assert.Equal(t, "Leon", leon.FirstName)
}

func TestMergeAllSeasonsFromRedisImpl_MergesYahooIntoBoxscore(t *testing.T) {
	ctx := context.Background()
	mockRedis := &MockRedisClient{}

	// Season 1: Connor with boxscore data only
	season1Players := map[int64]PartialPlayer{
		8476453: {
			ID:              8476453,
			FirstName:       "Connor",
			Position:        "C",
			HasBoxscoreData: true,
			HasYahooData:    false,
		},
	}
	var buf1 bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buf1).Encode(season1Players))

	// Season 2: Same player with Yahoo data only
	season2Players := map[int64]PartialPlayer{
		8476453: {
			ID:              8476453,
			FirstName:       "Connor",
			YahooImageSmall: "https://yahoo.com/connor.jpg",
			HasBoxscoreData: false,
			HasYahooData:    true,
		},
	}
	var buf2 bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buf2).Encode(season2Players))

	// Mock Redis Get for both keys
	redisCmd1 := redis.NewStringCmd(ctx)
	redisCmd1.SetVal(buf1.String())
	mockRedis.On("Get", ctx, "season-1").Return(redisCmd1)

	redisCmd2 := redis.NewStringCmd(ctx)
	redisCmd2.SetVal(buf2.String())
	mockRedis.On("Get", ctx, "season-2").Return(redisCmd2)

	// Mock Redis Del
	delCmd := redis.NewIntCmd(ctx)
	delCmd.SetVal(2)
	mockRedis.On("Del", ctx, "season-1", "season-2").Return(delCmd)

	result, err := mergeAllSeasonsFromRedisImpl(ctx, mockRedis, []string{"season-1", "season-2"})

	require.NoError(t, err)
	assert.Len(t, result, 1)

	// Connor should have merged boxscore AND Yahoo data
	connor := result[8476453]
	assert.True(t, connor.HasYahooData)
	assert.True(t, connor.HasBoxscoreData)
	assert.Equal(t, "C", connor.Position)               // from season 1
	assert.Equal(t, "https://yahoo.com/connor.jpg", connor.YahooImageSmall) // from season 2
}

func TestMergeAllSeasonsFromRedisImpl_MergesBoxscoreIntoYahoo(t *testing.T) {
	ctx := context.Background()
	mockRedis := &MockRedisClient{}

	// Season 1: Connor with Yahoo data only
	season1Players := map[int64]PartialPlayer{
		8476453: {
			ID:              8476453,
			FirstName:       "Connor",
			YahooImageSmall: "https://yahoo.com/connor.jpg",
			HasBoxscoreData: false,
			HasYahooData:    true,
		},
	}
	var buf1 bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buf1).Encode(season1Players))

	// Season 2: Same player with boxscore data only
	season2Players := map[int64]PartialPlayer{
		8476453: {
			ID:              8476453,
			FirstName:       "Connor",
			Position:        "C",
			HasBoxscoreData: true,
			HasYahooData:    false,
		},
	}
	var buf2 bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buf2).Encode(season2Players))

	// Mock Redis Get for both keys
	redisCmd1 := redis.NewStringCmd(ctx)
	redisCmd1.SetVal(buf1.String())
	mockRedis.On("Get", ctx, "season-1").Return(redisCmd1)

	redisCmd2 := redis.NewStringCmd(ctx)
	redisCmd2.SetVal(buf2.String())
	mockRedis.On("Get", ctx, "season-2").Return(redisCmd2)

	// Mock Redis Del
	delCmd := redis.NewIntCmd(ctx)
	delCmd.SetVal(2)
	mockRedis.On("Del", ctx, "season-1", "season-2").Return(delCmd)

	result, err := mergeAllSeasonsFromRedisImpl(ctx, mockRedis, []string{"season-1", "season-2"})

	require.NoError(t, err)
	assert.Len(t, result, 1)

	// Connor should have merged boxscore AND Yahoo data
	connor := result[8476453]
	assert.True(t, connor.HasYahooData)
	assert.True(t, connor.HasBoxscoreData)
	assert.Equal(t, "C", connor.Position)               // from season 2
	assert.Equal(t, "https://yahoo.com/connor.jpg", connor.YahooImageSmall) // from season 1
}

func TestMergeAllSeasonsFromRedisImpl_SkipsOnLoadError(t *testing.T) {
	ctx := context.Background()
	mockRedis := &MockRedisClient{}

	// Season 1: Valid data
	season1Players := map[int64]PartialPlayer{
		8476453: {ID: 8476453, FirstName: "Connor", HasBoxscoreData: true},
	}
	var buf1 bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buf1).Encode(season1Players))

	// Mock Redis Get - first succeeds, second fails
	redisCmd1 := redis.NewStringCmd(ctx)
	redisCmd1.SetVal(buf1.String())
	mockRedis.On("Get", ctx, "season-1").Return(redisCmd1)

	redisCmd2 := redis.NewStringCmd(ctx)
	redisCmd2.SetErr(redis.Nil) // Simulate error
	mockRedis.On("Get", ctx, "season-2").Return(redisCmd2)

	// Mock Redis Del
	delCmd := redis.NewIntCmd(ctx)
	delCmd.SetVal(1)
	mockRedis.On("Del", ctx, "season-1", "season-2").Return(delCmd)

	result, err := mergeAllSeasonsFromRedisImpl(ctx, mockRedis, []string{"season-1", "season-2"})

	// Should succeed with partial data
	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "Connor", result[8476453].FirstName)
}

func TestMergeAllSeasonsFromRedisImpl_SkipsOnDecodeError(t *testing.T) {
	ctx := context.Background()
	mockRedis := &MockRedisClient{}

	// Season 1: Valid data
	season1Players := map[int64]PartialPlayer{
		8476453: {ID: 8476453, FirstName: "Connor", HasBoxscoreData: true},
	}
	var buf1 bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buf1).Encode(season1Players))

	// Mock Redis Get - first succeeds, second returns invalid gob data
	redisCmd1 := redis.NewStringCmd(ctx)
	redisCmd1.SetVal(buf1.String())
	mockRedis.On("Get", ctx, "season-1").Return(redisCmd1)

	redisCmd2 := redis.NewStringCmd(ctx)
	redisCmd2.SetVal("invalid gob data")
	mockRedis.On("Get", ctx, "season-2").Return(redisCmd2)

	// Mock Redis Del
	delCmd := redis.NewIntCmd(ctx)
	delCmd.SetVal(1)
	mockRedis.On("Del", ctx, "season-1", "season-2").Return(delCmd)

	result, err := mergeAllSeasonsFromRedisImpl(ctx, mockRedis, []string{"season-1", "season-2"})

	// Should succeed with partial data (skips invalid season)
	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "Connor", result[8476453].FirstName)
}
