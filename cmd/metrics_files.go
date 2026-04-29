package cmd

import (
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/sperano/puckdb/core"
)

// fileTypeStats holds count and size for a file type
type fileTypeStats struct {
	count int64
	bytes int64
}

// dataPathStats holds combined results from a single directory walk.
type dataPathStats struct {
	totalBytes int64
	byType     map[core.FileType]*fileTypeStats
}

// collectDataPathStats walks the data path once, computing both total disk size
// and per-file-type statistics in a single pass.
func collectDataPathStats(dataPath string) dataPathStats {
	result := dataPathStats{byType: make(map[core.FileType]*fileTypeStats)}

	_ = filepath.WalkDir(dataPath, func(filePath string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}

		if strings.Contains(filePath, "/.git/") {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		size := info.Size()
		result.totalBytes += size

		fileType := classifyFileType(dataPath, filePath, d.Name())
		if result.byType[fileType] == nil {
			result.byType[fileType] = &fileTypeStats{}
		}
		result.byType[fileType].count++
		result.byType[fileType].bytes += size

		return nil
	})

	return result
}

// classifyFileType determines the file type from a file's path and filename.
// Top-level directory drives the dispatch; subtree helpers do the deeper work.
// As a final fallback, classifyByFilenamePrefix runs on the filename alone so
// files that wandered into non-standard parents (and synthetic test fixtures)
// still classify correctly.
func classifyFileType(dataPath, filePath, filename string) core.FileType {
	relPath, _ := filepath.Rel(dataPath, filePath)
	parts := strings.Split(relPath, string(filepath.Separator))

	if len(parts) == 0 {
		return core.Unknown
	}

	switch parts[0] {
	case "edge":
		return classifyEdgeFile(parts, filename)
	case "assets":
		return classifyAssetFile(parts, filename)
	case "players":
		// Both real player landings and 404-cache markers map to
		// core.PlayerLanding (see resource.MissingPlayerLanding.Type()).
		return core.PlayerLanding
	case "players-missing":
		return core.PlayerLanding
	case "yahoo-players", "yahoo-players-missing":
		return core.YahooPlayer
	case "game-keys":
		return core.GameKey
	case "nhl":
		if ft := classifyNHLFile(parts, filename); ft != core.Unknown {
			return ft
		}
	case "seasons":
		if ft := classifySeasonFile(parts, filename); ft != core.Unknown {
			return ft
		}
	}

	return classifyByFilenamePrefix(filename)
}

// classifyByFilenamePrefix is the path-agnostic fallback. Production layout
// puts these files under seasons/{S}/... or seasons/{S}/yahoo/{lid}/... — the
// real classifier paths catch those cases first. This fallback exists so test
// fixtures with synthetic flat paths and any future layout drift still emit
// the right Prometheus labels rather than collapsing to Unknown.
//
// Order matters: team- + -summary- is more specific than team- alone.
func classifyByFilenamePrefix(filename string) core.FileType {
	switch {
	case strings.HasPrefix(filename, "boxscore-"):
		return core.Boxscore
	case strings.HasPrefix(filename, "playbyplay-"):
		return core.PlayByPlay
	case strings.HasPrefix(filename, "shiftchart-"):
		return core.ShiftChart
	case strings.HasPrefix(filename, "gamestory-"):
		return core.GameStory
	case strings.HasPrefix(filename, "seasonseries-"):
		return core.SeasonSeries
	case strings.HasPrefix(filename, "daily-schedule-"):
		return core.DailySchedule
	case strings.HasPrefix(filename, "league-"):
		return core.League
	case strings.HasPrefix(filename, "team-") && strings.Contains(filename, "-summary-"):
		return core.TeamSummary
	case strings.HasPrefix(filename, "team-"):
		return core.Team
	case strings.HasPrefix(filename, "rosters-"):
		return core.Roster
	}
	return core.Unknown
}

// classifyNHLFile classifies files under the top-level nhl/ directory. The
// franchises and seasons-manifest singletons are matched both with and without
// the .json extension because os.DirEntry.Name() returns the full filename in
// production while existing test fixtures pass the extensionless form.
func classifyNHLFile(parts []string, filename string) core.FileType {
	switch filename {
	case "franchises", "franchises.json":
		return core.Franchises
	case "seasons-manifest", "seasons-manifest.json":
		return core.SeasonsManifest
	}
	// nhl/standings/standings-{seasonID}.json (vs DailyStandings under games/)
	if len(parts) >= 2 && parts[1] == "standings" && strings.HasPrefix(filename, "standings-") {
		return core.SeasonStandings
	}
	return core.Unknown
}

// classifySeasonFile classifies files under seasons/{year}/{subdir}/.../...
// parts[1] is the season year; parts[2] is the immediate subdir.
//
// Layouts (from resource/nhl.go and resource/yahoo.go):
//
//	seasons/{Y}/games/{YYYY}/{MM}/{DD}/{boxscore,playbyplay,...}-{gameID}.json
//	seasons/{Y}/rosters/roster-{teamAbbrev}.json
//	seasons/{Y}/clubstats/clubstats-{teamAbbrev}-{gameType}.json
//	seasons/{Y}/club-schedule/club-schedule-{teamAbbrev}.json
//	seasons/{Y}/player-gamelogs/player-{playerID}-{gameType}.json
//	seasons/{Y}/yahoo/{leagueID}/{league,teams,rosters,summaries,transactions,draft,matchups}/...
func classifySeasonFile(parts []string, filename string) core.FileType {
	if len(parts) < 3 {
		return core.Unknown
	}
	switch parts[2] {
	case "games":
		return classifyGamesFile(filename)
	case "rosters":
		// SeasonRoster uses singular "roster-" (Yahoo Roster uses plural "rosters-").
		if strings.HasPrefix(filename, "roster-") {
			return core.SeasonRoster
		}
	case "clubstats":
		if strings.HasPrefix(filename, "clubstats-") {
			return core.ClubStatsResource
		}
	case "club-schedule":
		if strings.HasPrefix(filename, "club-schedule-") {
			return core.ClubScheduleSeasonResource
		}
	case "player-gamelogs":
		if strings.HasPrefix(filename, "player-") {
			return core.PlayerGameLog
		}
	case "yahoo":
		return classifyYahooSeasonFile(parts, filename)
	}
	return core.Unknown
}

// classifyGamesFile dispatches by filename prefix within
// seasons/{Y}/games/{YYYY}/{MM}/{DD}/.
//
// "standings-YYYY-MM-DD.json" here is DailyStandings; the same "standings-"
// prefix under nhl/standings/ is SeasonStandings — disambiguated by parent
// directory in classifyNHLFile.
func classifyGamesFile(filename string) core.FileType {
	switch {
	case strings.HasPrefix(filename, "boxscore-"):
		return core.Boxscore
	case strings.HasPrefix(filename, "playbyplay-"):
		return core.PlayByPlay
	case strings.HasPrefix(filename, "shiftchart-"):
		return core.ShiftChart
	case strings.HasPrefix(filename, "gamestory-"):
		return core.GameStory
	case strings.HasPrefix(filename, "seasonseries-"):
		return core.SeasonSeries
	case strings.HasPrefix(filename, "daily-schedule-"):
		return core.DailySchedule
	case strings.HasPrefix(filename, "standings-"):
		return core.DailyStandings
	}
	return core.Unknown
}

// classifyYahooSeasonFile classifies files under
// seasons/{Y}/yahoo/{leagueID}/{subdir}/...
// parts[3] is the league ID; parts[4] is the subdir.
func classifyYahooSeasonFile(parts []string, filename string) core.FileType {
	if len(parts) < 5 {
		return core.Unknown
	}
	switch parts[4] {
	case "league":
		// Single file: league.xml
		return core.League
	case "teams":
		if strings.HasPrefix(filename, "team-") {
			return core.Team
		}
	case "rosters":
		if strings.HasPrefix(filename, "rosters-") {
			return core.Roster
		}
	case "summaries":
		if strings.HasPrefix(filename, "team-") && strings.Contains(filename, "-summary-") {
			return core.TeamSummary
		}
	case "transactions":
		if filename == "transactions.xml" {
			return core.YahooTransactions
		}
	case "draft":
		if filename == "draftresults.xml" {
			return core.YahooDraftResults
		}
	case "matchups":
		if strings.HasPrefix(filename, "week-") {
			return core.YahooMatchups
		}
	}
	return core.Unknown
}

// classifyAssetFile classifies files under the top-level assets/ directory.
//
// The path layouts here are duplicated from worker/asset/path_builders.go and
// must be kept in sync — the workflow writes files using the builder, this
// function reads them back via path inspection. A drift will silently
// reclassify newly-stored asset files as Unknown.
//
// Filename prefixes within each subtree are pairwise-disjoint, so the case
// ordering does not affect correctness.
func classifyAssetFile(parts []string, filename string) core.FileType {
	if len(parts) < 2 {
		return core.Unknown
	}
	switch parts[1] {
	case "players":
		switch {
		case strings.HasPrefix(filename, "headshot."):
			return core.PlayerHeadshot
		case strings.HasPrefix(filename, "hero."):
			return core.PlayerHeroImage
		case strings.HasPrefix(filename, "yahoo-small."):
			return core.PlayerYahooImageSmall
		case strings.HasPrefix(filename, "yahoo-medium."):
			return core.PlayerYahooImageMedium
		case strings.HasPrefix(filename, "yahoo-large."):
			return core.PlayerYahooImageLarge
		}
	case "teams":
		if len(parts) >= 3 && parts[2] == "logos" {
			return core.TeamLogo
		}
	case "yahoo":
		if len(parts) >= 3 && parts[2] == "leagues" {
			switch {
			case strings.HasPrefix(filename, "league-logo."):
				return core.YahooLeagueLogo
			case strings.HasPrefix(filename, "manager-"):
				return core.YahooManagerImage
			case strings.HasPrefix(filename, "logo."):
				return core.YahooTeamLogo
			}
		}
	}
	return core.Unknown
}

// classifyEdgeFile classifies files under the top-level edge/ directory into
// one of 22 granular Edge FileType values. Path layouts are duplicated from
// resource/edge.go and must be kept in sync.
//
// Layout:
//
//	edge/skaters/{playerID}/{prefix}-{season}-{gameType}.json
//	edge/goalies/{playerID}/{prefix}-{season}-{gameType}.json
//	edge/teams/{teamID}/{prefix}-{season}-{gameType}.json
//	edge/landing/{kind}-{season}-{gameType}.json
//
// Within each subtree, filename prefixes are pairwise-disjoint with one
// exception: under teams/, "zone-time-details-" must be matched before any
// hypothetical "zone-time-" sibling (currently absent). The skater's plain
// "zone-time-" lives under skaters/ and so cannot collide.
func classifyEdgeFile(parts []string, filename string) core.FileType {
	if len(parts) < 2 {
		return core.Unknown
	}
	switch parts[1] {
	case "skaters":
		switch {
		case strings.HasPrefix(filename, "detail-"):
			return core.EdgeSkaterDetail
		case strings.HasPrefix(filename, "speed-"):
			return core.EdgeSkaterSpeedDetail
		case strings.HasPrefix(filename, "distance-"):
			return core.EdgeSkaterDistanceDetail
		case strings.HasPrefix(filename, "shot-speed-"):
			return core.EdgeSkaterShotSpeedDetail
		case strings.HasPrefix(filename, "shot-location-"):
			return core.EdgeSkaterShotLocationDetail
		case strings.HasPrefix(filename, "zone-time-"):
			return core.EdgeSkaterZoneTime
		case strings.HasPrefix(filename, "comparison-"):
			return core.EdgeSkaterComparison
		}
	case "goalies":
		switch {
		case strings.HasPrefix(filename, "detail-"):
			return core.EdgeGoalieDetail
		case strings.HasPrefix(filename, "5v5-"):
			return core.EdgeGoalie5v5Detail
		case strings.HasPrefix(filename, "shot-location-"):
			return core.EdgeGoalieShotLocationDetail
		case strings.HasPrefix(filename, "save-pctg-"):
			return core.EdgeGoalieSavePctgDetail
		case strings.HasPrefix(filename, "comparison-"):
			return core.EdgeGoalieComparison
		}
	case "teams":
		switch {
		case strings.HasPrefix(filename, "detail-"):
			return core.EdgeTeamDetail
		case strings.HasPrefix(filename, "speed-"):
			return core.EdgeTeamSpeedDetail
		case strings.HasPrefix(filename, "distance-"):
			return core.EdgeTeamDistanceDetail
		case strings.HasPrefix(filename, "shot-speed-"):
			return core.EdgeTeamShotSpeedDetail
		case strings.HasPrefix(filename, "shot-location-"):
			return core.EdgeTeamShotLocationDetail
		case strings.HasPrefix(filename, "zone-time-details-"):
			return core.EdgeTeamZoneTimeDetails
		case strings.HasPrefix(filename, "comparison-"):
			return core.EdgeTeamComparison
		}
	case "landing":
		switch {
		case strings.HasPrefix(filename, "skater-"):
			return core.EdgeSkaterLanding
		case strings.HasPrefix(filename, "goalie-"):
			return core.EdgeGoalieLanding
		case strings.HasPrefix(filename, "team-"):
			return core.EdgeTeamLanding
		}
	}
	return core.Unknown
}
