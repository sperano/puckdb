package store

import (
	"encoding/xml"
	"fmt"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/config"
	"github.com/spf13/viper"
)

// NewStore creates a new instrumented file store using the configured data path.
func NewStore() Store {
	dataPath := viper.GetString(config.FlagDataPath)
	log.Trace().Str("path", dataPath).Msg("Initializing FileStore")
	return NewInstrumentedStore(NewFileStore(dataPath))
}

// ParseXML parses Yahoo Fantasy XML content.
func ParseXML(data []byte) (*FantasyContent, error) {
	var fantasy FantasyContent
	if err := xml.Unmarshal(data, &fantasy); err != nil {
		return nil, err
	}
	return &fantasy, nil
}

// YahooFetcher is a function that downloads content from Yahoo API.
type YahooFetcher func(url string) ([]byte, error)

// GetGameKey returns the Yahoo game key for a season, using file store.
// If not stored, it calls the fetcher to download and stores the result.
// The postDownload function is called after a successful download (for rate limiting).
func GetGameKey(fs Store, season int, fetcher YahooFetcher, postDownload func()) (int, error) {
	file := GameKeyFile{Season: season}

	var content []byte
	var err error

	if fs.Exists(file) {
		log.Debug().Int("season", season).Msg("Game key file found")
		content, err = fs.Read(file)
		if err != nil {
			return 0, fmt.Errorf("read stored game key for season %d: %w", season, err)
		}
	} else {
		log.Info().Int("season", season).Msg("Downloading game key from Yahoo")
		url := fmt.Sprintf("https://fantasysports.yahooapis.com/fantasy/v2/game/nhl;season=%d", season)
		content, err = fetcher(url)
		if err != nil {
			return 0, fmt.Errorf("fetch game key for season %d: %w", season, err)
		}

		if err := fs.MkdirAll(file.Dir(), 0755); err != nil {
			return 0, fmt.Errorf("mkdir for game key: %w", err)
		}
		if err := fs.Write(file, content); err != nil {
			return 0, fmt.Errorf("save game key for season %d: %w", season, err)
		}
		log.Info().Int("season", season).Str("path", Path(file)).Msg("Saved game key")
		if postDownload != nil {
			postDownload()
		}
	}

	fantasy, err := ParseXML(content)
	if err != nil {
		return 0, fmt.Errorf("parse game key response for season %d: %w", season, err)
	}

	var gameKey int
	if len(fantasy.Games) > 0 {
		gameKey = fantasy.Games[0].Key
	} else if fantasy.Game.Key != 0 {
		gameKey = fantasy.Game.Key
	} else {
		return 0, fmt.Errorf("no game key found for season %d", season)
	}

	log.Info().Int("season", season).Int("game_key", gameKey).Msg("Loaded Yahoo game key")
	return gameKey, nil
}
