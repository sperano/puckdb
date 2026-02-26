package store

import (
	"testing"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBoxscoreRepo_SaveAndGet(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewBoxscoreRepo(storage)

	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	gameID := nhl.GameID(2024020123)
	data := []byte(`{"id": 2024020123, "gameType": 2, "gameDate": "2025-01-15"}`)

	// Save boxscore
	err := repo.Save(date, gameID, data)
	require.NoError(t, err)

	// Verify it was stored at the correct path
	expectedPath := BoxscorePath(date, gameID)
	assert.True(t, storage.Has(expectedPath))

	// Get should return parsed boxscore
	boxscore, err := repo.Get(date, gameID)
	require.NoError(t, err)
	assert.Equal(t, gameID, boxscore.ID)
	assert.Equal(t, nhl.GameType(2), boxscore.GameType)
}

func TestBoxscoreRepo_GetRaw(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewBoxscoreRepo(storage)

	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	gameID := nhl.GameID(2024020123)
	data := []byte(`{"id": 2024020123}`)

	repo.Save(date, gameID, data)

	// GetRaw should return the exact bytes
	got, err := repo.GetRaw(date, gameID)
	require.NoError(t, err)
	assert.Equal(t, data, got)
}

func TestBoxscoreRepo_GetInvalidJSON(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewBoxscoreRepo(storage)

	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	gameID := nhl.GameID(2024020123)
	storage.SetFile(BoxscorePath(date, gameID), []byte(`not valid json`))

	_, err := repo.Get(date, gameID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse boxscore")
}

func TestBoxscoreRepo_Exists(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewBoxscoreRepo(storage)

	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	gameID := nhl.GameID(2024020123)

	// Initially does not exist
	assert.False(t, repo.Exists(date, gameID))

	// After saving, exists
	repo.Save(date, gameID, []byte(`{}`))
	assert.True(t, repo.Exists(date, gameID))
}

func TestBoxscoreRepo_GetNonExistent(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewBoxscoreRepo(storage)

	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	gameID := nhl.GameID(2024020123)

	_, err := repo.Get(date, gameID)
	assert.Error(t, err)
}
