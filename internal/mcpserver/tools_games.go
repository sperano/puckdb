package mcpserver

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

func registerGameTools(srv *server.MCPServer, queries *sqlcdb.Queries) {
	srv.AddTools(gameTools(queries)...)
}

func gameTools(q *sqlcdb.Queries) []server.ServerTool {
	return []server.ServerTool{
		getGameTool(q),
		gamesByDateTool(q),
		gamesBySeasonTool(q),
		gamesByTeamTool(q),
		gamesByTeamAndSeasonTool(q),
		gameThreeStarsTool(q),
		gameBroadcastsTool(q),
	}
}

func getGameTool(q *sqlcdb.Queries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_game",
			mcp.WithDescription("Get full details for a single NHL game by ID, including teams, score, game state, and venue."),
			mcp.WithNumber("game_id", mcp.Required(), mcp.Description("Game ID")),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			gameID, errResult := requireInt[int64](req, gameIDParam)
			if errResult != nil {
				return errResult, nil
			}
			return jsonResult(q.GetGame(ctx, gameID))
		},
	}
}

func gamesByDateTool(q *sqlcdb.Queries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_games_by_date",
			mcp.WithDescription("Get all NHL games scheduled on a specific date."),
			mcp.WithString("date", mcp.Required(), mcp.Description("Date in YYYY-MM-DD format")),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			d, errResult := requireDate(req)
			if errResult != nil {
				return errResult, nil
			}
			return toolResult(q.GetGamesByDate(ctx, d))
		},
	}
}

func gamesBySeasonTool(q *sqlcdb.Queries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_games_by_season",
			mcp.WithDescription("Get all NHL games for a season."),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20252026)")),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			season, errResult := requireInt[int32](req, seasonParam)
			if errResult != nil {
				return errResult, nil
			}
			return toolResult(q.GetGamesBySeason(ctx, season))
		},
	}
}

func gamesByTeamTool(q *sqlcdb.Queries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_games_by_team",
			mcp.WithDescription("Get all games (home and away) for a team across all seasons."),
			mcp.WithNumber("team_id", mcp.Required(), mcp.Description("Team ID (use find_team to resolve abbreviations)")),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			teamID, errResult := requireInt[int64](req, teamIDParam)
			if errResult != nil {
				return errResult, nil
			}
			return toolResult(q.GetGamesByTeam(ctx, teamID))
		},
	}
}

func gamesByTeamAndSeasonTool(q *sqlcdb.Queries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_games_by_team_and_season",
			mcp.WithDescription("Get all games (home and away) for a team in a specific season."),
			mcp.WithNumber("team_id", mcp.Required(), mcp.Description("Team ID")),
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
			return toolResult(q.GetGamesByTeamAndSeason(ctx, sqlcdb.GetGamesByTeamAndSeasonParams{
				HomeTeamID: teamID,
				Season:     season,
			}))
		},
	}
}

func gameThreeStarsTool(q *sqlcdb.Queries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_game_three_stars",
			mcp.WithDescription("Get the three-star selections for a game (1st, 2nd, 3rd star)."),
			mcp.WithNumber("game_id", mcp.Required(), mcp.Description("Game ID")),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			gameID, errResult := requireInt[int64](req, gameIDParam)
			if errResult != nil {
				return errResult, nil
			}
			return toolResult(q.GetGameThreeStars(ctx, gameID))
		},
	}
}

func gameBroadcastsTool(q *sqlcdb.Queries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_game_broadcasts",
			mcp.WithDescription("Get broadcast information for a game (TV/radio networks by market)."),
			mcp.WithNumber("game_id", mcp.Required(), mcp.Description("Game ID")),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			gameID, errResult := requireInt[int64](req, gameIDParam)
			if errResult != nil {
				return errResult, nil
			}
			return toolResult(q.GetGameBroadcasts(ctx, gameID))
		},
	}
}
