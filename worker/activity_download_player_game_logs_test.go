package worker

import (
	"context"
	"testing"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/store"
	"github.com/stretchr/testify/assert"
)

func TestDownloadPlayerGameLogsImpl_EmptyInput(t *testing.T) {
	mem := store.NewMemStorage()
	repos := store.NewRepos(mem)

	input := DownloadPlayerGameLogsInput{
		PlayerIDs: []int64{},
		StartYear: 2024,
	}

	result, err := downloadPlayerGameLogsImpl(context.Background(), repos, input)

	assert.NoError(t, err)
	assert.Equal(t, 0, result.Downloaded)
	assert.Equal(t, 0, result.CacheHits)
	assert.Empty(t, result.Errors)
}

func TestDownloadPlayerGameLogsImpl_CacheHit(t *testing.T) {
	mem := store.NewMemStorage()
	repos := store.NewRepos(mem)

	// Pre-populate file in cache
	playerID := nhl.PlayerID(8478402)
	seasonID := 20242025
	gameTypeID := nhl.GameTypeRegularSeason.ToInt()
	repos.Player.SaveGameLog(playerID, seasonID, gameTypeID, []byte(`{"gameLog":[]}`))

	input := DownloadPlayerGameLogsInput{
		PlayerIDs: []int64{8478402},
		StartYear: 2024,
		GameTypes: []int{nhl.GameTypeRegularSeason.ToInt()},
	}

	result, err := downloadPlayerGameLogsImpl(context.Background(), repos, input)

	assert.NoError(t, err)
	assert.Equal(t, 0, result.Downloaded)
	assert.Equal(t, 1, result.CacheHits)
	assert.Empty(t, result.Errors)
}

func TestDownloadPlayerGameLogsImpl_MultipleGameTypes(t *testing.T) {
	mem := store.NewMemStorage()
	repos := store.NewRepos(mem)

	playerID := nhl.PlayerID(8478402)
	seasonID := 20242025

	// Pre-populate both game types
	repos.Player.SaveGameLog(playerID, seasonID, nhl.GameTypeRegularSeason.ToInt(), []byte(`{"gameLog":[]}`))
	repos.Player.SaveGameLog(playerID, seasonID, nhl.GameTypePlayoffs.ToInt(), []byte(`{"gameLog":[]}`))

	input := DownloadPlayerGameLogsInput{
		PlayerIDs: []int64{8478402},
		StartYear: 2024,
		GameTypes: []int{nhl.GameTypeRegularSeason.ToInt(), nhl.GameTypePlayoffs.ToInt()},
	}

	result, err := downloadPlayerGameLogsImpl(context.Background(), repos, input)

	assert.NoError(t, err)
	assert.Equal(t, 0, result.Downloaded)
	assert.Equal(t, 2, result.CacheHits)
	assert.Empty(t, result.Errors)
}

func TestDownloadPlayerGameLogsImpl_InvalidGameType(t *testing.T) {
	mem := store.NewMemStorage()
	repos := store.NewRepos(mem)

	input := DownloadPlayerGameLogsInput{
		PlayerIDs: []int64{8478402},
		StartYear: 2024,
		GameTypes: []int{99}, // Invalid game type
	}

	result, err := downloadPlayerGameLogsImpl(context.Background(), repos, input)

	assert.NoError(t, err)
	assert.Equal(t, 0, result.Downloaded)
	assert.Equal(t, 0, result.CacheHits)
	assert.Len(t, result.Errors, 1)
	assert.Contains(t, result.Errors[0], "invalid game type")
}

func TestDownloadPlayerGameLogsImpl_DefaultsToRegularSeason(t *testing.T) {
	mem := store.NewMemStorage()
	repos := store.NewRepos(mem)

	// Pre-populate regular season file
	playerID := nhl.PlayerID(8478402)
	seasonID := 20242025
	repos.Player.SaveGameLog(playerID, seasonID, nhl.GameTypeRegularSeason.ToInt(), []byte(`{"gameLog":[]}`))

	input := DownloadPlayerGameLogsInput{
		PlayerIDs: []int64{8478402},
		StartYear: 2024,
		// No GameTypes specified - should default to regular season
	}

	result, err := downloadPlayerGameLogsImpl(context.Background(), repos, input)

	assert.NoError(t, err)
	assert.Equal(t, 0, result.Downloaded)
	assert.Equal(t, 1, result.CacheHits)
}

func TestDownloadPlayerGameLogsImpl_ContextCancellation(t *testing.T) {
	mem := store.NewMemStorage()
	repos := store.NewRepos(mem)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	input := DownloadPlayerGameLogsInput{
		PlayerIDs: []int64{8478402, 8479318},
		StartYear: 2024,
	}

	result, err := downloadPlayerGameLogsImpl(ctx, repos, input)

	assert.Error(t, err)
	assert.Equal(t, context.Canceled, err)
	assert.NotNil(t, result)
}

func TestSeasonToInt(t *testing.T) {
	tests := []struct {
		startYear int
		expected  int
	}{
		{2024, 20242025},
		{2023, 20232024},
		{1917, 19171918},
		{2000, 20002001},
	}

	for _, tc := range tests {
		season := nhl.NewSeason(tc.startYear)
		result := seasonToInt(season)
		assert.Equal(t, tc.expected, result, "Season %d-%d", tc.startYear, tc.startYear+1)
	}
}
