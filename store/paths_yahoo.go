package store

import (
	"fmt"
	"time"
)

// Note: YahooPlayerID type is defined in file_yahoo.go and will be kept during cleanup.

// LeaguePath returns the path for a Yahoo Fantasy league file.
// Example: seasons/2024/yahoo/12345/league/league.xml
func LeaguePath(season int, leagueID int) string {
	return fmt.Sprintf("seasons/%d/yahoo/%d/league/league.xml", season, leagueID)
}

// TeamPath returns the path for a Yahoo Fantasy team file.
// Example: seasons/2024/yahoo/12345/teams/team-01/team-01.xml
func TeamPath(season int, leagueID int, teamID int) string {
	return fmt.Sprintf("seasons/%d/yahoo/%d/teams/team-%02d/team-%02d.xml", season, leagueID, teamID, teamID)
}

// RosterPath returns the path for a Yahoo Fantasy roster file.
// Example: seasons/2024/yahoo/12345/rosters/team-01/rosters-01-2025-01-15.xml
func RosterPath(leagueID int, teamID int, date time.Time) string {
	season := DeduceSeason(date)
	return fmt.Sprintf("seasons/%d/yahoo/%d/rosters/team-%02d/rosters-%02d-%d-%02d-%02d.xml",
		season, leagueID, teamID, teamID, date.Year(), date.Month(), date.Day())
}

// TeamSummaryPath returns the path for a Yahoo Fantasy team summary file.
// Example: 2024/12345/summaries/team-01/team-01-summary-2025-01-15.xml
func TeamSummaryPath(leagueID int, teamID int, date time.Time) string {
	season := DeduceSeason(date)
	return fmt.Sprintf("seasons/%d/yahoo/%d/summaries/team-%02d/team-%02d-summary-%d-%02d-%02d.xml",
		season, leagueID, teamID, teamID, date.Year(), date.Month(), date.Day())
}

// YahooPlayerPath returns the path for a Yahoo Fantasy player HTML file.
// Example: yahoo-players/player-12345.html
func YahooPlayerPath(playerID YahooPlayerID) string {
	return fmt.Sprintf("yahoo-players/player-%d.html", playerID)
}

// MissingYahooPlayerPath returns the path for a missing Yahoo player marker.
// Example: yahoo-players-missing/player-12345.txt
func MissingYahooPlayerPath(playerID YahooPlayerID) string {
	return fmt.Sprintf("yahoo-players-missing/player-%d.txt", playerID)
}

// GameKeyPath returns the path for a Yahoo Fantasy game key file.
// Example: game-keys/gamekey-2024.xml
func GameKeyPath(season int) string {
	return fmt.Sprintf("game-keys/gamekey-%d.xml", season)
}

// YahooPlayersDir returns the directory containing Yahoo player files.
func YahooPlayersDir() string {
	return "yahoo-players"
}
