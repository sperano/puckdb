package config

import (
	"fmt"
	"os"
	"sync"

	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v2"
)

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

func GetYahooSeasonsConfig() (YahooSeasonsMap, error) {
	seasonsOnce.Do(func() {
		paramSeasons := viper.GetString(FlagYahooSeasons)
		log.Debug().Str("path", paramSeasons).Msg("Loading yahoo seasons config")
		cachedSeasons, seasonsErr = getYahooSeasons(paramSeasons)
	})
	return cachedSeasons, seasonsErr
}
