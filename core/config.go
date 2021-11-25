package core

import (
	"io/ioutil"
	"log"
	"time"

	"gopkg.in/yaml.v2"
)

type Config struct {
	LeagueID         int        `yaml:"league_id"`
	TotalTeams       int        `yaml:"total_teams"`
	CachePath        string     `yaml:"cache_path"`
	SeasonStartYear  int        `yaml:"season_start_year"`
	SeasonStartMonth time.Month `yaml:"season_start_month"`
	SeasonStartDay   int        `yaml:"season_start_day"`
}

var config Config

func GetConfig(path string) *Config {
	if config.LeagueID == 0 {
		yamlFile, err := ioutil.ReadFile(path)
		if err != nil {
			log.Fatalln(err)
		}
		err = yaml.Unmarshal(yamlFile, &config)
		if err != nil {
			log.Fatalln(err)
		}
	}
	return &config
}

func (c *Config) GetSeasonStart() time.Time {
	return time.Date(c.SeasonStartYear, c.SeasonStartMonth, c.SeasonStartDay, 0, 0, 0, 0, time.UTC)
}
