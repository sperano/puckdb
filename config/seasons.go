package config

import (
	"fmt"
	"github.com/golang-module/carbon/v2"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v2"
	"os"
	"time"
)

type League struct {
	LeagueID int   `yaml:"league_id"`
	TeamIDs  []int `yaml:"team_ids"`
}

type Season struct {
	Start   time.Time
	End     time.Time
	GameKey int
	Leagues []League
}

func (s Season) GetLeague(leagueID int) (League, error) {
	for _, l := range s.Leagues {
		if l.LeagueID == leagueID {
			return l, nil
		}
	}
	return League{}, fmt.Errorf("league not found: %d", leagueID)

}

func (s Season) StartYear() int {
	return s.Start.Year()
}

type Seasons []Season

func (s Seasons) AsInts() []int {
	ints := make([]int, len(s))
	for i, v := range s {
		ints[i] = v.StartYear()
	}
	return ints
}

func (s Seasons) Get(season int) (Season, error) {
	for _, ss := range s {
		if ss.StartYear() == season {
			return ss, nil
		}
	}
	return Season{}, fmt.Errorf("season not found: %d", season)
}

type YAMLSeason struct {
	Start   string   `yaml:"start"`
	End     string   `yaml:"end"`
	GameKey int      `yaml:"game_key"`
	Leagues []League `yaml:"leagues"`
}

func (y *YAMLSeason) ToSeason() Season {
	return Season{
		Start:   carbon.SetTimezone(time.UTC.String()).Parse(y.Start).ToStdTime(),
		End:     carbon.SetTimezone(time.UTC.String()).Parse(y.End).ToStdTime(),
		GameKey: y.GameKey,
		Leagues: y.Leagues,
	}
}

func getSeasons(path string) (Seasons, error) {
	log.Debug().Str("path", path).Msgf("Opening config file")
	yamlFile, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("can't read seasons config file %s: %w", path, err)
	}
	var yseasons []YAMLSeason
	err = yaml.Unmarshal(yamlFile, &yseasons)
	if err != nil {
		return nil, fmt.Errorf("can't unmarshal seasons config file %s: %w", path, err)
	}
	seasons := make([]Season, len(yseasons))
	for i, yseason := range yseasons {
		seasons[i] = yseason.ToSeason()
	}
	return seasons, nil
}

func GetSeasonsConfig() (Seasons, error) {
	paramSeasons := viper.GetString(FlagSeasons)
	log.Debug().Str("path", paramSeasons).Msg("Loading seasons")
	return getSeasons(paramSeasons)
}
