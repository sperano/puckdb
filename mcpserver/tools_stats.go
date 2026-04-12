package mcpserver

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/sqlcdb"
)

func registerStatsTools(srv *server.MCPServer, queries *sqlcdb.Queries) {
	srv.AddTool(
		mcp.NewTool("get_game_skater_stats",
			mcp.WithDescription("Get skater box score stats for all players in a game (TOI, goals, assists, shots, hits, blocks, etc.)."),
			mcp.WithNumber("game_id", mcp.Required(), mcp.Description("Game ID")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			gameID := req.GetInt("game_id", 0)
			if gameID == 0 {
				return mcp.NewToolResultError("game_id is required"), nil
			}
			stats, err := queries.GetGameSkaterStatsByGame(ctx, int64(gameID))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(stats)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_game_goalie_stats",
			mcp.WithDescription("Get goalie box score stats for all goalies in a game (saves, shots against, GAA, save%, etc.)."),
			mcp.WithNumber("game_id", mcp.Required(), mcp.Description("Game ID")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			gameID := req.GetInt("game_id", 0)
			if gameID == 0 {
				return mcp.NewToolResultError("game_id is required"), nil
			}
			stats, err := queries.GetGameGoalieStatsByGame(ctx, int64(gameID))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(stats)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_game_skater_stats_by_team",
			mcp.WithDescription("Get skater box score stats for a specific team in a game."),
			mcp.WithNumber("game_id", mcp.Required(), mcp.Description("Game ID")),
			mcp.WithNumber("team_id", mcp.Required(), mcp.Description("Team ID")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			gameID := req.GetInt("game_id", 0)
			if gameID == 0 {
				return mcp.NewToolResultError("game_id is required"), nil
			}
			teamID := req.GetInt("team_id", 0)
			if teamID == 0 {
				return mcp.NewToolResultError("team_id is required"), nil
			}
			stats, err := queries.GetGameSkaterStatsByGameAndTeam(ctx, sqlcdb.GetGameSkaterStatsByGameAndTeamParams{
				GameID: int64(gameID),
				TeamID: int64(teamID),
			})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(stats)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_game_goalie_stats_by_team",
			mcp.WithDescription("Get goalie box score stats for a specific team in a game."),
			mcp.WithNumber("game_id", mcp.Required(), mcp.Description("Game ID")),
			mcp.WithNumber("team_id", mcp.Required(), mcp.Description("Team ID")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			gameID := req.GetInt("game_id", 0)
			if gameID == 0 {
				return mcp.NewToolResultError("game_id is required"), nil
			}
			teamID := req.GetInt("team_id", 0)
			if teamID == 0 {
				return mcp.NewToolResultError("team_id is required"), nil
			}
			stats, err := queries.GetGameGoalieStatsByGameAndTeam(ctx, sqlcdb.GetGameGoalieStatsByGameAndTeamParams{
				GameID: int64(gameID),
				TeamID: int64(teamID),
			})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(stats)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_skater_season_totals",
			mcp.WithDescription("Get a skater's aggregated season totals (goals, assists, points, +/-, PIM, TOI, etc.) for a specific season."),
			mcp.WithNumber("player_id", mcp.Required(), mcp.Description("Player ID")),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20252026)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			playerID := req.GetInt("player_id", 0)
			if playerID == 0 {
				return mcp.NewToolResultError("player_id is required"), nil
			}
			season := req.GetInt("season", 0)
			if season == 0 {
				return mcp.NewToolResultError("season is required"), nil
			}
			totals, err := queries.GetSkaterSeasonTotals(ctx, sqlcdb.GetSkaterSeasonTotalsParams{
				PlayerID: int64(playerID),
				Season:   int32(season),
			})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultJSON(totals)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_goalie_season_totals",
			mcp.WithDescription("Get a goalie's aggregated season totals (wins, losses, GAA, save%, shutouts, etc.) for a specific season."),
			mcp.WithNumber("player_id", mcp.Required(), mcp.Description("Player ID")),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20252026)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			playerID := req.GetInt("player_id", 0)
			if playerID == 0 {
				return mcp.NewToolResultError("player_id is required"), nil
			}
			season := req.GetInt("season", 0)
			if season == 0 {
				return mcp.NewToolResultError("season is required"), nil
			}
			totals, err := queries.GetGoalieSeasonTotals(ctx, sqlcdb.GetGoalieSeasonTotalsParams{
				PlayerID: int64(playerID),
				Season:   int32(season),
			})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultJSON(totals)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_skater_game_log",
			mcp.WithDescription("Get a skater's game-by-game stats log for a season (one row per game played)."),
			mcp.WithNumber("player_id", mcp.Required(), mcp.Description("Player ID")),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20252026)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			playerID := req.GetInt("player_id", 0)
			if playerID == 0 {
				return mcp.NewToolResultError("player_id is required"), nil
			}
			season := req.GetInt("season", 0)
			if season == 0 {
				return mcp.NewToolResultError("season is required"), nil
			}
			stats, err := queries.GetSkaterStatsByPlayerAndSeason(ctx, sqlcdb.GetSkaterStatsByPlayerAndSeasonParams{
				PlayerID: int64(playerID),
				Season:   int32(season),
			})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(stats)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_goalie_game_log",
			mcp.WithDescription("Get a goalie's game-by-game stats log for a season (one row per game played)."),
			mcp.WithNumber("player_id", mcp.Required(), mcp.Description("Player ID")),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20252026)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			playerID := req.GetInt("player_id", 0)
			if playerID == 0 {
				return mcp.NewToolResultError("player_id is required"), nil
			}
			season := req.GetInt("season", 0)
			if season == 0 {
				return mcp.NewToolResultError("season is required"), nil
			}
			stats, err := queries.GetGoalieStatsByPlayerAndSeason(ctx, sqlcdb.GetGoalieStatsByPlayerAndSeasonParams{
				PlayerID: int64(playerID),
				Season:   int32(season),
			})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(stats)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_club_skater_stats",
			mcp.WithDescription("Get season skater stats for all players on a team. Defaults to regular season; pass game_type to query playoffs or preseason."),
			mcp.WithNumber("team_id", mcp.Required(), mcp.Description("Team ID")),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20252026)")),
			mcp.WithString("game_type", mcp.Description("Game type: regular_season (default), playoffs, preseason")),
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
			gameType := sqlcdb.GameType(req.GetString("game_type", string(sqlcdb.GameTypeRegularSeason)))
			stats, err := queries.GetClubSkaterStatsByTeam(ctx, sqlcdb.GetClubSkaterStatsByTeamParams{
				TeamID:   int64(teamID),
				Season:   int32(season),
				GameType: gameType,
			})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(stats)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_club_goalie_stats",
			mcp.WithDescription("Get season goalie stats for all goalies on a team. Defaults to regular season; pass game_type to query playoffs or preseason."),
			mcp.WithNumber("team_id", mcp.Required(), mcp.Description("Team ID")),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20252026)")),
			mcp.WithString("game_type", mcp.Description("Game type: regular_season (default), playoffs, preseason")),
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
			gameType := sqlcdb.GameType(req.GetString("game_type", string(sqlcdb.GameTypeRegularSeason)))
			stats, err := queries.GetClubGoalieStatsByTeam(ctx, sqlcdb.GetClubGoalieStatsByTeamParams{
				TeamID:   int64(teamID),
				Season:   int32(season),
				GameType: gameType,
			})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(stats)
		},
	)
}
