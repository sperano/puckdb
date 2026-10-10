package mcpserver

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

func registerPlayoffTools(srv *server.MCPServer, queries *sqlcdb.Queries) {
	srv.AddTools(playoffTools(queries)...)
}

func playoffTools(q *sqlcdb.Queries) []server.ServerTool {
	return []server.ServerTool{
		listGamesTool(q),
		playoffGamesTool(q),
		playoffSeriesTool(q),
		stanleyCupFinalsTool(q),
		stanleyCupWinnersTool(q),
	}
}

func listGamesTool(q *sqlcdb.Queries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("list_games",
			mcp.WithDescription("List games with optional filters. More flexible than get_games_by_season."),
			mcp.WithNumber("season", mcp.Description("Season ID (e.g. 20242025)")),
			mcp.WithString("game_type", mcp.Description("Filter by type: preseason, regular_season, playoffs, all_star")),
			mcp.WithNumber("team_id", mcp.Description("Filter by team ID")),
			mcp.WithString("start_date", mcp.Description("Start date (YYYY-MM-DD)")),
			mcp.WithString("end_date", mcp.Description("End date (YYYY-MM-DD)")),
			mcp.WithNumber("limit", mcp.Description("Max results (default 100)")),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			params, errResult := listGamesParams(req)
			if errResult != nil {
				return errResult, nil
			}
			return toolResult(q.ListGames(ctx, params))
		},
	}
}

// listGamesParams reads list_games' optional filters; 0 or an empty string
// is no filter.
func listGamesParams(req mcp.CallToolRequest) (sqlcdb.ListGamesParams, *mcp.CallToolResult) {
	var params sqlcdb.ListGamesParams

	season, errResult := optionalInt4Filter(req, seasonParam)
	if errResult != nil {
		return params, errResult
	}
	params.Season = season

	if gameType := req.GetString("game_type", ""); gameType != "" {
		params.GameType = sqlcdb.NullGameType{GameType: sqlcdb.GameType(gameType), Valid: true}
	}

	teamID, errResult := optionalInt8Filter(req, teamIDParam)
	if errResult != nil {
		return params, errResult
	}
	params.TeamID = teamID

	startDate, errResult := optionalDate(req, "start_date")
	if errResult != nil {
		return params, errResult
	}
	params.StartDate = startDate

	endDate, errResult := optionalDate(req, "end_date")
	if errResult != nil {
		return params, errResult
	}
	params.EndDate = endDate

	limit, errResult := optionalInt4Filter(req, limitParam)
	if errResult != nil {
		return params, errResult
	}
	params.Limit = limit

	return params, nil
}

func playoffGamesTool(q *sqlcdb.Queries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_playoff_games",
			mcp.WithDescription("Get playoff games for a season, optionally filtered by round (1=First Round, 2=Second Round, 3=Conference Finals, 4=Stanley Cup Finals) and/or team."),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20242025)")),
			mcp.WithNumber("round", mcp.Description("Playoff round: 1, 2, 3, or 4")),
			mcp.WithNumber("team_id", mcp.Description("Filter by team ID")),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			season, errResult := requireInt[int32](req, seasonParam)
			if errResult != nil {
				return errResult, nil
			}
			round, errResult := optionalInt4Filter(req, roundParam)
			if errResult != nil {
				return errResult, nil
			}
			teamID, errResult := optionalInt8Filter(req, teamIDParam)
			if errResult != nil {
				return errResult, nil
			}
			return toolResult(q.GetPlayoffGames(ctx, sqlcdb.GetPlayoffGamesParams{
				Season: season,
				Round:  round,
				TeamID: teamID,
			}))
		},
	}
}

func playoffSeriesTool(q *sqlcdb.Queries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_playoff_series",
			mcp.WithDescription("Get playoff series summaries for a season showing matchups and win counts. Optionally filter by round."),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20242025)")),
			mcp.WithNumber("round", mcp.Description("Playoff round: 1, 2, 3, or 4")),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			season, errResult := requireInt[int32](req, seasonParam)
			if errResult != nil {
				return errResult, nil
			}
			round, errResult := optionalInt4Filter(req, roundParam)
			if errResult != nil {
				return errResult, nil
			}
			return toolResult(q.GetPlayoffSeries(ctx, sqlcdb.GetPlayoffSeriesParams{
				Season: season,
				Round:  round,
			}))
		},
	}
}

func stanleyCupFinalsTool(q *sqlcdb.Queries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_stanley_cup_finals",
			mcp.WithDescription("Get all Stanley Cup Finals games for a season (playoff round 4)."),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20242025)")),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			season, errResult := requireInt[int32](req, seasonParam)
			if errResult != nil {
				return errResult, nil
			}
			return toolResult(q.GetStanleyCupFinals(ctx, season))
		},
	}
}

func stanleyCupWinnersTool(q *sqlcdb.Queries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_stanley_cup_winners",
			mcp.WithDescription("Get Stanley Cup champions for recent seasons. Returns champion, runner-up, and clinching game details."),
			mcp.WithNumber("limit", mcp.Description("Number of seasons to return (default 10)")),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			limit, errResult := optionalInt4Filter(req, limitParam)
			if errResult != nil {
				return errResult, nil
			}
			return toolResult(q.GetStanleyCupWinners(ctx, limit))
		},
	}
}
