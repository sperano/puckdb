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
