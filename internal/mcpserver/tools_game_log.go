package mcpserver

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

const (
	startDateArg = "start_date"
	endDateArg   = "end_date"

	// gameLogScopeHelp explains the season and date arguments of the game
	// log tools.
	gameLogScopeHelp = " Pass season, a date range (start_date and/or end_date, inclusive; " +
		"a missing bound is open), or both to keep only the season's games in the range. " +
		"Rows read by date range also carry the season."
)

// gameLogScope is what a game log call selects: a season, a date range,
// or both.
type gameLogScope struct {
	season      int32
	seasonGiven bool
	// start and end are always valid; a bound the call left out is
	// -infinity or infinity.
	start, end pgtype.Date
	rangeGiven bool
}

// readGameLogScope reads season, start_date and end_date; at least one
// must be given, and end_date may not precede start_date.
func readGameLogScope(req mcp.CallToolRequest) (gameLogScope, *mcp.CallToolResult) {
	var scope gameLogScope
	var errResult *mcp.CallToolResult
	scope.season, scope.seasonGiven, errResult = optionalInt[int32](req, seasonParam)
	if errResult != nil {
		return scope, errResult
	}
	start, errResult := optionalDate(req, startDateArg)
	if errResult != nil {
		return scope, errResult
	}
	end, errResult := optionalDate(req, endDateArg)
	if errResult != nil {
		return scope, errResult
	}
	if start.Valid && end.Valid && end.Time.Before(start.Time) {
		return scope, mcp.NewToolResultError(fmt.Sprintf("%s %s is before %s %s",
			endDateArg, end.Time.Format(time.DateOnly), startDateArg, start.Time.Format(time.DateOnly)))
	}
	scope.rangeGiven = start.Valid || end.Valid
	if !scope.seasonGiven && !scope.rangeGiven {
		return scope, mcp.NewToolResultError(fmt.Sprintf("%s is required unless %s or %s is given",
			seasonParam.name, startDateArg, endDateArg))
	}
	scope.start = openBound(start, pgtype.NegativeInfinity)
	scope.end = openBound(end, pgtype.Infinity)
	return scope, nil
}

// openBound returns d, or the open bound when d was not given.
func openBound(d pgtype.Date, open pgtype.InfinityModifier) pgtype.Date {
	if d.Valid {
		return d
	}
	return pgtype.Date{InfinityModifier: open, Valid: true}
}

// keepSeason drops the rows outside scope's season, when one is given.
func keepSeason[T any](scope gameLogScope, rows []T, err error, seasonOf func(T) int32) ([]T, error) {
	if err != nil || !scope.seasonGiven {
		return rows, err
	}
	return slices.DeleteFunc(rows, func(row T) bool { return seasonOf(row) != scope.season }), nil
}

// gameLogArgs are the arguments shared by the game log tools.
func gameLogArgs(description string) []mcp.ToolOption {
	return []mcp.ToolOption{
		mcp.WithDescription(description + gameLogScopeHelp),
		mcp.WithNumber(playerIDArg, mcp.Required(), mcp.Description("Player ID")),
		mcp.WithNumber(seasonParam.name, mcp.Description("Season ID (e.g. 20252026); required unless start_date or end_date is given")),
		mcp.WithString(startDateArg, mcp.Description("First game date, inclusive (YYYY-MM-DD)")),
		mcp.WithString(endDateArg, mcp.Description("Last game date, inclusive (YYYY-MM-DD)")),
	}
}

func skaterGameLogTool(q *sqlcdb.Queries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_skater_game_log",
			gameLogArgs("Get a skater's game-by-game stats log (one row per game played).")...),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			playerID, errResult := requireInt[int64](req, playerIDParam)
			if errResult != nil {
				return errResult, nil
			}
			scope, errResult := readGameLogScope(req)
			if errResult != nil {
				return errResult, nil
			}
			if !scope.rangeGiven {
				return toolResult(q.GetSkaterStatsByPlayerAndSeason(ctx, sqlcdb.GetSkaterStatsByPlayerAndSeasonParams{
					PlayerID: playerID,
					Season:   scope.season,
				}))
			}
			rows, err := q.GetSkaterStatsByPlayerAndDateRange(ctx, sqlcdb.GetSkaterStatsByPlayerAndDateRangeParams{
				PlayerID:   playerID,
				GameDate:   scope.start,
				GameDate_2: scope.end,
			})
			return toolResult(keepSeason(scope, rows, err, func(row sqlcdb.GetSkaterStatsByPlayerAndDateRangeRow) int32 {
				return row.Season
			}))
		},
	}
}

func goalieGameLogTool(q *sqlcdb.Queries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_goalie_game_log",
			gameLogArgs("Get a goalie's game-by-game stats log (one row per game played).")...),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			playerID, errResult := requireInt[int64](req, playerIDParam)
			if errResult != nil {
				return errResult, nil
			}
			scope, errResult := readGameLogScope(req)
			if errResult != nil {
				return errResult, nil
			}
			if !scope.rangeGiven {
				return toolResult(q.GetGoalieStatsByPlayerAndSeason(ctx, sqlcdb.GetGoalieStatsByPlayerAndSeasonParams{
					PlayerID: playerID,
					Season:   scope.season,
				}))
			}
			rows, err := q.GetGoalieStatsByPlayerAndDateRange(ctx, sqlcdb.GetGoalieStatsByPlayerAndDateRangeParams{
				PlayerID:   playerID,
				GameDate:   scope.start,
				GameDate_2: scope.end,
			})
			return toolResult(keepSeason(scope, rows, err, func(row sqlcdb.GetGoalieStatsByPlayerAndDateRangeRow) int32 {
				return row.Season
			}))
		},
	}
}
