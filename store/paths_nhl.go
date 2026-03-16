package store

import (
	"fmt"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
)

// Note: DeduceSeason is defined in file_nhl.go and will be kept during cleanup.

// gamesDir returns the directory path for game data on a specific date.
// Format: {season}/games/{year}/{month}/{day}
func gamesDir(date time.Time) string {
	season := DeduceSeason(date)
	return fmt.Sprintf("seasons/%d/games/%d/%02d/%02d", season, date.Year(), date.Month(), date.Day())
}

// DailySchedulePath returns the path for a daily schedule file.
// Example: seasons/2024/games/2025/01/15/daily-schedule-2025-01-15.json
func DailySchedulePath(date time.Time) string {
	return fmt.Sprintf("%s/daily-schedule-%d-%02d-%02d.json",
		gamesDir(date), date.Year(), date.Month(), date.Day())
}

// BoxscorePath returns the path for a boxscore file.
// Example: seasons/2024/games/2025/01/15/boxscore-2024020123.json
func BoxscorePath(date time.Time, gameID nhl.GameID) string {
	return fmt.Sprintf("%s/boxscore-%s.json", gamesDir(date), gameID.String())
}

// PlayByPlayPath returns the path for a play-by-play file.
// Example: seasons/2024/games/2025/01/15/playbyplay-2024020123.json
func PlayByPlayPath(date time.Time, gameID nhl.GameID) string {
	return fmt.Sprintf("%s/playbyplay-%s.json", gamesDir(date), gameID.String())
}

// ShiftChartPath returns the path for a shift chart file.
// Example: seasons/2024/games/2025/01/15/shiftchart-2024020123.json
func ShiftChartPath(date time.Time, gameID nhl.GameID) string {
	return fmt.Sprintf("%s/shiftchart-%s.json", gamesDir(date), gameID.String())
}

// GameStoryPath returns the path for a game story file.
// Example: seasons/2024/games/2025/01/15/gamestory-2024020123.json
func GameStoryPath(date time.Time, gameID nhl.GameID) string {
	return fmt.Sprintf("%s/gamestory-%s.json", gamesDir(date), gameID.String())
}

// SeasonSeriesPath returns the path for a season series file.
// Example: seasons/2024/games/2025/01/15/seasonseries-2024020123.json
func SeasonSeriesPath(date time.Time, gameID nhl.GameID) string {
	return fmt.Sprintf("%s/seasonseries-%s.json", gamesDir(date), gameID.String())
}

// PlayerLandingPath returns the path for a player landing page file.
// Example: players/player-8478402-landing.json
func PlayerLandingPath(playerID nhl.PlayerID) string {
	return fmt.Sprintf("players/player-%s-landing.json", playerID.String())
}

// MissingPlayerLandingPath returns the path for a missing player landing marker.
// Example: players-missing/player-8478402.json
func MissingPlayerLandingPath(playerID nhl.PlayerID) string {
	return fmt.Sprintf("players-missing/player-%s.json", playerID.String())
}

// FranchisesPath returns the path for the franchises file.
// This is a singleton file.
func FranchisesPath() string {
	return "nhl/franchises.json"
}

// SeasonsManifestPath returns the path for the seasons manifest file.
// This is a singleton file.
func SeasonsManifestPath() string {
	return "nhl/seasons-manifest.json"
}

// SeasonStandingsPath returns the path for a season standings file.
// Example: nhl/standings/standings-20242025.json
func SeasonStandingsPath(seasonID int) string {
	return fmt.Sprintf("nhl/standings/standings-%d.json", seasonID)
}

// PlayerGameLogPath returns the path for a player game log file.
// Example: 20242025/player-gamelogs/player-8478402-2.json
func PlayerGameLogPath(playerID nhl.PlayerID, seasonID int, gameType int) string {
	return fmt.Sprintf("%d/player-gamelogs/player-%s-%d.json", seasonID, playerID.String(), gameType)
}

// PlayerGameLogDir returns the directory for player game logs for a season.
// Example: 20242025/player-gamelogs
func PlayerGameLogDir(seasonID int) string {
	return fmt.Sprintf("%d/player-gamelogs", seasonID)
}
