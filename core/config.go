package core

import (
	"fmt"
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

func (c *Config) String() string {
	return fmt.Sprintf("LeagueID: %d\nTotalTeams: %d\nCache Path: %s\n", c.LeagueID, c.TotalTeams, c.CachePath)
}

var config Config

func init() {
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
