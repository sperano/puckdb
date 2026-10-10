package mcpserver

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

func registerResolveTools(srv *server.MCPServer, queries *sqlcdb.Queries) {
	srv.AddTools(resolveTools(queries)...)
}

func resolveTools(q *sqlcdb.Queries) []server.ServerTool {
	return []server.ServerTool{
		searchPlayerTool(q),
		findTeamTool(q),
		listTeamsTool(q),
		listSeasonsTool(q),
		listFranchisesTool(q),
	}
}

func findTeamTool(q *sqlcdb.Queries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("find_team",
			mcp.WithDescription("Resolve a team abbreviation (e.g. 'MTL', 'TOR') to a team ID for a given season."),
			mcp.WithString("abbrev", mcp.Required(), mcp.Description("Team abbreviation (e.g. MTL, TOR, BOS)")),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20252026)")),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			abbrev, err := req.RequireString("abbrev")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			season, errResult := requireInt[int32](req, seasonParam)
			if errResult != nil {
				return errResult, nil
			}
			teamID, err := q.GetTeamIDByAbbrev(ctx, sqlcdb.GetTeamIDByAbbrevParams{
				Season: season,
				Abbrev: abbrev,
			})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultJSON(map[string]any{"team_id": teamID, "abbrev": abbrev, "season": season})
		},
	}
}

func listTeamsTool(q *sqlcdb.Queries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("list_teams",
			mcp.WithDescription("List all NHL teams for a season, including team IDs, abbreviations, divisions, and franchise info."),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20252026)")),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			season, errResult := requireInt[int32](req, seasonParam)
			if errResult != nil {
				return errResult, nil
			}
			return toolResult(q.GetSeasonTeams(ctx, season))
		},
	}
}

func listSeasonsTool(q *sqlcdb.Queries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("list_seasons",
			mcp.WithDescription("List all available NHL seasons with their date ranges."),
		),
		Handler: func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return toolResult(q.GetAllSeasons(ctx))
		},
	}
}

func listFranchisesTool(q *sqlcdb.Queries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("list_franchises",
			mcp.WithDescription("List all NHL franchises with their full names and common names."),
		),
		Handler: func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return toolResult(q.GetAllFranchises(ctx))
		},
	}
}
