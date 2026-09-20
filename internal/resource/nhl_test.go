package resource_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
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
		{core.SeasonSeries, "SeasonSeries"},
		{core.DailyStandings, "DailyStandings"},
		{core.SeasonRoster, "SeasonRoster"},
		{core.ClubStatsResource, "ClubStatsResource"},
		{core.League, "League"},
		{core.Team, "Team"},
		{core.Roster, "Roster"},
		{core.TeamSummary, "TeamSummary"},
		{core.YahooPlayer, "YahooPlayer"},
		{core.GameKey, "GameKey"},
		{core.YahooTransactions, "YahooTransactions"},
		{core.YahooDraftResults, "YahooDraftResults"},
		{core.YahooMatchups, "YahooMatchups"},
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
		{"SeasonSeries", resource.SeasonSeries{Date: date, GameID: gameID}, core.SeasonSeries},
		{"DailyStandings", resource.DailyStandings{Date: date}, core.DailyStandings},
		{"SeasonRoster", resource.SeasonRoster{Season: 2024, TeamAbbrev: "MTL"}, core.SeasonRoster},
		{"ClubStatsResource", resource.ClubStatsResource{Season: 2024, TeamAbbrev: "MTL", GameType: 2}, core.ClubStatsResource},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.resource.Type(); got != tt.expected {
				t.Errorf("%s.Type() = %v, want %v", tt.name, got, tt.expected)
			}
		})
	}
}

func TestSeasonSeries_Path(t *testing.T) {
	r := resource.SeasonSeries{Date: time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC), GameID: nhl.GameID(2024020123)}
	expected := "seasons/2024/games/2025/01/15/seasonseries-2024020123.json"
	if got := r.Path(); got != expected {
		t.Errorf("SeasonSeries.Path() = %q, want %q", got, expected)
	}
}

func TestDailyStandings_Path(t *testing.T) {
	r := resource.DailyStandings{Date: time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)}
	expected := "seasons/2024/games/2025/01/15/standings-2025-01-15.json"
	if got := r.Path(); got != expected {
		t.Errorf("DailyStandings.Path() = %q, want %q", got, expected)
	}
}

func TestSeasonRoster_Path(t *testing.T) {
	r := resource.SeasonRoster{Season: 2024, TeamAbbrev: "MTL"}
	expected := "seasons/2024/rosters/roster-MTL.json"
	if got := r.Path(); got != expected {
		t.Errorf("SeasonRoster.Path() = %q, want %q", got, expected)
	}
}

func TestClubStatsResource_Path(t *testing.T) {
	r := resource.ClubStatsResource{Season: 2024, TeamAbbrev: "MTL", GameType: 2}
	expected := "seasons/2024/clubstats/clubstats-MTL-2.json"
	if got := r.Path(); got != expected {
		t.Errorf("ClubStatsResource.Path() = %q, want %q", got, expected)
	}
}

func TestDeduceSeason(t *testing.T) {
	tests := []struct {
		name     string
		date     time.Time
		expected int
	}{
		{"january_2025", time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC), 2024},
		{"september_2024", time.Date(2024, 9, 1, 0, 0, 0, 0, time.UTC), 2024},
		{"august_2024", time.Date(2024, 8, 31, 0, 0, 0, 0, time.UTC), 2023},
		{"december_2024", time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC), 2024},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resource.DeduceSeason(tt.date); got != tt.expected {
				t.Errorf("DeduceSeason(%s) = %d, want %d", tt.date.Format("2006-01-02"), got, tt.expected)
			}
		})
	}
}

// ============================================================================
// Parse / Format / URL tests
// ============================================================================

// mustMarshal marshals v to JSON and panics on error. For use in test data only.
func mustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func TestDailySchedule_ParseFormat(t *testing.T) {
	t.Parallel()
	r := resource.DailySchedule{Date: time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)}

	t.Run("parse_valid", func(t *testing.T) {
		result, err := r.Parse(mustMarshal(&nhl.DailySchedule{}))
		if err != nil {
			t.Fatalf("Parse() unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("Parse() returned nil")
		}
	})

	t.Run("parse_invalid_json", func(t *testing.T) {
		_, err := r.Parse([]byte(`not-json`))
		if err == nil {
			t.Fatal("Parse() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "2025-01-15") {
			t.Errorf("error %q should contain date context", err.Error())
		}
	})

	t.Run("round_trip", func(t *testing.T) {
		original := &nhl.DailySchedule{}
		data, err := r.Format(original)
		if err != nil {
			t.Fatalf("Format() unexpected error: %v", err)
		}
		parsed, err := r.Parse(data)
		if err != nil {
			t.Fatalf("Parse() after Format() unexpected error: %v", err)
		}
		if parsed == nil {
			t.Fatal("round-trip returned nil")
		}
	})
}

func TestBoxscore_ParseFormatURL(t *testing.T) {
	t.Parallel()
	r := resource.Boxscore{Date: time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC), GameID: nhl.GameID(2024020123)}

	t.Run("parse_valid", func(t *testing.T) {
		result, err := r.Parse([]byte(`{}`))
		if err != nil {
			t.Fatalf("Parse() unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("Parse() returned nil")
		}
	})

	t.Run("parse_invalid_json", func(t *testing.T) {
		_, err := r.Parse([]byte(`{bad}`))
		if err == nil {
			t.Fatal("Parse() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "2024020123") {
			t.Errorf("error %q should contain game ID", err.Error())
		}
	})

	t.Run("round_trip", func(t *testing.T) {
		fixture := nhl.FixtureBoxscore()
		fixture.Season = nhl.NewSeason(2024)
		data, err := r.Format(fixture)
		if err != nil {
			t.Fatalf("Format() unexpected error: %v", err)
		}
		if _, err := r.Parse(data); err != nil {
			t.Fatalf("Parse() after Format() unexpected error: %v", err)
		}
	})

	t.Run("url", func(t *testing.T) {
		got := r.URL()
		if !strings.Contains(got, "2024020123") {
			t.Errorf("URL() = %q, want game ID in URL", got)
		}
		if !strings.Contains(got, "boxscore") {
			t.Errorf("URL() = %q, want 'boxscore' in URL", got)
		}
	})
}

func TestPlayByPlay_ParseFormatURL(t *testing.T) {
	t.Parallel()
	r := resource.PlayByPlay{Date: time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC), GameID: nhl.GameID(2024020123)}

	t.Run("parse_valid", func(t *testing.T) {
		result, err := r.Parse([]byte(`{}`))
		if err != nil {
			t.Fatalf("Parse() unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("Parse() returned nil")
		}
	})

	t.Run("parse_invalid_json", func(t *testing.T) {
		_, err := r.Parse([]byte(`{bad}`))
		if err == nil {
			t.Fatal("Parse() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "2024020123") {
			t.Errorf("error %q should contain game ID", err.Error())
		}
	})

	t.Run("round_trip", func(t *testing.T) {
		fixture := nhl.FixturePlayByPlay()
		fixture.Season = nhl.NewSeason(2024)
		data, err := r.Format(fixture)
		if err != nil {
			t.Fatalf("Format() unexpected error: %v", err)
		}
		if _, err := r.Parse(data); err != nil {
			t.Fatalf("Parse() after Format() unexpected error: %v", err)
		}
	})

	t.Run("url", func(t *testing.T) {
		got := r.URL()
		if !strings.Contains(got, "2024020123") {
			t.Errorf("URL() = %q, want game ID in URL", got)
		}
		if !strings.Contains(got, "play-by-play") {
			t.Errorf("URL() = %q, want 'play-by-play' in URL", got)
		}
	})
}

func TestShiftChart_ParseFormatURL(t *testing.T) {
	t.Parallel()
	r := resource.ShiftChart{Date: time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC), GameID: nhl.GameID(2024020123)}

	t.Run("parse_valid", func(t *testing.T) {
		result, err := r.Parse([]byte(`{}`))
		if err != nil {
			t.Fatalf("Parse() unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("Parse() returned nil")
		}
	})

	t.Run("parse_invalid_json", func(t *testing.T) {
		_, err := r.Parse([]byte(`{bad}`))
		if err == nil {
			t.Fatal("Parse() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "2024020123") {
			t.Errorf("error %q should contain game ID", err.Error())
		}
	})

	t.Run("round_trip", func(t *testing.T) {
		data, err := r.Format(&nhl.ShiftChart{})
		if err != nil {
			t.Fatalf("Format() unexpected error: %v", err)
		}
		if _, err := r.Parse(data); err != nil {
			t.Fatalf("Parse() after Format() unexpected error: %v", err)
		}
	})

	t.Run("url", func(t *testing.T) {
		got := r.URL()
		if !strings.Contains(got, "2024020123") {
			t.Errorf("URL() = %q, want game ID in URL", got)
		}
		if !strings.Contains(got, "shiftcharts") {
			t.Errorf("URL() = %q, want 'shiftcharts' in URL", got)
		}
	})
}

func TestGameStory_ParseFormatURL(t *testing.T) {
	t.Parallel()
	r := resource.GameStory{Date: time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC), GameID: nhl.GameID(2024020123)}

	t.Run("parse_valid", func(t *testing.T) {
		result, err := r.Parse([]byte(`{}`))
		if err != nil {
			t.Fatalf("Parse() unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("Parse() returned nil")
		}
	})

	t.Run("parse_invalid_json", func(t *testing.T) {
		_, err := r.Parse([]byte(`{bad}`))
		if err == nil {
			t.Fatal("Parse() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "2024020123") {
			t.Errorf("error %q should contain game ID", err.Error())
		}
	})

	t.Run("round_trip", func(t *testing.T) {
		fixture := nhl.FixtureGameStory()
		fixture.Season = nhl.NewSeason(2024)
		data, err := r.Format(fixture)
		if err != nil {
			t.Fatalf("Format() unexpected error: %v", err)
		}
		if _, err := r.Parse(data); err != nil {
			t.Fatalf("Parse() after Format() unexpected error: %v", err)
		}
	})

	t.Run("url", func(t *testing.T) {
		got := r.URL()
		if !strings.Contains(got, "2024020123") {
			t.Errorf("URL() = %q, want game ID in URL", got)
		}
		if !strings.Contains(got, "game-story") {
			t.Errorf("URL() = %q, want 'game-story' in URL", got)
		}
	})
}

func TestSeasonSeries_ParseFormat(t *testing.T) {
	t.Parallel()
	r := resource.SeasonSeries{Date: time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC), GameID: nhl.GameID(2024020123)}

	t.Run("parse_valid", func(t *testing.T) {
		result, err := r.Parse([]byte(`{}`))
		if err != nil {
			t.Fatalf("Parse() unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("Parse() returned nil")
		}
	})

	t.Run("parse_invalid_json", func(t *testing.T) {
		_, err := r.Parse([]byte(`{bad}`))
		if err == nil {
			t.Fatal("Parse() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "2024020123") {
			t.Errorf("error %q should contain game ID", err.Error())
		}
	})

	t.Run("round_trip", func(t *testing.T) {
		data, err := r.Format(&nhl.SeasonSeriesMatchup{})
		if err != nil {
			t.Fatalf("Format() unexpected error: %v", err)
		}
		if _, err := r.Parse(data); err != nil {
			t.Fatalf("Parse() after Format() unexpected error: %v", err)
		}
	})
}

func TestPlayerLanding_ParseFormatURL(t *testing.T) {
	t.Parallel()
	r := resource.PlayerLanding{PlayerID: nhl.PlayerID(8478402)}

	t.Run("parse_valid", func(t *testing.T) {
		result, err := r.Parse(mustMarshal(&nhl.PlayerLanding{}))
		if err != nil {
			t.Fatalf("Parse() unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("Parse() returned nil")
		}
	})

	t.Run("parse_invalid_json", func(t *testing.T) {
		_, err := r.Parse([]byte(`{bad}`))
		if err == nil {
			t.Fatal("Parse() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "8478402") {
			t.Errorf("error %q should contain player ID", err.Error())
		}
	})

	t.Run("round_trip", func(t *testing.T) {
		data, err := r.Format(&nhl.PlayerLanding{})
		if err != nil {
			t.Fatalf("Format() unexpected error: %v", err)
		}
		if _, err := r.Parse(data); err != nil {
			t.Fatalf("Parse() after Format() unexpected error: %v", err)
		}
	})

	t.Run("url", func(t *testing.T) {
		got := r.URL()
		if !strings.Contains(got, "8478402") {
			t.Errorf("URL() = %q, want player ID in URL", got)
		}
		if !strings.Contains(got, "landing") {
			t.Errorf("URL() = %q, want 'landing' in URL", got)
		}
	})
}

func TestMissingPlayerLanding_ParseFormat(t *testing.T) {
	t.Parallel()
	r := resource.MissingPlayerLanding{PlayerID: nhl.PlayerID(8478402)}

	t.Run("parse_valid", func(t *testing.T) {
		result, err := r.Parse(mustMarshal(&store.MissingPlayerLandingData{
			FirstName: "Sidney",
			LastName:  "Crosby",
			Position:  "C",
		}))
		if err != nil {
			t.Fatalf("Parse() unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("Parse() returned nil")
		}
		if result.FirstName != "Sidney" {
			t.Errorf("FirstName = %q, want %q", result.FirstName, "Sidney")
		}
	})

	t.Run("parse_invalid_json", func(t *testing.T) {
		_, err := r.Parse([]byte(`{bad}`))
		if err == nil {
			t.Fatal("Parse() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "8478402") {
			t.Errorf("error %q should contain player ID", err.Error())
		}
	})

	t.Run("round_trip", func(t *testing.T) {
		original := &store.MissingPlayerLandingData{FirstName: "Sidney", LastName: "Crosby", Position: "C"}
		data, err := r.Format(original)
		if err != nil {
			t.Fatalf("Format() unexpected error: %v", err)
		}
		parsed, err := r.Parse(data)
		if err != nil {
			t.Fatalf("Parse() after Format() unexpected error: %v", err)
		}
		if parsed.FirstName != original.FirstName || parsed.LastName != original.LastName {
			t.Errorf("round-trip mismatch: got %+v, want %+v", parsed, original)
		}
	})
}

func TestFranchises_ParseFormatURL(t *testing.T) {
	t.Parallel()
	r := resource.Franchises{}

	t.Run("parse_valid", func(t *testing.T) {
		_, err := r.Parse(mustMarshal(nhl.FranchisesResponse{}))
		if err != nil {
			t.Fatalf("Parse() unexpected error: %v", err)
		}
	})

	t.Run("parse_invalid_json", func(t *testing.T) {
		_, err := r.Parse([]byte(`{bad}`))
		if err == nil {
			t.Fatal("Parse() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "franchises") {
			t.Errorf("error %q should contain 'franchises'", err.Error())
		}
	})

	t.Run("round_trip", func(t *testing.T) {
		data, err := r.Format(nhl.FranchisesResponse{})
		if err != nil {
			t.Fatalf("Format() unexpected error: %v", err)
		}
		if _, err := r.Parse(data); err != nil {
			t.Fatalf("Parse() after Format() unexpected error: %v", err)
		}
	})

	t.Run("url", func(t *testing.T) {
		got := r.URL()
		if !strings.Contains(got, "franchise") {
			t.Errorf("URL() = %q, want 'franchise' in URL", got)
		}
	})
}

func TestSeasonsManifest_ParseFormatURL(t *testing.T) {
	t.Parallel()
	r := resource.SeasonsManifest{}

	t.Run("parse_valid", func(t *testing.T) {
		_, err := r.Parse(mustMarshal(nhl.SeasonsResponse{}))
		if err != nil {
			t.Fatalf("Parse() unexpected error: %v", err)
		}
	})

	t.Run("parse_invalid_json", func(t *testing.T) {
		_, err := r.Parse([]byte(`{bad}`))
		if err == nil {
			t.Fatal("Parse() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "seasons") {
			t.Errorf("error %q should contain 'seasons'", err.Error())
		}
	})

	t.Run("round_trip", func(t *testing.T) {
		data, err := r.Format(nhl.SeasonsResponse{})
		if err != nil {
			t.Fatalf("Format() unexpected error: %v", err)
		}
		if _, err := r.Parse(data); err != nil {
			t.Fatalf("Parse() after Format() unexpected error: %v", err)
		}
	})

	t.Run("url", func(t *testing.T) {
		got := r.URL()
		if !strings.Contains(got, "standings-season") {
			t.Errorf("URL() = %q, want 'standings-season' in URL", got)
		}
	})
}

func TestSeasonStandings_ParseFormat(t *testing.T) {
	t.Parallel()
	r := resource.SeasonStandings{Season: nhl.NewSeason(2024)}

	t.Run("parse_valid", func(t *testing.T) {
		result, err := r.Parse(mustMarshal([]nhl.Standing{}))
		if err != nil {
			t.Fatalf("Parse() unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("Parse() returned nil slice")
		}
	})

	t.Run("parse_invalid_json", func(t *testing.T) {
		_, err := r.Parse([]byte(`{bad}`))
		if err == nil {
			t.Fatal("Parse() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "standings") {
			t.Errorf("error %q should contain 'standings'", err.Error())
		}
	})

	t.Run("round_trip", func(t *testing.T) {
		data, err := r.Format([]nhl.Standing{})
		if err != nil {
			t.Fatalf("Format() unexpected error: %v", err)
		}
		if _, err := r.Parse(data); err != nil {
			t.Fatalf("Parse() after Format() unexpected error: %v", err)
		}
	})
}

func TestPlayerGameLog_ParseURL(t *testing.T) {
	t.Parallel()
	r := resource.PlayerGameLog{PlayerID: nhl.PlayerID(8478402), Season: nhl.NewSeason(2024), GameType: 2}

	t.Run("parse_valid", func(t *testing.T) {
		// Use a minimal JSON object rather than marshaling nhl.PlayerGameLog{}
		// because that struct's GameType field refuses to marshal its zero value.
		result, err := r.Parse([]byte(`{}`))
		if err != nil {
			t.Fatalf("Parse() unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("Parse() returned nil")
		}
	})

	t.Run("parse_invalid_json", func(t *testing.T) {
		_, err := r.Parse([]byte(`{bad}`))
		if err == nil {
			t.Fatal("Parse() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "8478402") {
			t.Errorf("error %q should contain player ID", err.Error())
		}
	})

	t.Run("url", func(t *testing.T) {
		got := r.URL()
		if !strings.Contains(got, "8478402") {
			t.Errorf("URL() = %q, want player ID in URL", got)
		}
		if !strings.Contains(got, "game-log") {
			t.Errorf("URL() = %q, want 'game-log' in URL", got)
		}
	})
}

func TestDailyStandings_ParseFormat(t *testing.T) {
	t.Parallel()
	r := resource.DailyStandings{Date: time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)}

	t.Run("parse_valid", func(t *testing.T) {
		result, err := r.Parse(mustMarshal([]nhl.Standing{}))
		if err != nil {
			t.Fatalf("Parse() unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("Parse() returned nil slice")
		}
	})

	t.Run("parse_invalid_json", func(t *testing.T) {
		_, err := r.Parse([]byte(`{bad}`))
		if err == nil {
			t.Fatal("Parse() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "2025-01-15") {
			t.Errorf("error %q should contain date context", err.Error())
		}
	})

	t.Run("round_trip", func(t *testing.T) {
		data, err := r.Format([]nhl.Standing{})
		if err != nil {
			t.Fatalf("Format() unexpected error: %v", err)
		}
		if _, err := r.Parse(data); err != nil {
			t.Fatalf("Parse() after Format() unexpected error: %v", err)
		}
	})
}

func TestSeasonRoster_ParseFormat(t *testing.T) {
	t.Parallel()
	r := resource.SeasonRoster{Season: 2024, TeamAbbrev: "MTL"}

	t.Run("parse_valid", func(t *testing.T) {
		result, err := r.Parse(mustMarshal(&nhl.Roster{}))
		if err != nil {
			t.Fatalf("Parse() unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("Parse() returned nil")
		}
	})

	t.Run("parse_invalid_json", func(t *testing.T) {
		_, err := r.Parse([]byte(`{bad}`))
		if err == nil {
			t.Fatal("Parse() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "MTL") {
			t.Errorf("error %q should contain team abbrev", err.Error())
		}
	})

	t.Run("round_trip", func(t *testing.T) {
		data, err := r.Format(&nhl.Roster{})
		if err != nil {
			t.Fatalf("Format() unexpected error: %v", err)
		}
		if _, err := r.Parse(data); err != nil {
			t.Fatalf("Parse() after Format() unexpected error: %v", err)
		}
	})
}

func TestClubStatsResource_ParseFormat(t *testing.T) {
	t.Parallel()
	r := resource.ClubStatsResource{Season: 2024, TeamAbbrev: "MTL", GameType: 2}

	t.Run("parse_valid", func(t *testing.T) {
		result, err := r.Parse([]byte(`{}`))
		if err != nil {
			t.Fatalf("Parse() unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("Parse() returned nil")
		}
	})

	t.Run("parse_invalid_json", func(t *testing.T) {
		_, err := r.Parse([]byte(`{bad}`))
		if err == nil {
			t.Fatal("Parse() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "MTL") {
			t.Errorf("error %q should contain team abbrev", err.Error())
		}
	})

	t.Run("round_trip", func(t *testing.T) {
		// nhl.ClubStats has GameType and Season fields with strict marshalers that
		// reject zero values; provide valid values for the round-trip.
		stats := &nhl.ClubStats{GameType: nhl.GameTypeRegularSeason, Season: nhl.NewSeason(2024)}
		data, err := r.Format(stats)
		if err != nil {
			t.Fatalf("Format() unexpected error: %v", err)
		}
		if _, err := r.Parse(data); err != nil {
			t.Fatalf("Parse() after Format() unexpected error: %v", err)
		}
	})
}
