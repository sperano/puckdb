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
	edgeLeadersToolName = "get_edge_leaders"
	edgeGroupArg        = "group"
	edgeMetricArg       = "metric"
	edgeGameTypeArg     = "game_type"
	edgeMinGamesArg     = "min_games"
	edgeLeadersSection  = "leaders"

	// defaultEdgeLeadersLimit is the number of leaders returned without a
	// limit argument.
	defaultEdgeLeadersLimit = 25
	// maxEdgeLeadersLimit caps limit: enough for every team and the top of
	// any skater list without dumping a whole season.
	maxEdgeLeadersLimit = 200
	// firstEdgeSeasonID is the first season NHL Edge tracking covers.
	firstEdgeSeasonID = 20212022
	// maxEdgeMinGames bounds min_games well above an 82-game season plus
	// four playoff rounds.
	maxEdgeMinGames = 200

	edgeGroupSkater = "skater"
	edgeGroupGoalie = "goalie"
	edgeGroupTeam   = "team"

	edgeOrderAscending  = "asc"
	edgeOrderDescending = "desc"
)

// Integer arguments of get_edge_leaders. Its season is a season ID like
// every other tool's (20242025), and starts at the first Edge season, so a
// start year such as 2024 is refused with the accepted range.
var (
	edgeLeadersSeasonParam = intArg{name: "season", min: firstEdgeSeasonID, max: lastSeasonID}
	edgeLeadersLimitParam  = intArg{name: "limit", min: minimumPositiveInteger, max: maxEdgeLeadersLimit}
	edgeMinGamesParam      = intArg{name: edgeMinGamesArg, min: minimumPositiveInteger, max: maxEdgeMinGames}
)

// edgeLeaderGameTypes are the game types Edge data is imported for.
var edgeLeaderGameTypes = map[string]sqlcdb.GameType{
	string(sqlcdb.GameTypeRegularSeason): sqlcdb.GameTypeRegularSeason,
	string(sqlcdb.GameTypePlayoffs):      sqlcdb.GameTypePlayoffs,
}

type edgeLeadersQueries interface {
	GetEdgeSkaterLeaders(context.Context, sqlcdb.GetEdgeSkaterLeadersParams) ([]sqlcdb.GetEdgeSkaterLeadersRow, error)
	GetEdgeGoalieLeaders(context.Context, sqlcdb.GetEdgeGoalieLeadersParams) ([]sqlcdb.GetEdgeGoalieLeadersRow, error)
	GetEdgeTeamLeaders(context.Context, sqlcdb.GetEdgeTeamLeadersParams) ([]sqlcdb.GetEdgeTeamLeadersRow, error)
}

// edgeLeadersRequest is a validated get_edge_leaders call.
type edgeLeadersRequest struct {
	group    string
	metric   edgeMetricInfo
	season   int32
	gameType sqlcdb.GameType
	minGames int32
	limit    int32
}

func edgeLeadersTool(q edgeLeadersQueries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(edgeLeadersToolName,
			mcp.WithDescription("Rank skaters, goalies or teams of one season by an NHL Edge tracking metric (2021-2022 onward). "+
				"Best first: lowest first for gaa and dz_pctg, highest first for every other metric. "+
				"Returns a '# key=value' header (group, metric, order, units) and CSV rows with the value, the NHL percentile (players) or league rank (teams), league average and games played. "+
				"Rate stats such as gaa favour players with few games: pass min_games."),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20242025), 20212022 or later")),
			mcp.WithString(edgeGroupArg, mcp.Required(), mcp.Description("skater, goalie or team")),
			mcp.WithString(edgeMetricArg, mcp.Required(), mcp.Description("Metric key. skater: "+metricInfoKeys(edgeMetricInfos[edgeGroupSkater])+
				". goalie: "+metricInfoKeys(edgeMetricInfos[edgeGroupGoalie])+". team: "+metricInfoKeys(edgeMetricInfos[edgeGroupTeam]))),
			mcp.WithString(edgeGameTypeArg, mcp.Description("regular_season (default) or playoffs")),
			mcp.WithNumber(edgeMinGamesArg, mcp.Description("Skaters and goalies only: leave out players with fewer games played (club stats) in the season and game type")),
			mcp.WithNumber("limit", mcp.Description(fmt.Sprintf("Max rows (default %d, max %d)", defaultEdgeLeadersLimit, maxEdgeLeadersLimit))),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			r, errResult := parseEdgeLeadersRequest(req)
			if errResult != nil {
				return errResult, nil
			}
			rows, err := r.run(ctx, q)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			section, err := csvSection(edgeLeadersSection, rows)
			if err != nil {
				return nil, err
			}
			return mcp.NewToolResultText(r.header().String() + "\n" + section), nil
		},
	}
}

// parseEdgeLeadersRequest validates every argument before any query runs.
func parseEdgeLeadersRequest(req mcp.CallToolRequest) (edgeLeadersRequest, *mcp.CallToolResult) {
	var r edgeLeadersRequest
	var errResult *mcp.CallToolResult
	if r.season, errResult = requireInt[int32](req, edgeLeadersSeasonParam); errResult != nil {
		return r, errResult
	}
	if r.group, r.metric, errResult = requireEdgeMetric(req); errResult != nil {
		return r, errResult
	}
	if r.gameType, errResult = edgeLeaderGameType(req); errResult != nil {
		return r, errResult
	}
	if r.minGames, errResult = edgeMinGames(req, r.group); errResult != nil {
		return r, errResult
	}
	limit, given, errResult := optionalInt[int32](req, edgeLeadersLimitParam)
	if errResult != nil {
		return r, errResult
	}
	r.limit = defaultEdgeLeadersLimit
	if given {
		r.limit = limit
	}
	return r, nil
}

// requireEdgeMetric reads group and metric; the metric must belong to the
// group's whitelist, which is also the only way a key reaches SQL.
func requireEdgeMetric(req mcp.CallToolRequest) (string, edgeMetricInfo, *mcp.CallToolResult) {
	group := strings.ToLower(strings.TrimSpace(req.GetString(edgeGroupArg, "")))
	metrics, ok := edgeMetricInfos[group]
	if !ok {
		return "", edgeMetricInfo{}, mcp.NewToolResultError(fmt.Sprintf("unknown group %q; accepted groups: %s, %s, %s",
			group, edgeGroupSkater, edgeGroupGoalie, edgeGroupTeam))
	}
	key := strings.ToLower(strings.TrimSpace(req.GetString(edgeMetricArg, "")))
	for _, m := range metrics {
		if m.key == key {
			return group, m, nil
		}
	}
	return "", edgeMetricInfo{}, mcp.NewToolResultError(fmt.Sprintf("unknown %s metric %q; accepted metrics: %s",
		group, key, metricInfoKeys(metrics)))
}

func edgeLeaderGameType(req mcp.CallToolRequest) (sqlcdb.GameType, *mcp.CallToolResult) {
	name := strings.TrimSpace(req.GetString(edgeGameTypeArg, ""))
	if name == "" {
		return sqlcdb.GameTypeRegularSeason, nil
	}
	gameType, ok := edgeLeaderGameTypes[name]
	if !ok {
		return "", mcp.NewToolResultError(fmt.Sprintf("unknown game_type %q; accepted: %s, %s",
			name, sqlcdb.GameTypeRegularSeason, sqlcdb.GameTypePlayoffs))
	}
	return gameType, nil
}

// edgeMinGames reads min_games; absent or 0 is no minimum. Team rows have no
// games played, so a minimum there is refused rather than ignored.
func edgeMinGames(req mcp.CallToolRequest, group string) (int32, *mcp.CallToolResult) {
	minGames, given, errResult := optionalFilter[int32](req, edgeMinGamesParam)
	if errResult != nil {
		return 0, errResult
	}
	if given && group == edgeGroupTeam {
		return 0, mcp.NewToolResultError("min_games applies to skater and goalie groups only")
	}
	return minGames, nil
}

// run queries the group's leaders and converts them to output rows.
func (r edgeLeadersRequest) run(ctx context.Context, q edgeLeadersQueries) (any, error) {
	switch r.group {
	case edgeGroupSkater:
		rows, err := q.GetEdgeSkaterLeaders(ctx, sqlcdb.GetEdgeSkaterLeadersParams{
			Season: r.season, GameType: r.gameType, SortBy: r.metric.key,
			MinGames: r.minGames, Ascending: r.metric.ascending, ResultLimit: r.limit,
		})
		return playerLeaderRows(rows, skaterEdgeMetrics.get(r.metric.key), skaterLeaderPlayer), err
	case edgeGroupGoalie:
		rows, err := q.GetEdgeGoalieLeaders(ctx, sqlcdb.GetEdgeGoalieLeadersParams{
			Season: r.season, GameType: r.gameType, SortBy: r.metric.key,
			MinGames: r.minGames, Ascending: r.metric.ascending, ResultLimit: r.limit,
		})
		return playerLeaderRows(rows, goalieEdgeMetrics.get(r.metric.key), goalieLeaderPlayer), err
	default:
		rows, err := q.GetEdgeTeamLeaders(ctx, sqlcdb.GetEdgeTeamLeadersParams{
			Season: r.season, GameType: r.gameType, SortBy: r.metric.key,
			Ascending: r.metric.ascending, ResultLimit: r.limit,
		})
		return teamLeaderRows(rows, teamEdgeMetrics.get(r.metric.key)), err
	}
}

// header prints the request once, so the rows need not repeat it.
func (r edgeLeadersRequest) header() metadataHeader {
	var h metadataHeader
	h.add("group", r.group)
	h.add("metric", r.metric.key)
	h.add("season", r.season)
	h.add("game_type", r.gameType)
	order := edgeOrderDescending
	if r.metric.ascending {
		order = edgeOrderAscending
	}
	h.add("order", order)
	if r.metric.unit != "" {
		h.add("unit", r.metric.unit)
	}
	if r.metric.metricUnit != "" {
		h.add("value_metric_unit", r.metric.metricUnit)
	}
	if r.minGames > 0 {
		h.add(edgeMinGamesArg, r.minGames)
	}
	return h
}
