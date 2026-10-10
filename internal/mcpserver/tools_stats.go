package mcpserver

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

func registerStatsTools(srv *server.MCPServer, queries *sqlcdb.Queries) {
	srv.AddTools(statsTools(queries)...)
}

func statsTools(q *sqlcdb.Queries) []server.ServerTool {
	return []server.ServerTool{
		gameSkaterStatsTool(q),
		gameGoalieStatsTool(q),
		gameSkaterStatsByTeamTool(q),
		gameGoalieStatsByTeamTool(q),
		skaterSeasonTotalsTool(q),
		goalieSeasonTotalsTool(q),
		skaterGameLogTool(q),
		goalieGameLogTool(q),
		clubSkaterStatsTool(q),
		clubGoalieStatsTool(q),
	}
}

func gameSkaterStatsTool(q *sqlcdb.Queries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_game_skater_stats",
			mcp.WithDescription("Get skater box score stats for all players in a game (TOI, goals, assists, shots, hits, blocks, etc.)."),
			mcp.WithNumber("game_id", mcp.Required(), mcp.Description("Game ID")),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			gameID, errResult := requireInt[int64](req, gameIDParam)
			if errResult != nil {
				return errResult, nil
			}
			return toolResult(q.GetGameSkaterStatsByGame(ctx, gameID))
		},
	}
}

func gameGoalieStatsTool(q *sqlcdb.Queries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_game_goalie_stats",
			mcp.WithDescription("Get goalie box score stats for all goalies in a game (saves, shots against, GAA, save%, etc.)."),
			mcp.WithNumber("game_id", mcp.Required(), mcp.Description("Game ID")),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			gameID, errResult := requireInt[int64](req, gameIDParam)
			if errResult != nil {
				return errResult, nil
			}
			return toolResult(q.GetGameGoalieStatsByGame(ctx, gameID))
		},
	}
}

func gameSkaterStatsByTeamTool(q *sqlcdb.Queries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_game_skater_stats_by_team",
			mcp.WithDescription("Get skater box score stats for a specific team in a game."),
			mcp.WithNumber("game_id", mcp.Required(), mcp.Description("Game ID")),
			mcp.WithNumber("team_id", mcp.Required(), mcp.Description("Team ID")),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			gameID, errResult := requireInt[int64](req, gameIDParam)
			if errResult != nil {
				return errResult, nil
			}
			teamID, errResult := requireInt[int64](req, teamIDParam)
			if errResult != nil {
				return errResult, nil
			}
			return toolResult(q.GetGameSkaterStatsByGameAndTeam(ctx, sqlcdb.GetGameSkaterStatsByGameAndTeamParams{
				GameID: gameID,
				TeamID: teamID,
			}))
		},
	}
}

func gameGoalieStatsByTeamTool(q *sqlcdb.Queries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_game_goalie_stats_by_team",
			mcp.WithDescription("Get goalie box score stats for a specific team in a game."),
			mcp.WithNumber("game_id", mcp.Required(), mcp.Description("Game ID")),
			mcp.WithNumber("team_id", mcp.Required(), mcp.Description("Team ID")),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			gameID, errResult := requireInt[int64](req, gameIDParam)
			if errResult != nil {
				return errResult, nil
			}
			teamID, errResult := requireInt[int64](req, teamIDParam)
			if errResult != nil {
				return errResult, nil
			}
			return toolResult(q.GetGameGoalieStatsByGameAndTeam(ctx, sqlcdb.GetGameGoalieStatsByGameAndTeamParams{
				GameID: gameID,
				TeamID: teamID,
			}))
		},
	}
}

func skaterSeasonTotalsTool(q *sqlcdb.Queries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_skater_season_totals",
			mcp.WithDescription("Get a skater's aggregated season totals (goals, assists, points, +/-, PIM, TOI, etc.) for a specific season."),
			mcp.WithNumber(playerIDArg, mcp.Required(), mcp.Description("Player ID")),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20252026)")),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			playerID, errResult := requireInt[int64](req, playerIDParam)
			if errResult != nil {
				return errResult, nil
			}
			season, errResult := requireInt[int32](req, seasonParam)
			if errResult != nil {
				return errResult, nil
			}
			return jsonResult(q.GetSkaterSeasonTotals(ctx, sqlcdb.GetSkaterSeasonTotalsParams{
				PlayerID: playerID,
				Season:   season,
			}))
		},
	}
}

func goalieSeasonTotalsTool(q *sqlcdb.Queries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_goalie_season_totals",
			mcp.WithDescription("Get a goalie's aggregated season totals (wins, losses, GAA, save%, shutouts, etc.) for a specific season."),
			mcp.WithNumber(playerIDArg, mcp.Required(), mcp.Description("Player ID")),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20252026)")),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			playerID, errResult := requireInt[int64](req, playerIDParam)
			if errResult != nil {
				return errResult, nil
			}
			season, errResult := requireInt[int32](req, seasonParam)
			if errResult != nil {
				return errResult, nil
			}
			return jsonResult(q.GetGoalieSeasonTotals(ctx, sqlcdb.GetGoalieSeasonTotalsParams{
				PlayerID: playerID,
				Season:   season,
			}))
		},
	}
}

func skaterGameLogTool(q *sqlcdb.Queries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_skater_game_log",
			mcp.WithDescription("Get a skater's game-by-game stats log for a season (one row per game played)."),
			mcp.WithNumber(playerIDArg, mcp.Required(), mcp.Description("Player ID")),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20252026)")),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			playerID, errResult := requireInt[int64](req, playerIDParam)
			if errResult != nil {
				return errResult, nil
			}
			season, errResult := requireInt[int32](req, seasonParam)
			if errResult != nil {
				return errResult, nil
			}
			return toolResult(q.GetSkaterStatsByPlayerAndSeason(ctx, sqlcdb.GetSkaterStatsByPlayerAndSeasonParams{
				PlayerID: playerID,
				Season:   season,
			}))
		},
	}
}

func goalieGameLogTool(q *sqlcdb.Queries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_goalie_game_log",
			mcp.WithDescription("Get a goalie's game-by-game stats log for a season (one row per game played)."),
			mcp.WithNumber(playerIDArg, mcp.Required(), mcp.Description("Player ID")),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20252026)")),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			playerID, errResult := requireInt[int64](req, playerIDParam)
			if errResult != nil {
				return errResult, nil
			}
			season, errResult := requireInt[int32](req, seasonParam)
			if errResult != nil {
				return errResult, nil
			}
			return toolResult(q.GetGoalieStatsByPlayerAndSeason(ctx, sqlcdb.GetGoalieStatsByPlayerAndSeasonParams{
				PlayerID: playerID,
				Season:   season,
			}))
		},
	}
}

func clubSkaterStatsTool(q *sqlcdb.Queries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_club_skater_stats",
			mcp.WithDescription("Get season skater stats for all players on a team. Rows are per club: a traded player appears under each club he played for with only that stint's stats — use get_skater_season_totals for full-season lines. Only regular_season and playoffs are available (preseason is never fetched)."),
			mcp.WithNumber("team_id", mcp.Required(), mcp.Description("Team ID")),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20252026)")),
			mcp.WithString("game_type", mcp.Description("Game type: regular_season (default) or playoffs")),
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
			gameType := sqlcdb.GameType(req.GetString("game_type", string(sqlcdb.GameTypeRegularSeason)))
			return toolResult(q.GetClubSkaterStatsByTeam(ctx, sqlcdb.GetClubSkaterStatsByTeamParams{
				TeamID:   teamID,
				Season:   season,
				GameType: gameType,
			}))
		},
	}
}

func clubGoalieStatsTool(q *sqlcdb.Queries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_club_goalie_stats",
			mcp.WithDescription("Get season goalie stats for all goalies on a team. Rows are per club: a traded goalie appears under each club he played for with only that stint's stats — use get_goalie_season_totals for full-season lines. Only regular_season and playoffs are available (preseason is never fetched)."),
			mcp.WithNumber("team_id", mcp.Required(), mcp.Description("Team ID")),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20252026)")),
			mcp.WithString("game_type", mcp.Description("Game type: regular_season (default) or playoffs")),
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
			gameType := sqlcdb.GameType(req.GetString("game_type", string(sqlcdb.GameTypeRegularSeason)))
			return toolResult(q.GetClubGoalieStatsByTeam(ctx, sqlcdb.GetClubGoalieStatsByTeamParams{
				TeamID:   teamID,
				Season:   season,
				GameType: gameType,
			}))
		},
	}
}
