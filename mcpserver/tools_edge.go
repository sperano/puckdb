package mcpserver

import (
	"context"
	"database/sql"
	"errors"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/sqlcdb"
)

func registerEdgeTools(srv *server.MCPServer, queries *sqlcdb.Queries) {
	srv.AddTool(
		mcp.NewTool("get_edge_skater_stats",
			mcp.WithDescription("Get NHL Edge tracking stats for a skater: top skating speed, shot speed, distance skated, zone time (OZ/NZ/DZ), shot locations, and SOG summary with league percentiles and averages. Available from 2021-2022 season onward."),
			mcp.WithNumber("player_id", mcp.Required(), mcp.Description("Player ID")),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season start year (e.g. 2024 for 2024-2025)")),
			mcp.WithString("game_type", mcp.Description("Game type: regular_season (default), playoffs")),
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
			gameType := resolveEdgeGameType(req.GetString("game_type", ""))
			params := sqlcdb.GetEdgeSkaterStatsParams{
				PlayerID: int64(playerID),
				Season:   int32(season),
				GameType: gameType,
			}
			stats, err := queries.GetEdgeSkaterStats(ctx, params)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			shotLocs, err := queries.GetEdgeSkaterShotLocations(ctx, sqlcdb.GetEdgeSkaterShotLocationsParams(params))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			sogSummary, err := queries.GetEdgeSkaterSogSummary(ctx, sqlcdb.GetEdgeSkaterSogSummaryParams(params))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultJSON(edgeSkaterResponse{
				Stats:         stats,
				ShotLocations: shotLocs,
				SogSummary:    sogSummary,
			})
		},
	)

	srv.AddTool(
		mcp.NewTool("get_edge_goalie_stats",
			mcp.WithDescription("Get NHL Edge tracking stats for a goalie: GAA, games above .900, goal differential per 60, goal support avg, point percentage, shot location saves and save percentages with league percentiles and averages. Available from 2021-2022 season onward."),
			mcp.WithNumber("player_id", mcp.Required(), mcp.Description("Player ID")),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season start year (e.g. 2024 for 2024-2025)")),
			mcp.WithString("game_type", mcp.Description("Game type: regular_season (default), playoffs")),
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
			gameType := resolveEdgeGameType(req.GetString("game_type", ""))
			params := sqlcdb.GetEdgeGoalieStatsParams{
				PlayerID: int64(playerID),
				Season:   int32(season),
				GameType: gameType,
			}
			stats, err := queries.GetEdgeGoalieStats(ctx, params)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			locSummary, err := queries.GetEdgeGoalieShotLocationSummary(ctx, sqlcdb.GetEdgeGoalieShotLocationSummaryParams(params))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			shotLocs, err := queries.GetEdgeGoalieShotLocations(ctx, sqlcdb.GetEdgeGoalieShotLocationsParams(params))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultJSON(edgeGoalieResponse{
				Stats:                stats,
				ShotLocationSummary: locSummary,
				ShotLocations:       shotLocs,
			})
		},
	)

	srv.AddTool(
		mcp.NewTool("get_edge_team_stats",
			mcp.WithDescription("Get NHL Edge tracking stats for a team: top shot speed, shot attempts over 90mph, skating speed, bursts, distance, zone time (OZ/NZ/DZ) with league ranks, SOG summary, shot locations, zone time by strength (ES/PP/PK), and shot differential. Available from 2021-2022 season onward."),
			mcp.WithNumber("team_id", mcp.Required(), mcp.Description("Team ID")),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season start year (e.g. 2024 for 2024-2025)")),
			mcp.WithString("game_type", mcp.Description("Game type: regular_season (default), playoffs")),
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
			gameType := resolveEdgeGameType(req.GetString("game_type", ""))
			params := sqlcdb.GetEdgeTeamStatsParams{
				TeamID:   int64(teamID),
				Season:   int32(season),
				GameType: gameType,
			}
			stats, err := queries.GetEdgeTeamStats(ctx, params)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			sogSummary, err := queries.GetEdgeTeamSogSummary(ctx, sqlcdb.GetEdgeTeamSogSummaryParams(params))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			shotLocs, err := queries.GetEdgeTeamShotLocations(ctx, sqlcdb.GetEdgeTeamShotLocationsParams(params))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			zoneTime, err := queries.GetEdgeTeamZoneTimeByStrength(ctx, sqlcdb.GetEdgeTeamZoneTimeByStrengthParams(params))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			shotDiff, err := queries.GetEdgeTeamShotDifferential(ctx, sqlcdb.GetEdgeTeamShotDifferentialParams(params))
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
		},
	)
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
	Stats         sqlcdb.EdgeSkaterStat          `json:"stats"`
	ShotLocations []sqlcdb.EdgeSkaterShotLocation `json:"shotLocations"`
	SogSummary    []sqlcdb.EdgeSkaterSogSummary   `json:"sogSummary"`
}

type edgeGoalieResponse struct {
	Stats                sqlcdb.EdgeGoalieStat                 `json:"stats"`
	ShotLocationSummary []sqlcdb.EdgeGoalieShotLocationSummary `json:"shotLocationSummary"`
	ShotLocations       []sqlcdb.EdgeGoalieShotLocation        `json:"shotLocations"`
}

type edgeTeamResponse struct {
	Stats              sqlcdb.EdgeTeamStat                 `json:"stats"`
	SogSummary         []sqlcdb.EdgeTeamSogSummary          `json:"sogSummary"`
	ShotLocations      []sqlcdb.EdgeTeamShotLocation        `json:"shotLocations"`
	ZoneTimeByStrength []sqlcdb.EdgeTeamZoneTimeByStrength  `json:"zoneTimeByStrength"`
	ShotDifferential   *sqlcdb.EdgeTeamShotDifferential     `json:"shotDifferential,omitempty"`
}

