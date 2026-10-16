package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

const (
	teamScheduleToolName = "get_team_schedule"
	scheduleStartArg     = "start_date"
	scheduleEndArg       = "end_date"
	// maxScheduleSpanDays bounds the range, both dates included: a month
	// covers a fantasy week or a two-week fantasy playoff matchup with room to spare
	// and keeps the per-team game_dates column short.
	maxScheduleSpanDays = 31
	// lightNightMaxGames is the most games a light night has: at most 7
	// games means fewer than half of the 32 clubs play, so most fantasy
	// rosters have open slots that night.
	lightNightMaxGames = 7
	// heavyNightMinGames is the fewest games a heavy night has: 10 or more
	// games means at least 20 of the 32 clubs play, so a streamed player
	// often cannot be started.
	heavyNightMinGames = 10
	// Night classes of the nights section.
	nightLight  = "light"
	nightNormal = "normal"
	nightHeavy  = "heavy"
	// Section titles of get_team_schedule.
	scheduleTeamsSection  = "teams"
	scheduleNightsSection = "nights"
	// scheduleGameType is the only game type get_team_schedule counts: the
	// night thresholds assume all 32 clubs can play, while a playoff night
	// has at most 8 games and would always be light.
	scheduleGameType = sqlcdb.GameTypeRegularSeason
	// scheduleDay is one calendar day between two parsed dates.
	scheduleDay = 24 * time.Hour
)

type teamScheduleQueries interface {
	ListScheduleNights(context.Context, sqlcdb.ListScheduleNightsParams) ([]sqlcdb.ListScheduleNightsRow, error)
	GetTeamScheduleCounts(context.Context, sqlcdb.GetTeamScheduleCountsParams) ([]sqlcdb.GetTeamScheduleCountsRow, error)
}

// registerTeamScheduleTools adds get_team_schedule.
func registerTeamScheduleTools(srv *server.MCPServer, queries teamScheduleQueries) {
	srv.AddTools(teamScheduleTool(queries))
}

// scheduleNightRow is one date of the nights section.
type scheduleNightRow struct {
	Date  string `json:"date"`
	Games int32  `json:"games"`
	Night string `json:"night"`
}

func teamScheduleTool(q teamScheduleQueries) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(teamScheduleToolName,
			mcp.WithDescription(fmt.Sprintf("Regular-season games per NHL team in a date range, for weekly streaming: one CSV row "+
				"per team with games, home, away, back_to_backs (games the day after another game of the team, both in the range), "+
				"light_night_games / heavy_night_games (games on nights with at most %d / at least %d games league-wide) and "+
				"game_dates; then every date with games, its league-wide game count and night class (light/normal/heavy). "+
				"Teams are ordered by games, then light-night games (both most first), then abbreviation; clubs without a game "+
				"in the range (bye week) are listed with 0. Postponed and cancelled games do not count. Playoffs are not covered. "+
				"At most %d days, both dates included.", lightNightMaxGames, heavyNightMinGames, maxScheduleSpanDays)),
			mcp.WithString(scheduleStartArg, mcp.Required(), mcp.Description("First date (YYYY-MM-DD)")),
			mcp.WithString(scheduleEndArg, mcp.Required(), mcp.Description("Last date (YYYY-MM-DD), included")),
			mcp.WithNumber("team_id", mcp.Description("Only this team (use find_team to resolve abbreviations); nights stay league-wide")),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			params, errResult := teamScheduleParams(req)
			if errResult != nil {
				return errResult, nil
			}
			nights, err := q.ListScheduleNights(ctx, sqlcdb.ListScheduleNightsParams{
				StartDate: params.StartDate, EndDate: params.EndDate,
			})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			teams, err := q.GetTeamScheduleCounts(ctx, params)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			text, err := teamScheduleText(params, teams, nights)
			if err != nil {
				return nil, err
			}
			return mcp.NewToolResultText(text), nil
		},
	}
}

// teamScheduleParams reads get_team_schedule's arguments.
func teamScheduleParams(req mcp.CallToolRequest) (sqlcdb.GetTeamScheduleCountsParams, *mcp.CallToolResult) {
	params := sqlcdb.GetTeamScheduleCountsParams{
		LightNightMaxGames: lightNightMaxGames,
		HeavyNightMinGames: heavyNightMinGames,
	}
	var errResult *mcp.CallToolResult
	if params.StartDate, errResult = requireNamedDate(req, scheduleStartArg); errResult != nil {
		return params, errResult
	}
	if params.EndDate, errResult = requireNamedDate(req, scheduleEndArg); errResult != nil {
		return params, errResult
	}
	if params.StartDate.Time.After(params.EndDate.Time) {
		return params, mcp.NewToolResultError("start_date is after end_date")
	}
	if days := scheduleSpanDays(params); days > maxScheduleSpanDays {
		return params, mcp.NewToolResultError(fmt.Sprintf("the range spans %d days; at most %d are allowed", days, maxScheduleSpanDays))
	}
	params.TeamID, errResult = optionalInt8Filter(req, teamIDParam)
	return params, errResult
}

// scheduleSpanDays counts the days of the range, both dates included.
// parseDate yields UTC midnights, so the difference is whole days.
func scheduleSpanDays(params sqlcdb.GetTeamScheduleCountsParams) int {
	return int(params.EndDate.Time.Sub(params.StartDate.Time)/scheduleDay) + 1
}

// teamScheduleText renders the header line, the teams section and the
// nights section.
func teamScheduleText(params sqlcdb.GetTeamScheduleCountsParams, teams []sqlcdb.GetTeamScheduleCountsRow, nights []sqlcdb.ListScheduleNightsRow) (string, error) {
	nightRows := make([]scheduleNightRow, 0, len(nights))
	var games, light, heavy int32
	for _, n := range nights {
		class := nightClass(n.Games)
		switch class {
		case nightLight:
			light++
		case nightHeavy:
			heavy++
		}
		games += n.Games
		nightRows = append(nightRows, scheduleNightRow{Date: n.GameDate.Time.Format(time.DateOnly), Games: n.Games, Night: class})
	}

	var h metadataHeader
	h.add(scheduleStartArg, params.StartDate.Time.Format(time.DateOnly))
	h.add(scheduleEndArg, params.EndDate.Time.Format(time.DateOnly))
	h.add("days", scheduleSpanDays(params))
	h.add("game_type", scheduleGameType)
	if params.TeamID.Valid {
		h.add("team_id", params.TeamID.Int64)
	}
	h.add("games", games)
	h.add("nights_with_games", len(nights))
	h.add("light_nights", light)
	h.add("heavy_nights", heavy)
	h.add("light_night_max_games", lightNightMaxGames)
	h.add("heavy_night_min_games", heavyNightMinGames)

	teamsText, err := csvSection(scheduleTeamsSection, teams)
	if err != nil {
		return "", err
	}
	nightsText, err := csvSection(scheduleNightsSection, nightRows)
	if err != nil {
		return "", err
	}
	return h.String() + "\n" + teamsText + nightsText, nil
}

// nightClass classifies a night by its league-wide game count.
func nightClass(games int32) string {
	switch {
	case games <= lightNightMaxGames:
		return nightLight
	case games >= heavyNightMinGames:
		return nightHeavy
	default:
		return nightNormal
	}
}
