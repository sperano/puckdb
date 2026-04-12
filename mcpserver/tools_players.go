package mcpserver

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/sqlcdb"
)

func registerPlayerTools(srv *server.MCPServer, queries *sqlcdb.Queries) {
	srv.AddTool(
		mcp.NewTool("get_player",
			mcp.WithDescription("Get detailed information about a single NHL player by ID. Returns bio, position, team, draft info, etc."),
			mcp.WithNumber("player_id", mcp.Required(), mcp.Description("Player ID (use search_player to find IDs)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			playerID := req.GetInt("player_id", 0)
			if playerID == 0 {
				return mcp.NewToolResultError("player_id is required"), nil
			}
			player, err := queries.GetPlayer(ctx, int64(playerID))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultJSON(player)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_players_by_team",
			mcp.WithDescription("Get all players currently assigned to a team."),
			mcp.WithNumber("team_id", mcp.Required(), mcp.Description("Team ID (use find_team to resolve abbreviations)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			teamID := req.GetInt("team_id", 0)
			if teamID == 0 {
				return mcp.NewToolResultError("team_id is required"), nil
			}
			players, err := queries.GetPlayersByTeam(ctx, pgtype.Int8{Int64: int64(teamID), Valid: true})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(players)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_players_by_position",
			mcp.WithDescription("Get all players who play a given position."),
			mcp.WithString("position", mcp.Required(), mcp.Description("Position code: C, LW, RW, F, D, or G")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			pos, err := req.RequireString("position")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			players, err := queries.GetPlayersByPosition(ctx, sqlcdb.NullPlayerPosition{
				PlayerPosition: sqlcdb.PlayerPosition(pos),
				Valid:          true,
			})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(players)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_active_players",
			mcp.WithDescription("Get all currently active NHL players."),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			players, err := queries.GetActivePlayers(ctx)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(players)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_player_career_totals",
			mcp.WithDescription("Get a player's career season-by-season totals (goals, assists, points, +/-, PIM) across all leagues."),
			mcp.WithNumber("player_id", mcp.Required(), mcp.Description("Player ID")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			playerID := req.GetInt("player_id", 0)
			if playerID == 0 {
				return mcp.NewToolResultError("player_id is required"), nil
			}
			totals, err := queries.GetPlayerNHLSeasonTotals(ctx, int64(playerID))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(totals)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_player_awards",
			mcp.WithDescription("Get all awards/trophies won by a player (Hart, Norris, Vezina, etc.)."),
			mcp.WithNumber("player_id", mcp.Required(), mcp.Description("Player ID")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			playerID := req.GetInt("player_id", 0)
			if playerID == 0 {
				return mcp.NewToolResultError("player_id is required"), nil
			}
			awards, err := queries.GetPlayerAwards(ctx, int64(playerID))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(awards)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_player_roster_history",
			mcp.WithDescription("Get a player's roster history — which teams they played for each season, with position, jersey number, and physical info."),
			mcp.WithNumber("player_id", mcp.Required(), mcp.Description("Player ID")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			playerID := req.GetInt("player_id", 0)
			if playerID == 0 {
				return mcp.NewToolResultError("player_id is required"), nil
			}
			roster, err := queries.GetSeasonRosterByPlayer(ctx, int64(playerID))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(roster)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_player_three_stars",
			mcp.WithDescription("Get all three-star selections for a player (1st, 2nd, or 3rd star of the game)."),
			mcp.WithNumber("player_id", mcp.Required(), mcp.Description("Player ID")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			playerID := req.GetInt("player_id", 0)
			if playerID == 0 {
				return mcp.NewToolResultError("player_id is required"), nil
			}
			stars, err := queries.GetPlayerThreeStarSelections(ctx, int64(playerID))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(stars)
		},
	)
}
