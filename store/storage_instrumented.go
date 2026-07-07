package store

import (
	"context"
	"os"
	"regexp"
	"strings"
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

// File type matchers for path-based inference.
// Order matters: more specific patterns should come before general ones.
//
// Most patterns are fixed substrings (or simple prefix/suffix checks), so
// they're matched directly with strings.Contains/HasPrefix/HasSuffix rather
// than compiling and evaluating a regexp on every filesystem operation.
// Only the Yahoo team/league/game-key patterns have two independently
// wildcarded path segments (e.g. "yahoo/.*/teams/.*/team.xml$") and still
// need a real regexp to match correctly.
var fileTypePatterns = []struct {
	match    func(path string) bool
	fileType string
}{
	// NHL patterns
	{containsMatcher("/daily-schedule/"), "DailyScheduleFile"},
	{containsMatcher("/boxscores/"), "BoxscoreFile"},
	{containsMatcher("/play-by-play/"), "PlayByPlayFile"},
	{containsMatcher("/shiftcharts/"), "ShiftChartFile"},
	{containsMatcher("/gamestory/"), "GameStoryFile"},
	{containsMatcher("player-landings/missing/"), "PlayerLandingMissingFile"},
	{containsMatcher("player-landings/"), "PlayerLandingFile"},
	{containsMatcher("/player-gamelogs/"), "PlayerGameLogFile"},
	{exactMatcher("franchises.json"), "FranchisesFile"},
	{exactMatcher("seasons.json"), "SeasonsManifestFile"},
	{suffixMatcher("/standings.json"), "SeasonStandingsFile"},

	// Yahoo patterns
	{containsMatcher("yahoo/players/missing/"), "MissingYahooPlayerFile"},
	{containsMatcher("yahoo/players/"), "YahooPlayerFile"},
	{containsMatcher("yahoo/rosters/"), "RosterFile"},
	{containsMatcher("yahoo/team-summary/"), "TeamSummaryFile"},
	{regexpMatcher(`yahoo/.*/teams/.*/team\.xml$`), "TeamFile"},
	{regexpMatcher(`yahoo/.*/leagues/.*/league\.xml$`), "LeagueFile"},
	{regexpMatcher(`yahoo/.*/game-key\.xml$`), "GameKeyFile"},
}

// containsMatcher returns a matcher that reports whether path contains sub.
func containsMatcher(sub string) func(path string) bool {
	return func(path string) bool { return strings.Contains(path, sub) }
}

// exactMatcher returns a matcher that reports whether path equals s exactly.
func exactMatcher(s string) func(path string) bool {
	return func(path string) bool { return path == s }
}

// suffixMatcher returns a matcher that reports whether path ends with suffix.
func suffixMatcher(suffix string) func(path string) bool {
	return func(path string) bool { return strings.HasSuffix(path, suffix) }
}

// regexpMatcher compiles pattern once and returns a matcher backed by it, for
// patterns with more than one wildcarded path segment where substring checks
// alone can't preserve the required ordering.
func regexpMatcher(pattern string) func(path string) bool {
	re := regexp.MustCompile(pattern)
	return re.MatchString
}

// inferFileType determines the file type label from a path string.
func inferFileType(path string) string {
	for _, p := range fileTypePatterns {
		if p.match(path) {
			return p.fileType
		}
	}
	return "unknown"
}
