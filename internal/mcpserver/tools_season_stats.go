package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

const (
	defaultSkaterSeasonSort = "points"
	defaultGoalieSeasonSort = "wins"
	seasonSortByArg         = "sort_by"
	seasonPositionArg       = "position"
)

var skaterSeasonSortKeys = []string{
	"points", "goals", "assists", "plus_minus", "pim", "sog", "ppp", "ppg", "hits", "blocks", "gp", "avg_toi_min",
}

var goalieSeasonSortKeys = []string{
	"wins", "losses", "gaa", "sv_pct", "saves", "shots_against", "gp",
}

var skaterSeasonPositions = map[string]sqlcdb.PlayerPosition{
	"C":  sqlcdb.PlayerPositionC,
	"LW": sqlcdb.PlayerPositionLW,
	"RW": sqlcdb.PlayerPositionRW,
	"F":  sqlcdb.PlayerPositionF,
	"D":  sqlcdb.PlayerPositionD,
}

type seasonStatsQueries interface {
	GetSkaterSeasonStatsBySort(context.Context, sqlcdb.GetSkaterSeasonStatsBySortParams) ([]sqlcdb.SkaterSeasonStat, error)
	GetGoalieSeasonStatsBySort(context.Context, sqlcdb.GetGoalieSeasonStatsBySortParams) ([]sqlcdb.GoalieSeasonStat, error)
}

// registerSeasonStatsTools adds the season stat views. They read NHL data
// only, so they belong to the nhl toolset even though they are meant for
// fantasy analysis.
func registerSeasonStatsTools(srv *server.MCPServer, queries seasonStatsQueries) {
	addSkaterSeasonStatsTool(srv, queries)
	addGoalieSeasonStatsTool(srv, queries)
}

func addSkaterSeasonStatsTool(srv *server.MCPServer, queries seasonStatsQueries) {
	srv.AddTool(
		mcp.NewTool("get_skater_season_stats",
			mcp.WithDescription("Get fantasy-relevant regular-season stats for skaters. Sort by a supported stat key and optionally filter by NHL position. Use limit to control result size."),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20252026)")),
			mcp.WithString(seasonSortByArg, mcp.Description("Sort stat: points (default), goals, assists, plus_minus, pim, sog, ppp, ppg, hits, blocks, gp, avg_toi_min")),
			mcp.WithString(seasonPositionArg, mcp.Description("Optional NHL position filter: C, LW, RW, F, or D")),
			mcp.WithNumber("limit", mcp.Description("Max number of skaters to return (default 100)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			season := req.GetInt("season", 0)
			if season == 0 {
				return mcp.NewToolResultError("season is required"), nil
			}
			sortBy, errResult := requireSeasonSort(req, defaultSkaterSeasonSort, skaterSeasonSortKeys)
			if errResult != nil {
				return errResult, nil
			}
			position, errResult := requireSkaterPosition(req)
			if errResult != nil {
				return errResult, nil
			}
			stats, err := queries.GetSkaterSeasonStatsBySort(ctx, sqlcdb.GetSkaterSeasonStatsBySortParams{
				Season:      int32(season),
				Position:    position,
				SortBy:      sortBy,
				ResultLimit: int32(req.GetInt("limit", defaultResultLimit)),
			})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(stats)
		},
	)
}

func addGoalieSeasonStatsTool(srv *server.MCPServer, queries seasonStatsQueries) {
	srv.AddTool(
		mcp.NewTool("get_goalie_season_stats",
			mcp.WithDescription("Get fantasy-relevant regular-season stats for goalies. Sort by a supported stat key and optionally specify goalie position G. Lower GAA ranks first. Use limit to control result size."),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20252026)")),
			mcp.WithString(seasonSortByArg, mcp.Description("Sort stat: wins (default), losses, gaa, sv_pct, saves, shots_against, gp")),
			mcp.WithString(seasonPositionArg, mcp.Description("Optional NHL position filter: G")),
			mcp.WithNumber("limit", mcp.Description("Max number of goalies to return (default 100)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			season := req.GetInt("season", 0)
			if season == 0 {
				return mcp.NewToolResultError("season is required"), nil
			}
			sortBy, errResult := requireSeasonSort(req, defaultGoalieSeasonSort, goalieSeasonSortKeys)
			if errResult != nil {
				return errResult, nil
			}
			if errResult := requireGoaliePosition(req); errResult != nil {
				return errResult, nil
			}
			stats, err := queries.GetGoalieSeasonStatsBySort(ctx, sqlcdb.GetGoalieSeasonStatsBySortParams{
				Season:      int32(season),
				SortBy:      sortBy,
				ResultLimit: int32(req.GetInt("limit", defaultResultLimit)),
			})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(stats)
		},
	)
}

func requireSeasonSort(req mcp.CallToolRequest, defaultSort string, accepted []string) (string, *mcp.CallToolResult) {
	sortBy := req.GetString(seasonSortByArg, defaultSort)
	for _, key := range accepted {
		if sortBy == key {
			return sortBy, nil
		}
	}
	return "", mcp.NewToolResultError(fmt.Sprintf("unknown sort_by %q; accepted keys: %s", sortBy, strings.Join(accepted, ", ")))
}

func requireSkaterPosition(req mcp.CallToolRequest) (sqlcdb.NullPlayerPosition, *mcp.CallToolResult) {
	position := strings.ToUpper(strings.TrimSpace(req.GetString(seasonPositionArg, "")))
	if position == "" {
		return sqlcdb.NullPlayerPosition{}, nil
	}
	playerPosition, ok := skaterSeasonPositions[position]
	if !ok {
		return sqlcdb.NullPlayerPosition{}, mcp.NewToolResultError("unknown position; accepted positions: C, LW, RW, F, D")
	}
	return sqlcdb.NullPlayerPosition{PlayerPosition: playerPosition, Valid: true}, nil
}

func requireGoaliePosition(req mcp.CallToolRequest) *mcp.CallToolResult {
	position := strings.ToUpper(strings.TrimSpace(req.GetString(seasonPositionArg, "")))
	if position == "" || position == string(sqlcdb.PlayerPositionG) {
		return nil
	}
	return mcp.NewToolResultError("unknown goalie position; accepted position: G")
}
