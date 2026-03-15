package store

import (
	"testing"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInstrumentedStorage_Read(t *testing.T) {
	t.Parallel()
	inner := NewMemStorage()
	storage := NewInstrumentedStorage(inner)

	// Setup
	path := DailySchedulePath(time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC))
	inner.SetFile(path, []byte(`{"games": []}`))

	// Test
	data, err := storage.Read(path)
	require.NoError(t, err)
	assert.Equal(t, []byte(`{"games": []}`), data)
}

func TestInstrumentedStorage_Write(t *testing.T) {
	t.Parallel()
	inner := NewMemStorage()
	storage := NewInstrumentedStorage(inner)

	path := BoxscorePath(time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), nhl.GameID(2024020123))
	err := storage.Write(path, []byte(`{"id": 2024020123}`))
	require.NoError(t, err)

	// Verify via inner storage
	assert.True(t, inner.Has(path))
}

func TestInstrumentedStorage_Exists(t *testing.T) {
	t.Parallel()
	inner := NewMemStorage()
	storage := NewInstrumentedStorage(inner)

	path := "player-landings/player-8474564.json"
	assert.False(t, storage.Exists(path))

	inner.SetFile(path, []byte(`{}`))
	assert.True(t, storage.Exists(path))
}

func TestInstrumentedStorage_Delete(t *testing.T) {
	t.Parallel()
	inner := NewMemStorage()
	storage := NewInstrumentedStorage(inner)

	path := "player-landings/missing/player-12345.json"
	inner.SetFile(path, []byte(`{}`))

	err := storage.Delete(path)
	require.NoError(t, err)
	assert.False(t, inner.Has(path))
}

func TestInstrumentedStorage_List(t *testing.T) {
	t.Parallel()
	inner := NewMemStorage()
	storage := NewInstrumentedStorage(inner)

	// Setup
	inner.SetFile("20242025/boxscores/2024-01-15/2024020100.json", []byte(`{}`))
	inner.SetFile("20242025/boxscores/2024-01-15/2024020101.json", []byte(`{}`))

	files, err := storage.List("20242025/boxscores/2024-01-15", "json")
	require.NoError(t, err)
	assert.Len(t, files, 2)
}

// ============================================================================
// inferFileType tests
// ============================================================================

func TestInferFileType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path     string
		expected string
	}{
		// NHL file types
		{"20242025/daily-schedule/2024-01-15.json", "DailyScheduleFile"},
		{"20232024/daily-schedule/2023-10-01.json", "DailyScheduleFile"},

		{"20242025/boxscores/2024-01-15/2024020123.json", "BoxscoreFile"},
		{"20232024/boxscores/2023-10-01/2023020001.json", "BoxscoreFile"},

		{"20242025/play-by-play/2024-01-15/2024020123.json", "PlayByPlayFile"},
		{"20242025/shiftcharts/2024-01-15/2024020123.json", "ShiftChartFile"},
		{"20242025/gamestory/2024-01-15/2024020123.json", "GameStoryFile"},

		{"player-landings/player-8474564.json", "PlayerLandingFile"},
		{"player-landings/player-12345.json", "PlayerLandingFile"},
		{"player-landings/missing/player-8474564.json", "PlayerLandingMissingFile"},

		{"20242025/player-gamelogs/player-8474564-2.json", "PlayerGameLogFile"},

		{"franchises.json", "FranchisesFile"},
		{"seasons.json", "SeasonsManifestFile"},
		{"20242025/standings.json", "SeasonStandingsFile"},

		// Yahoo file types
		{"yahoo/players/player-12345.html", "YahooPlayerFile"},
		{"yahoo/players/missing/player-12345.html", "MissingYahooPlayerFile"},

		{"yahoo/rosters/12345/1/2024-01-15.xml", "RosterFile"},
		{"yahoo/team-summary/12345/1/2024-01-15.xml", "TeamSummaryFile"},

		{"yahoo/2023/teams/1/team.xml", "TeamFile"},
		{"yahoo/2023/leagues/12345/teams/1/team.xml", "TeamFile"},

		{"yahoo/2023/leagues/12345/league.xml", "LeagueFile"},

		{"yahoo/2023/game-key.xml", "GameKeyFile"},
		{"yahoo/2024/game-key.xml", "GameKeyFile"},

		// Unknown
		{"random/path/file.txt", "unknown"},
		{"foo.bar", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := inferFileType(tt.path)
			assert.Equal(t, tt.expected, result, "path: %s", tt.path)
		})
	}
}

// Verify that pattern matching produces same labels as old reflection-based approach.
// This ensures metrics continuity during migration.
func TestInferFileType_MatchesOldFileTypes(t *testing.T) {
	t.Parallel()

	// These are the exact type names used by reflect.TypeOf(file).Name()
	// in the old InstrumentedStore implementation.
	expectedTypes := map[string]string{
		"DailyScheduleFile":        "DailyScheduleFile",
		"BoxscoreFile":             "BoxscoreFile",
		"PlayByPlayFile":           "PlayByPlayFile",
		"ShiftChartFile":           "ShiftChartFile",
		"GameStoryFile":            "GameStoryFile",
		"PlayerLandingFile":        "PlayerLandingFile",
		"PlayerLandingMissingFile": "PlayerLandingMissingFile",
		"PlayerGameLogFile":        "PlayerGameLogFile",
		"FranchisesFile":           "FranchisesFile",
		"SeasonsManifestFile":      "SeasonsManifestFile",
		"SeasonStandingsFile":      "SeasonStandingsFile",
		"YahooPlayerFile":          "YahooPlayerFile",
		"MissingYahooPlayerFile":   "MissingYahooPlayerFile",
		"RosterFile":               "RosterFile",
		"TeamSummaryFile":          "TeamSummaryFile",
		"TeamFile":                 "TeamFile",
		"LeagueFile":               "LeagueFile",
		"GameKeyFile":              "GameKeyFile",
	}

	// Sample paths for each type
	pathToExpected := map[string]string{
		"20242025/daily-schedule/2024-01-15.json":          "DailyScheduleFile",
		"20242025/boxscores/2024-01-15/2024020123.json":    "BoxscoreFile",
		"20242025/play-by-play/2024-01-15/2024020123.json": "PlayByPlayFile",
		"20242025/shiftcharts/2024-01-15/2024020123.json":  "ShiftChartFile",
		"20242025/gamestory/2024-01-15/2024020123.json":    "GameStoryFile",
		"player-landings/player-8474564.json":              "PlayerLandingFile",
		"player-landings/missing/player-8474564.json":      "PlayerLandingMissingFile",
		"20242025/player-gamelogs/player-8474564-2.json":   "PlayerGameLogFile",
		"franchises.json":                           "FranchisesFile",
		"seasons.json":                              "SeasonsManifestFile",
		"20242025/standings.json":                   "SeasonStandingsFile",
		"yahoo/players/player-12345.html":           "YahooPlayerFile",
		"yahoo/players/missing/player-12345.html":   "MissingYahooPlayerFile",
		"yahoo/rosters/12345/1/2024-01-15.xml":      "RosterFile",
		"yahoo/team-summary/12345/1/2024-01-15.xml": "TeamSummaryFile",
		"yahoo/2023/leagues/12345/teams/1/team.xml": "TeamFile",
		"yahoo/2023/leagues/12345/league.xml":       "LeagueFile",
		"yahoo/2023/game-key.xml":                   "GameKeyFile",
	}

	for path, expected := range pathToExpected {
		result := inferFileType(path)
		assert.Equal(t, expectedTypes[expected], result,
			"path %q should produce file type %q matching old reflection name", path, expected)
	}
}
