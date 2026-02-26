package store

import (
	"encoding/xml"
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// YahooRepo provides access to Yahoo Fantasy data.
type YahooRepo struct {
	storage Storage
}

// NewYahooRepo creates a new YahooRepo.
func NewYahooRepo(storage Storage) *YahooRepo {
	return &YahooRepo{storage: storage}
}

// ============================================================================
// LEAGUE
// ============================================================================

// GetLeague returns the parsed league data.
func (r *YahooRepo) GetLeague(season int, leagueID int) (*FantasyContent, error) {
	data, err := r.storage.Read(LeaguePath(season, leagueID))
	if err != nil {
		return nil, err
	}

	var content FantasyContent
	if err := xml.Unmarshal(data, &content); err != nil {
		return nil, fmt.Errorf("parse league %d/%d: %w", season, leagueID, err)
	}

	return &content, nil
}

// GetLeagueRaw returns the raw league XML data.
func (r *YahooRepo) GetLeagueRaw(season int, leagueID int) ([]byte, error) {
	return r.storage.Read(LeaguePath(season, leagueID))
}

// SaveLeague stores league data.
func (r *YahooRepo) SaveLeague(season int, leagueID int, data []byte) error {
	return r.storage.Write(LeaguePath(season, leagueID), data)
}

// LeagueExists returns true if league data exists.
func (r *YahooRepo) LeagueExists(season int, leagueID int) bool {
	return r.storage.Exists(LeaguePath(season, leagueID))
}

// ============================================================================
// TEAM
// ============================================================================

// GetTeam returns the parsed team data.
func (r *YahooRepo) GetTeam(season int, leagueID int, teamID int) (*FantasyContent, error) {
	data, err := r.storage.Read(TeamPath(season, leagueID, teamID))
	if err != nil {
		return nil, err
	}

	var content FantasyContent
	if err := xml.Unmarshal(data, &content); err != nil {
		return nil, fmt.Errorf("parse team %d/%d/%d: %w", season, leagueID, teamID, err)
	}

	return &content, nil
}

// GetTeamRaw returns the raw team XML data.
func (r *YahooRepo) GetTeamRaw(season int, leagueID int, teamID int) ([]byte, error) {
	return r.storage.Read(TeamPath(season, leagueID, teamID))
}

// SaveTeam stores team data.
func (r *YahooRepo) SaveTeam(season int, leagueID int, teamID int, data []byte) error {
	return r.storage.Write(TeamPath(season, leagueID, teamID), data)
}

// TeamExists returns true if team data exists.
func (r *YahooRepo) TeamExists(season int, leagueID int, teamID int) bool {
	return r.storage.Exists(TeamPath(season, leagueID, teamID))
}

// ============================================================================
// ROSTER
// ============================================================================

// GetRoster returns the parsed roster data.
func (r *YahooRepo) GetRoster(leagueID int, teamID int, date time.Time) (*FantasyContent, error) {
	data, err := r.storage.Read(RosterPath(leagueID, teamID, date))
	if err != nil {
		return nil, err
	}

	var content FantasyContent
	if err := xml.Unmarshal(data, &content); err != nil {
		return nil, fmt.Errorf("parse roster %d/%d for %s: %w", leagueID, teamID, date.Format("2006-01-02"), err)
	}

	return &content, nil
}

// GetRosterRaw returns the raw roster XML data.
func (r *YahooRepo) GetRosterRaw(leagueID int, teamID int, date time.Time) ([]byte, error) {
	return r.storage.Read(RosterPath(leagueID, teamID, date))
}

// SaveRoster stores roster data.
func (r *YahooRepo) SaveRoster(leagueID int, teamID int, date time.Time, data []byte) error {
	return r.storage.Write(RosterPath(leagueID, teamID, date), data)
}

// RosterExists returns true if roster data exists.
func (r *YahooRepo) RosterExists(leagueID int, teamID int, date time.Time) bool {
	return r.storage.Exists(RosterPath(leagueID, teamID, date))
}

// ============================================================================
// TEAM SUMMARY
// ============================================================================

// GetTeamSummary returns the parsed team summary data.
func (r *YahooRepo) GetTeamSummary(leagueID int, teamID int, date time.Time) (*FantasyContent, error) {
	data, err := r.storage.Read(TeamSummaryPath(leagueID, teamID, date))
	if err != nil {
		return nil, err
	}

	var content FantasyContent
	if err := xml.Unmarshal(data, &content); err != nil {
		return nil, fmt.Errorf("parse team summary %d/%d for %s: %w", leagueID, teamID, date.Format("2006-01-02"), err)
	}

	return &content, nil
}

// GetTeamSummaryRaw returns the raw team summary XML data.
func (r *YahooRepo) GetTeamSummaryRaw(leagueID int, teamID int, date time.Time) ([]byte, error) {
	return r.storage.Read(TeamSummaryPath(leagueID, teamID, date))
}

// SaveTeamSummary stores team summary data.
func (r *YahooRepo) SaveTeamSummary(leagueID int, teamID int, date time.Time, data []byte) error {
	return r.storage.Write(TeamSummaryPath(leagueID, teamID, date), data)
}

// TeamSummaryExists returns true if team summary data exists.
func (r *YahooRepo) TeamSummaryExists(leagueID int, teamID int, date time.Time) bool {
	return r.storage.Exists(TeamSummaryPath(leagueID, teamID, date))
}

// ============================================================================
// YAHOO PLAYER (HTML)
// ============================================================================

// GetPlayer returns the raw player HTML data.
// Note: Returns raw bytes since Yahoo player pages are HTML, not XML.
func (r *YahooRepo) GetPlayer(playerID YahooPlayerID) ([]byte, error) {
	return r.storage.Read(YahooPlayerPath(playerID))
}

// SavePlayer stores player HTML data.
func (r *YahooRepo) SavePlayer(playerID YahooPlayerID, data []byte) error {
	return r.storage.Write(YahooPlayerPath(playerID), data)
}

// PlayerExists returns true if player data exists.
func (r *YahooRepo) PlayerExists(playerID YahooPlayerID) bool {
	return r.storage.Exists(YahooPlayerPath(playerID))
}

// IsPlayerMissing returns true if the player is marked as missing (404 cached).
func (r *YahooRepo) IsPlayerMissing(playerID YahooPlayerID) bool {
	return r.storage.Exists(MissingYahooPlayerPath(playerID))
}

// MarkPlayerMissing saves a missing player marker.
func (r *YahooRepo) MarkPlayerMissing(playerID YahooPlayerID) error {
	return r.storage.Write(MissingYahooPlayerPath(playerID), []byte("missing"))
}

// ListPlayers returns all Yahoo player IDs that have data stored.
func (r *YahooRepo) ListPlayers() ([]YahooPlayerID, error) {
	names, err := r.storage.List(YahooPlayersDir(), "html")
	if err != nil {
		return nil, err
	}

	pattern := regexp.MustCompile(`^player-(\d+)$`)
	var ids []YahooPlayerID

	for _, name := range names {
		if m := pattern.FindStringSubmatch(name); m != nil {
			id, _ := strconv.Atoi(m[1])
			ids = append(ids, YahooPlayerID(id))
		}
	}

	return ids, nil
}

// ============================================================================
// GAME KEY
// ============================================================================

// GetGameKey returns the parsed game key data.
func (r *YahooRepo) GetGameKey(season int) (*FantasyContent, error) {
	data, err := r.storage.Read(GameKeyPath(season))
	if err != nil {
		return nil, err
	}

	var content FantasyContent
	if err := xml.Unmarshal(data, &content); err != nil {
		return nil, fmt.Errorf("parse game key for season %d: %w", season, err)
	}

	return &content, nil
}

// GetGameKeyRaw returns the raw game key XML data.
func (r *YahooRepo) GetGameKeyRaw(season int) ([]byte, error) {
	return r.storage.Read(GameKeyPath(season))
}

// SaveGameKey stores game key data.
func (r *YahooRepo) SaveGameKey(season int, data []byte) error {
	return r.storage.Write(GameKeyPath(season), data)
}

// GameKeyExists returns true if game key data exists.
func (r *YahooRepo) GameKeyExists(season int) bool {
	return r.storage.Exists(GameKeyPath(season))
}
