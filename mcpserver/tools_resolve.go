package mcpserver

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/sqlcdb"
)

func registerResolveTools(srv *server.MCPServer, queries *sqlcdb.Queries) {
	srv.AddTool(
		mcp.NewTool("search_player",
			mcp.WithDescription("Search for NHL players by name (accent-insensitive). Returns matching players with IDs."),
			mcp.WithString("name", mcp.Required(), mcp.Description("Player name or partial name to search for")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			name, err := req.RequireString("name")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			players, err := queries.SearchPlayersByName(ctx, "%"+name+"%")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(players)
		},
	)

	srv.AddTool(
		mcp.NewTool("find_team",
			mcp.WithDescription("Resolve a team abbreviation (e.g. 'MTL', 'TOR') to a team ID for a given season."),
			mcp.WithString("abbrev", mcp.Required(), mcp.Description("Team abbreviation (e.g. MTL, TOR, BOS)")),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20252026)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			abbrev, err := req.RequireString("abbrev")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			season := req.GetInt("season", 0)
			if season == 0 {
				return mcp.NewToolResultError("season is required"), nil
			}
			teamID, err := queries.GetTeamIDByAbbrev(ctx, sqlcdb.GetTeamIDByAbbrevParams{
				Season: int32(season),
				Abbrev: abbrev,
			})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultJSON(map[string]any{"team_id": teamID, "abbrev": abbrev, "season": season})
		},
	)

	srv.AddTool(
		mcp.NewTool("list_teams",
			mcp.WithDescription("List all NHL teams for a season, including team IDs, abbreviations, divisions, and franchise info."),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20252026)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			season := req.GetInt("season", 0)
			if season == 0 {
				return mcp.NewToolResultError("season is required"), nil
			}
			teams, err := queries.GetSeasonTeams(ctx, int32(season))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(teams)
		},
	)

	srv.AddTool(
		mcp.NewTool("list_seasons",
			mcp.WithDescription("List all available NHL seasons with their date ranges."),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			seasons, err := queries.GetAllSeasons(ctx)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(seasons)
		},
	)

	srv.AddTool(
		mcp.NewTool("list_franchises",
			mcp.WithDescription("List all NHL franchises with their full names and common names."),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			franchises, err := queries.GetAllFranchises(ctx)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(franchises)
		},
	)
}
