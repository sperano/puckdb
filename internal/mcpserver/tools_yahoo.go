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
	GetYahooTeamManagersByLeague(ctx context.Context, leagueID int32) ([]sqlcdb.GetYahooTeamManagersByLeagueRow, error)
	GetYahooRosterWithPlayers(ctx context.Context, arg sqlcdb.GetYahooRosterWithPlayersParams) ([]sqlcdb.YahooRosterPlayer, error)
	GetYahooRotoStandings(ctx context.Context, leagueID int32) ([]sqlcdb.YahooRotoStanding, error)
	GetYahooSeasonTeamTotals(ctx context.Context, leagueID int32) ([]sqlcdb.YahooSeasonTeamTotal, error)
	GetUnrosteredSkaters(ctx context.Context, arg sqlcdb.GetUnrosteredSkatersParams) ([]sqlcdb.SkaterRecentStat, error)
	GetUnrosteredGoalies(ctx context.Context, arg sqlcdb.GetUnrosteredGoaliesParams) ([]sqlcdb.GoalieRecentStat, error)
	GetYahooMatchupsByLeague(ctx context.Context, leagueID int32) ([]sqlcdb.YahooMatchup, error)
	GetYahooMatchupsByWeek(ctx context.Context, arg sqlcdb.GetYahooMatchupsByWeekParams) ([]sqlcdb.YahooMatchup, error)
	GetYahooMatchupsByTeam(ctx context.Context, arg sqlcdb.GetYahooMatchupsByTeamParams) ([]sqlcdb.YahooMatchup, error)
	GetYahooDraftResultsByLeague(ctx context.Context, leagueID int32) ([]sqlcdb.YahooDraftResult, error)
	GetYahooLeagueStatCategories(ctx context.Context, leagueID int32) ([]sqlcdb.YahooLeagueStatCategory, error)
	GetYahooLeagueRosterPositions(ctx context.Context, leagueID int32) ([]sqlcdb.YahooLeagueRosterPosition, error)
	GetLatestYahooLeagueRuleSnapshot(ctx context.Context, arg sqlcdb.GetLatestYahooLeagueRuleSnapshotParams) (sqlcdb.YahooLeagueRuleSnapshot, error)
	ListYahooLeaguePlayersWithNHL(ctx context.Context, leagueKey string) ([]sqlcdb.ListYahooLeaguePlayersWithNHLRow, error)
}

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
		yahooSeasonTeamTotalsTool(q, guard),
		unrosteredSkatersTool(q, guard),
		unrosteredGoaliesTool(q, guard),
		yahooMatchupsTool(q, guard),
		yahooDraftResultsTool(q, guard),
		yahooLeagueSettingsTool(q, guard),
		yahooLeaguePlayersTool(q, guard),
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

func yahooRosterTool(q yahooQueries, guard leagueGuard) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_yahoo_roster",
			mcp.WithDescription("Get the roster for a Yahoo fantasy team on a specific date, joined with player details."),
			mcp.WithNumber(leagueIDArg, mcp.Required(), mcp.Description("Yahoo league ID")),
			mcp.WithNumber("team_id", mcp.Required(), mcp.Description("Yahoo team ID")),
			mcp.WithString("date", mcp.Required(), mcp.Description("Roster date in YYYY-MM-DD format")),
		),
		Handler: guard.scoped(func(ctx context.Context, req mcp.CallToolRequest, leagueID int32) (*mcp.CallToolResult, error) {
			teamID, errResult := requireInt[int32](req, yahooTeamIDParam)
			if errResult != nil {
				return errResult, nil
			}
			d, errResult := requireDate(req)
			if errResult != nil {
				return errResult, nil
			}
			return toolResult(q.GetYahooRosterWithPlayers(ctx, sqlcdb.GetYahooRosterWithPlayersParams{
				LeagueID: leagueID,
				TeamID:   teamID,
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

func yahooSeasonTeamTotalsTool(q yahooQueries, guard leagueGuard) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_yahoo_season_team_totals",
			mcp.WithDescription("Get each team's season totals in a Yahoo fantasy league, summed from its daily stats: goals, assists, plus/minus, PIM, PPP, SOG, wins and goals against. Sorted by goals + assists; other league categories are not included."),
			mcp.WithNumber(leagueIDArg, mcp.Required(), mcp.Description("Yahoo league ID")),
		),
		Handler: guard.scoped(func(ctx context.Context, _ mcp.CallToolRequest, leagueID int32) (*mcp.CallToolResult, error) {
			return toolResult(q.GetYahooSeasonTeamTotals(ctx, leagueID))
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
			season, errResult := requireInt[int32](req, seasonParam)
			if errResult != nil {
				return errResult, nil
			}
			d, errResult := requireDate(req)
			if errResult != nil {
				return errResult, nil
			}
			limit, errResult := limitOrDefault(req, defaultResultLimit)
			if errResult != nil {
				return errResult, nil
			}
			return query(ctx, unrosteredArgs{
				LeagueID: leagueID,
				Season:   season,
				Date:     d,
				Limit:    limit,
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

// yahooWeekParam is the optional matchup week filter.
var yahooWeekParam = intArg{name: "week", min: minimumPositiveInteger, max: math.MaxInt32}

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
			week, hasWeek, errResult := optionalInt[int32](req, yahooWeekParam)
			if errResult != nil {
				return errResult, nil
			}
			teamID, hasTeamID, errResult := optionalInt[int32](req, yahooTeamIDParam)
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
