package store

import (
	"testing"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/stretchr/testify/assert"
)

func TestDailySchedulePath(t *testing.T) {
	t.Parallel()

	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	expected := "seasons/2024/games/2025/01/15/daily-schedule-2025-01-15.json"

	assert.Equal(t, expected, DailySchedulePath(date))
}

func TestBoxscorePath(t *testing.T) {
	t.Parallel()

	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	gameID := nhl.GameID(2024020123)
	expected := "seasons/2024/games/2025/01/15/boxscore-2024020123.json"

	assert.Equal(t, expected, BoxscorePath(date, gameID))
}

func TestPlayByPlayPath(t *testing.T) {
	t.Parallel()

	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	gameID := nhl.GameID(2024020123)
	expected := "seasons/2024/games/2025/01/15/playbyplay-2024020123.json"

	assert.Equal(t, expected, PlayByPlayPath(date, gameID))
}

func TestShiftChartPath(t *testing.T) {
	t.Parallel()

	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	gameID := nhl.GameID(2024020123)
	expected := "seasons/2024/games/2025/01/15/shiftchart-2024020123.json"

	assert.Equal(t, expected, ShiftChartPath(date, gameID))
}

func TestGameStoryPath(t *testing.T) {
	t.Parallel()

	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	gameID := nhl.GameID(2024020123)
	expected := "seasons/2024/games/2025/01/15/gamestory-2024020123.json"

	assert.Equal(t, expected, GameStoryPath(date, gameID))
}

func TestPlayerLandingPath(t *testing.T) {
	t.Parallel()

	playerID := nhl.PlayerID(8478402)
	expected := "players/player-8478402-landing.json"

	assert.Equal(t, expected, PlayerLandingPath(playerID))
}

func TestMissingPlayerLandingPath(t *testing.T) {
	t.Parallel()

	playerID := nhl.PlayerID(8478402)
	expected := "players-missing/player-8478402.json"

	assert.Equal(t, expected, MissingPlayerLandingPath(playerID))
}

func TestFranchisesPath(t *testing.T) {
	t.Parallel()

	expected := "nhl/franchises.json"
	assert.Equal(t, expected, FranchisesPath())
}

func TestSeasonsManifestPath(t *testing.T) {
	t.Parallel()

	expected := "nhl/seasons-manifest.json"
	assert.Equal(t, expected, SeasonsManifestPath())
}

func TestSeasonStandingsPath(t *testing.T) {
	t.Parallel()

	seasonID := 20242025
	expected := "nhl/standings/standings-20242025.json"

	assert.Equal(t, expected, SeasonStandingsPath(seasonID))
}

func TestPlayerGameLogPath(t *testing.T) {
	t.Parallel()

	playerID := nhl.PlayerID(8478402)
	seasonID := 20242025
	gameType := 2
	expected := "20242025/player-gamelogs/player-8478402-2.json"

	assert.Equal(t, expected, PlayerGameLogPath(playerID, seasonID, gameType))
}

func TestPlayerGameLogDir(t *testing.T) {
	t.Parallel()

	seasonID := 20242025
	expected := "20242025/player-gamelogs"

	assert.Equal(t, expected, PlayerGameLogDir(seasonID))
}

// TestGamesDir verifies the internal gamesDir helper
func TestGamesDir(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		date     time.Time
		expected string
	}{
		{
			name:     "January date uses previous year as season",
			date:     time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC),
			expected: "seasons/2024/games/2025/01/15",
		},
		{
			name:     "October date uses current year as season",
			date:     time.Date(2024, 10, 15, 0, 0, 0, 0, time.UTC),
			expected: "seasons/2024/games/2024/10/15",
		},
		{
			name:     "Single digit month and day are zero-padded",
			date:     time.Date(2025, 2, 5, 0, 0, 0, 0, time.UTC),
			expected: "seasons/2024/games/2025/02/05",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, gamesDir(tt.date))
		})
	}
}
