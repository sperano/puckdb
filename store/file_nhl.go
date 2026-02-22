package store

import (
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
)

// DeduceSeason returns the season year for a given date.
// NHL seasons span two calendar years (e.g., 2024-2025 season starts in Sept 2024).
// Dates from Sept-Dec return the current year, Jan-Aug return previous year.
func DeduceSeason(day time.Time) int {
	if day.Month() >= time.September {
		return day.Year()
	}
	return day.Year() - 1
}

// ParsePlayerLandingFilename parses "player-8478402-landing" into PlayerLandingFile
func ParsePlayerLandingFilename(name string) File {
	const prefix = "player-"
	const suffix = "-landing"
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
		return nil
	}
	idStr := name[len(prefix) : len(name)-len(suffix)]
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return nil
	}
	return PlayerLandingFile{PlayerID: nhl.PlayerID(id)}
}

const gamesDirName = "games"

func gamesListDir(date time.Time) string {
	return fmt.Sprintf("%d/%s/%4d/%02d/%02d",
		DeduceSeason(date), gamesDirName, date.Year(), date.Month(), date.Day())
}

// ////////////////////////////////////////////////////////////////////////////
// DAILY SCHEDULE
// ////////////////////////////////////////////////////////////////////////////

// DailyScheduleFile represents the stored NHL daily schedule for a specific date.
type DailyScheduleFile struct {
	Date time.Time
}

func (f DailyScheduleFile) Ext() string  { return "json" }
func (f DailyScheduleFile) Dir() string  { return gamesListDir(f.Date) }
func (f DailyScheduleFile) Name() string {
	return fmt.Sprintf("daily-schedule-%4d-%02d-%02d", f.Date.Year(), f.Date.Month(), f.Date.Day())
}

// ////////////////////////////////////////////////////////////////////////////
// BOXSCORE
// ////////////////////////////////////////////////////////////////////////////

// BoxscoreFile represents the stored NHL boxscore for a specific game.
type BoxscoreFile struct {
	Date   time.Time
	GameID nhl.GameID
}

func (f BoxscoreFile) Ext() string  { return "json" }
func (f BoxscoreFile) Dir() string  { return gamesListDir(f.Date) }
func (f BoxscoreFile) Name() string { return fmt.Sprintf("boxscore-%s", f.GameID.String()) }

// ////////////////////////////////////////////////////////////////////////////
// PLAYER LANDING
// ////////////////////////////////////////////////////////////////////////////

const playerLandingDirName = "players"

// PlayerLandingFile represents the stored NHL player landing page data.
type PlayerLandingFile struct {
	PlayerID nhl.PlayerID
}

func (f PlayerLandingFile) Ext() string  { return "json" }
func (f PlayerLandingFile) Dir() string  { return playerLandingDirName }
func (f PlayerLandingFile) Name() string { return fmt.Sprintf("player-%s-landing", f.PlayerID.String()) }

// ////////////////////////////////////////////////////////////////////////////
// MISSING PLAYER LANDING
// ////////////////////////////////////////////////////////////////////////////

const missingPlayerLandingDirName = "players-missing"

// MissingPlayerLandingFile represents a stored 404 response for a player landing page.
// The content is JSON with basic player info extracted from boxscores.
type MissingPlayerLandingFile struct {
	PlayerID nhl.PlayerID
}

func (f MissingPlayerLandingFile) Ext() string  { return "json" }
func (f MissingPlayerLandingFile) Dir() string  { return missingPlayerLandingDirName }
func (f MissingPlayerLandingFile) Name() string { return fmt.Sprintf("player-%s", f.PlayerID.String()) }

// MissingPlayerLandingData holds the minimal player info stored when a 404 is cached.
// This data is extracted from boxscore appearances.
type MissingPlayerLandingData struct {
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Position  string `json:"position"`
}

// ////////////////////////////////////////////////////////////////////////////
// FRANCHISES
// ////////////////////////////////////////////////////////////////////////////

const franchisesDirName = "nhl"
const franchisesFilename = "franchises"

// FranchisesFile represents the stored list of all NHL franchises (historical and current).
// This is a singleton file since the franchise list is static.
type FranchisesFile struct{}

func (f FranchisesFile) Ext() string  { return "json" }
func (f FranchisesFile) Dir() string  { return franchisesDirName }
func (f FranchisesFile) Name() string { return franchisesFilename }

// ////////////////////////////////////////////////////////////////////////////
// SEASONS MANIFEST
// ////////////////////////////////////////////////////////////////////////////

const seasonsManifestFilename = "seasons-manifest"

// SeasonsManifestFile represents the stored list of all NHL seasons.
// This is a singleton file containing season metadata (dates, IDs).
type SeasonsManifestFile struct{}

func (f SeasonsManifestFile) Ext() string  { return "json" }
func (f SeasonsManifestFile) Dir() string  { return franchisesDirName }
func (f SeasonsManifestFile) Name() string { return seasonsManifestFilename }

// ////////////////////////////////////////////////////////////////////////////
// SEASON STANDINGS
// ////////////////////////////////////////////////////////////////////////////

const standingsDirName = "standings"

// SeasonStandingsFile represents the stored standings for a specific season.
// Contains team alignments (division, conference) for that season.
type SeasonStandingsFile struct {
	SeasonID int // e.g., 20232024
}

func (f SeasonStandingsFile) Ext() string  { return "json" }
func (f SeasonStandingsFile) Dir() string  { return path.Join(franchisesDirName, standingsDirName) }
func (f SeasonStandingsFile) Name() string { return fmt.Sprintf("standings-%d", f.SeasonID) }

// ////////////////////////////////////////////////////////////////////////////
// PLAY-BY-PLAY
// ////////////////////////////////////////////////////////////////////////////

// PlayByPlayFile represents the stored NHL play-by-play data for a specific game.
// Contains every event in the game (shots, hits, faceoffs, penalties, goals).
type PlayByPlayFile struct {
	Date   time.Time
	GameID nhl.GameID
}

func (f PlayByPlayFile) Ext() string  { return "json" }
func (f PlayByPlayFile) Dir() string  { return gamesListDir(f.Date) }
func (f PlayByPlayFile) Name() string { return fmt.Sprintf("playbyplay-%s", f.GameID.String()) }

// ////////////////////////////////////////////////////////////////////////////
// SHIFT CHART
// ////////////////////////////////////////////////////////////////////////////

// ShiftChartFile represents the stored NHL shift chart data for a specific game.
// Contains player shift/TOI data (ice time per shift, line combinations).
type ShiftChartFile struct {
	Date   time.Time
	GameID nhl.GameID
}

func (f ShiftChartFile) Ext() string  { return "json" }
func (f ShiftChartFile) Dir() string  { return gamesListDir(f.Date) }
func (f ShiftChartFile) Name() string { return fmt.Sprintf("shiftchart-%s", f.GameID.String()) }

// ////////////////////////////////////////////////////////////////////////////
// GAME STORY
// ////////////////////////////////////////////////////////////////////////////

// GameStoryFile represents the stored NHL game story for a specific game.
// Contains three stars, goal summaries with highlight clips, penalty details, and shootout info.
type GameStoryFile struct {
	Date   time.Time
	GameID nhl.GameID
}

func (f GameStoryFile) Ext() string  { return "json" }
func (f GameStoryFile) Dir() string  { return gamesListDir(f.Date) }
func (f GameStoryFile) Name() string { return fmt.Sprintf("gamestory-%s", f.GameID.String()) }

// ////////////////////////////////////////////////////////////////////////////
// PLAYER GAME LOG
// ////////////////////////////////////////////////////////////////////////////

const playerGameLogDirName = "player-gamelogs"

// PlayerGameLogFile represents the stored game log for a player in a specific season.
// Contains per-game stats for the player.
type PlayerGameLogFile struct {
	PlayerID nhl.PlayerID
	Season   int // e.g., 20242025
	GameType int // 2 = regular season, 3 = playoffs
}

func (f PlayerGameLogFile) Ext() string { return "json" }
func (f PlayerGameLogFile) Dir() string { return playerGameLogDirName }
func (f PlayerGameLogFile) Name() string {
	return fmt.Sprintf("player-%s-%d-%d", f.PlayerID.String(), f.Season, f.GameType)
}

// ParsePlayerGameLogFilename parses a filename like "player-8478402-20232024-2" into a PlayerGameLogFile.
func ParsePlayerGameLogFilename(name string) File {
	const prefix = "player-"
	if !strings.HasPrefix(name, prefix) {
		return nil
	}
	// Format: player-{playerID}-{season}-{gameType}
	parts := strings.Split(name[len(prefix):], "-")
	if len(parts) != 3 {
		return nil
	}
	playerID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return nil
	}
	season, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil
	}
	gameType, err := strconv.Atoi(parts[2])
	if err != nil {
		return nil
	}
	return PlayerGameLogFile{
		PlayerID: nhl.PlayerID(playerID),
		Season:   season,
		GameType: gameType,
	}
}
