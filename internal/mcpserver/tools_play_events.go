package mcpserver

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
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

func registerPlayEventTools(srv *server.MCPServer, queries *sqlcdb.Queries) {
	srv.AddTool(
		mcp.NewTool("get_game_play_events",
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
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			gameID := req.GetInt("game_id", 0)
			if gameID == 0 {
				return mcp.NewToolResultError("game_id is required"), nil
			}
			types := req.GetStringSlice("type_desc_keys", nil)
			var period pgtype.Int4
			if p := req.GetInt("period", 0); p != 0 {
				period = pgtype.Int4{Int32: int32(p), Valid: true}
			}
			var limit pgtype.Int4
			if l := req.GetInt("limit", 0); l > 0 {
				limit = pgtype.Int4{Int32: int32(l), Valid: true}
			}
			events, err := queries.GetGamePlayEvents(ctx, sqlcdb.GetGamePlayEventsParams{
				GameID:       int64(gameID),
				Period:       period,
				TypeDescKeys: types,
				Limit:        limit,
			})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(events)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_first_matching_event_per_team",
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
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			gameIDsInt, err := req.RequireIntSlice("game_ids")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if len(gameIDsInt) == 0 {
				return mcp.NewToolResultError("game_ids must be non-empty"), nil
			}
			types, err := req.RequireStringSlice("type_desc_keys")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if len(types) == 0 {
				return mcp.NewToolResultError("type_desc_keys must be non-empty"), nil
			}
			gameIDs := make([]int64, len(gameIDsInt))
			for i, id := range gameIDsInt {
				gameIDs[i] = int64(id)
			}
			events, err := queries.GetFirstMatchingEventPerTeam(ctx, sqlcdb.GetFirstMatchingEventPerTeamParams{
				GameIds:      gameIDs,
				TypeDescKeys: types,
			})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(events)
		},
	)
}
