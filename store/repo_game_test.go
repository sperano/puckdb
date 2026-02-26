package store

import (
	"testing"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlayByPlayRepo_SaveAndGet(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewPlayByPlayRepo(storage)

	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	gameID := nhl.GameID(2024020123)
	data := []byte(`{"id": 2024020123, "plays": []}`)

	err := repo.Save(date, gameID, data)
	require.NoError(t, err)

	assert.True(t, repo.Exists(date, gameID))

	pbp, err := repo.Get(date, gameID)
	require.NoError(t, err)
	assert.Equal(t, gameID, pbp.ID)
}

func TestPlayByPlayRepo_GetRaw(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewPlayByPlayRepo(storage)

	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	gameID := nhl.GameID(2024020123)
	data := []byte(`{"id": 2024020123}`)

	repo.Save(date, gameID, data)

	got, err := repo.GetRaw(date, gameID)
	require.NoError(t, err)
	assert.Equal(t, data, got)
}

func TestPlayByPlayRepo_GetInvalidJSON(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewPlayByPlayRepo(storage)

	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	gameID := nhl.GameID(2024020123)
	storage.SetFile(PlayByPlayPath(date, gameID), []byte(`not valid json`))

	_, err := repo.Get(date, gameID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse play-by-play")
}

func TestShiftChartRepo_SaveAndGet(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewShiftChartRepo(storage)

	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	gameID := nhl.GameID(2024020123)
	data := []byte(`{"data": []}`)

	err := repo.Save(date, gameID, data)
	require.NoError(t, err)

	assert.True(t, repo.Exists(date, gameID))

	chart, err := repo.Get(date, gameID)
	require.NoError(t, err)
	assert.NotNil(t, chart)
	assert.Empty(t, chart.Data)
}

func TestShiftChartRepo_GetRaw(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewShiftChartRepo(storage)

	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	gameID := nhl.GameID(2024020123)
	data := []byte(`{"id": 2024020123}`)

	repo.Save(date, gameID, data)

	got, err := repo.GetRaw(date, gameID)
	require.NoError(t, err)
	assert.Equal(t, data, got)
}

func TestShiftChartRepo_GetInvalidJSON(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewShiftChartRepo(storage)

	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	gameID := nhl.GameID(2024020123)
	storage.SetFile(ShiftChartPath(date, gameID), []byte(`not valid json`))

	_, err := repo.Get(date, gameID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse shift chart")
}

func TestGameStoryRepo_SaveAndGet(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewGameStoryRepo(storage)

	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	gameID := nhl.GameID(2024020123)
	data := []byte(`{"id": 2024020123}`)

	err := repo.Save(date, gameID, data)
	require.NoError(t, err)

	assert.True(t, repo.Exists(date, gameID))

	story, err := repo.Get(date, gameID)
	require.NoError(t, err)
	assert.Equal(t, gameID, story.ID)
}

func TestGameStoryRepo_GetRaw(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewGameStoryRepo(storage)

	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	gameID := nhl.GameID(2024020123)
	data := []byte(`{"id": 2024020123}`)

	repo.Save(date, gameID, data)

	got, err := repo.GetRaw(date, gameID)
	require.NoError(t, err)
	assert.Equal(t, data, got)
}

func TestGameStoryRepo_GetInvalidJSON(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewGameStoryRepo(storage)

	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	gameID := nhl.GameID(2024020123)
	storage.SetFile(GameStoryPath(date, gameID), []byte(`not valid json`))

	_, err := repo.Get(date, gameID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse game story")
}
