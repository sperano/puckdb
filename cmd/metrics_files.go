package cmd

import (
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/sperano/puckdb/store"
)

// fileTypeStats holds count and size for a file type
type fileTypeStats struct {
	count int64
	bytes int64
}

// dataPathStats holds combined results from a single directory walk.
type dataPathStats struct {
	totalBytes int64
	byType     map[string]*fileTypeStats
}

// collectDataPathStats walks the data path once, computing both total disk size
// and per-file-type statistics in a single pass.
func collectDataPathStats(dataPath string) dataPathStats {
	result := dataPathStats{byType: make(map[string]*fileTypeStats)}

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

// classifyFileType determines the file type based on path and filename.
func classifyFileType(dataPath, filePath, filename string) string {
	relPath, _ := filepath.Rel(dataPath, filePath)
	parts := strings.Split(relPath, string(filepath.Separator))

	if len(parts) == 0 {
		return store.FileTypeUnknown
	}

	// Check top-level directories first
	switch parts[0] {
	case "edge":
		return store.FileTypeEdge
	case "players":
		return store.FileTypePlayerLanding
	case "player-gamelogs":
		return store.FileTypePlayerGameLog
	case "yahoo-players":
		return store.FileTypeYahooPlayer
	case "yahoo-players-missing":
		return store.FileTypeYahooPlayer
	case "game-keys":
		return store.FileTypeGameKey
	case "assets":
		return classifyAssetFile(parts, filename)
	case "nhl":
		// nhl/franchises.json
		if filename == "franchises" {
			return store.FileTypeFranchises
		}
		// nhl/seasons-manifest.json
		if filename == "seasons-manifest" {
			return store.FileTypeSeasonsManifest
		}
		// nhl/standings/standings-*.json
		if len(parts) >= 2 && parts[1] == "standings" && strings.HasPrefix(filename, "standings-") {
			return store.FileTypeSeasonStandings
		}
	}

	// Check filename patterns for files in nested directories
	// Note: Order matters! More specific prefixes must come first.
	if strings.HasPrefix(filename, "boxscore-") {
		return store.FileTypeBoxscore
	}
	if strings.HasPrefix(filename, "playbyplay-") {
		return store.FileTypePlayByPlay
	}
	if strings.HasPrefix(filename, "shiftchart-") {
		return store.FileTypeShiftChart
	}
	if strings.HasPrefix(filename, "gamestory-") {
		return store.FileTypeGameStory
	}
	if strings.HasPrefix(filename, "daily-schedule-") {
		return store.FileTypeDailySchedule
	}
	if strings.HasPrefix(filename, "league-") {
		return store.FileTypeLeague
	}
	// Check team summary files (team-XX-summary-*) BEFORE team- (more specific match first)
	if strings.HasPrefix(filename, "team-") && strings.Contains(filename, "-summary-") {
		return store.FileTypeTeamSummary
	}
	if strings.HasPrefix(filename, "team-") {
		return store.FileTypeTeam
	}
	if strings.HasPrefix(filename, "rosters-") {
		return store.FileTypeRoster
	}

	return store.FileTypeUnknown
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
func classifyAssetFile(parts []string, filename string) string {
	if len(parts) < 2 {
		return store.FileTypeUnknown
	}
	switch parts[1] {
	case "players":
		switch {
		case strings.HasPrefix(filename, "headshot."):
			return store.FileTypePlayerHeadshot
		case strings.HasPrefix(filename, "hero."):
			return store.FileTypePlayerHeroImage
		case strings.HasPrefix(filename, "yahoo-small."):
			return store.FileTypePlayerYahooImageSmall
		case strings.HasPrefix(filename, "yahoo-medium."):
			return store.FileTypePlayerYahooImageMedium
		case strings.HasPrefix(filename, "yahoo-large."):
			return store.FileTypePlayerYahooImageLarge
		}
	case "teams":
		if len(parts) >= 3 && parts[2] == "logos" {
			return store.FileTypeTeamLogo
		}
	case "yahoo":
		if len(parts) >= 3 && parts[2] == "leagues" {
			switch {
			case strings.HasPrefix(filename, "league-logo."):
				return store.FileTypeYahooLeagueLogo
			case strings.HasPrefix(filename, "manager-"):
				return store.FileTypeYahooManagerImage
			case strings.HasPrefix(filename, "logo."):
				return store.FileTypeYahooTeamLogo
			}
		}
	}
	return store.FileTypeUnknown
}
