package mcpserver

import (
	"testing"

	"github.com/sperano/puckdb/sqlcdb"
	"github.com/stretchr/testify/assert"
)

// expectedTools lists every tool name that should be registered.
// Update this list whenever a new tool is added.
var expectedTools = []string{
	// Resolution (5)
	"search_player",
	"find_team",
	"list_teams",
	"list_seasons",
	"list_franchises",
	// Players (8)
	"get_player",
	"get_players_by_team",
	"get_players_by_position",
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
	// Standings (4)
	"get_standings_by_date",
	"get_standings_by_season",
	"get_standings_by_season_and_date",
	"get_standings_by_team",
	// Fantasy (10)
	"get_yahoo_leagues",
	"get_yahoo_teams_by_league",
	"get_yahoo_roster",
	"get_yahoo_roto_standings",
	"get_skater_season_stats",
	"get_goalie_season_stats",
	"get_unrostered_skaters",
	"get_unrostered_goalies",
	"get_yahoo_matchups",
	"get_yahoo_draft_results",
}

func TestNewServer_RegistersAllTools(t *testing.T) {
	// NewServer accepts *sqlcdb.Queries; nil is fine here because
	// we only inspect registrations, not invoke any tool.
	srv := NewServer((*sqlcdb.Queries)(nil))

	tools := srv.ListTools() // map[string]*server.ServerTool
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}

	assert.Len(t, names, len(expectedTools), "tool count mismatch — update expectedTools when adding new tools")
	for _, want := range expectedTools {
		assert.Contains(t, names, want)
	}
}
