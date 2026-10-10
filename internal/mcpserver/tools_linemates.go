package mcpserver

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

const (
	// defaultLinemateLimit covers a full club's regular skaters.
	defaultLinemateLimit = 20
	// maxLinemateLimit covers every skater of two clubs in one season, for
	// a traded player.
	maxLinemateLimit    = 100
	linemateGameTypeArg = "game_type"
	linemateStartArg    = "start_date"
	linemateEndArg      = "end_date"
)

// linemateLimitParam bounds get_player_linemates' limit.
var linemateLimitParam = intArg{name: "limit", min: minimumPositiveInteger, max: maxLinemateLimit}

// linemateGameTypes are the game types with shift charts; absent means
// regular season and playoffs together.
var linemateGameTypes = []sqlcdb.GameType{
	sqlcdb.GameTypeRegularSeason,
	sqlcdb.GameTypePlayoffs,
	sqlcdb.GameTypePreseason,
}

// registerLinemateTools adds get_player_linemates.
func registerLinemateTools(srv *server.MCPServer, queries linemateQueries) {
	srv.AddTools(linemateTool(queries))
}

type linemateQueries interface {
	ListPlayerEvenStrengthLinemates(context.Context, sqlcdb.ListPlayerEvenStrengthLinematesParams) ([]sqlcdb.ListPlayerEvenStrengthLinematesRow, error)
}

func linemateTool(q linemateQueries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_player_linemates",
			mcp.WithDescription("Who a skater plays with: teammates ranked by shared even-strength (5v5, 4v4, 3v3) time on ice, "+
				"with games together and share_pct (shared time as a % of the player's own even-strength time with that club). "+
				"Pass a season, a date range, or both. Rows are per club, so a traded player lists each club's linemates. "+
				"Only games whose shift charts were imported count; no rows usually means none were."),
			mcp.WithNumber(playerIDArg, mcp.Required(), mcp.Description("Player ID (use search_player to resolve a name)")),
			mcp.WithNumber("season", mcp.Description("Season ID (e.g. 20252026)")),
			mcp.WithString(linemateStartArg, mcp.Description("First game date (YYYY-MM-DD)")),
			mcp.WithString(linemateEndArg, mcp.Description("Last game date (YYYY-MM-DD)")),
			mcp.WithString(linemateGameTypeArg, mcp.Description("regular_season, playoffs or preseason (default: regular season and playoffs)")),
			mcp.WithNumber("limit", mcp.Description(fmt.Sprintf("Max teammates (default %d, max %d)", defaultLinemateLimit, maxLinemateLimit))),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			params, errResult := linemateParams(req)
			if errResult != nil {
				return errResult, nil
			}
			return toolResult(q.ListPlayerEvenStrengthLinemates(ctx, params))
		},
	}
}

// linemateParams reads get_player_linemates' arguments; a season or a date
// bound is required so an answer is never silently career-wide.
func linemateParams(req mcp.CallToolRequest) (sqlcdb.ListPlayerEvenStrengthLinematesParams, *mcp.CallToolResult) {
	var params sqlcdb.ListPlayerEvenStrengthLinematesParams
	var errResult *mcp.CallToolResult
	if params.PlayerID, errResult = requireInt[int64](req, playerIDParam); errResult != nil {
		return params, errResult
	}
	season, hasSeason, errResult := optionalInt[int32](req, seasonParam)
	if errResult != nil {
		return params, errResult
	}
	params.Season.Int32, params.Season.Valid = season, hasSeason
	if params.StartDate, errResult = optionalDate(req, linemateStartArg); errResult != nil {
		return params, errResult
	}
	if params.EndDate, errResult = optionalDate(req, linemateEndArg); errResult != nil {
		return params, errResult
	}
	if !hasSeason && !params.StartDate.Valid && !params.EndDate.Valid {
		return params, mcp.NewToolResultError("pass a season, a start_date/end_date range, or both")
	}
	if params.StartDate.Valid && params.EndDate.Valid && params.StartDate.Time.After(params.EndDate.Time) {
		return params, mcp.NewToolResultError("start_date is after end_date")
	}
	if params.GameType, errResult = linemateGameType(req); errResult != nil {
		return params, errResult
	}
	params.ResultLimit, errResult = boundedLimitOrDefault(req, linemateLimitParam, defaultLinemateLimit)
	return params, errResult
}

// linemateGameType reads the optional game type; absent or empty is NULL,
// which the query reads as regular season and playoffs.
func linemateGameType(req mcp.CallToolRequest) (sqlcdb.NullGameType, *mcp.CallToolResult) {
	raw := req.GetString(linemateGameTypeArg, "")
	name := strings.ToLower(strings.TrimSpace(raw))
	if name == "" {
		return sqlcdb.NullGameType{}, nil
	}
	gameType := sqlcdb.GameType(name)
	if !slices.Contains(linemateGameTypes, gameType) {
		accepted := make([]string, len(linemateGameTypes))
		for i, t := range linemateGameTypes {
			accepted[i] = string(t)
		}
		return sqlcdb.NullGameType{}, mcp.NewToolResultError(fmt.Sprintf("unknown game_type %s; accepted: %s",
			formatRaw(raw), strings.Join(accepted, ", ")))
	}
	return sqlcdb.NullGameType{GameType: gameType, Valid: true}, nil
}
