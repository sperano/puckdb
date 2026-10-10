package mcpserver

import (
	"context"
	"maps"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
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
	// Players (10)
	"get_player",
	"get_players_by_team",
	"get_players_by_position",
	"get_players_by_birthplace",
	"get_active_players",
	"get_player_career_totals",
	"get_player_awards",
	"get_player_roster_history",
	"get_player_three_stars",
	"get_season_roster",
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
	"get_yahoo_season_team_totals",
	"get_unrostered_skaters",
	"get_unrostered_goalies",
	"get_yahoo_matchups",
	"get_yahoo_draft_results",
	"get_yahoo_league_settings",
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

// capturingDB is a sqlcdb.DBTX that records every statement and answers
// with no rows, so a handler test can see whether and with which
// arguments a call reached SQL without a database.
type capturingDB struct {
	calls []capturedQuery
}

type capturedQuery struct {
	sql  string
	args []any
}

func (d *capturingDB) record(sql string, args []any) {
	d.calls = append(d.calls, capturedQuery{sql: sql, args: args})
}

func (d *capturingDB) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	d.record(sql, args)
	return pgconn.CommandTag{}, nil
}

func (d *capturingDB) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	d.record(sql, args)
	return noRows{}, nil
}

func (d *capturingDB) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	d.record(sql, args)
	return noRow{}
}

func (d *capturingDB) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	panic("CopyFrom is not used by read-only tools")
}

func (d *capturingDB) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults {
	panic("SendBatch is not used by read-only tools")
}

// args returns the arguments of every captured statement, in order.
func (d *capturingDB) args() []any {
	var all []any
	for _, call := range d.calls {
		all = append(all, call.args...)
	}
	return all
}

type noRow struct{}

func (noRow) Scan(...any) error { return pgx.ErrNoRows }

type noRows struct{}

func (noRows) Close()                                       {}
func (noRows) Err() error                                   { return nil }
func (noRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (noRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (noRows) Next() bool                                   { return false }
func (noRows) Scan(...any) error                            { return pgx.ErrNoRows }
func (noRows) Values() ([]any, error)                       { return nil, nil }
func (noRows) RawValues() [][]byte                          { return nil }
func (noRows) Conn() *pgx.Conn                              { return nil }

// nhlTestServer registers the nhl toolset over db.
func nhlTestServer(db *capturingDB) *server.MCPServer {
	srv := server.NewMCPServer("test", serverVersion)
	registerNHLTools(srv, sqlcdb.New(db))
	return srv
}

// callNHLTool calls one nhl tool over a fresh capturingDB.
func callNHLTool(t *testing.T, name string, args map[string]any) (*mcp.CallToolResult, *capturingDB) {
	t.Helper()
	db := &capturingDB{}
	tool := nhlTestServer(db).GetTool(name)
	require.NotNil(t, tool, name)
	return callTool(t, tool.Handler, args), db
}

// validNHLArgs holds a valid value for every argument an nhl tool takes.
var validNHLArgs = map[string]any{
	"season":               float64(testSeasonID),
	"game_id":              float64(2025020001),
	"team_id":              float64(8),
	playerIDArg:            float64(matchedPlayerID),
	yahooIDArg:             float64(matchedYahooID),
	playerNameArg:          "Suzuki",
	"limit":                float64(5),
	"round":                float64(1),
	"period":               float64(2),
	"date":                 "2025-10-08",
	"start_date":           "2025-10-01",
	"end_date":             "2025-10-31",
	"abbrev":               "MTL",
	"position":             "C",
	"birth_country":        "CAN",
	"birth_city":           "Montr%al",
	"birth_state_province": "Quebec",
	"game_type":            "regular_season",
	seasonSortByArg:        "points",
	"type_desc_keys":       []any{"goal"},
	"game_ids":             []any{float64(2025020001)},
}

// nhlArgOverrides replace validNHLArgs values for tools that accept
// different ones.
var nhlArgOverrides = map[string]map[string]any{
	"get_goalie_season_stats": {seasonSortByArg: defaultGoalieSeasonSort, seasonPositionArg: string(sqlcdb.PlayerPositionG)},
}

// exclusiveNHLArgs are arguments left out of a tool's valid call because
// the tool refuses them together with another one.
var exclusiveNHLArgs = map[string]string{
	"get_player":    yahooIDArg,
	"search_player": yahooIDArg,
}

// validNHLCall builds a valid call of tool from its schema.
func validNHLCall(t *testing.T, tool mcp.Tool) map[string]any {
	t.Helper()
	args := map[string]any{}
	for name := range tool.InputSchema.Properties {
		if exclusiveNHLArgs[tool.Name] == name {
			continue
		}
		value, ok := nhlArgOverrides[tool.Name][name]
		if !ok {
			value, ok = validNHLArgs[name]
		}
		require.True(t, ok, "add a valid value for %s.%s to validNHLArgs", tool.Name, name)
		args[name] = value
	}
	return args
}

// numberArgs lists the number-typed arguments of tool, including array
// items.
func numberArgs(tool mcp.Tool) []string {
	var names []string
	for name, property := range tool.InputSchema.Properties {
		schema, _ := property.(map[string]any)
		items, _ := schema["items"].(map[string]any)
		if schema["type"] == "number" || items["type"] == "number" {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

func withArg(args map[string]any, name string, value any) map[string]any {
	changed := maps.Clone(args)
	if _, isArray := args[name].([]any); isArray {
		value = []any{value}
	}
	changed[name] = value
	return changed
}

// malformedIntegers are values no integer argument of any tool accepts.
var malformedIntegers = map[string]any{
	"negative":       float64(-1),
	"fractional":     2025020001.5,
	"beyond float":   float64(maxExactJSONInteger + 1),
	"non-numeric":    "abc",
	"decimal string": "1.0",
}

// TestNHLToolsAcceptValidCalls checks the valid calls the malformed-value
// sweep starts from: each one reaches SQL.
func TestNHLToolsAcceptValidCalls(t *testing.T) {
	for _, tool := range nhlTestServer(&capturingDB{}).ListTools() {
		t.Run(tool.Tool.Name, func(t *testing.T) {
			args := validNHLCall(t, tool.Tool)
			result, db := callNHLTool(t, tool.Tool.Name, args)
			require.NotEmpty(t, db.calls, resultText(t, result))
			assert.NotContains(t, resultText(t, result), "invalid ")
			for _, arg := range numberArgs(tool.Tool) {
				want := args[arg]
				if items, isArray := want.([]any); isArray {
					want = items[0]
				}
				if want == nil {
					continue
				}
				assert.True(t, sqlArgsHoldInteger(db.args(), int64(want.(float64))),
					"%s %v did not reach SQL unchanged: %v", arg, want, db.args())
			}
		})
	}
}

// sqlArgsHoldInteger reports whether a statement received want as an
// integer parameter of any width, nullable or in an array.
func sqlArgsHoldInteger(args []any, want int64) bool {
	for _, arg := range args {
		switch v := arg.(type) {
		case int32:
			if int64(v) == want {
				return true
			}
		case int64:
			if v == want {
				return true
			}
		case pgtype.Int4:
			if v.Valid && int64(v.Int32) == want {
				return true
			}
		case pgtype.Int8:
			if v.Valid && v.Int64 == want {
				return true
			}
		case []int64:
			if slices.Contains(v, want) {
				return true
			}
		}
	}
	return false
}

// TestNHLToolsRejectMalformedIntegersBeforeSQL sweeps every number
// argument of every nhl tool: a malformed value is a tool error and no
// statement runs.
func TestNHLToolsRejectMalformedIntegersBeforeSQL(t *testing.T) {
	for _, tool := range nhlTestServer(&capturingDB{}).ListTools() {
		valid := validNHLCall(t, tool.Tool)
		for _, arg := range numberArgs(tool.Tool) {
			for kind, value := range malformedIntegers {
				t.Run(tool.Tool.Name+"/"+arg+"/"+kind, func(t *testing.T) {
					result, db := callNHLTool(t, tool.Tool.Name, withArg(valid, arg, value))
					assert.True(t, result.IsError)
					assert.Contains(t, resultText(t, result), "invalid "+arg)
					assert.Empty(t, db.calls, "a malformed %s reached SQL", arg)
				})
			}
		}
	}
}

// TestNHLToolsRequireRequiredIntegers removes each required number
// argument in turn.
func TestNHLToolsRequireRequiredIntegers(t *testing.T) {
	for _, tool := range nhlTestServer(&capturingDB{}).ListTools() {
		valid := validNHLCall(t, tool.Tool)
		for _, arg := range numberArgs(tool.Tool) {
			if !slices.Contains(tool.Tool.InputSchema.Required, arg) {
				continue
			}
			t.Run(tool.Tool.Name+"/"+arg, func(t *testing.T) {
				args := maps.Clone(valid)
				delete(args, arg)
				result, db := callNHLTool(t, tool.Tool.Name, args)
				assert.True(t, result.IsError)
				assert.Equal(t, arg+" is required", resultText(t, result))
				assert.Empty(t, db.calls)
			})
		}
	}
}
