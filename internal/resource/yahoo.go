package resource

import (
	"encoding/xml"
	"fmt"
	"strings"
	"time"

	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/store"
)

const (
	baseYahooAPIURL    = "https://fantasysports.yahooapis.com/fantasy/v2"
	baseYahooSportsURL = "https://sports.yahoo.com"
	yahooNHLGameCode   = "nhl"

	// YahooPlayersDir is the directory containing Yahoo player HTML files.
	YahooPlayersDir = "yahoo-players"
	// YahooPlayersMissingDir is the directory containing missing Yahoo player markers.
	YahooPlayersMissingDir = "yahoo-players-missing"
)

// YahooPlayerID is the unique identifier Yahoo assigns to a player.
// This is distinct from nhl.PlayerID to prevent accidental mixing of ID namespaces.
type YahooPlayerID = store.YahooPlayerID

// League represents a Yahoo Fantasy league resource.
type League struct {
	Season   int
	LeagueID int
	GameKey  int // Required for URL, not for path
}

func (l League) Path() string {
	return fmt.Sprintf("seasons/%d/yahoo/%d/league/league.xml", l.Season, l.LeagueID)
}

func (l League) URL() string {
	return fmt.Sprintf("%s/league/%d.l.%d/settings", baseYahooAPIURL, l.GameKey, l.LeagueID)
}

func (l League) Type() core.FileType { return core.League }

func (l League) Parse(data []byte) (*store.FantasyContent, error) {
	var content store.FantasyContent
	if err := xml.Unmarshal(data, &content); err != nil {
		return nil, fmt.Errorf("parse league %d/%d: %w", l.Season, l.LeagueID, err)
	}
	return &content, nil
}

// Team represents a Yahoo Fantasy team resource.
type Team struct {
	Season   int
	LeagueID int
	TeamID   int
	GameKey  int // Required for URL, not for path
}

func (t Team) Path() string {
	return fmt.Sprintf("seasons/%d/yahoo/%d/teams/team-%02d/team-%02d.xml",
		t.Season, t.LeagueID, t.TeamID, t.TeamID)
}

func (t Team) URL() string {
	return fmt.Sprintf("%s/team/%d.l.%d.t.%d", baseYahooAPIURL, t.GameKey, t.LeagueID, t.TeamID)
}

func (t Team) Type() core.FileType { return core.Team }

func (t Team) Parse(data []byte) (*store.FantasyContent, error) {
	var content store.FantasyContent
	if err := xml.Unmarshal(data, &content); err != nil {
		return nil, fmt.Errorf("parse team %d/%d/%d: %w", t.Season, t.LeagueID, t.TeamID, err)
	}
	return &content, nil
}

// Roster represents a Yahoo Fantasy team roster for a specific date.
type Roster struct {
	LeagueID int
	TeamID   int
	Date     time.Time
	GameKey  int // Required for URL, not for path
}

func (r Roster) Path() string {
	season := DeduceSeason(r.Date)
	return fmt.Sprintf("seasons/%d/yahoo/%d/rosters/team-%02d/rosters-%02d-%d-%02d-%02d.xml",
		season, r.LeagueID, r.TeamID, r.TeamID, r.Date.Year(), r.Date.Month(), r.Date.Day())
}

func (r Roster) URL() string {
	return fmt.Sprintf("%s/team/%d.l.%d.t.%d/roster;date=%d-%02d-%02d/players",
		baseYahooAPIURL, r.GameKey, r.LeagueID, r.TeamID,
		r.Date.Year(), r.Date.Month(), r.Date.Day())
}

func (r Roster) Type() core.FileType { return core.Roster }

func (r Roster) Parse(data []byte) (*store.FantasyContent, error) {
	var content store.FantasyContent
	if err := xml.Unmarshal(data, &content); err != nil {
		return nil, fmt.Errorf("parse roster %d/%d for %s: %w",
			r.LeagueID, r.TeamID, r.Date.Format("2006-01-02"), err)
	}
	return &content, nil
}

// TeamSummary represents a Yahoo Fantasy team's daily stats summary.
type TeamSummary struct {
	LeagueID int
	TeamID   int
	Date     time.Time
	GameKey  int // Required for URL, not for path
}

func (t TeamSummary) Path() string {
	season := DeduceSeason(t.Date)
	return fmt.Sprintf("seasons/%d/yahoo/%d/summaries/team-%02d/team-%02d-summary-%d-%02d-%02d.xml",
		season, t.LeagueID, t.TeamID, t.TeamID, t.Date.Year(), t.Date.Month(), t.Date.Day())
}

func (t TeamSummary) URL() string {
	return fmt.Sprintf("%s/team/%d.l.%d.t.%d/stats;type=date;date=%d-%02d-%02d",
		baseYahooAPIURL, t.GameKey, t.LeagueID, t.TeamID,
		t.Date.Year(), t.Date.Month(), t.Date.Day())
}

func (t TeamSummary) Type() core.FileType { return core.TeamSummary }

func (t TeamSummary) Parse(data []byte) (*store.FantasyContent, error) {
	var content store.FantasyContent
	if err := xml.Unmarshal(data, &content); err != nil {
		return nil, fmt.Errorf("parse team summary %d/%d for %s: %w",
			t.LeagueID, t.TeamID, t.Date.Format("2006-01-02"), err)
	}
	return &content, nil
}

// YahooPlayer represents a Yahoo Sports player page (HTML).
type YahooPlayer struct {
	PlayerID YahooPlayerID
}

func (y YahooPlayer) Path() string {
	return fmt.Sprintf("%s/player-%d.html", YahooPlayersDir, y.PlayerID)
}

func (y YahooPlayer) URL() string {
	return fmt.Sprintf("%s/nhl/players/%d/", baseYahooSportsURL, y.PlayerID)
}

func (y YahooPlayer) Type() core.FileType { return core.YahooPlayer }

// Parse returns the raw HTML bytes since Yahoo player pages are HTML, not structured data.
func (y YahooPlayer) Parse(data []byte) ([]byte, error) {
	return data, nil
}

func (y YahooPlayer) Format(data []byte) ([]byte, error) {
	return data, nil
}

// MissingYahooPlayer is a marker for Yahoo player IDs that returned 404.
// This does not implement URLResource since it's just a cache marker.
type MissingYahooPlayer struct {
	PlayerID YahooPlayerID
}

func (m MissingYahooPlayer) Path() string {
	return fmt.Sprintf("%s/player-%d.txt", YahooPlayersMissingDir, m.PlayerID)
}

func (m MissingYahooPlayer) Type() core.FileType { return core.YahooPlayer }

// Parse returns the raw bytes (typically empty or a placeholder).
func (m MissingYahooPlayer) Parse(data []byte) ([]byte, error) {
	return data, nil
}

func (m MissingYahooPlayer) Format(data []byte) ([]byte, error) {
	return data, nil
}

// GameKey represents a Yahoo Fantasy game key resource.
// The game key maps a season to Yahoo's internal game identifier.
type GameKey struct {
	Season int
}

func (g GameKey) Path() string {
	return fmt.Sprintf("game-keys/gamekey-%d.xml", g.Season)
}

func (g GameKey) URL() string {
	return fmt.Sprintf("%s/games;game_codes=nhl;seasons=%d", baseYahooAPIURL, g.Season)
}

func (g GameKey) Type() core.FileType { return core.GameKey }

func (g GameKey) Parse(data []byte) (*store.FantasyContent, error) {
	content, _, err := g.parse(data)
	return content, err
}

// ParseKey parses a game key response and returns the validated game key.
func (g GameKey) ParseKey(data []byte) (int, error) {
	_, game, err := g.parse(data)
	return game.Key, err
}

func (g GameKey) parse(data []byte) (*store.FantasyContent, store.FantasyGame, error) {
	var content store.FantasyContent
	if err := xml.Unmarshal(data, &content); err != nil {
		return nil, store.FantasyGame{}, fmt.Errorf("parse game key for season %d: %w", g.Season, err)
	}
	game, err := gameKeyEntry(content)
	if err != nil {
		return nil, store.FantasyGame{}, fmt.Errorf("parse game key for season %d: %w", g.Season, err)
	}
	if game.Key <= 0 || game.Season != g.Season || !strings.EqualFold(game.Code, yahooNHLGameCode) {
		return nil, store.FantasyGame{}, fmt.Errorf(
			"parse game key for season %d: yahoo game key %d is for code %q season %d",
			g.Season, game.Key, game.Code, game.Season)
	}
	return &content, game, nil
}

func gameKeyEntry(content store.FantasyContent) (store.FantasyGame, error) {
	if len(content.Games) > 0 {
		return content.Games[0], nil
	}
	if content.Game.Key != 0 || content.Game.Code != "" || content.Game.Season != 0 {
		return content.Game, nil
	}
	return store.FantasyGame{}, fmt.Errorf("response contains no game")
}

// Transactions represents a Yahoo Fantasy league's transaction history.
type Transactions struct {
	Season   int
	LeagueID int
	GameKey  int
}

func (t Transactions) Path() string {
	return fmt.Sprintf("seasons/%d/yahoo/%d/transactions/transactions.xml", t.Season, t.LeagueID)
}

func (t Transactions) URL() string {
	return fmt.Sprintf("%s/league/%d.l.%d/transactions", baseYahooAPIURL, t.GameKey, t.LeagueID)
}

func (t Transactions) Type() core.FileType { return core.YahooTransactions }

func (t Transactions) Parse(data []byte) (*store.FantasyContent, error) {
	var content store.FantasyContent
	if err := xml.Unmarshal(data, &content); err != nil {
		return nil, fmt.Errorf("parse transactions %d/%d: %w", t.Season, t.LeagueID, err)
	}
	return &content, nil
}

// DraftResults represents a Yahoo Fantasy league's draft results.
type DraftResults struct {
	Season   int
	LeagueID int
	GameKey  int
}

func (d DraftResults) Path() string {
	return fmt.Sprintf("seasons/%d/yahoo/%d/draft/draftresults.xml", d.Season, d.LeagueID)
}

func (d DraftResults) URL() string {
	return fmt.Sprintf("%s/league/%d.l.%d/draftresults", baseYahooAPIURL, d.GameKey, d.LeagueID)
}

func (d DraftResults) Type() core.FileType { return core.YahooDraftResults }

func (d DraftResults) Parse(data []byte) (*store.FantasyContent, error) {
	var content store.FantasyContent
	if err := xml.Unmarshal(data, &content); err != nil {
		return nil, fmt.Errorf("parse draft results %d/%d: %w", d.Season, d.LeagueID, err)
	}
	return &content, nil
}

// Matchups represents a Yahoo Fantasy league's weekly scoreboard.
type Matchups struct {
	Season   int
	LeagueID int
	Week     int
	GameKey  int
}

func (m Matchups) Path() string {
	return fmt.Sprintf("seasons/%d/yahoo/%d/matchups/week-%d.xml", m.Season, m.LeagueID, m.Week)
}

func (m Matchups) URL() string {
	return fmt.Sprintf("%s/league/%d.l.%d/scoreboard;week=%d",
		baseYahooAPIURL, m.GameKey, m.LeagueID, m.Week)
}

func (m Matchups) Type() core.FileType { return core.YahooMatchups }

func (m Matchups) Parse(data []byte) (*store.FantasyContent, error) {
	var content store.FantasyContent
	if err := xml.Unmarshal(data, &content); err != nil {
		return nil, fmt.Errorf("parse matchups %d/%d week %d: %w", m.Season, m.LeagueID, m.Week, err)
	}
	return &content, nil
}
