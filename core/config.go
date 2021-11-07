package core

import "time"

type Config struct {
	LeagueID         int
	TotalTeams       int
	CachePath        string
	SeasonStartYear  int
	SeasonStartMonth time.Month
	SeasonStartDay   int
}

var config Config

func init() {
	config = Config{
		LeagueID:  22030,
		CachePath: "./cache",
	}
}

func GetConfig() *Config {
	return &config
}
