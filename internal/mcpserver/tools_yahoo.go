package mcpserver

import (
	"context"
	"math"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// yahooQueries is what the yahoo toolset reads; *sqlcdb.Queries satisfies it.
type yahooQueries interface {
	leagueLookup
	GetAllYahooLeagues(ctx context.Context) ([]sqlcdb.YahooLeague, error)
	GetYahooTeamsByLeague(ctx context.Context, leagueID int32) ([]sqlcdb.YahooTeam, error)
	GetYahooRosterWithPlayers(ctx context.Context, arg sqlcdb.GetYahooRosterWithPlayersParams) ([]sqlcdb.YahooRosterPlayer, error)
	GetYahooRotoStandings(ctx context.Context, leagueID int32) ([]sqlcdb.YahooRotoStanding, error)
	GetUnrosteredSkaters(ctx context.Context, arg sqlcdb.GetUnrosteredSkatersParams) ([]sqlcdb.SkaterRecentStat, error)
	GetUnrosteredGoalies(ctx context.Context, arg sqlcdb.GetUnrosteredGoaliesParams) ([]sqlcdb.GoalieRecentStat, error)
	GetYahooMatchupsByLeague(ctx context.Context, leagueID int32) ([]sqlcdb.YahooMatchup, error)
	GetYahooMatchupsByWeek(ctx context.Context, arg sqlcdb.GetYahooMatchupsByWeekParams) ([]sqlcdb.YahooMatchup, error)
	GetYahooMatchupsByTeam(ctx context.Context, arg sqlcdb.GetYahooMatchupsByTeamParams) ([]sqlcdb.YahooMatchup, error)
	GetYahooDraftResultsByLeague(ctx context.Context, leagueID int32) ([]sqlcdb.YahooDraftResult, error)
}

const minimumPositiveInteger = 1

// leagueIndependentYahooTools names the yahoo tools that take no league_id;
// each must still honor the league allowlist on its own. Every other yahoo
// tool must run through leagueGuard.scoped; TestYahooToolsAreLeagueGuarded
// enforces it, so a new league-scoped tool cannot skip the guard.
var leagueIndependentYahooTools = map[string]struct{}{
	"get_yahoo_leagues": {}, // lists leagues through leagueGuard.filter
}

func registerYahooTools(srv *server.MCPServer, q yahooQueries, guard leagueGuard) {
	srv.AddTools(yahooTools(q, guard)...)
}

func yahooTools(q yahooQueries, guard leagueGuard) []server.ServerTool {
	return []server.ServerTool{
		yahooLeaguesTool(q, guard),
		yahooTeamsTool(q, guard),
		yahooRosterTool(q, guard),
		yahooRotoStandingsTool(q, guard),
		unrosteredSkatersTool(q, guard),
		unrosteredGoaliesTool(q, guard),
		yahooMatchupsTool(q, guard),
		yahooDraftResultsTool(q, guard),
	}
}

func yahooLeaguesTool(q yahooQueries, guard leagueGuard) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_yahoo_leagues",
			mcp.WithDescription("List all Yahoo fantasy hockey leagues this server serves."),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			leagues, err := q.GetAllYahooLeagues(ctx)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(guard.filter(leagues))
		},
	}
}

func yahooTeamsTool(q yahooQueries, guard leagueGuard) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_yahoo_teams_by_league",
			mcp.WithDescription("List all teams in a Yahoo fantasy league."),
			mcp.WithNumber(leagueIDArg, mcp.Required(), mcp.Description("Yahoo league ID (use get_yahoo_leagues to find IDs)")),
		),
		Handler: guard.scoped(func(ctx context.Context, _ mcp.CallToolRequest, leagueID int32) (*mcp.CallToolResult, error) {
			return toolResult(q.GetYahooTeamsByLeague(ctx, leagueID))
		}),
	}
}

func yahooRosterTool(q yahooQueries, guard leagueGuard) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_yahoo_roster",
			mcp.WithDescription("Get the roster for a Yahoo fantasy team on a specific date, joined with player details."),
			mcp.WithNumber(leagueIDArg, mcp.Required(), mcp.Description("Yahoo league ID")),
			mcp.WithNumber("team_id", mcp.Required(), mcp.Description("Yahoo team ID")),
			mcp.WithString("date", mcp.Required(), mcp.Description("Roster date in YYYY-MM-DD format")),
		),
		Handler: guard.scoped(func(ctx context.Context, req mcp.CallToolRequest, leagueID int32) (*mcp.CallToolResult, error) {
			teamID := req.GetInt("team_id", 0)
			if teamID == 0 {
				return mcp.NewToolResultError("team_id is required"), nil
			}
			d, errResult := requireDate(req)
			if errResult != nil {
				return errResult, nil
			}
			return toolResult(q.GetYahooRosterWithPlayers(ctx, sqlcdb.GetYahooRosterWithPlayersParams{
				LeagueID: leagueID,
				TeamID:   int32(teamID),
				Date:     d,
			}))
		}),
	}
}

func yahooRotoStandingsTool(q yahooQueries, guard leagueGuard) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_yahoo_roto_standings",
			mcp.WithDescription("Get rotisserie standings for a Yahoo fantasy league (category ranks and total points per team)."),
			mcp.WithNumber(leagueIDArg, mcp.Required(), mcp.Description("Yahoo league ID")),
		),
		Handler: guard.scoped(func(ctx context.Context, _ mcp.CallToolRequest, leagueID int32) (*mcp.CallToolResult, error) {
			return toolResult(q.GetYahooRotoStandings(ctx, leagueID))
		}),
	}
}

// unrosteredTool builds the waiver-wire tool for one position group; the
// skater and goalie variants differ only in name, wording and query.
func unrosteredTool(name, players string, guard leagueGuard, query func(context.Context, unrosteredArgs) (*mcp.CallToolResult, error)) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(name,
			mcp.WithDescription("Get "+players+" not rostered in a fantasy league as of a date, sorted by recent performance. Useful for waiver wire analysis."),
			mcp.WithNumber(leagueIDArg, mcp.Required(), mcp.Description("Yahoo league ID")),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20252026)")),
			mcp.WithString("date", mcp.Required(), mcp.Description("Reference date in YYYY-MM-DD format")),
			mcp.WithNumber("limit", mcp.Description("Max number of "+players+" to return (default 100)")),
		),
		Handler: guard.scoped(func(ctx context.Context, req mcp.CallToolRequest, leagueID int32) (*mcp.CallToolResult, error) {
			season := req.GetInt("season", 0)
			if season == 0 {
				return mcp.NewToolResultError("season is required"), nil
			}
			d, errResult := requireDate(req)
			if errResult != nil {
				return errResult, nil
			}
			return query(ctx, unrosteredArgs{
				LeagueID: leagueID,
				Season:   int32(season),
				Date:     d,
				Limit:    int32(req.GetInt("limit", defaultResultLimit)),
			})
		}),
	}
}

// unrosteredArgs are the shared parameters of the unrostered queries.
type unrosteredArgs struct {
	LeagueID int32
	Season   int32
	Date     pgtype.Date
	Limit    int32
}

func unrosteredSkatersTool(q yahooQueries, guard leagueGuard) server.ServerTool {
	return unrosteredTool("get_unrostered_skaters", "skaters", guard,
		func(ctx context.Context, a unrosteredArgs) (*mcp.CallToolResult, error) {
			return toolResult(q.GetUnrosteredSkaters(ctx, sqlcdb.GetUnrosteredSkatersParams{
				Season: a.Season, LeagueID: a.LeagueID, Date: a.Date, Limit: a.Limit,
			}))
		})
}

func unrosteredGoaliesTool(q yahooQueries, guard leagueGuard) server.ServerTool {
	return unrosteredTool("get_unrostered_goalies", "goalies", guard,
		func(ctx context.Context, a unrosteredArgs) (*mcp.CallToolResult, error) {
			return toolResult(q.GetUnrosteredGoalies(ctx, sqlcdb.GetUnrosteredGoaliesParams{
				Season: a.Season, LeagueID: a.LeagueID, Date: a.Date, Limit: a.Limit,
			}))
		})
}

func yahooMatchupsTool(q yahooQueries, guard leagueGuard) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_yahoo_matchups",
			mcp.WithDescription("Get head-to-head matchups for a Yahoo fantasy league (week, teams, scores), optionally filtered by week and/or team. When both filters are given, returns that team's matchup in the specified week."),
			mcp.WithNumber(leagueIDArg, mcp.Required(), mcp.Description("Yahoo league ID")),
			mcp.WithNumber("week", mcp.Description("Filter by matchup week"),
				mcp.Min(minimumPositiveInteger), mcp.Max(math.MaxInt32), mcp.MultipleOf(minimumPositiveInteger)),
			mcp.WithNumber("team_id", mcp.Description("Filter to matchups involving this Yahoo team ID"),
				mcp.Min(minimumPositiveInteger), mcp.Max(math.MaxInt32), mcp.MultipleOf(minimumPositiveInteger)),
		),
		Handler: guard.scoped(func(ctx context.Context, req mcp.CallToolRequest, leagueID int32) (*mcp.CallToolResult, error) {
			week, hasWeek, errResult := optionalPositiveInt32(req, "week")
			if errResult != nil {
				return errResult, nil
			}
			teamID, hasTeamID, errResult := optionalPositiveInt32(req, "team_id")
			if errResult != nil {
				return errResult, nil
			}

			if hasTeamID {
				matchups, err := q.GetYahooMatchupsByTeam(ctx, sqlcdb.GetYahooMatchupsByTeamParams{
					LeagueID: leagueID,
					Team1ID:  teamID,
				})
				if err == nil && hasWeek {
					matchups = filterYahooMatchupsByWeek(matchups, week)
				}
				return toolResult(matchups, err)
			}
			if hasWeek {
				return toolResult(q.GetYahooMatchupsByWeek(ctx, sqlcdb.GetYahooMatchupsByWeekParams{
					LeagueID: leagueID,
					Week:     week,
				}))
			}
			return toolResult(q.GetYahooMatchupsByLeague(ctx, leagueID))
		}),
	}
}

// optionalPositiveInt32 reads an optional positive integer query argument.
func optionalPositiveInt32(req mcp.CallToolRequest, name string) (int32, bool, *mcp.CallToolResult) {
	raw, present := req.GetArguments()[name]
	if !present {
		return 0, false, nil
	}
	value, valid := integerValue(raw)
	if !valid || value < minimumPositiveInteger || value > math.MaxInt32 {
		return 0, false, mcp.NewToolResultError("invalid " + name)
	}
	return int32(value), true, nil
}

func integerValue(raw any) (int64, bool) {
	switch value := raw.(type) {
	case int:
		return int64(value), true
	case int32:
		return int64(value), true
	case int64:
		return value, true
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) || math.Trunc(value) != value {
			return 0, false
		}
		return int64(value), true
	default:
		return 0, false
	}
}

func filterYahooMatchupsByWeek(matchups []sqlcdb.YahooMatchup, week int32) []sqlcdb.YahooMatchup {
	filtered := make([]sqlcdb.YahooMatchup, 0, len(matchups))
	for _, matchup := range matchups {
		if matchup.Week == week {
			filtered = append(filtered, matchup)
		}
	}
	return filtered
}

func yahooDraftResultsTool(q yahooQueries, guard leagueGuard) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_yahoo_draft_results",
			mcp.WithDescription("Get the draft results for a Yahoo fantasy league (pick order, round, team, and player)."),
			mcp.WithNumber(leagueIDArg, mcp.Required(), mcp.Description("Yahoo league ID")),
		),
		Handler: guard.scoped(func(ctx context.Context, _ mcp.CallToolRequest, leagueID int32) (*mcp.CallToolResult, error) {
			return toolResult(q.GetYahooDraftResultsByLeague(ctx, leagueID))
		}),
	}
}

// toolResult renders query rows as CSV, or the query error as a tool error.
func toolResult[T any](rows []T, err error) (*mcp.CallToolResult, error) {
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return ResultCSV(rows)
}

// requireDate reads the required "date" argument (YYYY-MM-DD); on failure it
// returns the tool error to send back.
func requireDate(req mcp.CallToolRequest) (pgtype.Date, *mcp.CallToolResult) {
	var d pgtype.Date
	dateStr, err := req.RequireString("date")
	if err != nil {
		return d, mcp.NewToolResultError(err.Error())
	}
	if err := d.Scan(dateStr); err != nil {
		return d, mcp.NewToolResultError("invalid date format, use YYYY-MM-DD")
	}
	return d, nil
}
