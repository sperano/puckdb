package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScheduleRepo_SaveAndGet(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewScheduleRepo(storage)

	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	data := []byte(`{"date": "2025-01-15", "games": [], "numberOfGames": 0}`)

	// Save schedule
	err := repo.Save(date, data)
	require.NoError(t, err)

	// Verify it was stored at the correct path
	expectedPath := DailySchedulePath(date)
	assert.True(t, storage.Has(expectedPath))
	assert.Equal(t, data, storage.Get(expectedPath))

	// Get should return parsed schedule
	schedule, err := repo.Get(date)
	require.NoError(t, err)
	assert.NotNil(t, schedule)
	assert.Equal(t, "2025-01-15", schedule.Date)
}

func TestScheduleRepo_GetRaw(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewScheduleRepo(storage)

	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	data := []byte(`{"gameWeek": []}`)

	repo.Save(date, data)

	// GetRaw should return the exact bytes
	got, err := repo.GetRaw(date)
	require.NoError(t, err)
	assert.Equal(t, data, got)
}

func TestScheduleRepo_GetInvalidJSON(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewScheduleRepo(storage)

	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	storage.SetFile(DailySchedulePath(date), []byte(`not valid json`))

	_, err := repo.Get(date)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse daily schedule")
}

func TestScheduleRepo_Exists(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewScheduleRepo(storage)

	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)

	// Initially does not exist
	assert.False(t, repo.Exists(date))

	// After saving, exists
	repo.Save(date, []byte(`{}`))
	assert.True(t, repo.Exists(date))
}

func TestScheduleRepo_GetNonExistent(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewScheduleRepo(storage)

	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)

	_, err := repo.Get(date)
	assert.Error(t, err)
}
