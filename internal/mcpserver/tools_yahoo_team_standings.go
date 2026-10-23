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

const (
	yahooTeamStandingsToolName = "get_yahoo_team_standings"
	// typicalYahooTeamStandingsCategories allows for common scoring
	// categories plus Yahoo's display-only values.
	typicalYahooTeamStandingsCategories = 30
	// defaultYahooTeamStandingsLimit covers a normal full league snapshot.
	defaultYahooTeamStandingsLimit = maxLeagueTeams * typicalYahooTeamStandingsCategories
	// maxYahooTeamStandingsLimit bounds unusual leagues and imported stats.
	maxYahooTeamStandingsLimit = 1000
)

var yahooTeamStandingsLimitParam = intArg{
	name: "limit",
	min:  minimumPositiveInteger,
	max:  maxYahooTeamStandingsLimit,
}

// yahooTeamStandingRow is one category of one team's captured standings
// snapshot. Category fields are empty when the team has no category values.
type yahooTeamStandingRow struct {
	Date               pgtype.Date   `json:"date"`
	TeamID             int32         `json:"team_id"`
	Rank               pgtype.Int4   `json:"rank"`
	PlayoffSeed        pgtype.Int4   `json:"playoff_seed"`
	Wins               pgtype.Int4   `json:"wins"`
	Losses             pgtype.Int4   `json:"losses"`
	Ties               pgtype.Int4   `json:"ties"`
	Percentage         pgtype.Float8 `json:"percentage"`
	DivisionWins       pgtype.Int4   `json:"division_wins"`
	DivisionLosses     pgtype.Int4   `json:"division_losses"`
	DivisionTies       pgtype.Int4   `json:"division_ties"`
	DivisionPercentage pgtype.Float8 `json:"division_percentage"`
	StatID             pgtype.Int4   `json:"stat_id"`
	CategoryName       pgtype.Text   `json:"category_name"`
	CategoryAbbr       pgtype.Text   `json:"category_abbr"`
	Value              pgtype.Float8 `json:"value"`
	RawValue           string        `json:"raw_value"`
}

type yahooTeamStandingsParams struct {
	LeagueID int32
	Date     pgtype.Date
	TeamID   pgtype.Int4
	Limit    int32
}

func yahooTeamStandingsTool(q yahooQueries, guard leagueGuard) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(yahooTeamStandingsToolName,
			mcp.WithDescription("Get a dated Yahoo head-to-head standings snapshot as CSV, with one row per team and category. "+
				"Records repeat across a team's category rows; a team without category values has one row with empty category fields. "+
				"Omit date for the league's latest captured snapshot. An explicit date is never backfilled from a later snapshot. "+
				"Rows are ordered by team_id and stat_id; limit counts rows and may truncate a team's category list."),
			mcp.WithNumber(leagueIDArg, mcp.Required(), mcp.Description("Yahoo league ID (use get_yahoo_leagues to find IDs)")),
			mcp.WithString("date", mcp.Description("Snapshot date in YYYY-MM-DD format; omit for latest")),
			mcp.WithNumber(yahooTeamIDParam.name, mcp.Description("Optional Yahoo team ID"),
				mcp.Min(float64(yahooTeamIDParam.min)), mcp.Max(float64(yahooTeamIDParam.max)), mcp.MultipleOf(minimumPositiveInteger)),
			mcp.WithNumber(yahooTeamStandingsLimitParam.name, mcp.Description(fmt.Sprintf("Max CSV rows (default %d, max %d)",
				defaultYahooTeamStandingsLimit, maxYahooTeamStandingsLimit))),
		),
		Handler: guard.scoped(func(ctx context.Context, req mcp.CallToolRequest, leagueID int32) (*mcp.CallToolResult, error) {
			params, errResult := readYahooTeamStandingsParams(req, leagueID)
			if errResult != nil {
				return errResult, nil
			}
			rows, err := loadYahooTeamStandings(ctx, q, params)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return ResultCSV(rows)
		}),
	}
}

func readYahooTeamStandingsParams(req mcp.CallToolRequest, leagueID int32) (yahooTeamStandingsParams, *mcp.CallToolResult) {
	params := yahooTeamStandingsParams{LeagueID: leagueID}
	var errResult *mcp.CallToolResult
	if params.Date, errResult = optionalDate(req, "date"); errResult != nil {
		return params, errResult
	}
	if params.TeamID, errResult = optionalInt4Filter(req, yahooTeamIDParam); errResult != nil {
		return params, errResult
	}
	params.Limit, errResult = boundedLimitOrDefault(req, yahooTeamStandingsLimitParam, defaultYahooTeamStandingsLimit)
	return params, errResult
}

func loadYahooTeamStandings(ctx context.Context, q yahooQueries, params yahooTeamStandingsParams) ([]yahooTeamStandingRow, error) {
	date, err := resolveYahooTeamStandingsDate(ctx, q, params.LeagueID, params.Date)
	if err != nil || !date.Valid {
		return nil, err
	}
	queryParams := sqlcdb.GetYahooTeamSummarySnapshotParams{
		LeagueID: params.LeagueID,
		Date:     date,
		TeamID:   params.TeamID,
	}
	summaries, err := q.GetYahooTeamSummarySnapshot(ctx, queryParams)
	if err != nil {
		return nil, fmt.Errorf("load standings of league %d on %s: %w", params.LeagueID, date.Time.Format(time.DateOnly), err)
	}
	summaries = capturedYahooTeamSummaries(summaries)
	if len(summaries) == 0 {
		return nil, nil
	}
	stats, err := q.GetYahooTeamSummaryStatsSnapshot(ctx, sqlcdb.GetYahooTeamSummaryStatsSnapshotParams(queryParams))
	if err != nil {
		return nil, fmt.Errorf("load standings categories of league %d on %s: %w", params.LeagueID, date.Time.Format(time.DateOnly), err)
	}
	return yahooTeamStandingRows(summaries, stats, params.Limit), nil
}

func resolveYahooTeamStandingsDate(ctx context.Context, q yahooQueries, leagueID int32, date pgtype.Date) (pgtype.Date, error) {
	if date.Valid {
		return date, nil
	}
	latest, err := q.GetLatestYahooStandingsSnapshotDate(ctx, leagueID)
	if err != nil {
		return pgtype.Date{}, fmt.Errorf("load latest standings date of league %d: %w", leagueID, err)
	}
	return latest, nil
}

func capturedYahooTeamSummaries(summaries []sqlcdb.YahooTeamSummary) []sqlcdb.YahooTeamSummary {
	captured := make([]sqlcdb.YahooTeamSummary, 0, len(summaries))
	for _, summary := range summaries {
		if summary.StandingsCapturedAt.Valid {
			captured = append(captured, summary)
		}
	}
	return captured
}

func yahooTeamStandingRows(summaries []sqlcdb.YahooTeamSummary, stats []sqlcdb.GetYahooTeamSummaryStatsSnapshotRow, limit int32) []yahooTeamStandingRow {
	statsByTeam := make(map[int32][]sqlcdb.GetYahooTeamSummaryStatsSnapshotRow, len(summaries))
	for _, stat := range stats {
		statsByTeam[stat.TeamID] = append(statsByTeam[stat.TeamID], stat)
	}
	rows := make([]yahooTeamStandingRow, 0, min(len(stats), int(limit)))
	for _, summary := range summaries {
		teamStats := statsByTeam[summary.TeamID]
		if len(teamStats) == 0 {
			rows = append(rows, yahooTeamStandingRowOf(summary, nil))
		} else {
			for i := range teamStats {
				rows = append(rows, yahooTeamStandingRowOf(summary, &teamStats[i]))
			}
		}
		if len(rows) >= int(limit) {
			return rows[:limit]
		}
	}
	return rows
}

func yahooTeamStandingRowOf(summary sqlcdb.YahooTeamSummary, stat *sqlcdb.GetYahooTeamSummaryStatsSnapshotRow) yahooTeamStandingRow {
	row := yahooTeamStandingRow{
		Date: summary.Date, TeamID: summary.TeamID, Rank: summary.StandingsRank,
		PlayoffSeed: summary.StandingsPlayoffSeed, Wins: summary.H2hWins,
		Losses: summary.H2hLosses, Ties: summary.H2hTies, Percentage: summary.H2hPercentage,
		DivisionWins: summary.H2hDivisionWins, DivisionLosses: summary.H2hDivisionLosses,
		DivisionTies: summary.H2hDivisionTies, DivisionPercentage: summary.H2hDivisionPercentage,
	}
	if stat != nil {
		row.StatID = pgtype.Int4{Int32: stat.StatID, Valid: true}
		row.CategoryName = stat.Name
		row.CategoryAbbr = stat.Abbr
		row.Value = stat.Value
		row.RawValue = stat.RawValue
	}
	return row
}
