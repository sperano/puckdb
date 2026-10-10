package mcpserver

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// edgeSeasonParam is the Edge tools' season argument. Its description below
// says "season start year" (e.g. 2024 for 2024-2025), unlike seasonParam's
// season ID (e.g. 20242025); whether that is the form clients actually need
// is an open question for the owner, so this is only bounded to a positive
// int4 rather than reusing seasonParam's season-ID range.
var edgeSeasonParam = intArg{name: "season", min: minimumPositiveInteger, max: math.MaxInt32}

func registerEdgeTools(srv *server.MCPServer, queries *sqlcdb.Queries) {
	srv.AddTools(edgeTools(queries)...)
}

func edgeTools(q *sqlcdb.Queries) []server.ServerTool {
	return []server.ServerTool{
		edgeSkaterStatsTool(q),
		edgeGoalieStatsTool(q),
		edgeTeamStatsTool(q),
		edgeLeadersTool(q),
	}
}

func edgeSkaterStatsTool(q *sqlcdb.Queries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_edge_skater_stats",
			mcp.WithDescription("Get NHL Edge tracking stats for a skater: top skating speed, shot speed, distance skated, zone time (OZ/NZ/DZ), shot locations, and SOG summary with league percentiles and averages. Available from 2021-2022 season onward."),
			mcp.WithNumber("player_id", mcp.Required(), mcp.Description("Player ID")),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season start year (e.g. 2024 for 2024-2025)")),
			mcp.WithString("game_type", mcp.Description("Game type: regular_season (default), playoffs")),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			playerID, errResult := requireInt[int64](req, playerIDParam)
			if errResult != nil {
				return errResult, nil
			}
			season, errResult := requireInt[int32](req, edgeSeasonParam)
			if errResult != nil {
				return errResult, nil
			}
			params := sqlcdb.GetEdgeSkaterStatsParams{
				PlayerID: playerID,
				Season:   season,
				GameType: resolveEdgeGameType(req.GetString("game_type", "")),
			}
			return edgeSkaterStatsResult(ctx, q, params)
		},
	}
}

// edgeSkaterStatsResult runs the edge skater sub-queries and assembles the
// JSON response.
func edgeSkaterStatsResult(ctx context.Context, q *sqlcdb.Queries, params sqlcdb.GetEdgeSkaterStatsParams) (*mcp.CallToolResult, error) {
	stats, err := q.GetEdgeSkaterStats(ctx, params)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	shotLocs, err := q.GetEdgeSkaterShotLocations(ctx, sqlcdb.GetEdgeSkaterShotLocationsParams(params))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	sogSummary, err := q.GetEdgeSkaterSogSummary(ctx, sqlcdb.GetEdgeSkaterSogSummaryParams(params))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return ResultJSON(edgeSkaterResponse{
		Stats:         stats,
		ShotLocations: shotLocs,
		SogSummary:    sogSummary,
	})
}

func edgeGoalieStatsTool(q *sqlcdb.Queries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_edge_goalie_stats",
			mcp.WithDescription("Get NHL Edge tracking stats for a goalie: GAA, games above .900, goal differential per 60, goal support avg, point percentage, shot location saves and save percentages with league percentiles and averages. Available from 2021-2022 season onward."),
			mcp.WithNumber("player_id", mcp.Required(), mcp.Description("Player ID")),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season start year (e.g. 2024 for 2024-2025)")),
			mcp.WithString("game_type", mcp.Description("Game type: regular_season (default), playoffs")),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			playerID, errResult := requireInt[int64](req, playerIDParam)
			if errResult != nil {
				return errResult, nil
			}
			season, errResult := requireInt[int32](req, edgeSeasonParam)
			if errResult != nil {
				return errResult, nil
			}
			params := sqlcdb.GetEdgeGoalieStatsParams{
				PlayerID: playerID,
				Season:   season,
				GameType: resolveEdgeGameType(req.GetString("game_type", "")),
			}
			return edgeGoalieStatsResult(ctx, q, params)
		},
	}
}

// edgeGoalieStatsResult runs the edge goalie sub-queries and assembles the
// JSON response.
func edgeGoalieStatsResult(ctx context.Context, q *sqlcdb.Queries, params sqlcdb.GetEdgeGoalieStatsParams) (*mcp.CallToolResult, error) {
	stats, err := q.GetEdgeGoalieStats(ctx, params)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	locSummary, err := q.GetEdgeGoalieShotLocationSummary(ctx, sqlcdb.GetEdgeGoalieShotLocationSummaryParams(params))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	shotLocs, err := q.GetEdgeGoalieShotLocations(ctx, sqlcdb.GetEdgeGoalieShotLocationsParams(params))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return ResultJSON(edgeGoalieResponse{
		Stats:               stats,
		ShotLocationSummary: locSummary,
		ShotLocations:       shotLocs,
	})
}

func edgeTeamStatsTool(q *sqlcdb.Queries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_edge_team_stats",
			mcp.WithDescription("Get NHL Edge tracking stats for a team: top shot speed, shot attempts over 90mph, skating speed, bursts, distance, zone time (OZ/NZ/DZ) with league ranks, SOG summary, shot locations, zone time by strength (ES/PP/PK), and shot differential. Available from 2021-2022 season onward."),
			mcp.WithNumber("team_id", mcp.Required(), mcp.Description("Team ID")),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season start year (e.g. 2024 for 2024-2025)")),
			mcp.WithString("game_type", mcp.Description("Game type: regular_season (default), playoffs")),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			teamID, errResult := requireInt[int64](req, teamIDParam)
			if errResult != nil {
				return errResult, nil
			}
			season, errResult := requireInt[int32](req, edgeSeasonParam)
			if errResult != nil {
				return errResult, nil
			}
			params := sqlcdb.GetEdgeTeamStatsParams{
				TeamID:   teamID,
				Season:   season,
				GameType: resolveEdgeGameType(req.GetString("game_type", "")),
			}
			return edgeTeamStatsResult(ctx, q, params)
		},
	}
}

// edgeTeamStatsResult runs the edge team sub-queries and assembles the JSON
// response.
func edgeTeamStatsResult(ctx context.Context, q *sqlcdb.Queries, params sqlcdb.GetEdgeTeamStatsParams) (*mcp.CallToolResult, error) {
	stats, err := q.GetEdgeTeamStats(ctx, params)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	sogSummary, err := q.GetEdgeTeamSogSummary(ctx, sqlcdb.GetEdgeTeamSogSummaryParams(params))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	shotLocs, err := q.GetEdgeTeamShotLocations(ctx, sqlcdb.GetEdgeTeamShotLocationsParams(params))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	zoneTime, err := q.GetEdgeTeamZoneTimeByStrength(ctx, sqlcdb.GetEdgeTeamZoneTimeByStrengthParams(params))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	shotDiff, err := q.GetEdgeTeamShotDifferential(ctx, sqlcdb.GetEdgeTeamShotDifferentialParams(params))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return mcp.NewToolResultError(err.Error()), nil
	}
	var shotDiffPtr *sqlcdb.EdgeTeamShotDifferential
	if err == nil {
		shotDiffPtr = &shotDiff
	}
	return ResultJSON(edgeTeamResponse{
		Stats:              stats,
		SogSummary:         sogSummary,
		ShotLocations:      shotLocs,
		ZoneTimeByStrength: zoneTime,
		ShotDifferential:   shotDiffPtr,
	})
}

// resolveEdgeGameType converts a game_type string parameter to a sqlcdb.GameType.
// Defaults to regular_season if empty or unrecognized.
func resolveEdgeGameType(gt string) sqlcdb.GameType {
	switch gt {
	case "playoffs":
		return sqlcdb.GameTypePlayoffs
	case "preseason":
		return sqlcdb.GameTypePreseason
	default:
		return sqlcdb.GameTypeRegularSeason
	}
}

// Response types bundle main stats with sub-table data for JSON serialization.

type edgeSkaterResponse struct {
	Stats         sqlcdb.EdgeSkaterStat           `json:"stats"`
	ShotLocations []sqlcdb.EdgeSkaterShotLocation `json:"shotLocations"`
	SogSummary    []sqlcdb.EdgeSkaterSogSummary   `json:"sogSummary"`
}

type edgeGoalieResponse struct {
	Stats               sqlcdb.EdgeGoalieStat                  `json:"stats"`
	ShotLocationSummary []sqlcdb.EdgeGoalieShotLocationSummary `json:"shotLocationSummary"`
	ShotLocations       []sqlcdb.EdgeGoalieShotLocation        `json:"shotLocations"`
}

type edgeTeamResponse struct {
	Stats              sqlcdb.EdgeTeamStat                 `json:"stats"`
	SogSummary         []sqlcdb.EdgeTeamSogSummary         `json:"sogSummary"`
	ShotLocations      []sqlcdb.EdgeTeamShotLocation       `json:"shotLocations"`
	ZoneTimeByStrength []sqlcdb.EdgeTeamZoneTimeByStrength `json:"zoneTimeByStrength"`
	ShotDifferential   *sqlcdb.EdgeTeamShotDifferential    `json:"shotDifferential,omitempty"`
}
