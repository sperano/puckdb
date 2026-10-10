package mcpserver

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

func registerStandingsTools(srv *server.MCPServer, queries *sqlcdb.Queries) {
	srv.AddTools(standingsTools(queries)...)
}

func standingsTools(q *sqlcdb.Queries) []server.ServerTool {
	return []server.ServerTool{
		standingsByDateTool(q),
		standingsBySeasonTool(q),
		standingsBySeasonAndDateTool(q),
		standingsByTeamTool(q),
	}
}

func standingsByDateTool(q *sqlcdb.Queries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_standings_by_date",
			mcp.WithDescription("Get NHL standings snapshot for all teams on a specific date."),
			mcp.WithString("date", mcp.Required(), mcp.Description("Date in YYYY-MM-DD format")),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			d, errResult := requireDate(req)
			if errResult != nil {
				return errResult, nil
			}
			return toolResult(q.GetStandingsSnapshotsByDate(ctx, d))
		},
	}
}

func standingsBySeasonTool(q *sqlcdb.Queries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_standings_by_season",
			mcp.WithDescription("Get all standings snapshots for a season (one row per team per snapshot date)."),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20252026)")),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			season, errResult := requireInt[int32](req, seasonParam)
			if errResult != nil {
				return errResult, nil
			}
			return toolResult(q.GetStandingsSnapshotsBySeason(ctx, season))
		},
	}
}

func standingsBySeasonAndDateTool(q *sqlcdb.Queries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_standings_by_season_and_date",
			mcp.WithDescription("Get standings snapshot for all teams at a specific point within a season."),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20252026)")),
			mcp.WithString("date", mcp.Required(), mcp.Description("Date in YYYY-MM-DD format")),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			season, errResult := requireInt[int32](req, seasonParam)
			if errResult != nil {
				return errResult, nil
			}
			d, errResult := requireDate(req)
			if errResult != nil {
				return errResult, nil
			}
			return toolResult(q.GetStandingsSnapshotsBySeasonAndDate(ctx, sqlcdb.GetStandingsSnapshotsBySeasonAndDateParams{
				Season: season,
				Date:   d,
			}))
		},
	}
}

func standingsByTeamTool(q *sqlcdb.Queries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_standings_by_team",
			mcp.WithDescription("Get all standings snapshots for a specific team across a season (tracks their rank/record over time)."),
			mcp.WithNumber("team_id", mcp.Required(), mcp.Description("Team ID (use find_team to resolve abbreviations)")),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20252026)")),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			teamID, errResult := requireInt[int64](req, teamIDParam)
			if errResult != nil {
				return errResult, nil
			}
			season, errResult := requireInt[int32](req, seasonParam)
			if errResult != nil {
				return errResult, nil
			}
			return toolResult(q.GetStandingsSnapshotsByTeam(ctx, sqlcdb.GetStandingsSnapshotsByTeamParams{
				TeamID: teamID,
				Season: season,
			}))
		},
	}
}
