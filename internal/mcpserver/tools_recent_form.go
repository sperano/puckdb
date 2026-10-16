package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

const (
	recentFormToolName = "get_recent_form"
	recentFormGroupArg = "group"
	recentFormAsOfArg  = "as_of"
	recentFormDaysArg  = "days"
	recentFormSection  = "players"

	recentFormSkaters = "skaters"
	recentFormGoalies = "goalies"

	// defaultRecentFormDays is the usual "last month" form window.
	defaultRecentFormDays = 30
	// maxRecentFormDays is a full year, longer than any regular season.
	maxRecentFormDays = 366
	// defaultRecentFormLimit covers the top of a waiver-wire search.
	defaultRecentFormLimit = 50
	// maxRecentFormLimit covers every skater who plays in a season.
	maxRecentFormLimit = 1000
	// minGoalieAppearanceSeconds leaves out relief stints shorter than five
	// minutes, as the goalie_recent_stats view does.
	minGoalieAppearanceSeconds = 300
	// recentFormGameType is the only game type counted: form is about the
	// regular season, as in the *_recent_stats views.
	recentFormGameType = sqlcdb.GameTypeRegularSeason
)

var (
	recentFormDaysParam  = intArg{name: recentFormDaysArg, min: minimumPositiveInteger, max: maxRecentFormDays}
	recentFormLimitParam = intArg{name: "limit", min: minimumPositiveInteger, max: maxRecentFormLimit}
)

type recentFormQueries interface {
	GetSkaterRecentForm(context.Context, sqlcdb.GetSkaterRecentFormParams) ([]sqlcdb.GetSkaterRecentFormRow, error)
	GetGoalieRecentForm(context.Context, sqlcdb.GetGoalieRecentFormParams) ([]sqlcdb.GetGoalieRecentFormRow, error)
}

// registerRecentFormTools adds get_recent_form; now supplies the default
// as_of (the server's local date).
func registerRecentFormTools(srv *server.MCPServer, queries recentFormQueries, now func() time.Time) {
	srv.AddTools(recentFormTool(queries, now))
}

// recentFormRequest is a validated get_recent_form call.
type recentFormRequest struct {
	group    string
	season   int32
	asOf     pgtype.Date
	days     int32
	position sqlcdb.NullPlayerPosition
	limit    int32
}

func recentFormTool(q recentFormQueries, now func() time.Time) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(recentFormToolName,
			mcp.WithDescription("Recent form: per-player regular-season totals over the days calendar days ending at as_of (both ends inclusive), "+
				"for skaters (ordered by points, then goals) or goalies (ordered by wins, then save percentage; "+
				fmt.Sprintf("appearances under %d seconds of ice time, i.e. relief stints, are left out). ", minGoalieAppearanceSeconds)+
				"Returns a '# key=value' header (group, season, window_start, as_of, days) and CSV rows; last_team_id is the club of the player's last game in the window. "+
				"Only games of the given season count, so a window reaching back past opening night holds fewer games."),
			mcp.WithNumber("season", mcp.Required(), mcp.Description("Season ID (e.g. 20252026)")),
			mcp.WithString(recentFormGroupArg, mcp.Required(), mcp.Description("skaters or goalies")),
			mcp.WithString(recentFormAsOfArg, mcp.Description("Last day of the window (YYYY-MM-DD, default: the server's local date today)")),
			mcp.WithNumber(recentFormDaysArg, mcp.Description(fmt.Sprintf("Window length in days, as_of included (default %d, max %d)", defaultRecentFormDays, maxRecentFormDays))),
			mcp.WithString(seasonPositionArg, mcp.Description("Optional NHL position filter: C, LW, RW, F or D for skaters, G for goalies")),
			mcp.WithNumber("limit", mcp.Description(fmt.Sprintf("Max players (default %d, max %d)", defaultRecentFormLimit, maxRecentFormLimit))),
		),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			r, errResult := parseRecentFormRequest(req, now)
			if errResult != nil {
				return errResult, nil
			}
			rows, err := r.run(ctx, q)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			section, err := csvSection(recentFormSection, rows)
			if err != nil {
				return nil, err
			}
			return mcp.NewToolResultText(r.header().String() + "\n" + section), nil
		},
	}
}

// parseRecentFormRequest validates every argument before any query runs.
func parseRecentFormRequest(req mcp.CallToolRequest, now func() time.Time) (recentFormRequest, *mcp.CallToolResult) {
	var r recentFormRequest
	var errResult *mcp.CallToolResult
	if r.season, errResult = requireInt[int32](req, seasonParam); errResult != nil {
		return r, errResult
	}
	if r.group, errResult = requireRecentFormGroup(req); errResult != nil {
		return r, errResult
	}
	if r.asOf, errResult = optionalDate(req, recentFormAsOfArg); errResult != nil {
		return r, errResult
	}
	if !r.asOf.Valid {
		r.asOf = localDate(now())
	}
	days, given, errResult := optionalInt[int32](req, recentFormDaysParam)
	if errResult != nil {
		return r, errResult
	}
	r.days = defaultRecentFormDays
	if given {
		r.days = days
	}
	if r.position, errResult = recentFormPosition(req, r.group); errResult != nil {
		return r, errResult
	}
	r.limit, errResult = boundedLimitOrDefault(req, recentFormLimitParam, defaultRecentFormLimit)
	return r, errResult
}

// requireRecentFormGroup reads the required group: skaters or goalies.
func requireRecentFormGroup(req mcp.CallToolRequest) (string, *mcp.CallToolResult) {
	raw, errResult := optionalString(req, recentFormGroupArg)
	if errResult != nil {
		return "", errResult
	}
	group := strings.ToLower(strings.TrimSpace(raw))
	switch group {
	case "":
		return "", mcp.NewToolResultError(recentFormGroupArg + " is required")
	case recentFormSkaters, recentFormGoalies:
		return group, nil
	default:
		return "", mcp.NewToolResultError(fmt.Sprintf("unknown %s %s; accepted: %s, %s",
			recentFormGroupArg, formatRaw(raw), recentFormSkaters, recentFormGoalies))
	}
}

// recentFormPosition reads the position filter allowed for group. The
// goalie query has no position parameter: G is the only goalie position.
func recentFormPosition(req mcp.CallToolRequest, group string) (sqlcdb.NullPlayerPosition, *mcp.CallToolResult) {
	if group == recentFormGoalies {
		return sqlcdb.NullPlayerPosition{}, requireGoaliePosition(req)
	}
	return requireSkaterPosition(req)
}

// localDate is t's calendar date in its own location, as a date argument.
func localDate(t time.Time) pgtype.Date {
	year, month, day := t.Date()
	return pgtype.Date{Time: time.Date(year, month, day, 0, 0, 0, 0, time.UTC), Valid: true}
}

// windowStart is the first day of the window, matching the queries'
// game_date > as_of - days.
func (r recentFormRequest) windowStart() time.Time {
	return r.asOf.Time.AddDate(0, 0, 1-int(r.days))
}

func (r recentFormRequest) run(ctx context.Context, q recentFormQueries) (any, error) {
	if r.group == recentFormGoalies {
		return q.GetGoalieRecentForm(ctx, sqlcdb.GetGoalieRecentFormParams{
			Season: r.season, GameType: recentFormGameType, AsOf: r.asOf, Days: r.days,
			MinToiSeconds: minGoalieAppearanceSeconds, ResultLimit: r.limit,
		})
	}
	return q.GetSkaterRecentForm(ctx, sqlcdb.GetSkaterRecentFormParams{
		Season: r.season, GameType: recentFormGameType, AsOf: r.asOf, Days: r.days,
		Position: r.position, ResultLimit: r.limit,
	})
}

// header prints the resolved window once, so a defaulted as_of is visible.
func (r recentFormRequest) header() metadataHeader {
	var h metadataHeader
	h.add(recentFormGroupArg, r.group)
	h.add("season", r.season)
	h.add("game_type", recentFormGameType)
	h.add("window_start", r.windowStart().Format(time.DateOnly))
	h.add(recentFormAsOfArg, r.asOf.Time.Format(time.DateOnly))
	h.add(recentFormDaysArg, r.days)
	if r.position.Valid {
		h.add(seasonPositionArg, r.position.PlayerPosition)
	}
	return h
}
