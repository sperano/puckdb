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
	return fmt.Sprintf("LeagueID: %d\n", c.LeagueID)
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
