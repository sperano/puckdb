package store

import (
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"
)

// ParseYahooPlayerFilename parses "player-123" into YahooPlayerFile{PlayerID: 123}
func ParseYahooPlayerFilename(name string) File {
	const prefix = "player-"
	if !strings.HasPrefix(name, prefix) {
		return nil
	}
	idStr := name[len(prefix):]
	id, err := strconv.Atoi(idStr)
	if err != nil {
		return nil
	}
	return YahooPlayerFile{PlayerID: id}
}

// ////////////////////////////////////////////////////////////////////////////
// LEAGUE
// ////////////////////////////////////////////////////////////////////////////

const leagueDirectory = "league"
const leagueFilename = leagueDirectory

// LeagueFile represents the stored Yahoo Fantasy league metadata.
type LeagueFile struct {
	Season   int
	LeagueID int
}

func (l LeagueFile) Dir() string  { return path.Join(strconv.Itoa(l.Season), strconv.Itoa(l.LeagueID), leagueDirectory) }
func (l LeagueFile) Name() string { return leagueFilename }
func (l LeagueFile) Ext() string  { return "xml" }

// ////////////////////////////////////////////////////////////////////////////
// TEAM
// ////////////////////////////////////////////////////////////////////////////

const teamFilenamePrefix = "team"
const teamsDirName = "teams"

func teamDir(season int, league int, teamID int) string {
	return path.Join(strconv.Itoa(season), strconv.Itoa(league), teamsDirName, fmt.Sprintf("%s-%02d", teamFilenamePrefix, teamID))
}

// TeamFile represents a stored Yahoo Fantasy team.
type TeamFile struct {
	Season   int
	LeagueID int
	TeamID   int
}

func (f TeamFile) Ext() string  { return "xml" }
func (f TeamFile) Dir() string  { return teamDir(f.Season, f.LeagueID, f.TeamID) }
func (f TeamFile) Name() string { return fmt.Sprintf("team-%02d", f.TeamID) }

// ////////////////////////////////////////////////////////////////////////////
// ROSTER
// ////////////////////////////////////////////////////////////////////////////

const rostersDirName = "rosters"

func rostersDir(season int, leagueID int, teamID int) string {
	return path.Join(strconv.Itoa(season), strconv.Itoa(leagueID), rostersDirName, fmt.Sprintf("team-%02d", teamID))
}

// RosterFile represents a stored Yahoo Fantasy team roster for a specific date.
type RosterFile struct {
	TeamID   int
	LeagueID int
	Date     time.Time
}

func (f RosterFile) Ext() string  { return "xml" }
func (f RosterFile) Dir() string  { return rostersDir(DeduceSeason(f.Date), f.LeagueID, f.TeamID) }
func (f RosterFile) Name() string {
	return fmt.Sprintf("rosters-%02d-%d-%02d-%02d", f.TeamID, f.Date.Year(), f.Date.Month(), f.Date.Day())
}

// ////////////////////////////////////////////////////////////////////////////
// TEAM SUMMARY
// ////////////////////////////////////////////////////////////////////////////

const teamSummariesDirName = "summaries"

func teamSummaryDir(season int, leagueID int, teamID int) string {
	return path.Join(strconv.Itoa(season), strconv.Itoa(leagueID), teamSummariesDirName, fmt.Sprintf("team-%02d", teamID))
}

// TeamSummaryFile represents a stored Yahoo Fantasy team summary for a specific date.
type TeamSummaryFile struct {
	TeamID   int
	LeagueID int
	Date     time.Time
}

func (f TeamSummaryFile) Ext() string { return "xml" }
func (f TeamSummaryFile) Dir() string { return teamSummaryDir(DeduceSeason(f.Date), f.LeagueID, f.TeamID) }
func (f TeamSummaryFile) Name() string {
	return fmt.Sprintf("team-%02d-summary-%4d-%02d-%02d", f.TeamID, f.Date.Year(), f.Date.Month(), f.Date.Day())
}

// ////////////////////////////////////////////////////////////////////////////
// YAHOO PLAYER
// ////////////////////////////////////////////////////////////////////////////

const yahooPlayersDirName = "yahoo-players"

// YahooPlayerFile represents a stored Yahoo Fantasy player HTML page.
type YahooPlayerFile struct {
	PlayerID int
}

func (f YahooPlayerFile) Ext() string  { return "html" }
func (f YahooPlayerFile) Dir() string  { return yahooPlayersDirName }
func (f YahooPlayerFile) Name() string { return fmt.Sprintf("player-%d", f.PlayerID) }

// ////////////////////////////////////////////////////////////////////////////
// MISSING YAHOO PLAYER
// ////////////////////////////////////////////////////////////////////////////

const missingYahooPlayersDirName = "yahoo-players-missing"

// MissingYahooPlayerFile represents a stored 404/missing Yahoo Fantasy player.
type MissingYahooPlayerFile struct {
	PlayerID int
}

func (f MissingYahooPlayerFile) Ext() string  { return "txt" }
func (f MissingYahooPlayerFile) Dir() string  { return missingYahooPlayersDirName }
func (f MissingYahooPlayerFile) Name() string { return fmt.Sprintf("player-%d", f.PlayerID) }

// ////////////////////////////////////////////////////////////////////////////
// GAME KEY
// ////////////////////////////////////////////////////////////////////////////

const gameKeysDirName = "game-keys"

// GameKeyFile represents a stored Yahoo Fantasy game key for a season.
// Game keys are unique identifiers for a sport+season combination in Yahoo Fantasy.
type GameKeyFile struct {
	Season int
}

func (f GameKeyFile) Ext() string  { return "xml" }
func (f GameKeyFile) Dir() string  { return gameKeysDirName }
func (f GameKeyFile) Name() string { return fmt.Sprintf("gamekey-%d", f.Season) }
