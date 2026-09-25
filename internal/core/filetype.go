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
	PlayerHeadshot
	PlayerHeroImage
	PlayerYahooImage
	TeamLogo
	YahooTeamLogo
	YahooLeagueLogo
	YahooManagerImage
	YahooLeaguePlayers
)

// fileTypeEntry binds a FileType constant to its display name. The display
// name is used as a Prometheus metric label value, so changing it can break
// Grafana queries.
type fileTypeEntry struct {
	ft   FileType
	name string
}

// fileTypes is the single source of truth for FileType ↔ name mappings.
// When adding a new FileType constant to the iota block above, add an entry
// here in the same order. AllFileTypes, String(), and ParseFileType all
// derive from this slice.
//
// Note: ClubScheduleSeasonResource → "ClubScheduleSeason" (no "Resource"
// suffix). Preserved as-is so existing Grafana queries keep working.
var fileTypes = []fileTypeEntry{
	{Unknown, "Unknown"},
	{DailySchedule, "DailySchedule"},
	{Boxscore, "Boxscore"},
	{PlayByPlay, "PlayByPlay"},
	{ShiftChart, "ShiftChart"},
	{GameStory, "GameStory"},
	{Franchises, "Franchises"},
	{SeasonsManifest, "SeasonsManifest"},
	{SeasonStandings, "SeasonStandings"},
	{PlayerLanding, "PlayerLanding"},
	{PlayerGameLog, "PlayerGameLog"},
	{League, "League"},
	{Team, "Team"},
	{Roster, "Roster"},
	{TeamSummary, "TeamSummary"},
	{YahooPlayer, "YahooPlayer"},
	{GameKey, "GameKey"},
	{SeasonSeries, "SeasonSeries"},
	{DailyStandings, "DailyStandings"},
	{SeasonRoster, "SeasonRoster"},
	{ClubStatsResource, "ClubStatsResource"},
	{YahooTransactions, "YahooTransactions"},
	{YahooDraftResults, "YahooDraftResults"},
	{YahooMatchups, "YahooMatchups"},
	{EdgeSkaterDetail, "EdgeSkaterDetail"},
	{EdgeSkaterSpeedDetail, "EdgeSkaterSpeedDetail"},
	{EdgeSkaterDistanceDetail, "EdgeSkaterDistanceDetail"},
	{EdgeSkaterShotSpeedDetail, "EdgeSkaterShotSpeedDetail"},
	{EdgeSkaterShotLocationDetail, "EdgeSkaterShotLocationDetail"},
	{EdgeSkaterZoneTime, "EdgeSkaterZoneTime"},
	{EdgeSkaterComparison, "EdgeSkaterComparison"},
	{EdgeGoalieDetail, "EdgeGoalieDetail"},
	{EdgeGoalie5v5Detail, "EdgeGoalie5v5Detail"},
	{EdgeGoalieShotLocationDetail, "EdgeGoalieShotLocationDetail"},
	{EdgeGoalieSavePctgDetail, "EdgeGoalieSavePctgDetail"},
	{EdgeGoalieComparison, "EdgeGoalieComparison"},
	{EdgeTeamDetail, "EdgeTeamDetail"},
	{EdgeTeamSpeedDetail, "EdgeTeamSpeedDetail"},
	{EdgeTeamDistanceDetail, "EdgeTeamDistanceDetail"},
	{EdgeTeamShotSpeedDetail, "EdgeTeamShotSpeedDetail"},
	{EdgeTeamShotLocationDetail, "EdgeTeamShotLocationDetail"},
	{EdgeTeamZoneTimeDetails, "EdgeTeamZoneTimeDetails"},
	{EdgeTeamComparison, "EdgeTeamComparison"},
	{EdgeSkaterLanding, "EdgeSkaterLanding"},
	{EdgeGoalieLanding, "EdgeGoalieLanding"},
	{EdgeTeamLanding, "EdgeTeamLanding"},
	{ClubScheduleSeasonResource, "ClubScheduleSeason"},
	{PlayerHeadshot, "PlayerHeadshot"},
	{PlayerHeroImage, "PlayerHeroImage"},
	{PlayerYahooImage, "PlayerYahooImage"},
	{TeamLogo, "TeamLogo"},
	{YahooTeamLogo, "YahooTeamLogo"},
	{YahooLeagueLogo, "YahooLeagueLogo"},
	{YahooManagerImage, "YahooManagerImage"},
	{YahooLeaguePlayers, "YahooLeaguePlayers"},
}

// AllFileTypes lists every declared FileType in iota declaration order,
// including Unknown. Derived from fileTypes at package init.
//
// This slice is exposed for read-only iteration. Mutating it will affect
// every caller — don't.
var AllFileTypes []FileType

// nameByType and typeByName are O(1) lookup caches built from fileTypes
// at init. They keep String() and ParseFileType() off the hot-path linear
// scan that a 56-entry table would otherwise impose on every metric label.
var (
	nameByType map[FileType]string
	typeByName map[string]FileType
)

func init() {
	AllFileTypes = make([]FileType, len(fileTypes))
	nameByType = make(map[FileType]string, len(fileTypes))
	typeByName = make(map[string]FileType, len(fileTypes))
	for i, e := range fileTypes {
		AllFileTypes[i] = e.ft
		nameByType[e.ft] = e.name
		typeByName[e.name] = e.ft
	}
}

// String returns the string representation of the FileType. Unrecognized
// values return "Unknown".
func (f FileType) String() string {
	if name, ok := nameByType[f]; ok {
		return name
	}
	return "Unknown"
}

// ParseFileType converts a string name to a FileType.
// Returns Unknown and false if the name is not recognized.
func ParseFileType(name string) (FileType, bool) {
	ft, ok := typeByName[name]
	return ft, ok
}
