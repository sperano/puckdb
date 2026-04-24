package store

import (
	"context"
	"os"
	"regexp"
	"time"

	"github.com/sperano/puckdb/metrics"
)

// InstrumentedStorage wraps a Storage to record metrics for all operations.
type InstrumentedStorage struct {
	inner Storage
}

// NewInstrumentedStorage creates an instrumented wrapper around the given Storage.
func NewInstrumentedStorage(inner Storage) *InstrumentedStorage {
	return &InstrumentedStorage{inner: inner}
}

// Read reads from storage and records timing/size metrics.
func (s *InstrumentedStorage) Read(ctx context.Context, path string) ([]byte, error) {
	start := time.Now()
	data, err := s.inner.Read(ctx, path)
	duration := time.Since(start)

	ft := inferFileType(path)
	if err == nil {
		metrics.ObserveFSOp("read", ft, duration, len(data))
	} else {
		metrics.ObserveFSOp("read", ft, duration, 0)
	}
	return data, err
}

// Write writes to storage and records timing/size metrics.
func (s *InstrumentedStorage) Write(ctx context.Context, path string, data []byte) error {
	start := time.Now()
	err := s.inner.Write(ctx, path, data)
	duration := time.Since(start)

	ft := inferFileType(path)
	if err == nil {
		metrics.ObserveFSOp("write", ft, duration, len(data))
	} else {
		metrics.ObserveFSOp("write", ft, duration, 0)
	}
	return err
}

// Exists checks if a path exists and records timing metrics.
func (s *InstrumentedStorage) Exists(ctx context.Context, path string) bool {
	start := time.Now()
	exists := s.inner.Exists(ctx, path)
	duration := time.Since(start)

	metrics.ObserveFSOp("exists", inferFileType(path), duration, 0)
	return exists
}

// Delete removes a path and records timing metrics.
func (s *InstrumentedStorage) Delete(ctx context.Context, path string) error {
	start := time.Now()
	err := s.inner.Delete(ctx, path)
	duration := time.Since(start)

	metrics.ObserveFSOp("delete", inferFileType(path), duration, 0)
	return err
}

// List lists files and records timing metrics.
func (s *InstrumentedStorage) List(ctx context.Context, dir string, ext string) ([]string, error) {
	start := time.Now()
	files, err := s.inner.List(ctx, dir, ext)
	duration := time.Since(start)

	metrics.ObserveFSOp("list", inferFileType(dir), duration, len(files))
	return files, err
}

// Stat returns file information and records timing metrics.
func (s *InstrumentedStorage) Stat(ctx context.Context, path string) (os.FileInfo, error) {
	start := time.Now()
	info, err := s.inner.Stat(ctx, path)
	duration := time.Since(start)

	metrics.ObserveFSOp("stat", inferFileType(path), duration, 0)
	return info, err
}

// File type patterns for path-based inference.
// Order matters: more specific patterns should come before general ones.
var fileTypePatterns = []struct {
	pattern  *regexp.Regexp
	fileType string
}{
	// NHL patterns
	{regexp.MustCompile(`/daily-schedule/`), "DailyScheduleFile"},
	{regexp.MustCompile(`/boxscores/`), "BoxscoreFile"},
	{regexp.MustCompile(`/play-by-play/`), "PlayByPlayFile"},
	{regexp.MustCompile(`/shiftcharts/`), "ShiftChartFile"},
	{regexp.MustCompile(`/gamestory/`), "GameStoryFile"},
	{regexp.MustCompile(`player-landings/missing/`), "PlayerLandingMissingFile"},
	{regexp.MustCompile(`player-landings/`), "PlayerLandingFile"},
	{regexp.MustCompile(`/player-gamelogs/`), "PlayerGameLogFile"},
	{regexp.MustCompile(`^franchises\.json$`), "FranchisesFile"},
	{regexp.MustCompile(`^seasons\.json$`), "SeasonsManifestFile"},
	{regexp.MustCompile(`/standings\.json$`), "SeasonStandingsFile"},

	// Yahoo patterns
	{regexp.MustCompile(`yahoo/players/missing/`), "MissingYahooPlayerFile"},
	{regexp.MustCompile(`yahoo/players/`), "YahooPlayerFile"},
	{regexp.MustCompile(`yahoo/rosters/`), "RosterFile"},
	{regexp.MustCompile(`yahoo/team-summary/`), "TeamSummaryFile"},
	{regexp.MustCompile(`yahoo/.*/teams/.*/team\.xml$`), "TeamFile"},
	{regexp.MustCompile(`yahoo/.*/leagues/.*/league\.xml$`), "LeagueFile"},
	{regexp.MustCompile(`yahoo/.*/game-key\.xml$`), "GameKeyFile"},
}

// inferFileType determines the file type label from a path string.
func inferFileType(path string) string {
	for _, p := range fileTypePatterns {
		if p.pattern.MatchString(path) {
			return p.fileType
		}
	}
	return "unknown"
}
