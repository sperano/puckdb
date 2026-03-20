// Package filetype defines the FileType enum for categorizing stored resources.
// This package has no dependencies to avoid import cycles between store, metrics, and resource.
package core

// FileType identifies the type of stored resource for metrics and logging.
type FileType int

const (
	Unknown FileType = iota
	DailySchedule
	Boxscore
	PlayByPlay
	ShiftChart
	GameStory
	Franchises
	SeasonsManifest
	SeasonStandings
	PlayerLanding
	PlayerGameLog
	League
	Team
	Roster
	TeamSummary
	YahooPlayer
	GameKey
	SeasonSeries
)

var names = [...]string{
	Unknown:         "Unknown",
	DailySchedule:   "DailySchedule",
	Boxscore:        "Boxscore",
	PlayByPlay:      "PlayByPlay",
	ShiftChart:      "ShiftChart",
	GameStory:       "GameStory",
	Franchises:      "Franchises",
	SeasonsManifest: "SeasonsManifest",
	SeasonStandings: "SeasonStandings",
	PlayerLanding:   "PlayerLanding",
	PlayerGameLog:   "PlayerGameLog",
	League:          "League",
	Team:            "Team",
	Roster:          "Roster",
	TeamSummary:     "TeamSummary",
	YahooPlayer:     "YahooPlayer",
	GameKey:         "GameKey",
	SeasonSeries:    "SeasonSeries",
}

// String returns the string representation of the FileType.
func (f FileType) String() string {
	if f < 0 || int(f) >= len(names) {
		return names[Unknown]
	}
	return names[f]
}

// ParseFileType converts a string name to a FileType.
// Returns Unknown and false if the name is not recognized.
func ParseFileType(name string) (FileType, bool) {
	for i, n := range names {
		if n == name {
			return FileType(i), true
		}
	}
	return Unknown, false
}
