package mcpserver

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/sqlcdb"
)

const defaultFantasyLimit = 100

func registerFantasyTools(srv *server.MCPServer, queries *sqlcdb.Queries) {
	srv.AddTool(
		mcp.NewTool("get_yahoo_leagues",
			mcp.WithDescription("List all Yahoo fantasy hockey leagues in the database."),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			leagues, err := queries.GetAllYahooLeagues(ctx)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(leagues)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_yahoo_teams_by_league",
			mcp.WithDescription("List all teams in a Yahoo fantasy league."),
			mcp.WithNumber("league_id", mcp.Required(), mcp.Description("Yahoo league ID (use get_yahoo_leagues to find IDs)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			leagueID := req.GetInt("league_id", 0)
			if leagueID == 0 {
				return mcp.NewToolResultError("league_id is required"), nil
			}
			teams, err := queries.GetYahooTeamsByLeague(ctx, int32(leagueID))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(teams)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_yahoo_roster",
			mcp.WithDescription("Get the roster for a Yahoo fantasy team on a specific date, joined with player details."),
			mcp.WithNumber("league_id", mcp.Required(), mcp.Description("Yahoo league ID")),
			mcp.WithNumber("team_id", mcp.Required(), mcp.Description("Yahoo team ID")),
			mcp.WithString("date", mcp.Required(), mcp.Description("Roster date in YYYY-MM-DD format")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			leagueID := req.GetInt("league_id", 0)
			if leagueID == 0 {
				return mcp.NewToolResultError("league_id is required"), nil
			}
			teamID := req.GetInt("team_id", 0)
			if teamID == 0 {
				return mcp.NewToolResultError("team_id is required"), nil
			}
			dateStr, err := req.RequireString("date")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			var d pgtype.Date
			if err := d.Scan(dateStr); err != nil {
				return mcp.NewToolResultError("invalid date format, use YYYY-MM-DD"), nil
			}
			roster, err := queries.GetYahooRosterWithPlayers(ctx, sqlcdb.GetYahooRosterWithPlayersParams{
				LeagueID: int32(leagueID),
				TeamID:   int32(teamID),
				Date:     d,
			})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(roster)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_yahoo_roto_standings",
			mcp.WithDescription("Get rotisserie standings for a Yahoo fantasy league (category ranks and total points per team)."),
			mcp.WithNumber("league_id", mcp.Required(), mcp.Description("Yahoo league ID")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			leagueID := req.GetInt("league_id", 0)
			if leagueID == 0 {
				return mcp.NewToolResultError("league_id is required"), nil
			}
			standings, err := queries.GetYahooRotoStandings(ctx, int32(leagueID))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(standings)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_skater_season_stats",
			mcp.WithDescription("Get fantasy-relevant season stats for skaters, sorted by points. Use limit to control result size."),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20252026)")),
			mcp.WithNumber("limit", mcp.Description("Max number of skaters to return (default 100)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			season := req.GetInt("season", 0)
			if season == 0 {
				return mcp.NewToolResultError("season is required"), nil
			}
			limit := req.GetInt("limit", defaultFantasyLimit)
			stats, err := queries.GetSkaterSeasonStats(ctx, sqlcdb.GetSkaterSeasonStatsParams{
				Season: int32(season),
				Limit:  int32(limit),
			})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(stats)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_goalie_season_stats",
			mcp.WithDescription("Get fantasy-relevant season stats for goalies, sorted by wins. Use limit to control result size."),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20252026)")),
			mcp.WithNumber("limit", mcp.Description("Max number of goalies to return (default 100)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			season := req.GetInt("season", 0)
			if season == 0 {
				return mcp.NewToolResultError("season is required"), nil
			}
			limit := req.GetInt("limit", defaultFantasyLimit)
			stats, err := queries.GetGoalieSeasonStats(ctx, sqlcdb.GetGoalieSeasonStatsParams{
				Season: int32(season),
				Limit:  int32(limit),
			})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(stats)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_unrostered_skaters",
			mcp.WithDescription("Get skaters not rostered in a fantasy league as of a date, sorted by recent performance. Useful for waiver wire analysis."),
			mcp.WithNumber("league_id", mcp.Required(), mcp.Description("Yahoo league ID")),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20252026)")),
			mcp.WithString("date", mcp.Required(), mcp.Description("Reference date in YYYY-MM-DD format")),
			mcp.WithNumber("limit", mcp.Description("Max number of skaters to return (default 100)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			leagueID := req.GetInt("league_id", 0)
			if leagueID == 0 {
				return mcp.NewToolResultError("league_id is required"), nil
			}
			season := req.GetInt("season", 0)
			if season == 0 {
				return mcp.NewToolResultError("season is required"), nil
			}
			dateStr, err := req.RequireString("date")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			var d pgtype.Date
			if err := d.Scan(dateStr); err != nil {
				return mcp.NewToolResultError("invalid date format, use YYYY-MM-DD"), nil
			}
			limit := req.GetInt("limit", defaultFantasyLimit)
			skaters, err := queries.GetUnrosteredSkaters(ctx, sqlcdb.GetUnrosteredSkatersParams{
				LeagueID: int32(leagueID),
				Season:   int32(season),
				Date:     d,
				Limit:    int32(limit),
			})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(skaters)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_unrostered_goalies",
			mcp.WithDescription("Get goalies not rostered in a fantasy league as of a date, sorted by recent performance. Useful for waiver wire analysis."),
			mcp.WithNumber("league_id", mcp.Required(), mcp.Description("Yahoo league ID")),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20252026)")),
			mcp.WithString("date", mcp.Required(), mcp.Description("Reference date in YYYY-MM-DD format")),
			mcp.WithNumber("limit", mcp.Description("Max number of goalies to return (default 100)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			leagueID := req.GetInt("league_id", 0)
			if leagueID == 0 {
				return mcp.NewToolResultError("league_id is required"), nil
			}
			season := req.GetInt("season", 0)
			if season == 0 {
				return mcp.NewToolResultError("season is required"), nil
			}
			dateStr, err := req.RequireString("date")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			var d pgtype.Date
			if err := d.Scan(dateStr); err != nil {
				return mcp.NewToolResultError("invalid date format, use YYYY-MM-DD"), nil
			}
			limit := req.GetInt("limit", defaultFantasyLimit)
			goalies, err := queries.GetUnrosteredGoalies(ctx, sqlcdb.GetUnrosteredGoaliesParams{
				LeagueID: int32(leagueID),
				Season:   int32(season),
				Date:     d,
				Limit:    int32(limit),
			})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(goalies)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_yahoo_matchups",
			mcp.WithDescription("Get all head-to-head matchups for a Yahoo fantasy league (week, teams, scores)."),
			mcp.WithNumber("league_id", mcp.Required(), mcp.Description("Yahoo league ID")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			leagueID := req.GetInt("league_id", 0)
			if leagueID == 0 {
				return mcp.NewToolResultError("league_id is required"), nil
			}
			matchups, err := queries.GetYahooMatchupsByLeague(ctx, int32(leagueID))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(matchups)
		},
	)

	srv.AddTool(
		mcp.NewTool("get_yahoo_draft_results",
			mcp.WithDescription("Get the draft results for a Yahoo fantasy league (pick order, round, team, and player)."),
			mcp.WithNumber("league_id", mcp.Required(), mcp.Description("Yahoo league ID")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			leagueID := req.GetInt("league_id", 0)
			if leagueID == 0 {
				return mcp.NewToolResultError("league_id is required"), nil
			}
			results, err := queries.GetYahooDraftResultsByLeague(ctx, int32(leagueID))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(results)
		},
	)
}
