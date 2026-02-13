package cmd

import (
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/sperano/puckdb/cache"
)

// calculateDirSize calculates the total size of a directory, excluding .git
func calculateDirSize(path string) (int64, error) {
	var size int64
	err := filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Skip .git directory
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			size += info.Size()
		}
		return nil
	})
	return size, err
}

// fileTypeStats holds count and size for a file type
type fileTypeStats struct {
	count int64
	bytes int64
}

// collectFileTypeStats walks the data path and categorizes files by type
func collectFileTypeStats(dataPath string) map[string]*fileTypeStats {
	stats := make(map[string]*fileTypeStats)

	_ = filepath.WalkDir(dataPath, func(filePath string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}

		// Skip .git directory
		if strings.Contains(filePath, "/.git/") {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		fileType := classifyFileType(dataPath, filePath, d.Name())
		if stats[fileType] == nil {
			stats[fileType] = &fileTypeStats{}
		}
		stats[fileType].count++
		stats[fileType].bytes += info.Size()

		return nil
	})

	return stats
}

// classifyFileType determines the file type based on path and filename
func classifyFileType(dataPath, filePath, filename string) string {
	relPath, _ := filepath.Rel(dataPath, filePath)
	parts := strings.Split(relPath, string(filepath.Separator))

	if len(parts) == 0 {
		return cache.FileTypeUnknown
	}

	// Check top-level directories first
	switch parts[0] {
	case "players":
		return cache.FileTypePlayerLanding
	case "yahoo-players":
		return cache.FileTypeYahooPlayer
	case "yahoo-players-missing":
		return cache.FileTypeYahooPlayer
	case "game-keys":
		return cache.FileTypeGameKey
	}

	// Check filename patterns for files in nested directories
	// Note: Order matters! More specific prefixes must come first.
	if strings.HasPrefix(filename, "boxscore-") {
		return cache.FileTypeBoxscore
	}
	if strings.HasPrefix(filename, "daily-schedule-") {
		return cache.FileTypeDailySchedule
	}
	if strings.HasPrefix(filename, "league-") {
		return cache.FileTypeLeague
	}
	// Check team-summary- BEFORE team- (more specific match first)
	if strings.HasPrefix(filename, "team-summary-") {
		return cache.FileTypeTeamSummary
	}
	if strings.HasPrefix(filename, "team-") {
		return cache.FileTypeTeam
	}
	if strings.HasPrefix(filename, "rosters-") {
		return cache.FileTypeRoster
	}

	return cache.FileTypeUnknown
}
