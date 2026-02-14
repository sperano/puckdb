package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// --- downloadSeasonsManifestImpl tests ---

func TestDownloadSeasonsManifest_CacheHit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}

	file := store.SeasonsManifestFile{}

	seasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2022), StandingsStart: "2022-10-07", StandingsEnd: "2023-04-14"},
		{ID: nhl.NewSeason(2023), StandingsStart: "2023-10-10", StandingsEnd: "2024-04-18"},
	}
	seasonsJSON, _ := json.Marshal(seasons)

	fs.On("Exists", file).Return(true)
	fs.On("Read", file).Return(seasonsJSON, nil)

	result, err := downloadSeasonsManifestImpl(ctx, fs, client)

	require.NoError(t, err)
	assert.Equal(t, 2, result.Count)
	assert.True(t, result.FromCache)
	fs.AssertExpectations(t)
	client.AssertNotCalled(t, "SeasonStandingManifest")
}

func TestDownloadSeasonsManifest_CacheMiss(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}

	file := store.SeasonsManifestFile{}

	fs.On("Exists", file).Return(false)

	seasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2022), StandingsStart: "2022-10-07", StandingsEnd: "2023-04-14"},
		{ID: nhl.NewSeason(2023), StandingsStart: "2023-10-10", StandingsEnd: "2024-04-18"},
		{ID: nhl.NewSeason(2024), StandingsStart: "2024-10-04", StandingsEnd: "2025-04-17"},
	}
	client.On("SeasonStandingManifest", ctx).Return(seasons, nil)
	fs.On("Write", file, mock.Anything).Return(nil)

	result, err := downloadSeasonsManifestImpl(ctx, fs, client)

	require.NoError(t, err)
	assert.Equal(t, 3, result.Count)
	assert.False(t, result.FromCache)
	fs.AssertExpectations(t)
	client.AssertExpectations(t)
}

func TestDownloadSeasonsManifest_APIError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}

	file := store.SeasonsManifestFile{}

	fs.On("Exists", file).Return(false)
	client.On("SeasonStandingManifest", ctx).Return(nil, errors.New("API unavailable"))

	result, err := downloadSeasonsManifestImpl(ctx, fs, client)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "API unavailable")
	assert.Equal(t, 0, result.Count)
	fs.AssertExpectations(t)
	client.AssertExpectations(t)
}

func TestDownloadSeasonsManifest_CacheCorrupt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}

	file := store.SeasonsManifestFile{}

	fs.On("Exists", file).Return(true)
	fs.On("Read", file).Return([]byte("not valid json"), nil)

	seasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2022), StandingsStart: "2022-10-07", StandingsEnd: "2023-04-14"},
	}
	client.On("SeasonStandingManifest", ctx).Return(seasons, nil)
	fs.On("Write", file, mock.Anything).Return(nil)

	result, err := downloadSeasonsManifestImpl(ctx, fs, client)

	require.NoError(t, err)
	assert.Equal(t, 1, result.Count)
	assert.False(t, result.FromCache)
	fs.AssertExpectations(t)
	client.AssertExpectations(t)
}

func TestDownloadSeasonsManifest_WriteError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := NewMockFileSystem()
	client := &MockNHLClient{}

	file := store.SeasonsManifestFile{}

	fs.On("Exists", file).Return(false)

	seasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2022), StandingsStart: "2022-10-07", StandingsEnd: "2023-04-14"},
	}
	client.On("SeasonStandingManifest", ctx).Return(seasons, nil)
	fs.On("Write", file, mock.Anything).Return(errors.New("disk full"))

	result, err := downloadSeasonsManifestImpl(ctx, fs, client)

	// Write error is not fatal
	require.NoError(t, err)
	assert.Equal(t, 1, result.Count)
	assert.False(t, result.FromCache)
	fs.AssertExpectations(t)
	client.AssertExpectations(t)
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
