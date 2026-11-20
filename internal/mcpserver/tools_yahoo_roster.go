package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// rosterFreshnessNote tells the model how far imported rosters reach, so an
// empty date is read as "not imported yet" rather than "unchanged".
const rosterFreshnessNote = "Rosters are imported once a day for the days before the import, so today's " +
	"lineup is never imported and yesterday's is the newest one at best."

func yahooRosterTool(q yahooQueries, guard leagueGuard) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("get_yahoo_roster",
			mcp.WithDescription("Get the roster for a Yahoo fantasy team on a specific date, joined with player details. "+
				rosterFreshnessNote+" When the date is after the team's latest imported roster, the result is that "+
				"latest roster, under a first line naming its date; when an earlier date has no imported roster, the result "+
				"says so and gives the team's latest imported roster date instead of rows."),
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
			rows, err := q.GetYahooRosterWithPlayers(ctx, sqlcdb.GetYahooRosterWithPlayersParams{
				LeagueID: leagueID,
				TeamID:   teamID,
				Date:     d,
			})
			if err != nil || len(rows) > 0 {
				return toolResult(rows, err)
			}
			return noTeamRosterResult(ctx, q, leagueID, teamID, d), nil
		}),
	}
}

// noTeamRosterResult answers a date with no roster rows for the team: a
// date after the team's latest imported roster gets that roster, any other
// date the latest imported date.
func noTeamRosterResult(ctx context.Context, q yahooQueries, leagueID, teamID int32, date pgtype.Date) *mcp.CallToolResult {
	latest, err := q.GetLatestYahooTeamRosterDate(ctx, sqlcdb.GetLatestYahooTeamRosterDateParams{
		LeagueID: leagueID,
		TeamID:   teamID,
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("load latest roster date of team %d in league %d: %v", teamID, leagueID, err))
	}
	if latest.Valid && date.Time.After(latest.Time) {
		return latestTeamRosterResult(ctx, q, leagueID, teamID, date, latest)
	}
	return mcp.NewToolResultText(fmt.Sprintf("no roster imported for team %d of league %d on %s; %s",
		teamID, leagueID, date.Time.Format(time.DateOnly), latestRosterDateText(latest)))
}

// latestTeamRosterResult answers a date after the team's latest imported
// roster with that roster, under a line naming both dates.
func latestTeamRosterResult(ctx context.Context, q yahooQueries, leagueID, teamID int32, date, latest pgtype.Date) *mcp.CallToolResult {
	rows, err := q.GetYahooRosterWithPlayers(ctx, sqlcdb.GetYahooRosterWithPlayersParams{
		LeagueID: leagueID, TeamID: teamID, Date: latest,
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("load roster of team %d in league %d on %s: %v",
			teamID, leagueID, latest.Time.Format(time.DateOnly), err))
	}
	text, err := csvText(rows)
	if err != nil {
		return mcp.NewToolResultError(err.Error())
	}
	return mcp.NewToolResultText(fmt.Sprintf("no roster imported for team %d of league %d on %s, after its latest imported "+
		"roster; this is the latest one, of %s:\n%s", teamID, leagueID, date.Time.Format(time.DateOnly),
		latest.Time.Format(time.DateOnly), text))
}

// latestRosterDateText names the latest imported roster date, or says there
// is none.
func latestRosterDateText(latest pgtype.Date) string {
	if !latest.Valid {
		return "no roster of this team has been imported"
	}
	return "latest imported date: " + latest.Time.Format(time.DateOnly)
}
