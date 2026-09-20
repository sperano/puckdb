package mcpserver

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

func registerGameTools(srv *server.MCPServer, queries *sqlcdb.Queries) {
	srv.AddTool(
		mcp.NewTool("get_game",
			mcp.WithDescription("Get full details for a single NHL game by ID, including teams, score, game state, and venue."),
			mcp.WithNumber("game_id", mcp.Required(), mcp.Description("Game ID")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			gameID := req.GetInt("game_id", 0)
			if gameID == 0 {
				return mcp.NewToolResultError("game_id is required"), nil
			}
			game, err := queries.GetGame(ctx, int64(gameID))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultJSON(game)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_games_by_date",
			mcp.WithDescription("Get all NHL games scheduled on a specific date."),
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
			games, err := queries.GetGamesByDate(ctx, d)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(games)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_games_by_season",
			mcp.WithDescription("Get all NHL games for a season."),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20252026)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			season := req.GetInt("season", 0)
			if season == 0 {
				return mcp.NewToolResultError("season is required"), nil
			}
			games, err := queries.GetGamesBySeason(ctx, int32(season))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(games)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_games_by_team",
			mcp.WithDescription("Get all games (home and away) for a team across all seasons."),
			mcp.WithNumber("team_id", mcp.Required(), mcp.Description("Team ID (use find_team to resolve abbreviations)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			teamID := req.GetInt("team_id", 0)
			if teamID == 0 {
				return mcp.NewToolResultError("team_id is required"), nil
			}
			games, err := queries.GetGamesByTeam(ctx, int64(teamID))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(games)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_games_by_team_and_season",
			mcp.WithDescription("Get all games (home and away) for a team in a specific season."),
			mcp.WithNumber("team_id", mcp.Required(), mcp.Description("Team ID")),
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
			games, err := queries.GetGamesByTeamAndSeason(ctx, sqlcdb.GetGamesByTeamAndSeasonParams{
				HomeTeamID: int64(teamID),
				Season:     int32(season),
			})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(games)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_game_three_stars",
			mcp.WithDescription("Get the three-star selections for a game (1st, 2nd, 3rd star)."),
			mcp.WithNumber("game_id", mcp.Required(), mcp.Description("Game ID")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			gameID := req.GetInt("game_id", 0)
			if gameID == 0 {
				return mcp.NewToolResultError("game_id is required"), nil
			}
			stars, err := queries.GetGameThreeStars(ctx, int64(gameID))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(stars)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_game_broadcasts",
			mcp.WithDescription("Get broadcast information for a game (TV/radio networks by market)."),
			mcp.WithNumber("game_id", mcp.Required(), mcp.Description("Game ID")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			gameID := req.GetInt("game_id", 0)
			if gameID == 0 {
				return mcp.NewToolResultError("game_id is required"), nil
			}
			broadcasts, err := queries.GetGameBroadcasts(ctx, int64(gameID))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(broadcasts)
		},
	)
}
