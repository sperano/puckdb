package core

import (
	"time"
)

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
	// TODO!!!
	config = Config{
		LeagueID:         22030,
		TotalTeams:       10,
		CachePath:        "./cache",
		SeasonStartYear:  2021,
		SeasonStartMonth: 10,
		SeasonStartDay:   12,
	}
}

func GetConfig() *Config {
	return &config
}

func (c *Config) GetSeasonStart() time.Time {
	return time.Date(c.SeasonStartYear, c.SeasonStartMonth, c.SeasonStartDay, 0, 0, 0, 0, time.UTC)
}
