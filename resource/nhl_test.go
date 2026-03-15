package resource_test

import (
	"testing"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/store"
)

// TestNHLPathCompatibility verifies that resource paths match the existing store path functions.
// This ensures backwards compatibility with existing stored data.

func TestDailySchedule_Path(t *testing.T) {
	tests := []struct {
		name string
		date time.Time
	}{
		{"mid_season", time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)},
		{"start_of_season", time.Date(2024, 10, 1, 0, 0, 0, 0, time.UTC)},
		{"end_of_season", time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := resource.DailySchedule{Date: tt.date}
			expected := store.DailySchedulePath(tt.date)
			if got := r.Path(); got != expected {
				t.Errorf("DailySchedule.Path() = %q, want %q", got, expected)
			}
		})
	}
}

func TestBoxscore_Path(t *testing.T) {
	tests := []struct {
		name   string
		date   time.Time
		gameID nhl.GameID
	}{
		{"regular_season", time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC), nhl.GameID(2024020123)},
		{"playoffs", time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC), nhl.GameID(2024030111)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := resource.Boxscore{Date: tt.date, GameID: tt.gameID}
			expected := store.BoxscorePath(tt.date, tt.gameID)
			if got := r.Path(); got != expected {
				t.Errorf("Boxscore.Path() = %q, want %q", got, expected)
			}
		})
	}
}

func TestPlayByPlay_Path(t *testing.T) {
	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	gameID := nhl.GameID(2024020123)

	r := resource.PlayByPlay{Date: date, GameID: gameID}
	expected := store.PlayByPlayPath(date, gameID)
	if got := r.Path(); got != expected {
		t.Errorf("PlayByPlay.Path() = %q, want %q", got, expected)
	}
}

func TestShiftChart_Path(t *testing.T) {
	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	gameID := nhl.GameID(2024020123)

	r := resource.ShiftChart{Date: date, GameID: gameID}
	expected := store.ShiftChartPath(date, gameID)
	if got := r.Path(); got != expected {
		t.Errorf("ShiftChart.Path() = %q, want %q", got, expected)
	}
}

func TestGameStory_Path(t *testing.T) {
	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	gameID := nhl.GameID(2024020123)

	r := resource.GameStory{Date: date, GameID: gameID}
	expected := store.GameStoryPath(date, gameID)
	if got := r.Path(); got != expected {
		t.Errorf("GameStory.Path() = %q, want %q", got, expected)
	}
}

func TestPlayerLanding_Path(t *testing.T) {
	playerID := nhl.PlayerID(8478402)

	r := resource.PlayerLanding{PlayerID: playerID}
	expected := store.PlayerLandingPath(playerID)
	if got := r.Path(); got != expected {
		t.Errorf("PlayerLanding.Path() = %q, want %q", got, expected)
	}
}

func TestMissingPlayerLanding_Path(t *testing.T) {
	playerID := nhl.PlayerID(8478402)

	r := resource.MissingPlayerLanding{PlayerID: playerID}
	expected := store.MissingPlayerLandingPath(playerID)
	if got := r.Path(); got != expected {
		t.Errorf("MissingPlayerLanding.Path() = %q, want %q", got, expected)
	}
}

func TestFranchises_Path(t *testing.T) {
	r := resource.Franchises{}
	expected := store.FranchisesPath()
	if got := r.Path(); got != expected {
		t.Errorf("Franchises.Path() = %q, want %q", got, expected)
	}
}

func TestSeasonsManifest_Path(t *testing.T) {
	r := resource.SeasonsManifest{}
	expected := store.SeasonsManifestPath()
	if got := r.Path(); got != expected {
		t.Errorf("SeasonsManifest.Path() = %q, want %q", got, expected)
	}
}

func TestSeasonStandings_Path(t *testing.T) {
	season := nhl.NewSeason(2024)

	r := resource.SeasonStandings{Season: season}
	expected := store.SeasonStandingsPath(season.ToInt())
	if got := r.Path(); got != expected {
		t.Errorf("SeasonStandings.Path() = %q, want %q", got, expected)
	}
}

func TestPlayerGameLog_Path(t *testing.T) {
	tests := []struct {
		name     string
		playerID nhl.PlayerID
		season   nhl.Season
		gameType int
		expected string
	}{
		{"regular_season", nhl.PlayerID(8478402), nhl.NewSeason(2024), 2, "/seasons/2024/player-gamelogs/player-8478402-2.json"},
		{"playoffs", nhl.PlayerID(8478402), nhl.NewSeason(2024), 3, "/seasons/2024/player-gamelogs/player-8478402-3.json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := resource.PlayerGameLog{PlayerID: tt.playerID, Season: tt.season, GameType: tt.gameType}
			if got := r.Path(); got != tt.expected {
				t.Errorf("PlayerGameLog.Path() = %q, want %q", got, tt.expected)
			}
		})
	}
}

// TestFileType_String verifies that FileType.String() matches existing store constants.
func TestFileType_String(t *testing.T) {
	tests := []struct {
		fileType core.FileType
		expected string
	}{
		{core.Unknown, store.FileTypeUnknown},
		{core.DailySchedule, store.FileTypeDailySchedule},
		{core.Boxscore, store.FileTypeBoxscore},
		{core.PlayByPlay, store.FileTypePlayByPlay},
		{core.ShiftChart, store.FileTypeShiftChart},
		{core.GameStory, store.FileTypeGameStory},
		{core.Franchises, store.FileTypeFranchises},
		{core.SeasonsManifest, store.FileTypeSeasonsManifest},
		{core.SeasonStandings, store.FileTypeSeasonStandings},
		{core.PlayerLanding, store.FileTypePlayerLanding},
		{core.PlayerGameLog, store.FileTypePlayerGameLog},
		{core.League, store.FileTypeLeague},
		{core.Team, store.FileTypeTeam},
		{core.Roster, store.FileTypeRoster},
		{core.TeamSummary, store.FileTypeTeamSummary},
		{core.YahooPlayer, store.FileTypeYahooPlayer},
		{core.GameKey, store.FileTypeGameKey},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.fileType.String(); got != tt.expected {
				t.Errorf("FileType(%d).String() = %q, want %q", tt.fileType, got, tt.expected)
			}
		})
	}
}

// TestResource_Type verifies that each resource returns the correct FileType.
func TestResource_Type(t *testing.T) {
	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	gameID := nhl.GameID(2024020123)
	playerID := nhl.PlayerID(8478402)

	tests := []struct {
		name     string
		resource core.Resource
		expected core.FileType
	}{
		{"DailySchedule", resource.DailySchedule{Date: date}, core.DailySchedule},
		{"Boxscore", resource.Boxscore{Date: date, GameID: gameID}, core.Boxscore},
		{"PlayByPlay", resource.PlayByPlay{Date: date, GameID: gameID}, core.PlayByPlay},
		{"ShiftChart", resource.ShiftChart{Date: date, GameID: gameID}, core.ShiftChart},
		{"GameStory", resource.GameStory{Date: date, GameID: gameID}, core.GameStory},
		{"PlayerLanding", resource.PlayerLanding{PlayerID: playerID}, core.PlayerLanding},
		{"MissingPlayerLanding", resource.MissingPlayerLanding{PlayerID: playerID}, core.PlayerLanding},
		{"Franchises", resource.Franchises{}, core.Franchises},
		{"SeasonsManifest", resource.SeasonsManifest{}, core.SeasonsManifest},
		{"SeasonStandings", resource.SeasonStandings{Season: nhl.NewSeason(2024)}, core.SeasonStandings},
		{"PlayerGameLog", resource.PlayerGameLog{PlayerID: playerID, Season: nhl.NewSeason(2024), GameType: 2}, core.PlayerGameLog},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.resource.Type(); got != tt.expected {
				t.Errorf("%s.Type() = %v, want %v", tt.name, got, tt.expected)
			}
		})
	}
}
