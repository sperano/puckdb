package worker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/go-redis/redis/v8"
	"github.com/go-redis/redismock/v8"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// --- downloadSeasonsManifestImpl tests ---

func TestDownloadSeasonsManifest_RedisHit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	redisClient, mockRedis := redismock.NewClientMock()
	nhlClient := &MockNHLClient{}

	seasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2022), StandingsStart: "2022-10-07", StandingsEnd: "2023-04-14"},
		{ID: nhl.NewSeason(2023), StandingsStart: "2023-10-10", StandingsEnd: "2024-04-18"},
	}
	seasonsJSON, _ := json.Marshal(seasons)

	mockRedis.ExpectGet(redisSeasonsManifestKey).SetVal(string(seasonsJSON))

	result, err := downloadSeasonsManifestImpl(ctx, fs, redisClient, nhlClient)

	require.NoError(t, err)
	assert.Equal(t, 2, result.Count)
	assert.True(t, result.FromCache)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
	nhlClient.AssertNotCalled(t, "SeasonStandingManifest")
}

func TestDownloadSeasonsManifest_RedisMiss_FilesystemHit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	redisClient, mockRedis := redismock.NewClientMock()
	nhlClient := &MockNHLClient{}

	file := store.SeasonsManifestFile{}
	seasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2022), StandingsStart: "2022-10-07", StandingsEnd: "2023-04-14"},
	}
	seasonsJSON, _ := json.Marshal(seasons)

	// Create a real temp file so os.Stat succeeds and sees it as fresh
	tmpFile, err := os.CreateTemp("", "seasons-manifest-*.json")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	// Redis miss
	mockRedis.ExpectGet(redisSeasonsManifestKey).SetErr(redis.Nil)
	// Filesystem hit (file exists and is fresh - FullPath returns real path for os.Stat)
	fs.On("Exists", file).Return(true)
	fs.On("Read", file).Return(seasonsJSON, nil)
	fs.On("FullPath", file).Return(tmpFile.Name())
	// Cache in Redis after reading from filesystem
	mockRedis.ExpectSet(redisSeasonsManifestKey, seasonsJSON, config.DefaultSeasonsManifestCacheTTL).SetVal("OK")

	result, err := downloadSeasonsManifestImpl(ctx, fs, redisClient, nhlClient)

	require.NoError(t, err)
	assert.Equal(t, 1, result.Count)
	assert.True(t, result.FromCache)
	fs.AssertExpectations(t)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
	nhlClient.AssertNotCalled(t, "SeasonStandingManifest")
}

func TestDownloadSeasonsManifest_AllMiss_APIFetch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	redisClient, mockRedis := redismock.NewClientMock()
	nhlClient := &MockNHLClient{}

	file := store.SeasonsManifestFile{}
	seasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2022), StandingsStart: "2022-10-07", StandingsEnd: "2023-04-14"},
		{ID: nhl.NewSeason(2023), StandingsStart: "2023-10-10", StandingsEnd: "2024-04-18"},
		{ID: nhl.NewSeason(2024), StandingsStart: "2024-10-04", StandingsEnd: "2025-04-17"},
	}
	seasonsJSON, _ := json.Marshal(seasons)

	// Redis miss
	mockRedis.ExpectGet(redisSeasonsManifestKey).SetErr(redis.Nil)
	// Filesystem miss
	fs.On("Exists", file).Return(false)
	// API fetch
	nhlClient.On("SeasonStandingManifest", ctx).Return(seasons, nil)
	// Write to filesystem
	fs.On("Write", file, mock.Anything).Return(nil)
	// Cache in Redis
	mockRedis.ExpectSet(redisSeasonsManifestKey, seasonsJSON, config.DefaultSeasonsManifestCacheTTL).SetVal("OK")

	result, err := downloadSeasonsManifestImpl(ctx, fs, redisClient, nhlClient)

	require.NoError(t, err)
	assert.Equal(t, 3, result.Count)
	assert.False(t, result.FromCache)
	fs.AssertExpectations(t)
	nhlClient.AssertExpectations(t)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestDownloadSeasonsManifest_APIError_StaleFallback(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	redisClient, mockRedis := redismock.NewClientMock()
	nhlClient := &MockNHLClient{}

	file := store.SeasonsManifestFile{}
	staleSeasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2022), StandingsStart: "2022-10-07", StandingsEnd: "2023-04-14"},
	}
	staleJSON, _ := json.Marshal(staleSeasons)

	// Redis miss
	mockRedis.ExpectGet(redisSeasonsManifestKey).SetErr(redis.Nil)
	// Filesystem exists but stale (FullPath returns non-existent path so os.Stat fails -> treated as stale)
	fs.On("Exists", file).Return(true)
	fs.On("Read", file).Return(staleJSON, nil)
	fs.On("FullPath", file).Return("/nonexistent/path.json")
	// API fails
	nhlClient.On("SeasonStandingManifest", ctx).Return(nil, errors.New("API unavailable"))
	// Falls back to stale data, caches in Redis
	mockRedis.ExpectSet(redisSeasonsManifestKey, staleJSON, config.DefaultSeasonsManifestCacheTTL).SetVal("OK")

	result, err := downloadSeasonsManifestImpl(ctx, fs, redisClient, nhlClient)

	// Should succeed with stale data
	require.NoError(t, err)
	assert.Equal(t, 1, result.Count)
	assert.True(t, result.FromCache)
	fs.AssertExpectations(t)
	nhlClient.AssertExpectations(t)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

func TestDownloadSeasonsManifest_APIError_NoFallback(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	redisClient, mockRedis := redismock.NewClientMock()
	nhlClient := &MockNHLClient{}

	file := store.SeasonsManifestFile{}

	// Redis miss
	mockRedis.ExpectGet(redisSeasonsManifestKey).SetErr(redis.Nil)
	// Filesystem miss
	fs.On("Exists", file).Return(false)
	// API fails
	nhlClient.On("SeasonStandingManifest", ctx).Return(nil, errors.New("API unavailable"))

	result, err := downloadSeasonsManifestImpl(ctx, fs, redisClient, nhlClient)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "API unavailable")
	assert.Equal(t, 0, result.Count)
	fs.AssertExpectations(t)
	nhlClient.AssertExpectations(t)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

// --- downloadSeasonStandingsImpl tests ---

func TestDownloadSeasonStandings_CacheHit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}

	seasonID := 20222023
	file := store.SeasonStandingsFile{SeasonID: seasonID}

	standings := []nhl.Standing{
		{TeamAbbrev: nhl.LocalizedString{Default: "MTL"}, TeamName: nhl.LocalizedString{Default: "Montreal Canadiens"}},
		{TeamAbbrev: nhl.LocalizedString{Default: "TOR"}, TeamName: nhl.LocalizedString{Default: "Toronto Maple Leafs"}},
	}
	standingsJSON, _ := json.Marshal(standings)

	fs.On("Exists", file).Return(true)
	fs.On("Read", file).Return(standingsJSON, nil)

	result, err := downloadSeasonStandingsImpl(ctx, fs, client, seasonID)

	require.NoError(t, err)
	assert.Equal(t, seasonID, result.SeasonID)
	assert.Equal(t, 2, result.TeamCount)
	assert.True(t, result.FromCache)
	fs.AssertExpectations(t)
	client.AssertNotCalled(t, "LeagueStandingsForSeason")
}

func TestDownloadSeasonStandings_CacheMiss(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}

	seasonID := 20222023
	file := store.SeasonStandingsFile{SeasonID: seasonID}

	fs.On("Exists", file).Return(false)

	standings := []nhl.Standing{
		{TeamAbbrev: nhl.LocalizedString{Default: "MTL"}, TeamName: nhl.LocalizedString{Default: "Montreal Canadiens"}},
		{TeamAbbrev: nhl.LocalizedString{Default: "TOR"}, TeamName: nhl.LocalizedString{Default: "Toronto Maple Leafs"}},
		{TeamAbbrev: nhl.LocalizedString{Default: "BOS"}, TeamName: nhl.LocalizedString{Default: "Boston Bruins"}},
	}
	season, _ := nhl.SeasonFromInt(seasonID)
	client.On("LeagueStandingsForSeason", ctx, season).Return(standings, nil)
	fs.On("Write", file, mock.Anything).Return(nil)

	result, err := downloadSeasonStandingsImpl(ctx, fs, client, seasonID)

	require.NoError(t, err)
	assert.Equal(t, seasonID, result.SeasonID)
	assert.Equal(t, 3, result.TeamCount)
	assert.False(t, result.FromCache)
	fs.AssertExpectations(t)
	client.AssertExpectations(t)
}

func TestDownloadSeasonStandings_APIError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}

	seasonID := 20222023
	file := store.SeasonStandingsFile{SeasonID: seasonID}

	fs.On("Exists", file).Return(false)

	season, _ := nhl.SeasonFromInt(seasonID)
	client.On("LeagueStandingsForSeason", ctx, season).Return(nil, errors.New("API unavailable"))

	result, err := downloadSeasonStandingsImpl(ctx, fs, client, seasonID)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "API unavailable")
	assert.Equal(t, seasonID, result.SeasonID)
	assert.Equal(t, 0, result.TeamCount)
	fs.AssertExpectations(t)
	client.AssertExpectations(t)
}

func TestDownloadSeasonStandings_InvalidSeasonID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}

	invalidSeasonID := 123 // Invalid format
	file := store.SeasonStandingsFile{SeasonID: invalidSeasonID}

	fs.On("Exists", file).Return(false)

	result, err := downloadSeasonStandingsImpl(ctx, fs, client, invalidSeasonID)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse season ID")
	assert.Equal(t, invalidSeasonID, result.SeasonID)
	fs.AssertExpectations(t)
	client.AssertNotCalled(t, "LeagueStandingsForSeason")
}

func TestDownloadSeasonStandings_CacheCorrupt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}

	seasonID := 20222023
	file := store.SeasonStandingsFile{SeasonID: seasonID}

	fs.On("Exists", file).Return(true)
	fs.On("Read", file).Return([]byte("not valid json"), nil)

	standings := []nhl.Standing{
		{TeamAbbrev: nhl.LocalizedString{Default: "MTL"}, TeamName: nhl.LocalizedString{Default: "Montreal Canadiens"}},
	}
	season, _ := nhl.SeasonFromInt(seasonID)
	client.On("LeagueStandingsForSeason", ctx, season).Return(standings, nil)
	fs.On("Write", file, mock.Anything).Return(nil)

	result, err := downloadSeasonStandingsImpl(ctx, fs, client, seasonID)

	require.NoError(t, err)
	assert.Equal(t, seasonID, result.SeasonID)
	assert.Equal(t, 1, result.TeamCount)
	assert.False(t, result.FromCache)
	fs.AssertExpectations(t)
	client.AssertExpectations(t)
}

func TestDownloadSeasonStandings_WriteError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}

	seasonID := 20222023
	file := store.SeasonStandingsFile{SeasonID: seasonID}

	fs.On("Exists", file).Return(false)

	standings := []nhl.Standing{
		{TeamAbbrev: nhl.LocalizedString{Default: "MTL"}, TeamName: nhl.LocalizedString{Default: "Montreal Canadiens"}},
	}
	season, _ := nhl.SeasonFromInt(seasonID)
	client.On("LeagueStandingsForSeason", ctx, season).Return(standings, nil)
	fs.On("Write", file, mock.Anything).Return(errors.New("disk full"))

	result, err := downloadSeasonStandingsImpl(ctx, fs, client, seasonID)

	// Write error is logged but not fatal - should still return success
	require.NoError(t, err)
	assert.Equal(t, seasonID, result.SeasonID)
	assert.Equal(t, 1, result.TeamCount)
	assert.False(t, result.FromCache)
	fs.AssertExpectations(t)
	client.AssertExpectations(t)
}

// --- upsertSeasonsImpl tests ---

func TestUpsertSeasons_Success(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	upserter := &MockSeasonsUpserter{}

	seasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2022), StandingsStart: "2022-10-07", StandingsEnd: "2023-04-14"},
		{ID: nhl.NewSeason(2023), StandingsStart: "2023-10-10", StandingsEnd: "2024-04-18"},
	}

	upserter.On("UpsertSeason", ctx, mock.AnythingOfType("sqlcdb.UpsertSeasonParams")).Return(nil).Times(2)

	result, err := upsertSeasonsImpl(ctx, upserter, seasons)

	require.NoError(t, err)
	assert.Equal(t, 2, result.SeasonsUpserted)
	upserter.AssertExpectations(t)
}

func TestUpsertSeasons_EmptyInput(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	upserter := &MockSeasonsUpserter{}

	result, err := upsertSeasonsImpl(ctx, upserter, []nhl.SeasonInfo{})

	require.NoError(t, err)
	assert.Equal(t, 0, result.SeasonsUpserted)
	upserter.AssertNotCalled(t, "UpsertSeason")
}

func TestUpsertSeasons_InvalidStartDate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	upserter := &MockSeasonsUpserter{}

	seasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2022), StandingsStart: "invalid-date", StandingsEnd: "2023-04-14"},
	}

	result, err := upsertSeasonsImpl(ctx, upserter, seasons)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse standings start")
	assert.Equal(t, 0, result.SeasonsUpserted)
	upserter.AssertNotCalled(t, "UpsertSeason")
}

func TestUpsertSeasons_InvalidEndDate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	upserter := &MockSeasonsUpserter{}

	seasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2022), StandingsStart: "2022-10-07", StandingsEnd: "invalid-date"},
	}

	result, err := upsertSeasonsImpl(ctx, upserter, seasons)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse standings end")
	assert.Equal(t, 0, result.SeasonsUpserted)
	upserter.AssertNotCalled(t, "UpsertSeason")
}

func TestUpsertSeasons_UpsertError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	upserter := &MockSeasonsUpserter{}

	seasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2022), StandingsStart: "2022-10-07", StandingsEnd: "2023-04-14"},
	}

	upserter.On("UpsertSeason", ctx, mock.AnythingOfType("sqlcdb.UpsertSeasonParams")).Return(errors.New("database error"))

	result, err := upsertSeasonsImpl(ctx, upserter, seasons)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "upsert season")
	assert.Contains(t, err.Error(), "database error")
	assert.Equal(t, 0, result.SeasonsUpserted)
	upserter.AssertExpectations(t)
}

// --- upsertSeasonTeamsImpl tests ---

func TestUpsertSeasonTeams_Success(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	upserter := &MockSeasonTeamsUpserter{}

	seasonID := 20222023
	confName := "Eastern"
	confAbbrev := "E"
	standings := []nhl.Standing{
		{
			TeamAbbrev:       nhl.LocalizedString{Default: "MTL"},
			TeamName:         nhl.LocalizedString{Default: "Montreal Canadiens"},
			DivisionName:     "Atlantic",
			DivisionAbbrev:   "A",
			ConferenceName:   &confName,
			ConferenceAbbrev: &confAbbrev,
		},
		{
			TeamAbbrev:       nhl.LocalizedString{Default: "TOR"},
			TeamName:         nhl.LocalizedString{Default: "Toronto Maple Leafs"},
			DivisionName:     "Atlantic",
			DivisionAbbrev:   "A",
			ConferenceName:   &confName,
			ConferenceAbbrev: &confAbbrev,
		},
	}

	upserter.On("UpsertSeasonTeam", ctx, mock.AnythingOfType("sqlcdb.UpsertSeasonTeamParams")).Return(nil).Times(2)

	result, err := upsertSeasonTeamsImpl(ctx, upserter, seasonID, standings)

	require.NoError(t, err)
	assert.Equal(t, seasonID, result.SeasonID)
	assert.Equal(t, 2, result.TeamsUpserted)
	upserter.AssertExpectations(t)
}

func TestUpsertSeasonTeams_EmptyInput(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	upserter := &MockSeasonTeamsUpserter{}

	seasonID := 20222023

	result, err := upsertSeasonTeamsImpl(ctx, upserter, seasonID, []nhl.Standing{})

	require.NoError(t, err)
	assert.Equal(t, seasonID, result.SeasonID)
	assert.Equal(t, 0, result.TeamsUpserted)
	upserter.AssertNotCalled(t, "UpsertSeasonTeam")
}

func TestUpsertSeasonTeams_NilConference(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	upserter := &MockSeasonTeamsUpserter{}

	seasonID := 20222023
	standings := []nhl.Standing{
		{
			TeamAbbrev:       nhl.LocalizedString{Default: "MTL"},
			TeamName:         nhl.LocalizedString{Default: "Montreal Canadiens"},
			DivisionName:     "Atlantic",
			DivisionAbbrev:   "A",
			ConferenceName:   nil,
			ConferenceAbbrev: nil,
		},
	}

	upserter.On("UpsertSeasonTeam", ctx, mock.AnythingOfType("sqlcdb.UpsertSeasonTeamParams")).Return(nil)

	result, err := upsertSeasonTeamsImpl(ctx, upserter, seasonID, standings)

	require.NoError(t, err)
	assert.Equal(t, 1, result.TeamsUpserted)
	upserter.AssertExpectations(t)
}

func TestUpsertSeasonTeams_UpsertError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	upserter := &MockSeasonTeamsUpserter{}

	seasonID := 20222023
	standings := []nhl.Standing{
		{
			TeamAbbrev:     nhl.LocalizedString{Default: "MTL"},
			TeamName:       nhl.LocalizedString{Default: "Montreal Canadiens"},
			DivisionName:   "Atlantic",
			DivisionAbbrev: "A",
		},
	}

	upserter.On("UpsertSeasonTeam", ctx, mock.AnythingOfType("sqlcdb.UpsertSeasonTeamParams")).Return(errors.New("database error"))

	result, err := upsertSeasonTeamsImpl(ctx, upserter, seasonID, standings)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "upsert season team")
	assert.Contains(t, err.Error(), "database error")
	assert.Equal(t, seasonID, result.SeasonID)
	assert.Equal(t, 0, result.TeamsUpserted)
	upserter.AssertExpectations(t)
}

// --- initializeSeasonTeamsImpl tests ---

// mockSeasonTeamsInitializer is a test double for seasonTeamsInitializer.
type mockSeasonTeamsInitializer struct {
	downloadResult DownloadSeasonStandingsResult
	downloadErr    error
	upsertResult   UpsertSeasonTeamsResult
	upsertErr      error
}

func (m *mockSeasonTeamsInitializer) DownloadStandings(_ context.Context, _ int) (DownloadSeasonStandingsResult, error) {
	return m.downloadResult, m.downloadErr
}

func (m *mockSeasonTeamsInitializer) UpsertTeams(_ context.Context, _ int) (UpsertSeasonTeamsResult, error) {
	return m.upsertResult, m.upsertErr
}

func TestInitializeSeasonTeamsImpl_Success(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	seasonID := 20222023

	init := &mockSeasonTeamsInitializer{
		downloadResult: DownloadSeasonStandingsResult{
			SeasonID:  seasonID,
			TeamCount: 32,
			FromCache: false,
		},
		upsertResult: UpsertSeasonTeamsResult{
			SeasonID:      seasonID,
			TeamsUpserted: 32,
		},
	}

	result, err := initializeSeasonTeamsImpl(ctx, init, seasonID)

	require.NoError(t, err)
	assert.Equal(t, seasonID, result.SeasonID)
	assert.Equal(t, 32, result.DownloadResult.TeamCount)
	assert.False(t, result.DownloadResult.FromCache)
	assert.Equal(t, 32, result.UpsertResult.TeamsUpserted)
}

func TestInitializeSeasonTeamsImpl_DownloadError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	seasonID := 20222023

	init := &mockSeasonTeamsInitializer{
		downloadErr: errors.New("NHL API unavailable"),
	}

	result, err := initializeSeasonTeamsImpl(ctx, init, seasonID)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "download season standings")
	assert.Contains(t, err.Error(), "NHL API unavailable")
	assert.Equal(t, seasonID, result.SeasonID)
}

func TestInitializeSeasonTeamsImpl_UpsertError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	seasonID := 20222023

	init := &mockSeasonTeamsInitializer{
		downloadResult: DownloadSeasonStandingsResult{
			SeasonID:  seasonID,
			TeamCount: 32,
			FromCache: true,
		},
		upsertErr: errors.New("database connection failed"),
	}

	result, err := initializeSeasonTeamsImpl(ctx, init, seasonID)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "upsert season teams")
	assert.Contains(t, err.Error(), "database connection failed")
	// Download should have succeeded
	assert.Equal(t, 32, result.DownloadResult.TeamCount)
}
