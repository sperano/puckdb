package mcpserver

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// registerSeasonStatsTools adds the season stat views. They read NHL data
// only, so they belong to the nhl toolset even though they are meant for
// fantasy analysis.
func registerSeasonStatsTools(srv *server.MCPServer, queries *sqlcdb.Queries) {
	srv.AddTool(
		mcp.NewTool("get_skater_season_stats",
			mcp.WithDescription("Get fantasy-relevant season stats for skaters, sorted by points. Use limit to control result size."),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20252026)")),
			mcp.WithNumber("limit", mcp.Description("Max number of skaters to return (default 100)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			season := req.GetInt("season", 0)
			if season == 0 {
				return mcp.NewToolResultError("season is required"), nil
			}
			limit := req.GetInt("limit", defaultResultLimit)
			stats, err := queries.GetSkaterSeasonStats(ctx, sqlcdb.GetSkaterSeasonStatsParams{
				Season: int32(season),
				Limit:  int32(limit),
			})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(stats)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_goalie_season_stats",
			mcp.WithDescription("Get fantasy-relevant season stats for goalies, sorted by wins. Use limit to control result size."),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20252026)")),
			mcp.WithNumber("limit", mcp.Description("Max number of goalies to return (default 100)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			season := req.GetInt("season", 0)
			if season == 0 {
				return mcp.NewToolResultError("season is required"), nil
			}
			limit := req.GetInt("limit", defaultResultLimit)
			stats, err := queries.GetGoalieSeasonStats(ctx, sqlcdb.GetGoalieSeasonStatsParams{
				Season: int32(season),
				Limit:  int32(limit),
			})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(stats)
		},
	)
}
