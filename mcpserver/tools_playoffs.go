package mcpserver

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/sqlcdb"
)

func registerPlayoffTools(srv *server.MCPServer, queries *sqlcdb.Queries) {
	// Option A: Flexible game listing with filters (exposes existing ListGames query)
	srv.AddTool(
		mcp.NewTool("list_games",
			mcp.WithDescription("List games with optional filters. More flexible than get_games_by_season."),
			mcp.WithNumber("season", mcp.Description("Season ID (e.g. 20242025)")),
			mcp.WithString("game_type", mcp.Description("Filter by type: preseason, regular_season, playoffs, all_star")),
			mcp.WithNumber("team_id", mcp.Description("Filter by team ID")),
			mcp.WithString("start_date", mcp.Description("Start date (YYYY-MM-DD)")),
			mcp.WithString("end_date", mcp.Description("End date (YYYY-MM-DD)")),
			mcp.WithNumber("limit", mcp.Description("Max results (default 100)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			params := sqlcdb.ListGamesParams{}

			if season := req.GetInt("season", 0); season != 0 {
				params.Season = pgtype.Int4{Int32: int32(season), Valid: true}
			}
			if gameType := req.GetString("game_type", ""); gameType != "" {
				params.GameType = sqlcdb.NullGameType{
					GameType: sqlcdb.GameType(gameType),
					Valid:    true,
				}
			}
			if teamID := req.GetInt("team_id", 0); teamID != 0 {
				params.TeamID = pgtype.Int8{Int64: int64(teamID), Valid: true}
			}
			if startDate := req.GetString("start_date", ""); startDate != "" {
				var d pgtype.Date
				if err := d.Scan(startDate); err != nil {
					return mcp.NewToolResultError("invalid start_date format, use YYYY-MM-DD"), nil
				}
				params.StartDate = d
			}
			if endDate := req.GetString("end_date", ""); endDate != "" {
				var d pgtype.Date
				if err := d.Scan(endDate); err != nil {
					return mcp.NewToolResultError("invalid end_date format, use YYYY-MM-DD"), nil
				}
				params.EndDate = d
			}
			if limit := req.GetInt("limit", 0); limit != 0 {
				params.Limit = pgtype.Int4{Int32: int32(limit), Valid: true}
			}

			games, err := queries.ListGames(ctx, params)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(games)
		},
	)

	// Option B: Targeted playoff tools
	srv.AddTool(
		mcp.NewTool("get_playoff_games",
			mcp.WithDescription("Get playoff games for a season, optionally filtered by round (1=First Round, 2=Second Round, 3=Conference Finals, 4=Stanley Cup Finals) and/or team."),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20242025)")),
			mcp.WithNumber("round", mcp.Description("Playoff round: 1, 2, 3, or 4")),
			mcp.WithNumber("team_id", mcp.Description("Filter by team ID")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			season := req.GetInt("season", 0)
			if season == 0 {
				return mcp.NewToolResultError("season is required"), nil
			}

			params := sqlcdb.GetPlayoffGamesParams{
				Season: int32(season),
			}
			if round := req.GetInt("round", 0); round != 0 {
				params.Round = pgtype.Int4{Int32: int32(round), Valid: true}
			}
			if teamID := req.GetInt("team_id", 0); teamID != 0 {
				params.TeamID = pgtype.Int8{Int64: int64(teamID), Valid: true}
			}

			games, err := queries.GetPlayoffGames(ctx, params)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(games)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_playoff_series",
			mcp.WithDescription("Get playoff series summaries for a season showing matchups and win counts. Optionally filter by round."),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20242025)")),
			mcp.WithNumber("round", mcp.Description("Playoff round: 1, 2, 3, or 4")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			season := req.GetInt("season", 0)
			if season == 0 {
				return mcp.NewToolResultError("season is required"), nil
			}

			params := sqlcdb.GetPlayoffSeriesParams{
				Season: int32(season),
			}
			if round := req.GetInt("round", 0); round != 0 {
				params.Round = pgtype.Int4{Int32: int32(round), Valid: true}
			}

			series, err := queries.GetPlayoffSeries(ctx, params)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(series)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_stanley_cup_finals",
			mcp.WithDescription("Get all Stanley Cup Finals games for a season (playoff round 4)."),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20242025)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			season := req.GetInt("season", 0)
			if season == 0 {
				return mcp.NewToolResultError("season is required"), nil
			}

			games, err := queries.GetStanleyCupFinals(ctx, int32(season))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(games)
		},
	)

	// Option C: High-level championship query
	srv.AddTool(
		mcp.NewTool("get_stanley_cup_winners",
			mcp.WithDescription("Get Stanley Cup champions for recent seasons. Returns champion, runner-up, and clinching game details."),
			mcp.WithNumber("limit", mcp.Description("Number of seasons to return (default 10)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var limitParam pgtype.Int4
			if limit := req.GetInt("limit", 0); limit != 0 {
				limitParam = pgtype.Int4{Int32: int32(limit), Valid: true}
			}

			winners, err := queries.GetStanleyCupWinners(ctx, limitParam)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(winners)
		},
	)
}
