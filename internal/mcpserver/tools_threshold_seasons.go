package mcpserver

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

const (
	skaterThresholdToolName = "get_skater_threshold_seasons"
	goalieThresholdToolName = "get_goalie_threshold_seasons"
	thresholdStatArg        = "stat"
	thresholdGameTypeArg    = "game_type"
	// defaultMinThresholdSeasons counts every player with one qualifying
	// season.
	defaultMinThresholdSeasons = 1
	// defaultThresholdLimit and maxThresholdLimit bound the players returned:
	// a low threshold over a century of seasons matches thousands.
	defaultThresholdLimit = 50
	maxThresholdLimit     = 500
)

// Integer arguments of the threshold tools.
var (
	thresholdMinValueParam   = intArg{name: "min_value", min: 0, max: math.MaxInt32}
	thresholdMinSeasonsParam = intArg{name: "min_seasons", min: minimumPositiveInteger, max: math.MaxInt32}
	thresholdFromSeasonParam = intArg{name: "from_season", min: firstNHLSeasonID, max: lastSeasonID}
	thresholdToSeasonParam   = intArg{name: "to_season", min: firstNHLSeasonID, max: lastSeasonID}
	thresholdLimitParam      = intArg{name: "limit", min: 0, max: maxThresholdLimit}
)

// skaterThresholdStats and goalieThresholdStats are the stat keys the
// queries map to a club_*_stats column; the first is the default. Only
// counting stats: a rate (GAA, save %, shooting %) cannot be summed across
// a traded player's clubs.
var (
	skaterThresholdStats = []string{"points", "goals", "assists", "plus_minus", "pim", "ppg", "shg", "gwg", "otg", "sog", "gp"}
	goalieThresholdStats = []string{"wins", "losses", "otl", "shutouts", "saves", "shots_against", "gp", "gs"}
)

type thresholdQueries interface {
	GetSkaterThresholdSeasons(context.Context, sqlcdb.GetSkaterThresholdSeasonsParams) ([]sqlcdb.GetSkaterThresholdSeasonsRow, error)
	GetGoalieThresholdSeasons(context.Context, sqlcdb.GetGoalieThresholdSeasonsParams) ([]sqlcdb.GetGoalieThresholdSeasonsRow, error)
}

// thresholdArgs are the validated arguments shared by both tools.
type thresholdArgs struct {
	stat                 string
	minValue, minSeasons int32
	fromSeason, toSeason int32
	gameType             sqlcdb.GameType
	limit                int32
}

func thresholdTools(q thresholdQueries) []server.ServerTool {
	return []server.ServerTool{
		skaterThresholdTool(q),
		goalieThresholdTool(q),
	}
}

func skaterThresholdTool(q thresholdQueries) server.ServerTool {
	return server.ServerTool{
		Tool: thresholdTool(skaterThresholdToolName, "get_club_skater_stats", skaterThresholdStats,
			"Ranks skaters by how many seasons their stat reached min_value, e.g. at least 10 seasons of 80+ points since 2001-02 "+
				"(stat=points, min_value=80, min_seasons=10, from_season=20012002). "),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args, errResult := readThresholdArgs(req, skaterThresholdStats)
			if errResult != nil {
				return errResult, nil
			}
			return toolResult(q.GetSkaterThresholdSeasons(ctx, sqlcdb.GetSkaterThresholdSeasonsParams{
				Stat: args.stat, MinValue: args.minValue, MinSeasons: args.minSeasons,
				FromSeason: args.fromSeason, ToSeason: args.toSeason, GameType: args.gameType, ResultLimit: args.limit,
			}))
		},
	}
}

func goalieThresholdTool(q thresholdQueries) server.ServerTool {
	return server.ServerTool{
		Tool: thresholdTool(goalieThresholdToolName, "get_club_goalie_stats", goalieThresholdStats,
			"Ranks goalies by how many seasons their stat reached min_value, e.g. at least 5 seasons of 40+ wins "+
				"(stat=wins, min_value=40, min_seasons=5). "),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args, errResult := readThresholdArgs(req, goalieThresholdStats)
			if errResult != nil {
				return errResult, nil
			}
			return toolResult(q.GetGoalieThresholdSeasons(ctx, sqlcdb.GetGoalieThresholdSeasonsParams{
				Stat: args.stat, MinValue: args.minValue, MinSeasons: args.minSeasons,
				FromSeason: args.fromSeason, ToSeason: args.toSeason, GameType: args.gameType, ResultLimit: args.limit,
			}))
		},
	}
}

// thresholdTool builds the schema both tools share; summary opens the
// description and clubTool names the per-club tool over the same table.
func thresholdTool(name, clubTool string, stats []string, summary string) mcp.Tool {
	return mcp.NewTool(name,
		mcp.WithDescription(summary+
			"A season counts when the player's total for it, summed over every club he played for that season "+
			"(NHL club stats, the same source as "+clubTool+"), is at least min_value. "+
			"One row per player with qualifying_seasons, best_value and seasons (each qualifying season as season_id:value, oldest first), "+
			"most qualifying seasons first. The current season counts with its totals so far."),
		mcp.WithString(thresholdStatArg, mcp.Description("Stat: "+strings.Join(stats, ", ")+" (default "+stats[0]+")")),
		mcp.WithNumber(thresholdMinValueParam.name, mcp.Required(), mcp.Description("Smallest season value that qualifies (0 or more)")),
		mcp.WithNumber(thresholdMinSeasonsParam.name, mcp.Description(fmt.Sprintf("Fewest qualifying seasons a player needs (default %d)", defaultMinThresholdSeasons))),
		mcp.WithNumber(thresholdFromSeasonParam.name, mcp.Description("First season ID counted, inclusive (e.g. 20012002; default the first NHL season)")),
		mcp.WithNumber(thresholdToSeasonParam.name, mcp.Description("Last season ID counted, inclusive (e.g. 20252026; default the latest season)")),
		mcp.WithString(thresholdGameTypeArg, mcp.Description("regular_season (default) or playoffs")),
		mcp.WithNumber(thresholdLimitParam.name, mcp.Description(fmt.Sprintf("Max players returned (default %d, at most %d)", defaultThresholdLimit, maxThresholdLimit))),
	)
}

// readThresholdArgs validates every argument before any SQL runs.
func readThresholdArgs(req mcp.CallToolRequest, stats []string) (thresholdArgs, *mcp.CallToolResult) {
	var args thresholdArgs
	var errResult *mcp.CallToolResult
	if args.stat, errResult = thresholdStat(req, stats); errResult != nil {
		return args, errResult
	}
	if args.minValue, errResult = requireInt[int32](req, thresholdMinValueParam); errResult != nil {
		return args, errResult
	}
	if args.minSeasons, errResult = intOrDefault(req, thresholdMinSeasonsParam, defaultMinThresholdSeasons); errResult != nil {
		return args, errResult
	}
	if args.fromSeason, errResult = intOrDefault(req, thresholdFromSeasonParam, firstNHLSeasonID); errResult != nil {
		return args, errResult
	}
	if args.toSeason, errResult = intOrDefault(req, thresholdToSeasonParam, lastSeasonID); errResult != nil {
		return args, errResult
	}
	if args.fromSeason > args.toSeason {
		return args, mcp.NewToolResultError(fmt.Sprintf("from_season %d is after to_season %d", args.fromSeason, args.toSeason))
	}
	if args.gameType, errResult = regularOrPlayoffsGameType(req, thresholdGameTypeArg); errResult != nil {
		return args, errResult
	}
	args.limit, errResult = intOrDefault(req, thresholdLimitParam, defaultThresholdLimit)
	return args, errResult
}

// thresholdStat reads the stat key; absent or empty is the first of stats.
func thresholdStat(req mcp.CallToolRequest, stats []string) (string, *mcp.CallToolResult) {
	raw, errResult := optionalString(req, thresholdStatArg)
	if errResult != nil {
		return "", errResult
	}
	stat := strings.ToLower(strings.TrimSpace(raw))
	if stat == "" {
		return stats[0], nil
	}
	for _, key := range stats {
		if stat == key {
			return stat, nil
		}
	}
	return "", mcp.NewToolResultError(fmt.Sprintf("unknown %s %s; accepted: %s",
		thresholdStatArg, formatRaw(raw), strings.Join(stats, ", ")))
}
