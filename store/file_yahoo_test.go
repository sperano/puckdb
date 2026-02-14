package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestParseYahooPlayerFilename(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		filename   string
		expectedID int
		shouldNil  bool
	}{
		{
			name:       "valid player ID",
			filename:   "player-123",
			expectedID: 123,
			shouldNil:  false,
		},
		{
			name:       "another valid ID",
			filename:   "player-9999",
			expectedID: 9999,
			shouldNil:  false,
		},
		{
			name:      "missing prefix",
			filename:  "123",
			shouldNil: true,
		},
		{
			name:      "non-numeric ID",
			filename:  "player-abc",
			shouldNil: true,
		},
		{
			name:      "empty string",
			filename:  "",
			shouldNil: true,
		},
		{
			name:      "wrong prefix",
			filename:  "plyr-123",
			shouldNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseYahooPlayerFilename(tt.filename)
			if tt.shouldNil {
				assert.Nil(t, result)
			} else {
				ypf, ok := result.(YahooPlayerFile)
				assert.True(t, ok)
				assert.Equal(t, tt.expectedID, ypf.PlayerID)
			}
		})
	}
}

func TestLeagueFile(t *testing.T) {
	t.Parallel()

	file := LeagueFile{Season: 2024, LeagueID: 12345}

	assert.Equal(t, "xml", file.Ext())
	assert.Equal(t, "2024/12345/league", file.Dir())
	assert.Equal(t, "league", file.Name())
}

func TestTeamFile(t *testing.T) {
	t.Parallel()

	file := TeamFile{Season: 2024, LeagueID: 12345, TeamID: 7}

	assert.Equal(t, "xml", file.Ext())
	assert.Equal(t, "2024/12345/teams/team-07", file.Dir())
	assert.Equal(t, "team-07", file.Name())
}

func TestTeamDir(t *testing.T) {
	t.Parallel()

	result := teamDir(2024, 12345, 3)
	assert.Equal(t, "2024/12345/teams/team-03", result)
}

func TestRosterFile(t *testing.T) {
	t.Parallel()

	// Test date in October (same year season)
	date := time.Date(2024, time.October, 15, 0, 0, 0, 0, time.UTC)
	file := RosterFile{TeamID: 5, LeagueID: 12345, Date: date}

	assert.Equal(t, "xml", file.Ext())
	assert.Equal(t, "2024/12345/rosters/team-05", file.Dir())
	assert.Equal(t, "rosters-05-2024-10-15", file.Name())
}

func TestRosterFile_JanuaryDate(t *testing.T) {
	t.Parallel()

	// Test date in January (previous year season)
	date := time.Date(2025, time.January, 10, 0, 0, 0, 0, time.UTC)
	file := RosterFile{TeamID: 3, LeagueID: 99999, Date: date}

	assert.Equal(t, "xml", file.Ext())
	// Season should be 2024 (DeduceSeason returns 2024 for Jan 2025)
	assert.Equal(t, "2024/99999/rosters/team-03", file.Dir())
	assert.Equal(t, "rosters-03-2025-01-10", file.Name())
}

func TestRostersDir(t *testing.T) {
	t.Parallel()

	result := rostersDir(2024, 12345, 5)
	assert.Equal(t, "2024/12345/rosters/team-05", result)
}

func TestTeamSummaryFile(t *testing.T) {
	t.Parallel()

	date := time.Date(2024, time.November, 20, 0, 0, 0, 0, time.UTC)
	file := TeamSummaryFile{TeamID: 8, LeagueID: 54321, Date: date}

	assert.Equal(t, "xml", file.Ext())
	assert.Equal(t, "2024/54321/summaries/team-08", file.Dir())
	assert.Equal(t, "team-08-summary-2024-11-20", file.Name())
}

func TestTeamSummaryDir(t *testing.T) {
	t.Parallel()

	result := teamSummaryDir(2024, 12345, 9)
	assert.Equal(t, "2024/12345/summaries/team-09", result)
}

func TestYahooPlayerFile(t *testing.T) {
	t.Parallel()

	file := YahooPlayerFile{PlayerID: 12345}

	assert.Equal(t, "html", file.Ext())
	assert.Equal(t, "yahoo-players", file.Dir())
	assert.Equal(t, "player-12345", file.Name())
}

func TestMissingYahooPlayerFile(t *testing.T) {
	t.Parallel()

	file := MissingYahooPlayerFile{PlayerID: 99999}

	assert.Equal(t, "txt", file.Ext())
	assert.Equal(t, "yahoo-players-missing", file.Dir())
	assert.Equal(t, "player-99999", file.Name())
}

func TestGameKeyFile(t *testing.T) {
	t.Parallel()

	file := GameKeyFile{Season: 2024}

	assert.Equal(t, "xml", file.Ext())
	assert.Equal(t, "game-keys", file.Dir())
	assert.Equal(t, "gamekey-2024", file.Name())
}
