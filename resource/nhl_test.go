package resource_test

import (
	"testing"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/resource"
)

func TestDailySchedule_Path(t *testing.T) {
	tests := []struct {
		name     string
		date     time.Time
		expected string
	}{
		{"mid_season", time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC), "seasons/2024/games/2025/01/15/daily-schedule-2025-01-15.json"},
		{"start_of_season", time.Date(2024, 10, 1, 0, 0, 0, 0, time.UTC), "seasons/2024/games/2024/10/01/daily-schedule-2024-10-01.json"},
		{"end_of_season", time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC), "seasons/2024/games/2025/06/15/daily-schedule-2025-06-15.json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := resource.DailySchedule{Date: tt.date}
			if got := r.Path(); got != tt.expected {
				t.Errorf("DailySchedule.Path() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestBoxscore_Path(t *testing.T) {
	tests := []struct {
		name     string
		date     time.Time
		gameID   nhl.GameID
		expected string
	}{
		{"regular_season", time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC), nhl.GameID(2024020123), "seasons/2024/games/2025/01/15/boxscore-2024020123.json"},
		{"playoffs", time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC), nhl.GameID(2024030111), "seasons/2024/games/2025/05/01/boxscore-2024030111.json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := resource.Boxscore{Date: tt.date, GameID: tt.gameID}
			if got := r.Path(); got != tt.expected {
				t.Errorf("Boxscore.Path() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestPlayByPlay_Path(t *testing.T) {
	r := resource.PlayByPlay{Date: time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC), GameID: nhl.GameID(2024020123)}
	expected := "seasons/2024/games/2025/01/15/playbyplay-2024020123.json"
	if got := r.Path(); got != expected {
		t.Errorf("PlayByPlay.Path() = %q, want %q", got, expected)
	}
}

func TestShiftChart_Path(t *testing.T) {
	r := resource.ShiftChart{Date: time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC), GameID: nhl.GameID(2024020123)}
	expected := "seasons/2024/games/2025/01/15/shiftchart-2024020123.json"
	if got := r.Path(); got != expected {
		t.Errorf("ShiftChart.Path() = %q, want %q", got, expected)
	}
}

func TestGameStory_Path(t *testing.T) {
	r := resource.GameStory{Date: time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC), GameID: nhl.GameID(2024020123)}
	expected := "seasons/2024/games/2025/01/15/gamestory-2024020123.json"
	if got := r.Path(); got != expected {
		t.Errorf("GameStory.Path() = %q, want %q", got, expected)
	}
}

func TestPlayerLanding_Path(t *testing.T) {
	r := resource.PlayerLanding{PlayerID: nhl.PlayerID(8478402)}
	expected := "players/player-8478402-landing.json"
	if got := r.Path(); got != expected {
		t.Errorf("PlayerLanding.Path() = %q, want %q", got, expected)
	}
}

func TestMissingPlayerLanding_Path(t *testing.T) {
	r := resource.MissingPlayerLanding{PlayerID: nhl.PlayerID(8478402)}
	expected := "players-missing/player-8478402.json"
	if got := r.Path(); got != expected {
		t.Errorf("MissingPlayerLanding.Path() = %q, want %q", got, expected)
	}
}

func TestFranchises_Path(t *testing.T) {
	r := resource.Franchises{}
	expected := "nhl/franchises.json"
	if got := r.Path(); got != expected {
		t.Errorf("Franchises.Path() = %q, want %q", got, expected)
	}
}

func TestSeasonsManifest_Path(t *testing.T) {
	r := resource.SeasonsManifest{}
	expected := "nhl/seasons-manifest.json"
	if got := r.Path(); got != expected {
		t.Errorf("SeasonsManifest.Path() = %q, want %q", got, expected)
	}
}

func TestSeasonStandings_Path(t *testing.T) {
	r := resource.SeasonStandings{Season: nhl.NewSeason(2024)}
	expected := "nhl/standings/standings-20242025.json"
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

// TestFileType_String verifies that FileType.String() matches expected names.
func TestFileType_String(t *testing.T) {
	tests := []struct {
		fileType core.FileType
		expected string
	}{
		{core.Unknown, "Unknown"},
		{core.DailySchedule, "DailySchedule"},
		{core.Boxscore, "Boxscore"},
		{core.PlayByPlay, "PlayByPlay"},
		{core.ShiftChart, "ShiftChart"},
		{core.GameStory, "GameStory"},
		{core.Franchises, "Franchises"},
		{core.SeasonsManifest, "SeasonsManifest"},
		{core.SeasonStandings, "SeasonStandings"},
		{core.PlayerLanding, "PlayerLanding"},
		{core.PlayerGameLog, "PlayerGameLog"},
		{core.League, "League"},
		{core.Team, "Team"},
		{core.Roster, "Roster"},
		{core.TeamSummary, "TeamSummary"},
		{core.YahooPlayer, "YahooPlayer"},
		{core.GameKey, "GameKey"},
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
