package config

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v2"
)

// ErrYahooNotConfigured signals that no Yahoo seasons config file is set.
// Callers use errors.Is to distinguish "Yahoo integration is off" from real
// failures (missing file, malformed YAML), which must not be silently swallowed.
var ErrYahooNotConfigured = errors.New("yahoo seasons config not set")

var (
	cachedSeasons YahooSeasonsMap
	seasonsOnce   sync.Once
	seasonsErr    error
)

type League struct {
	LeagueID int   `yaml:"league_id"`
	TeamIDs  []int `yaml:"team_ids"`
}

type Season struct {
	Leagues []League `yaml:"leagues"`
}

func (s Season) GetLeague(leagueID int) (League, error) {
	for _, l := range s.Leagues {
		if l.LeagueID == leagueID {
			return l, nil
		}
	}
	return League{}, fmt.Errorf("league not found: %d", leagueID)
}

// YahooSeasonsMap is a map of seasons keyed by start year
type YahooSeasonsMap map[int]Season

func getYahooSeasons(path string) (YahooSeasonsMap, error) {
	log.Debug().Str("path", path).Msgf("Opening yahoo seasons config file")
	yamlFile, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("can't read yahoo seasons config file %s: %w", path, err)
	}
	var seasons YahooSeasonsMap
	err = yaml.Unmarshal(yamlFile, &seasons)
	if err != nil {
		return nil, fmt.Errorf("can't unmarshal yahoo seasons config file %s: %w", path, err)
	}
	return seasons, nil
}

// GetYahooSeasonsConfig returns the Yahoo seasons map from the configured YAML
// file. Returns ErrYahooNotConfigured if no path is configured; a wrapped I/O
// or YAML error if the file is unreadable or malformed.
func GetYahooSeasonsConfig() (YahooSeasonsMap, error) {
	seasonsOnce.Do(func() {
		paramSeasons := viper.GetString(FlagYahooSeasons)
		if paramSeasons == "" {
			seasonsErr = ErrYahooNotConfigured
			return
		}
		log.Debug().Str("path", paramSeasons).Msg("Loading yahoo seasons config")
		cachedSeasons, seasonsErr = getYahooSeasons(paramSeasons)
	})
	return cachedSeasons, seasonsErr
}
