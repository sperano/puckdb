package store

import (
	"testing"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/stretchr/testify/assert"
)

func TestDeduceSeason(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		date     time.Time
		expected int
	}{
		{
			name:     "September - start of season",
			date:     time.Date(2024, time.September, 15, 0, 0, 0, 0, time.UTC),
			expected: 2024,
		},
		{
			name:     "October",
			date:     time.Date(2024, time.October, 10, 0, 0, 0, 0, time.UTC),
			expected: 2024,
		},
		{
			name:     "December",
			date:     time.Date(2024, time.December, 25, 0, 0, 0, 0, time.UTC),
			expected: 2024,
		},
		{
			name:     "January - previous year season",
			date:     time.Date(2025, time.January, 15, 0, 0, 0, 0, time.UTC),
			expected: 2024,
		},
		{
			name:     "April - playoff time",
			date:     time.Date(2025, time.April, 20, 0, 0, 0, 0, time.UTC),
			expected: 2024,
		},
		{
			name:     "August - offseason",
			date:     time.Date(2025, time.August, 1, 0, 0, 0, 0, time.UTC),
			expected: 2024,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DeduceSeason(tt.date)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParsePlayerLandingFilename(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		filename   string
		expectedID nhl.PlayerID
		shouldNil  bool
	}{
		{
			name:       "valid player ID",
			filename:   "player-8478402-landing",
			expectedID: 8478402,
			shouldNil:  false,
		},
		{
			name:       "another valid ID",
			filename:   "player-123-landing",
			expectedID: 123,
			shouldNil:  false,
		},
		{
			name:      "missing prefix",
			filename:  "8478402-landing",
			shouldNil: true,
		},
		{
			name:      "missing suffix",
			filename:  "player-8478402",
			shouldNil: true,
		},
		{
			name:      "non-numeric ID",
			filename:  "player-abc-landing",
			shouldNil: true,
		},
		{
			name:      "empty string",
			filename:  "",
			shouldNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParsePlayerLandingFilename(tt.filename)
			if tt.shouldNil {
				assert.Nil(t, result)
			} else {
				plf, ok := result.(PlayerLandingFile)
				assert.True(t, ok)
				assert.Equal(t, tt.expectedID, plf.PlayerID)
			}
		})
	}
}

func TestDailyScheduleFile(t *testing.T) {
	t.Parallel()

	date := time.Date(2024, time.January, 15, 0, 0, 0, 0, time.UTC)
	file := DailyScheduleFile{Date: date}

	assert.Equal(t, "json", file.Ext())
	assert.Equal(t, "2023/games/2024/01/15", file.Dir())
	assert.Equal(t, "daily-schedule-2024-01-15", file.Name())
}

func TestBoxscoreFile(t *testing.T) {
	t.Parallel()

	date := time.Date(2024, time.October, 10, 0, 0, 0, 0, time.UTC)
	file := BoxscoreFile{Date: date, GameID: 2024020001}

	assert.Equal(t, "json", file.Ext())
	assert.Equal(t, "2024/games/2024/10/10", file.Dir())
	assert.Equal(t, "boxscore-2024020001", file.Name())
}

func TestPlayerLandingFile(t *testing.T) {
	t.Parallel()

	file := PlayerLandingFile{PlayerID: 8478402}

	assert.Equal(t, "json", file.Ext())
	assert.Equal(t, "players", file.Dir())
	assert.Equal(t, "player-8478402-landing", file.Name())
}

func TestMissingPlayerLandingFile(t *testing.T) {
	t.Parallel()

	file := MissingPlayerLandingFile{PlayerID: 8478402}

	assert.Equal(t, "json", file.Ext())
	assert.Equal(t, "players-missing", file.Dir())
	assert.Equal(t, "player-8478402", file.Name())
}

func TestFranchisesFile(t *testing.T) {
	t.Parallel()

	file := FranchisesFile{}

	assert.Equal(t, "json", file.Ext())
	assert.Equal(t, "nhl", file.Dir())
	assert.Equal(t, "franchises", file.Name())
}

func TestSeasonsManifestFile(t *testing.T) {
	t.Parallel()

	file := SeasonsManifestFile{}

	assert.Equal(t, "json", file.Ext())
	assert.Equal(t, "nhl", file.Dir())
	assert.Equal(t, "seasons-manifest", file.Name())
}

func TestSeasonStandingsFile(t *testing.T) {
	t.Parallel()

	file := SeasonStandingsFile{SeasonID: 20232024}

	assert.Equal(t, "json", file.Ext())
	assert.Equal(t, "nhl/standings", file.Dir())
	assert.Equal(t, "standings-20232024", file.Name())
}

func TestGamesListDir(t *testing.T) {
	t.Parallel()

	// Test October date (uses same year for season)
	date1 := time.Date(2024, time.October, 15, 0, 0, 0, 0, time.UTC)
	dir1 := gamesListDir(date1)
	assert.Equal(t, "2024/games/2024/10/15", dir1)

	// Test January date (uses previous year for season)
	date2 := time.Date(2025, time.January, 10, 0, 0, 0, 0, time.UTC)
	dir2 := gamesListDir(date2)
	assert.Equal(t, "2024/games/2025/01/10", dir2)
}
