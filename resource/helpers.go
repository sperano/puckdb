package resource

import (
	"fmt"
	"time"
)

// DeduceSeason returns the NHL season start year for a given date.
// NHL seasons start in September, so dates from September onward belong to that year's season.
func DeduceSeason(day time.Time) int {
	if day.Month() >= time.September {
		return day.Year()
	}
	return day.Year() - 1
}

// gamesDir returns the directory path for game data on a specific date.
// Format: seasons/{season}/games/{year}/{month}/{day}
func gamesDir(date time.Time) string {
	season := DeduceSeason(date)
	return fmt.Sprintf("seasons/%d/games/%d/%02d/%02d", season, date.Year(), date.Month(), date.Day())
}
