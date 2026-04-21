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
	DailyStandings
	SeasonRoster
	ClubStatsResource
	YahooTransactions
	YahooDraftResults
	YahooMatchups
	EdgeSkaterDetail
	EdgeSkaterSpeedDetail
	EdgeSkaterDistanceDetail
	EdgeSkaterShotSpeedDetail
	EdgeSkaterShotLocationDetail
	EdgeSkaterZoneTime
	EdgeSkaterComparison
	EdgeGoalieDetail
	EdgeGoalie5v5Detail
	EdgeGoalieShotLocationDetail
	EdgeGoalieSavePctgDetail
	EdgeGoalieComparison
	EdgeTeamDetail
	EdgeTeamSpeedDetail
	EdgeTeamDistanceDetail
	EdgeTeamShotSpeedDetail
	EdgeTeamShotLocationDetail
	EdgeTeamZoneTimeDetails
	EdgeTeamComparison
	EdgeSkaterLanding
	EdgeGoalieLanding
	EdgeTeamLanding
	ClubScheduleSeasonResource
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
	SeasonSeries:     "SeasonSeries",
	DailyStandings:   "DailyStandings",
	SeasonRoster:     "SeasonRoster",
	ClubStatsResource: "ClubStatsResource",
	YahooTransactions: "YahooTransactions",
	YahooDraftResults: "YahooDraftResults",
	YahooMatchups:               "YahooMatchups",
	EdgeSkaterDetail:            "EdgeSkaterDetail",
	EdgeSkaterSpeedDetail:       "EdgeSkaterSpeedDetail",
	EdgeSkaterDistanceDetail:    "EdgeSkaterDistanceDetail",
	EdgeSkaterShotSpeedDetail:   "EdgeSkaterShotSpeedDetail",
	EdgeSkaterShotLocationDetail: "EdgeSkaterShotLocationDetail",
	EdgeSkaterZoneTime:          "EdgeSkaterZoneTime",
	EdgeSkaterComparison:        "EdgeSkaterComparison",
	EdgeGoalieDetail:            "EdgeGoalieDetail",
	EdgeGoalie5v5Detail:         "EdgeGoalie5v5Detail",
	EdgeGoalieShotLocationDetail: "EdgeGoalieShotLocationDetail",
	EdgeGoalieSavePctgDetail:    "EdgeGoalieSavePctgDetail",
	EdgeGoalieComparison:        "EdgeGoalieComparison",
	EdgeTeamDetail:              "EdgeTeamDetail",
	EdgeTeamSpeedDetail:         "EdgeTeamSpeedDetail",
	EdgeTeamDistanceDetail:      "EdgeTeamDistanceDetail",
	EdgeTeamShotSpeedDetail:     "EdgeTeamShotSpeedDetail",
	EdgeTeamShotLocationDetail:  "EdgeTeamShotLocationDetail",
	EdgeTeamZoneTimeDetails:     "EdgeTeamZoneTimeDetails",
	EdgeTeamComparison:          "EdgeTeamComparison",
	EdgeSkaterLanding:           "EdgeSkaterLanding",
	EdgeGoalieLanding:           "EdgeGoalieLanding",
	EdgeTeamLanding:             "EdgeTeamLanding",
	ClubScheduleSeasonResource:  "ClubScheduleSeason",
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
