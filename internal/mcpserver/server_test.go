package mcpserver

import (
	"slices"
	"testing"

	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// expectedNHLTools lists every tool the nhl toolset registers.
// Update this list whenever a new NHL tool is added.
var expectedNHLTools = []string{
	// Resolution (5)
	"search_player",
	"find_team",
	"list_teams",
	"list_seasons",
	"list_franchises",
	// Players (9)
	"get_player",
	"get_players_by_team",
	"get_players_by_position",
	"get_players_by_birthplace",
	"get_active_players",
	"get_player_career_totals",
	"get_player_awards",
	"get_player_roster_history",
	"get_player_three_stars",
	// Games (7)
	"get_game",
	"get_games_by_date",
	"get_games_by_season",
	"get_games_by_team",
	"get_games_by_team_and_season",
	"get_game_three_stars",
	"get_game_broadcasts",
	// Stats (10)
	"get_game_skater_stats",
	"get_game_goalie_stats",
	"get_game_skater_stats_by_team",
	"get_game_goalie_stats_by_team",
	"get_skater_season_totals",
	"get_goalie_season_totals",
	"get_skater_game_log",
	"get_goalie_game_log",
	"get_club_skater_stats",
	"get_club_goalie_stats",
	// Season stats (2)
	"get_skater_season_stats",
	"get_goalie_season_stats",
	// Standings (4)
	"get_standings_by_date",
	"get_standings_by_season",
	"get_standings_by_season_and_date",
	"get_standings_by_team",
	// Edge (3)
	"get_edge_skater_stats",
	"get_edge_goalie_stats",
	"get_edge_team_stats",
	// Playoffs (5)
	"list_games",
	"get_playoff_games",
	"get_playoff_series",
	"get_stanley_cup_finals",
	"get_stanley_cup_winners",
	// Play events (2)
	"get_game_play_events",
	"get_first_matching_event_per_team",
}

// expectedYahooTools lists every tool the yahoo toolset registers.
// Update this list whenever a new Yahoo tool is added.
var expectedYahooTools = []string{
	"get_yahoo_leagues",
	"get_yahoo_teams_by_league",
	"get_yahoo_roster",
	"get_yahoo_roto_standings",
	"get_unrostered_skaters",
	"get_unrostered_goalies",
	"get_yahoo_matchups",
	"get_yahoo_draft_results",
}

// registeredToolNames builds a server for opts and returns its tool names.
// NewServer accepts *sqlcdb.Queries; nil is fine here because only the
// registrations are inspected, no tool is invoked.
func registeredToolNames(t *testing.T, opts Options) []string {
	t.Helper()
	srv, err := NewServer((*sqlcdb.Queries)(nil), opts)
	require.NoError(t, err)
	names := make([]string, 0, len(srv.ListTools()))
	for name := range srv.ListTools() {
		names = append(names, name)
	}
	return names
}

func TestNewServer_RegistersSelectedToolsets(t *testing.T) {
	tests := []struct {
		name     string
		toolsets []Toolset
		want     []string
	}{
		{"nhl", []Toolset{ToolsetNHL}, expectedNHLTools},
		{"yahoo", []Toolset{ToolsetYahoo}, expectedYahooTools},
		{"nhl and yahoo", []Toolset{ToolsetNHL, ToolsetYahoo}, append(slices.Clone(expectedNHLTools), expectedYahooTools...)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			names := registeredToolNames(t, Options{Toolsets: tt.toolsets})
			assert.ElementsMatch(t, tt.want, names, "update expectedNHLTools/expectedYahooTools when adding tools")
		})
	}
}

// TestToolsetsDoNotOverlap pins the clean cut between the toolsets: a
// combined server must not silently replace one toolset's tool with the
// other's.
func TestToolsetsDoNotOverlap(t *testing.T) {
	for _, name := range expectedYahooTools {
		assert.NotContains(t, expectedNHLTools, name)
	}
}

func TestNewServer_RejectsInvalidOptions(t *testing.T) {
	_, err := NewServer((*sqlcdb.Queries)(nil), Options{})
	require.ErrorContains(t, err, "no toolset selected")

	_, err = NewServer((*sqlcdb.Queries)(nil), Options{Toolsets: []Toolset{"espn"}})
	require.ErrorContains(t, err, `unknown toolset "espn"`)
}
