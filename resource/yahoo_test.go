package resource_test

import (
	"testing"
	"time"

	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/resource"
)

func TestLeague_Path(t *testing.T) {
	tests := []struct {
		name     string
		season   int
		leagueID int
		expected string
	}{
		{"standard", 2024, 12345, "seasons/2024/yahoo/12345/league/league.xml"},
		{"different_season", 2023, 67890, "seasons/2023/yahoo/67890/league/league.xml"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := resource.League{Season: tt.season, LeagueID: tt.leagueID, GameKey: 453}
			if got := r.Path(); got != tt.expected {
				t.Errorf("League.Path() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestLeague_URL(t *testing.T) {
	r := resource.League{Season: 2024, LeagueID: 12345, GameKey: 453}
	expected := "https://fantasysports.yahooapis.com/fantasy/v2/league/453.l.12345/settings"
	if got := r.URL(); got != expected {
		t.Errorf("League.URL() = %q, want %q", got, expected)
	}
}

func TestTeam_Path(t *testing.T) {
	tests := []struct {
		name     string
		season   int
		leagueID int
		teamID   int
		expected string
	}{
		{"single_digit_team", 2024, 12345, 1, "seasons/2024/yahoo/12345/teams/team-01/team-01.xml"},
		{"double_digit_team", 2024, 12345, 12, "seasons/2024/yahoo/12345/teams/team-12/team-12.xml"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := resource.Team{Season: tt.season, LeagueID: tt.leagueID, TeamID: tt.teamID, GameKey: 453}
			if got := r.Path(); got != tt.expected {
				t.Errorf("Team.Path() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestTeam_URL(t *testing.T) {
	r := resource.Team{Season: 2024, LeagueID: 12345, TeamID: 5, GameKey: 453}
	expected := "https://fantasysports.yahooapis.com/fantasy/v2/team/453.l.12345.t.5"
	if got := r.URL(); got != expected {
		t.Errorf("Team.URL() = %q, want %q", got, expected)
	}
}

func TestRoster_Path(t *testing.T) {
	tests := []struct {
		name     string
		leagueID int
		teamID   int
		date     time.Time
		expected string
	}{
		{"mid_season", 12345, 1, time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC),
			"seasons/2024/yahoo/12345/rosters/team-01/rosters-01-2025-01-15.xml"},
		{"start_of_season", 12345, 5, time.Date(2024, 10, 1, 0, 0, 0, 0, time.UTC),
			"seasons/2024/yahoo/12345/rosters/team-05/rosters-05-2024-10-01.xml"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := resource.Roster{LeagueID: tt.leagueID, TeamID: tt.teamID, Date: tt.date, GameKey: 453}
			if got := r.Path(); got != tt.expected {
				t.Errorf("Roster.Path() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestRoster_URL(t *testing.T) {
	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	r := resource.Roster{LeagueID: 12345, TeamID: 5, Date: date, GameKey: 453}
	expected := "https://fantasysports.yahooapis.com/fantasy/v2/team/453.l.12345.t.5/roster;date=2025-01-15/players"
	if got := r.URL(); got != expected {
		t.Errorf("Roster.URL() = %q, want %q", got, expected)
	}
}

func TestTeamSummary_Path(t *testing.T) {
	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	r := resource.TeamSummary{LeagueID: 12345, TeamID: 5, Date: date, GameKey: 453}
	expected := "seasons/2024/yahoo/12345/summaries/team-05/team-05-summary-2025-01-15.xml"
	if got := r.Path(); got != expected {
		t.Errorf("TeamSummary.Path() = %q, want %q", got, expected)
	}
}

func TestTeamSummary_URL(t *testing.T) {
	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	r := resource.TeamSummary{LeagueID: 12345, TeamID: 5, Date: date, GameKey: 453}
	expected := "https://fantasysports.yahooapis.com/fantasy/v2/team/453.l.12345.t.5/stats;type=date;date=2025-01-15"
	if got := r.URL(); got != expected {
		t.Errorf("TeamSummary.URL() = %q, want %q", got, expected)
	}
}

func TestYahooPlayer_Path(t *testing.T) {
	playerID := resource.YahooPlayerID(12345)
	r := resource.YahooPlayer{PlayerID: playerID}
	expected := "yahoo-players/player-12345.html"
	if got := r.Path(); got != expected {
		t.Errorf("YahooPlayer.Path() = %q, want %q", got, expected)
	}
}

func TestYahooPlayer_URL(t *testing.T) {
	r := resource.YahooPlayer{PlayerID: 12345}
	expected := "https://sports.yahoo.com/nhl/players/12345/"
	if got := r.URL(); got != expected {
		t.Errorf("YahooPlayer.URL() = %q, want %q", got, expected)
	}
}

func TestMissingYahooPlayer_Path(t *testing.T) {
	playerID := resource.YahooPlayerID(12345)
	r := resource.MissingYahooPlayer{PlayerID: playerID}
	expected := "yahoo-players-missing/player-12345.txt"
	if got := r.Path(); got != expected {
		t.Errorf("MissingYahooPlayer.Path() = %q, want %q", got, expected)
	}
}

func TestGameKey_Path(t *testing.T) {
	r := resource.GameKey{Season: 2024}
	expected := "game-keys/gamekey-2024.xml"
	if got := r.Path(); got != expected {
		t.Errorf("GameKey.Path() = %q, want %q", got, expected)
	}
}

func TestGameKey_URL(t *testing.T) {
	r := resource.GameKey{Season: 2024}
	expected := "https://fantasysports.yahooapis.com/fantasy/v2/games;game_codes=nhl;seasons=2024"
	if got := r.URL(); got != expected {
		t.Errorf("GameKey.URL() = %q, want %q", got, expected)
	}
}

// TestYahooResource_Type verifies that each Yahoo resource returns the correct FileType.
func TestYahooResource_Type(t *testing.T) {
	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		resource core.Resource
		expected core.FileType
	}{
		{"League", resource.League{Season: 2024, LeagueID: 12345, GameKey: 453}, core.League},
		{"Team", resource.Team{Season: 2024, LeagueID: 12345, TeamID: 1, GameKey: 453}, core.Team},
		{"Roster", resource.Roster{LeagueID: 12345, TeamID: 1, Date: date, GameKey: 453}, core.Roster},
		{"TeamSummary", resource.TeamSummary{LeagueID: 12345, TeamID: 1, Date: date, GameKey: 453}, core.TeamSummary},
		{"YahooPlayer", resource.YahooPlayer{PlayerID: 12345}, core.YahooPlayer},
		{"MissingYahooPlayer", resource.MissingYahooPlayer{PlayerID: 12345}, core.YahooPlayer},
		{"GameKey", resource.GameKey{Season: 2024}, core.GameKey},
		{"Transactions", resource.Transactions{Season: 2023, LeagueID: 12345, GameKey: 453}, core.YahooTransactions},
		{"DraftResults", resource.DraftResults{Season: 2023, LeagueID: 12345, GameKey: 453}, core.YahooDraftResults},
		{"Matchups", resource.Matchups{Season: 2023, LeagueID: 12345, Week: 3, GameKey: 453}, core.YahooMatchups},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.resource.Type(); got != tt.expected {
				t.Errorf("%s.Type() = %v, want %v", tt.name, got, tt.expected)
			}
		})
	}
}

func TestTransactions_Path(t *testing.T) {
	r := resource.Transactions{Season: 2023, LeagueID: 12345, GameKey: 453}
	expected := "seasons/2023/yahoo/12345/transactions/transactions.xml"
	if got := r.Path(); got != expected {
		t.Errorf("Transactions.Path() = %q, want %q", got, expected)
	}
}

func TestDraftResults_Path(t *testing.T) {
	r := resource.DraftResults{Season: 2023, LeagueID: 12345, GameKey: 453}
	expected := "seasons/2023/yahoo/12345/draft/draftresults.xml"
	if got := r.Path(); got != expected {
		t.Errorf("DraftResults.Path() = %q, want %q", got, expected)
	}
}

func TestMatchups_Path(t *testing.T) {
	r := resource.Matchups{Season: 2023, LeagueID: 12345, Week: 3, GameKey: 453}
	expected := "seasons/2023/yahoo/12345/matchups/week-3.xml"
	if got := r.Path(); got != expected {
		t.Errorf("Matchups.Path() = %q, want %q", got, expected)
	}
}
