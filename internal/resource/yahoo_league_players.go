package resource

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"time"

	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/store"
)

// LeaguePlayersPageSize is the largest page Yahoo serves for the league
// players collection.
const LeaguePlayersPageSize = 25

// LeaguePlayers is one page of every player Yahoo lists for a league (no
// status filter: free agents, waivers and rostered players alike), with the
// league's eligible positions and current status.
type LeaguePlayers struct {
	Season   int
	LeagueID int
	// DownloadID names the download the page belongs to; each download
	// writes its own directory so a failed one cannot overwrite the pages of
	// the committed snapshot.
	DownloadID int64
	// Start is the zero-based offset of the page's first player.
	Start   int
	GameKey int // Required for URL, not for path
}

func (l LeaguePlayers) Path() string {
	return fmt.Sprintf("seasons/%d/yahoo/%d/players/%d/players-%04d.xml", l.Season, l.LeagueID, l.DownloadID, l.Start)
}

func (l LeaguePlayers) URL() string {
	return fmt.Sprintf("%s/league/%d.l.%d/players;start=%d;count=%d",
		baseYahooAPIURL, l.GameKey, l.LeagueID, l.Start, LeaguePlayersPageSize)
}

func (l LeaguePlayers) Type() core.FileType { return core.YahooLeaguePlayers }

func (l LeaguePlayers) Parse(data []byte) (*store.FantasyContent, error) {
	var content store.FantasyContent
	if err := xml.Unmarshal(data, &content); err != nil {
		return nil, fmt.Errorf("parse league players %d/%d start %d: %w", l.Season, l.LeagueID, l.Start, err)
	}
	if content.League.ID != l.LeagueID {
		return nil, fmt.Errorf("parse league players %d/%d start %d: response league ID %d does not match requested league %d",
			l.Season, l.LeagueID, l.Start, content.League.ID, l.LeagueID)
	}
	keyGame, keyLeague, err := parseYahooLeagueKey(content.League.Key)
	if err != nil {
		return nil, fmt.Errorf("parse league players %d/%d start %d: %w",
			l.Season, l.LeagueID, l.Start, err)
	}
	if keyLeague != l.LeagueID {
		return nil, fmt.Errorf("parse league players %d/%d start %d: response league key %q contains league ID %d, want %d",
			l.Season, l.LeagueID, l.Start, content.League.Key, keyLeague, l.LeagueID)
	}
	if l.GameKey > 0 && keyGame != l.GameKey {
		return nil, fmt.Errorf("parse league players %d/%d start %d: response league key %q does not match verified key %d.l.%d",
			l.Season, l.LeagueID, l.Start, content.League.Key, l.GameKey, l.LeagueID)
	}
	return &content, nil
}

// LeaguePlayerPool is the manifest of a complete player pool download. It is
// written after the last page and names that download's directory, so the
// pages it names always form one snapshot.
type LeaguePlayerPool struct {
	Season   int
	LeagueID int
}

// LeaguePlayerPoolManifest describes one complete pool download.
type LeaguePlayerPoolManifest struct {
	DownloadID int64     `json:"downloadId"`
	LeagueKey  string    `json:"leagueKey"`
	GameKey    int       `json:"gameKey"`
	FetchedAt  time.Time `json:"fetchedAt"`
	// Starts lists the page offsets that make up the pool, in order.
	Starts  []int `json:"starts"`
	Players int   `json:"players"`
}

func (l LeaguePlayerPool) Path() string {
	return fmt.Sprintf("seasons/%d/yahoo/%d/players/manifest.json", l.Season, l.LeagueID)
}

func (l LeaguePlayerPool) Type() core.FileType { return core.YahooLeaguePlayers }

func (l LeaguePlayerPool) Parse(data []byte) (LeaguePlayerPoolManifest, error) {
	var manifest LeaguePlayerPoolManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return manifest, fmt.Errorf("parse player pool manifest %d/%d: %w", l.Season, l.LeagueID, err)
	}
	return manifest, nil
}

func (l LeaguePlayerPool) Format(manifest LeaguePlayerPoolManifest) ([]byte, error) {
	return json.Marshal(manifest)
}
