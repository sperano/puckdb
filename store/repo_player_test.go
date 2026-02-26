package store

import (
	"testing"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlayerRepo_SaveAndGetLanding(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewPlayerRepo(storage)

	playerID := nhl.PlayerID(8478402)
	data := []byte(`{"playerId": 8478402, "isActive": true, "position": "C"}`)

	err := repo.SaveLanding(playerID, data)
	require.NoError(t, err)

	assert.True(t, repo.LandingExists(playerID))

	landing, err := repo.GetLanding(playerID)
	require.NoError(t, err)
	assert.Equal(t, playerID, landing.PlayerID)
}

func TestPlayerRepo_GetLandingRaw(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewPlayerRepo(storage)

	playerID := nhl.PlayerID(8478402)
	data := []byte(`{"playerId": 8478402}`)

	repo.SaveLanding(playerID, data)

	got, err := repo.GetLandingRaw(playerID)
	require.NoError(t, err)
	assert.Equal(t, data, got)
}

func TestPlayerRepo_GetLandingInvalidJSON(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewPlayerRepo(storage)

	playerID := nhl.PlayerID(8478402)
	storage.SetFile(PlayerLandingPath(playerID), []byte(`not valid json`))

	_, err := repo.GetLanding(playerID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse player landing")
}

func TestPlayerRepo_MissingPlayer(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewPlayerRepo(storage)

	playerID := nhl.PlayerID(8478402)

	// Initially not missing
	assert.False(t, repo.IsMissing(playerID))

	// Mark as missing
	info := MissingPlayerLandingData{
		FirstName: "Connor",
		LastName:  "McDavid",
		Position:  "C",
	}
	err := repo.MarkMissing(playerID, info)
	require.NoError(t, err)

	// Now is missing
	assert.True(t, repo.IsMissing(playerID))

	// Can retrieve the info
	got, err := repo.GetMissing(playerID)
	require.NoError(t, err)
	assert.Equal(t, "Connor", got.FirstName)
	assert.Equal(t, "McDavid", got.LastName)
	assert.Equal(t, "C", got.Position)
}

func TestPlayerRepo_ListLandings(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewPlayerRepo(storage)

	// Create some player landing files
	storage.SetFile("players/player-8478402-landing.json", []byte(`{}`))
	storage.SetFile("players/player-8479318-landing.json", []byte(`{}`))
	storage.SetFile("players/player-8480800-landing.json", []byte(`{}`))
	storage.SetFile("players/other-file.json", []byte(`{}`)) // should be ignored

	ids, err := repo.ListLandings()
	require.NoError(t, err)

	assert.Len(t, ids, 3)
	assert.Contains(t, ids, nhl.PlayerID(8478402))
	assert.Contains(t, ids, nhl.PlayerID(8479318))
	assert.Contains(t, ids, nhl.PlayerID(8480800))
}

func TestPlayerRepo_SaveAndGetGameLog(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewPlayerRepo(storage)

	playerID := nhl.PlayerID(8478402)
	seasonID := 20242025
	gameType := 2
	data := []byte(`{"gameTypeId": 2, "gameLog": []}`)

	err := repo.SaveGameLog(playerID, seasonID, gameType, data)
	require.NoError(t, err)

	assert.True(t, repo.GameLogExists(playerID, seasonID, gameType))

	log, err := repo.GetGameLog(playerID, seasonID, gameType)
	require.NoError(t, err)
	assert.NotNil(t, log)
	assert.Equal(t, nhl.GameType(2), log.GameType)
}

func TestPlayerRepo_GetGameLogRaw(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewPlayerRepo(storage)

	playerID := nhl.PlayerID(8478402)
	seasonID := 20242025
	gameType := 2
	data := []byte(`{"seasonId": 20242025}`)

	repo.SaveGameLog(playerID, seasonID, gameType, data)

	got, err := repo.GetGameLogRaw(playerID, seasonID, gameType)
	require.NoError(t, err)
	assert.Equal(t, data, got)
}

func TestPlayerRepo_GetGameLogInvalidJSON(t *testing.T) {
	t.Parallel()
	storage := NewMemStorage()
	repo := NewPlayerRepo(storage)

	playerID := nhl.PlayerID(8478402)
	seasonID := 20242025
	gameType := 2
	storage.SetFile(PlayerGameLogPath(playerID, seasonID, gameType), []byte(`not valid json`))

	_, err := repo.GetGameLog(playerID, seasonID, gameType)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse game log")
}
