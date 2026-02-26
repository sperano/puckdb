package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSeasonRepo_SaveAndGetManifest(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewSeasonRepo(storage)

	// GetManifest expects a JSON array at the top level
	data := []byte(`[]`)

	err := repo.SaveManifest(data)
	require.NoError(t, err)

	assert.True(t, repo.ManifestExists())

	seasons, err := repo.GetManifest()
	require.NoError(t, err)
	assert.NotNil(t, seasons)
	assert.Empty(t, seasons)
}

func TestSeasonRepo_GetManifestRaw(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewSeasonRepo(storage)

	data := []byte(`[]`)

	repo.SaveManifest(data)

	got, err := repo.GetManifestRaw()
	require.NoError(t, err)
	assert.Equal(t, data, got)
}

func TestSeasonRepo_GetManifestInvalidJSON(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewSeasonRepo(storage)

	storage.SetFile(SeasonsManifestPath(), []byte(`not valid json`))

	_, err := repo.GetManifest()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse seasons manifest")
}

func TestSeasonRepo_SaveAndGetStandings(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewSeasonRepo(storage)

	seasonID := 20242025
	// GetStandings expects a JSON array at the top level
	data := []byte(`[]`)

	err := repo.SaveStandings(seasonID, data)
	require.NoError(t, err)

	assert.True(t, repo.StandingsExists(seasonID))

	standings, err := repo.GetStandings(seasonID)
	require.NoError(t, err)
	assert.NotNil(t, standings)
	assert.Empty(t, standings)
}

func TestSeasonRepo_GetStandingsRaw(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewSeasonRepo(storage)

	seasonID := 20242025
	data := []byte(`[]`)

	repo.SaveStandings(seasonID, data)

	got, err := repo.GetStandingsRaw(seasonID)
	require.NoError(t, err)
	assert.Equal(t, data, got)
}

func TestSeasonRepo_GetStandingsInvalidJSON(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewSeasonRepo(storage)

	seasonID := 20242025
	storage.SetFile(SeasonStandingsPath(seasonID), []byte(`not valid json`))

	_, err := repo.GetStandings(seasonID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse standings")
}

func TestSeasonRepo_StandingsExists(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewSeasonRepo(storage)

	seasonID := 20242025

	assert.False(t, repo.StandingsExists(seasonID))

	repo.SaveStandings(seasonID, []byte(`{}`))
	assert.True(t, repo.StandingsExists(seasonID))
}
