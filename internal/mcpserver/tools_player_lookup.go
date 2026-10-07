package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

const (
	// playerIDArg is the NHL player ID argument.
	playerIDArg = "player_id"
	// yahooIDArg is the Yahoo player ID argument of get_player and
	// search_player, the bridge from the Yahoo tools to the NHL ones.
	yahooIDArg = "yahoo_id"
	// playerNameArg is the name argument of search_player.
	playerNameArg = "name"
)

// playerQueries is what get_player and search_player read;
// *sqlcdb.Queries satisfies it.
type playerQueries interface {
	GetPlayer(ctx context.Context, id int64) (sqlcdb.Player, error)
	GetPlayerByYahooID(ctx context.Context, yahooID pgtype.Int8) (sqlcdb.Player, error)
	SearchPlayersByName(ctx context.Context, lower string) ([]sqlcdb.Player, error)
	SearchPlayersByFullName(ctx context.Context, arg sqlcdb.SearchPlayersByFullNameParams) ([]sqlcdb.Player, error)
}

func getPlayerTool(q playerQueries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_player",
			mcp.WithDescription("Get detailed information about a single NHL player, by NHL ID or by Yahoo player ID. Returns bio, position, team, draft info, both IDs, etc. Pass exactly one of player_id and yahoo_id."),
			mcp.WithNumber(playerIDArg, mcp.Description("NHL player ID (use search_player to find IDs)")),
			mcp.WithNumber(yahooIDArg, mcp.Description("Yahoo player ID, as returned by the Yahoo tools (yahoo_player_id, player_id). Finds the NHL player matched to it")),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			playerID, hasPlayerID, errResult := optionalID(req, playerIDArg)
			if errResult != nil {
				return errResult, nil
			}
			yahooID, hasYahooID, errResult := optionalID(req, yahooIDArg)
			if errResult != nil {
				return errResult, nil
			}
			if errResult := exactlyOne(playerIDArg, hasPlayerID, yahooIDArg, hasYahooID); errResult != nil {
				return errResult, nil
			}
			if hasYahooID {
				player, errResult := playerByYahooID(ctx, q, yahooID)
				if errResult != nil {
					return errResult, nil
				}
				return ResultJSON(player)
			}
			player, err := q.GetPlayer(ctx, playerID)
			if errors.Is(err, pgx.ErrNoRows) {
				return mcp.NewToolResultError(fmt.Sprintf("no player with %s %d", playerIDArg, playerID)), nil
			}
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultJSON(player)
		},
	}
}

func searchPlayerTool(q playerQueries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("search_player",
			mcp.WithDescription("Search for NHL players by name (accent-insensitive), or find the NHL player matched to a Yahoo player ID. Supports single terms ('Suzuki') or full names ('Nick Suzuki'). Results ranked by relevance: exact matches first, then prefix, then substring. Pass exactly one of name and yahoo_id."),
			mcp.WithString(playerNameArg, mcp.Description("Player name or partial name to search for")),
			mcp.WithNumber(yahooIDArg, mcp.Description("Yahoo player ID, as returned by the Yahoo tools (yahoo_player_id, player_id). Returns the one NHL player matched to it")),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			name := strings.TrimSpace(req.GetString(playerNameArg, ""))
			yahooID, hasYahooID, errResult := optionalID(req, yahooIDArg)
			if errResult != nil {
				return errResult, nil
			}
			if errResult := exactlyOne(playerNameArg, name != "", yahooIDArg, hasYahooID); errResult != nil {
				return errResult, nil
			}
			if hasYahooID {
				player, errResult := playerByYahooID(ctx, q, yahooID)
				if errResult != nil {
					return errResult, nil
				}
				return ResultCSV([]sqlcdb.Player{player})
			}
			players, err := searchPlayersByName(ctx, q, name)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(players)
		},
	}
}

// searchPlayersByName searches by full name when name has two or more
// words, by any name part otherwise.
func searchPlayersByName(ctx context.Context, q playerQueries, name string) ([]sqlcdb.Player, error) {
	parts := strings.Fields(name)
	if len(parts) >= 2 {
		return q.SearchPlayersByFullName(ctx, sqlcdb.SearchPlayersByFullNameParams{
			Lower:   parts[0],
			Lower_2: strings.Join(parts[1:], " "),
		})
	}
	return q.SearchPlayersByName(ctx, name)
}

// playerByYahooID finds the NHL player matched to a Yahoo player ID. A
// Yahoo ID no player is matched to gets a "not found" result that points
// to the name search, not the bare database error.
func playerByYahooID(ctx context.Context, q playerQueries, yahooID int64) (sqlcdb.Player, *mcp.CallToolResult) {
	player, err := q.GetPlayerByYahooID(ctx, pgtype.Int8{Int64: yahooID, Valid: true})
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlcdb.Player{}, mcp.NewToolResultError(fmt.Sprintf(
			"no NHL player is matched to %s %d; the player may not be matched yet, try search_player by name",
			yahooIDArg, yahooID))
	}
	if err != nil {
		return sqlcdb.Player{}, mcp.NewToolResultError(err.Error())
	}
	return player, nil
}

// optionalID reads an optional positive integer ID argument. An absent or
// zero argument is not given (clients often send 0 for "none"); a negative
// or non-numeric one is an error result.
func optionalID(req mcp.CallToolRequest, name string) (id int64, given bool, errResult *mcp.CallToolResult) {
	raw, ok := req.GetArguments()[name]
	if !ok {
		return 0, false, nil
	}
	n, err := req.RequireInt(name)
	if err != nil || n < 0 {
		return 0, false, mcp.NewToolResultError(fmt.Sprintf("invalid %s %v: want a positive integer", name, raw))
	}
	return int64(n), n != 0, nil
}

// exactlyOne requires exactly one of two alternative arguments. Both
// together are refused rather than letting one win, so a mismatched pair
// cannot silently answer for the wrong player.
func exactlyOne(first string, hasFirst bool, second string, hasSecond bool) *mcp.CallToolResult {
	switch {
	case hasFirst && hasSecond:
		return mcp.NewToolResultError(fmt.Sprintf("pass either %s or %s, not both", first, second))
	case !hasFirst && !hasSecond:
		return mcp.NewToolResultError(fmt.Sprintf("%s or %s is required", first, second))
	}
	return nil
}
