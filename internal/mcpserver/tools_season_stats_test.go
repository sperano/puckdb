package mcpserver

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type seasonStatsQueryFake struct {
	skaterCalls int
	goalieCalls int
	skater      sqlcdb.GetSkaterSeasonStatsBySortParams
	goalie      sqlcdb.GetGoalieSeasonStatsBySortParams
}

func (f *seasonStatsQueryFake) GetSkaterSeasonStatsBySort(_ context.Context, params sqlcdb.GetSkaterSeasonStatsBySortParams) ([]sqlcdb.SkaterSeasonStat, error) {
	f.skaterCalls++
	f.skater = params
	return nil, nil
}

func (f *seasonStatsQueryFake) GetGoalieSeasonStatsBySort(_ context.Context, params sqlcdb.GetGoalieSeasonStatsBySortParams) ([]sqlcdb.GoalieSeasonStat, error) {
	f.goalieCalls++
	f.goalie = params
	return nil, nil
}

func seasonStatsTestServer(queries seasonStatsQueries) *server.MCPServer {
	srv := server.NewMCPServer("test", serverVersion)
	registerSeasonStatsTools(srv, queries)
	return srv
}

func TestSkaterSeasonStatsToolDefaultsLimitAndFiltersPosition(t *testing.T) {
	queries := &seasonStatsQueryFake{}
	tool := seasonStatsTestServer(queries).GetTool("get_skater_season_stats")
	require.NotNil(t, tool)

	result := callTool(t, tool.Handler, map[string]any{
		"season": float64(20252026), "position": "lw", "limit": float64(7),
	})

	assert.False(t, result.IsError)
	assert.Equal(t, 1, queries.skaterCalls)
	assert.Equal(t, int32(20252026), queries.skater.Season)
	assert.Equal(t, defaultSkaterSeasonSort, queries.skater.SortBy)
	assert.Equal(t, int32(7), queries.skater.ResultLimit)
	assert.Equal(t, sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionLW, Valid: true}, queries.skater.Position)
}

func TestSkaterSeasonStatsToolRejectsUnknownSortKey(t *testing.T) {
	queries := &seasonStatsQueryFake{}
	tool := seasonStatsTestServer(queries).GetTool("get_skater_season_stats")

	result := callTool(t, tool.Handler, map[string]any{"season": float64(20252026), seasonSortByArg: "points; DROP TABLE players"})

	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "accepted keys: points, goals, assists")
	assert.Zero(t, queries.skaterCalls)
}

func TestSkaterSeasonStatsToolRejectsUnknownPosition(t *testing.T) {
	queries := &seasonStatsQueryFake{}
	tool := seasonStatsTestServer(queries).GetTool("get_skater_season_stats")

	result := callTool(t, tool.Handler, map[string]any{"season": float64(20252026), seasonPositionArg: "G"})

	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "accepted positions: C, LW, RW, F, D")
	assert.Zero(t, queries.skaterCalls)
}

func TestGoalieSeasonStatsToolPassesSortAndDefaultLimit(t *testing.T) {
	queries := &seasonStatsQueryFake{}
	tool := seasonStatsTestServer(queries).GetTool("get_goalie_season_stats")
	require.NotNil(t, tool)

	result := callTool(t, tool.Handler, map[string]any{
		"season": float64(20252026), seasonSortByArg: "gaa", seasonPositionArg: "g",
	})

	assert.False(t, result.IsError)
	assert.Equal(t, 1, queries.goalieCalls)
	assert.Equal(t, int32(20252026), queries.goalie.Season)
	assert.Equal(t, "gaa", queries.goalie.SortBy)
	assert.Equal(t, int32(defaultResultLimit), queries.goalie.ResultLimit)
}

func TestGoalieSeasonStatsToolRejectsUnknownPosition(t *testing.T) {
	queries := &seasonStatsQueryFake{}
	tool := seasonStatsTestServer(queries).GetTool("get_goalie_season_stats")

	result := callTool(t, tool.Handler, map[string]any{
		"season": float64(20252026), seasonPositionArg: "D",
	})

	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "accepted position: G")
	assert.Zero(t, queries.goalieCalls)
}

func TestGoalieSeasonStatsToolRejectsUnknownSortKey(t *testing.T) {
	queries := &seasonStatsQueryFake{}
	tool := seasonStatsTestServer(queries).GetTool("get_goalie_season_stats")

	result := callTool(t, tool.Handler, map[string]any{"season": float64(20252026), seasonSortByArg: "made_up"})

	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "accepted keys: wins, losses, gaa")
	assert.Zero(t, queries.goalieCalls)
}

func TestSeasonStatsToolSchemasExposeArguments(t *testing.T) {
	srv := seasonStatsTestServer(&seasonStatsQueryFake{})
	skater := srv.GetTool("get_skater_season_stats")
	goalie := srv.GetTool("get_goalie_season_stats")
	require.NotNil(t, skater)
	require.NotNil(t, goalie)
	assert.Contains(t, skater.Tool.InputSchema.Properties, seasonSortByArg)
	assert.Contains(t, skater.Tool.InputSchema.Properties, seasonPositionArg)
	assert.Contains(t, goalie.Tool.InputSchema.Properties, seasonSortByArg)
	assert.Contains(t, goalie.Tool.InputSchema.Properties, seasonPositionArg)
}

func TestRequireSeasonSortListsEveryAcceptedKey(t *testing.T) {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{seasonSortByArg: "invalid"}

	_, result := requireSeasonSort(req, defaultSkaterSeasonSort, skaterSeasonSortKeys)

	require.NotNil(t, result)
	assert.Contains(t, resultText(t, result), "avg_toi_min")
}
