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
	cachedSeasons SeasonsMap
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

// SeasonsMap is a map of seasons keyed by start year
type SeasonsMap map[int]Season

func getSeasons(path string) (SeasonsMap, error) {
	log.Debug().Str("path", path).Msgf("Opening config file")
	yamlFile, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("can't read seasons config file %s: %w", path, err)
	}
	var seasons SeasonsMap
	err = yaml.Unmarshal(yamlFile, &seasons)
	if err != nil {
		return nil, fmt.Errorf("can't unmarshal seasons config file %s: %w", path, err)
	}
	return seasons, nil
}

func GetSeasonsConfig() (SeasonsMap, error) {
	seasonsOnce.Do(func() {
		paramSeasons := viper.GetString(FlagSeasons)
		log.Debug().Str("path", paramSeasons).Msg("Loading seasons")
		cachedSeasons, seasonsErr = getSeasons(paramSeasons)
	})
	return cachedSeasons, seasonsErr
}
