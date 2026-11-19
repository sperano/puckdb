package resource

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"strings"
	"time"

	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/yahookey"
)

const (
	// YahooGamesDir is the directory holding the list of Yahoo's NHL games.
	YahooGamesDir = "yahoo-games"
	// YahooGameDir is the per-season directory holding the season's Yahoo
	// game files (seasons/<year>/yahoo-game). It sits beside seasons/<year>/yahoo,
	// whose subdirectories are league IDs.
	YahooGameDir = "yahoo-game"

	// GamePlayersPageSize is the largest page Yahoo serves for the game
	// players collection.
	GamePlayersPageSize = 25
)

// YahooGames is the list of every NHL game (one per season) Yahoo Fantasy
// has run, which maps a season to its game key.
type YahooGames struct{}

func (YahooGames) Path() string {
	return fmt.Sprintf("%s/games-%s.xml", YahooGamesDir, yahooNHLGameCode)
}

func (YahooGames) URL() string {
	return fmt.Sprintf("%s/games;game_codes=%s", baseYahooAPIURL, yahooNHLGameCode)
}

func (YahooGames) Type() core.FileType { return core.YahooGames }

// Parse rejects a list without games, a game that is not NHL or lacks its
// key or season, and two games for one season or with one key.
func (YahooGames) Parse(data []byte) (*store.FantasyContent, error) {
	var content store.FantasyContent
	if err := xml.Unmarshal(data, &content); err != nil {
		return nil, fmt.Errorf("parse Yahoo NHL games: %w", err)
	}
	if err := validateNHLGames(content.Games); err != nil {
		return nil, fmt.Errorf("parse Yahoo NHL games: %w", err)
	}
	return &content, nil
}

func validateNHLGames(games []store.FantasyGame) error {
	if len(games) == 0 {
		return fmt.Errorf("response contains no game")
	}
	seasons := make(map[int]bool, len(games))
	keys := make(map[int]bool, len(games))
	for _, game := range games {
		if !strings.EqualFold(game.Code, yahooNHLGameCode) || game.Key <= 0 || game.Season <= 0 {
			return fmt.Errorf("game key %d is for code %q season %d", game.Key, game.Code, game.Season)
		}
		if seasons[game.Season] || keys[game.Key] {
			return fmt.Errorf("game key %d or season %d is listed twice", game.Key, game.Season)
		}
		seasons[game.Season], keys[game.Key] = true, true
	}
	return nil
}

// FindNHLGame returns the season's game from a parsed YahooGames list.
func FindNHLGame(content *store.FantasyContent, season int) (store.FantasyGame, bool) {
	for _, game := range content.Games {
		if game.Season == season {
			return game, true
		}
	}
	return store.FantasyGame{}, false
}

// GamePlayers is one page of every player Yahoo lists for a season's NHL
// game. The collection takes no status filter (Yahoo ignores status= at game
// level), so a page is the same whatever filter a caller would pass.
type GamePlayers struct {
	Season int
	// DownloadID names the download the page belongs to; each download
	// writes its own directory so a failed one cannot overwrite the pages of
	// the committed list.
	DownloadID int64
	// Start is the zero-based offset of the page's first player.
	Start   int
	GameKey int // Required for URL, not for path
}

func (g GamePlayers) Path() string {
	return fmt.Sprintf("seasons/%d/%s/players/%d/players-%04d.xml", g.Season, YahooGameDir, g.DownloadID, g.Start)
}

func (g GamePlayers) URL() string {
	return fmt.Sprintf("%s/game/%d/players;start=%d;count=%d", baseYahooAPIURL, g.GameKey, g.Start, GamePlayersPageSize)
}

func (g GamePlayers) Type() core.FileType { return core.YahooGamePlayers }

// Parse checks that the page belongs to the requested season's NHL game (and,
// when GameKey is set, to that game key) and that every player key names
// that game and the player's ID.
func (g GamePlayers) Parse(data []byte) (*store.FantasyContent, error) {
	var content store.FantasyContent
	if err := xml.Unmarshal(data, &content); err != nil {
		return nil, fmt.Errorf("parse game players %d start %d: %w", g.Season, g.Start, err)
	}
	if err := g.validate(content.Game); err != nil {
		return nil, fmt.Errorf("parse game players %d start %d: %w", g.Season, g.Start, err)
	}
	return &content, nil
}

func (g GamePlayers) validate(game store.FantasyGame) error {
	if !strings.EqualFold(game.Code, yahooNHLGameCode) {
		return fmt.Errorf("response game code %q is not NHL", game.Code)
	}
	if game.Season != g.Season {
		return fmt.Errorf("response season %d does not match requested season %d", game.Season, g.Season)
	}
	if game.Key <= 0 {
		return fmt.Errorf("response game key %d is not a game key", game.Key)
	}
	if g.GameKey > 0 && game.Key != g.GameKey {
		return fmt.Errorf("response game key %d does not match requested game key %d", game.Key, g.GameKey)
	}
	for _, player := range game.Players.Slice {
		key, err := yahookey.ParsePlayerKey(player.Key)
		if err != nil || key.GameKey != game.Key || key.PlayerID != player.ID {
			return fmt.Errorf("player key %q does not name player %d of game %d", player.Key, player.ID, game.Key)
		}
	}
	return nil
}

// GamePlayerList is the manifest of a complete game player list download.
// It is written after the last page and names that download's directory, so
// the pages it names always form one list.
type GamePlayerList struct {
	Season int
}

// GamePlayerListManifest describes one complete game player list download.
type GamePlayerListManifest struct {
	DownloadID int64     `json:"downloadId"`
	GameKey    int       `json:"gameKey"`
	FetchedAt  time.Time `json:"fetchedAt"`
	// Starts lists the page offsets that make up the list, in order.
	Starts  []int `json:"starts"`
	Players int   `json:"players"`
}

func (g GamePlayerList) Path() string {
	return fmt.Sprintf("seasons/%d/%s/players/manifest.json", g.Season, YahooGameDir)
}

func (g GamePlayerList) Type() core.FileType { return core.YahooGamePlayers }

// Parse rejects a manifest that Validate rejects, so a malformed one is
// never taken for a committed list.
func (g GamePlayerList) Parse(data []byte) (GamePlayerListManifest, error) {
	manifest, err := parseValue[GamePlayerListManifest](data, "parse game player list manifest %d", g.Season)
	if err != nil {
		return manifest, err
	}
	if err := manifest.Validate(); err != nil {
		return GamePlayerListManifest{}, fmt.Errorf("parse game player list manifest %d: %w", g.Season, err)
	}
	return manifest, nil
}

// Validate checks that the manifest names one download of one game: a
// download ID, a game key, a fetch time, a player count that is not negative
// and page offsets that start at 0 and strictly increase, so no page is named
// twice.
func (m GamePlayerListManifest) Validate() error {
	switch {
	case m.DownloadID <= 0:
		return fmt.Errorf("download ID %d is not a download ID", m.DownloadID)
	case m.GameKey <= 0:
		return fmt.Errorf("game key %d is not a game key", m.GameKey)
	case m.FetchedAt.IsZero():
		return fmt.Errorf("no fetch time")
	case m.Players < 0:
		return fmt.Errorf("player count %d is negative", m.Players)
	case len(m.Starts) == 0 || m.Starts[0] != 0:
		return fmt.Errorf("page offsets %v do not start at 0", m.Starts)
	}
	for i := 1; i < len(m.Starts); i++ {
		if m.Starts[i] <= m.Starts[i-1] {
			return fmt.Errorf("page offsets %v do not strictly increase", m.Starts)
		}
	}
	return nil
}

func (g GamePlayerList) Format(manifest GamePlayerListManifest) ([]byte, error) {
	return json.Marshal(manifest)
}
