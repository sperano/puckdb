package store

import (
	"testing"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRepos(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repos := NewRepos(storage)

	assert.NotNil(t, repos.Schedule)
	assert.NotNil(t, repos.Boxscore)
	assert.NotNil(t, repos.PlayByPlay)
	assert.NotNil(t, repos.ShiftChart)
	assert.NotNil(t, repos.GameStory)
	assert.NotNil(t, repos.Player)
	assert.NotNil(t, repos.Franchise)
	assert.NotNil(t, repos.Season)
	assert.NotNil(t, repos.Yahoo)
	assert.Equal(t, storage, repos.Storage)
}

func TestNewFileRepos(t *testing.T) {
	t.Parallel()
	repos := NewFileRepos(t.TempDir())

	assert.NotNil(t, repos.Schedule)
	assert.NotNil(t, repos.Storage)
	_, ok := repos.Storage.(*FSStorage)
	assert.True(t, ok, "Storage should be FSStorage")
}

func TestNewMemRepos(t *testing.T) {
	t.Parallel()
	repos := NewMemRepos()

	assert.NotNil(t, repos.Schedule)
	assert.NotNil(t, repos.Storage)
	_, ok := repos.Storage.(*MemStorage)
	assert.True(t, ok, "Storage should be MemStorage")
}

func TestRepos_SharedStorage(t *testing.T) {
	t.Parallel()
	repos := NewMemRepos()

	// Write data via Schedule repo
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	scheduleData := []byte(`{"games": []}`)
	err := repos.Schedule.Save(date, scheduleData)
	require.NoError(t, err)

	// Write data via Boxscore repo
	gameID := nhl.GameID(2024020123)
	boxscoreData := []byte(`{"id": 2024020123}`)
	err = repos.Boxscore.Save(date, gameID, boxscoreData)
	require.NoError(t, err)

	// Verify both are accessible via underlying storage
	storage := repos.Storage.(*MemStorage)
	assert.True(t, storage.Has(DailySchedulePath(date)))
	assert.True(t, storage.Has(BoxscorePath(date, gameID)))
}

func TestRepos_IntegrationExample(t *testing.T) {
	t.Parallel()
	repos := NewMemRepos()

	// Simulate a workflow: save schedule, then boxscores for games in it
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	gameID1 := nhl.GameID(2024020100)
	gameID2 := nhl.GameID(2024020101)

	// Save schedule
	err := repos.Schedule.Save(date, []byte(`{"games": [{"id": 2024020100}, {"id": 2024020101}]}`))
	require.NoError(t, err)

	// Save boxscores
	err = repos.Boxscore.Save(date, gameID1, []byte(`{"id": 2024020100, "homeTeam": {"abbrev": "TOR"}}`))
	require.NoError(t, err)

	err = repos.Boxscore.Save(date, gameID2, []byte(`{"id": 2024020101, "homeTeam": {"abbrev": "MTL"}}`))
	require.NoError(t, err)

	// Verify all exists
	assert.True(t, repos.Schedule.Exists(date))
	assert.True(t, repos.Boxscore.Exists(date, gameID1))
	assert.True(t, repos.Boxscore.Exists(date, gameID2))
}
