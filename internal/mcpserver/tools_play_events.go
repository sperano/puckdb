package mcpserver

import (
	"context"
	"math"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// playEventTypes enumerates the valid type_desc_key values in the play_events table.
// Kept in sync with the play_event_type Postgres enum and sqlcdb.PlayEventType* constants.
var playEventTypes = []string{
	"faceoff",
	"hit",
	"giveaway",
	"goal",
	"shot-on-goal",
	"missed-shot",
	"blocked-shot",
	"penalty",
	"stoppage",
	"period-start",
	"period-end",
	"shootout-complete",
	"game-end",
	"takeaway",
	"delayed-penalty",
	"failed-shot-attempt",
}

// playPeriodParam is the optional period filter on get_game_play_events.
var playPeriodParam = intArg{name: "period", min: minimumPositiveInteger, max: math.MaxInt32}

// gameIDsParam is the game_ids array argument of
// get_first_matching_event_per_team; same bounds as gameIDParam, but named
// for the plural argument so error messages reference "game_ids".
var gameIDsParam = intArg{name: "game_ids", min: gameIDParam.min, max: gameIDParam.max}

func registerPlayEventTools(srv *server.MCPServer, queries *sqlcdb.Queries) {
	srv.AddTools(playEventTools(queries)...)
}

func playEventTools(q *sqlcdb.Queries) []server.ServerTool {
	return []server.ServerTool{
		gamePlayEventsTool(q),
		firstMatchingEventPerTeamTool(q),
	}
}

func gamePlayEventsTool(q *sqlcdb.Queries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_game_play_events",
			mcp.WithDescription(
				"Get play-by-play events for a single game, ordered chronologically by sort_order. "+
					"Returns the full play_events row (~41 columns) so callers can reason about shots, goals, "+
					"hits, faceoffs, penalties, score state, on-ice coordinates, etc. "+
					"Optional filters: type_desc_keys narrows event types (e.g. ['shot-on-goal','goal'] for shots), "+
					"period restricts to one period, limit caps the row count. "+
					"Combine type_desc_keys with limit=N to get a team-agnostic 'first N events of type X' (e.g. "+
					"first 2 shots of the game). Use event_owner_team_id on each row to attribute events to teams."),
			mcp.WithNumber("game_id", mcp.Required(), mcp.Description("Game ID")),
			mcp.WithArray("type_desc_keys",
				mcp.Description("Optional filter: only return events whose type_desc_key is in this list. Omit or pass empty for all events."),
				mcp.WithStringEnumItems(playEventTypes),
			),
			mcp.WithNumber("period", mcp.Description("Optional filter: only return events from this period (1, 2, 3, 4+ for OT, 5 for shootout).")),
			mcp.WithNumber("limit", mcp.Description("Optional cap on the number of rows returned (after filters, in sort_order). Omit for no cap.")),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			gameID, errResult := requireInt[int64](req, gameIDParam)
			if errResult != nil {
				return errResult, nil
			}
			period, errResult := optionalInt4Filter(req, playPeriodParam)
			if errResult != nil {
				return errResult, nil
			}
			limit, errResult := optionalInt4Filter(req, limitParam)
			if errResult != nil {
				return errResult, nil
			}
			return toolResult(q.GetGamePlayEvents(ctx, sqlcdb.GetGamePlayEventsParams{
				GameID:       gameID,
				Period:       period,
				TypeDescKeys: req.GetStringSlice("type_desc_keys", nil),
				Limit:        limit,
			}))
		},
	}
}

func firstMatchingEventPerTeamTool(q *sqlcdb.Queries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_first_matching_event_per_team",
			mcp.WithDescription(
				"For each (game, team) pair in the supplied list of games, return the earliest play "+
					"event (by sort_order) whose type_desc_key matches the type_desc_keys filter. "+
					"IMPORTANT: type_desc_keys is a PREREQUISITE filter applied before the per-team "+
					"earliest selection — it is not a post-filter. So type_desc_keys=['shot-on-goal','goal'] "+
					"returns each team's first SHOT (not their first event-of-any-kind that happens to be "+
					"a shot). Required and non-empty. Returns one row per team per game (up to 2 rows per game). "+
					"Use this to answer cross-game questions like 'how often does a team score on its "+
					"first shot?' (type_desc_keys=['shot-on-goal','goal'] → check whether row's type_desc_key=='goal'), "+
					"'who lands the first hit?' (['hit']), 'first faceoff winner per team?' (['faceoff']), etc. "+
					"Use list_games or get_games_by_season to resolve game_ids for a scope."),
			mcp.WithArray("game_ids", mcp.Required(),
				mcp.Description("List of game IDs to scan."),
				mcp.WithNumberItems(),
			),
			mcp.WithArray("type_desc_keys", mcp.Required(),
				mcp.Description("Prerequisite event-type filter applied before the per-team earliest selection. Must be non-empty. E.g. ['shot-on-goal','goal'] for 'first shot' (shots on goal include goals)."),
				mcp.WithStringEnumItems(playEventTypes),
			),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			gameIDs, errResult := requireIntSlice[int64](req, gameIDsParam)
			if errResult != nil {
				return errResult, nil
			}
			if len(gameIDs) == 0 {
				return mcp.NewToolResultError("game_ids must be non-empty"), nil
			}
			types, err := req.RequireStringSlice("type_desc_keys")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if len(types) == 0 {
				return mcp.NewToolResultError("type_desc_keys must be non-empty"), nil
			}
			return toolResult(q.GetFirstMatchingEventPerTeam(ctx, sqlcdb.GetFirstMatchingEventPerTeamParams{
				GameIds:      gameIDs,
				TypeDescKeys: types,
			}))
		},
	}
}
