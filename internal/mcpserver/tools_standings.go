package mcpserver

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

func registerStandingsTools(srv *server.MCPServer, queries *sqlcdb.Queries) {
	srv.AddTool(
		mcp.NewTool("get_standings_by_date",
			mcp.WithDescription("Get NHL standings snapshot for all teams on a specific date."),
			mcp.WithString("date", mcp.Required(), mcp.Description("Date in YYYY-MM-DD format")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			dateStr, err := req.RequireString("date")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			var d pgtype.Date
			if err := d.Scan(dateStr); err != nil {
				return mcp.NewToolResultError("invalid date format, use YYYY-MM-DD"), nil
			}
			rows, err := queries.GetStandingsSnapshotsByDate(ctx, d)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(rows)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_standings_by_season",
			mcp.WithDescription("Get all standings snapshots for a season (one row per team per snapshot date)."),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20252026)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			season := req.GetInt("season", 0)
			if season == 0 {
				return mcp.NewToolResultError("season is required"), nil
			}
			rows, err := queries.GetStandingsSnapshotsBySeason(ctx, int32(season))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(rows)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_standings_by_season_and_date",
			mcp.WithDescription("Get standings snapshot for all teams at a specific point within a season."),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20252026)")),
			mcp.WithString("date", mcp.Required(), mcp.Description("Date in YYYY-MM-DD format")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			season := req.GetInt("season", 0)
			if season == 0 {
				return mcp.NewToolResultError("season is required"), nil
			}
			dateStr, err := req.RequireString("date")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			var d pgtype.Date
			if err := d.Scan(dateStr); err != nil {
				return mcp.NewToolResultError("invalid date format, use YYYY-MM-DD"), nil
			}
			rows, err := queries.GetStandingsSnapshotsBySeasonAndDate(ctx, sqlcdb.GetStandingsSnapshotsBySeasonAndDateParams{
				Season: int32(season),
				Date:   d,
			})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(rows)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_standings_by_team",
			mcp.WithDescription("Get all standings snapshots for a specific team across a season (tracks their rank/record over time)."),
			mcp.WithNumber("team_id", mcp.Required(), mcp.Description("Team ID (use find_team to resolve abbreviations)")),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20252026)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			teamID := req.GetInt("team_id", 0)
			if teamID == 0 {
				return mcp.NewToolResultError("team_id is required"), nil
			}
			season := req.GetInt("season", 0)
			if season == 0 {
				return mcp.NewToolResultError("season is required"), nil
			}
			rows, err := queries.GetStandingsSnapshotsByTeam(ctx, sqlcdb.GetStandingsSnapshotsByTeamParams{
				TeamID: int64(teamID),
				Season: int32(season),
			})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(rows)
		},
	)
}
