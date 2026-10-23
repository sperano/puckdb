package mcpserver

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type teamStandingsYahooQueries struct {
	*fakeYahooQueries
	latest       pgtype.Date
	latestErr    error
	latestCalls  []int32
	summaries    []sqlcdb.YahooTeamSummary
	summariesErr error
	summaryCalls []sqlcdb.GetYahooTeamSummarySnapshotParams
	stats        []sqlcdb.GetYahooTeamSummaryStatsSnapshotRow
	statsErr     error
	statsCalls   []sqlcdb.GetYahooTeamSummaryStatsSnapshotParams
}

func (q *teamStandingsYahooQueries) GetLatestYahooStandingsSnapshotDate(_ context.Context, leagueID int32) (pgtype.Date, error) {
	q.latestCalls = append(q.latestCalls, leagueID)
	return q.latest, q.latestErr
}

func (q *teamStandingsYahooQueries) GetYahooTeamSummarySnapshot(_ context.Context, arg sqlcdb.GetYahooTeamSummarySnapshotParams) ([]sqlcdb.YahooTeamSummary, error) {
	q.summaryCalls = append(q.summaryCalls, arg)
	return q.summaries, q.summariesErr
}

func (q *teamStandingsYahooQueries) GetYahooTeamSummaryStatsSnapshot(_ context.Context, arg sqlcdb.GetYahooTeamSummaryStatsSnapshotParams) ([]sqlcdb.GetYahooTeamSummaryStatsSnapshotRow, error) {
	q.statsCalls = append(q.statsCalls, arg)
	return q.stats, q.statsErr
}

func newTeamStandingsYahooQueries(date pgtype.Date) *teamStandingsYahooQueries {
	return &teamStandingsYahooQueries{fakeYahooQueries: newFakeYahooQueries(), latest: date}
}

func standingsTestDate() pgtype.Date {
	return pgtype.Date{Time: time.Date(2026, time.October, 21, 0, 0, 0, 0, time.UTC), Valid: true}
}

func capturedSummary(date pgtype.Date, teamID int32) sqlcdb.YahooTeamSummary {
	return sqlcdb.YahooTeamSummary{
		LeagueID: allowedLeagueID, TeamID: teamID, Date: date,
		StandingsCapturedAt: pgtype.Timestamptz{Time: date.Time.Add(12 * time.Hour), Valid: true},
		StandingsRank:       pgtype.Int4{Int32: teamID, Valid: true},
		H2hWins:             pgtype.Int4{Int32: 5, Valid: true},
		H2hLosses:           pgtype.Int4{Int32: 2, Valid: true},
		H2hTies:             pgtype.Int4{Int32: 1, Valid: true},
		H2hPercentage:       pgtype.Float8{Float64: 0.688, Valid: true},
	}
}

func csvRecord(t *testing.T, records [][]string, row int) map[string]string {
	t.Helper()
	record := make(map[string]string, len(records[0]))
	for i, name := range records[0] {
		record[name] = records[row][i]
	}
	return record
}

func TestYahooTeamStandingsUsesLatestSnapshotAndReturnsCategories(t *testing.T) {
	date := standingsTestDate()
	q := newTeamStandingsYahooQueries(date)
	q.summaries = []sqlcdb.YahooTeamSummary{capturedSummary(date, 2), capturedSummary(date, 7)}
	q.stats = []sqlcdb.GetYahooTeamSummaryStatsSnapshotRow{
		{TeamID: 2, StatID: 1, Value: pgtype.Float8{Float64: 12, Valid: true}, RawValue: "12",
			Name: pgtype.Text{String: "Goals", Valid: true}, Abbr: pgtype.Text{String: "G", Valid: true}},
		{TeamID: 2, StatID: 999, RawValue: "-"},
	}
	tool := yahooTestServer(q, nil).GetTool(yahooTeamStandingsToolName)

	result := callTool(t, tool.Handler, map[string]any{leagueIDArg: float64(allowedLeagueID)})
	require.False(t, result.IsError, resultText(t, result))
	records := parseCSV(t, resultText(t, result))
	require.Len(t, records, 4)
	assert.Equal(t, []int32{allowedLeagueID}, q.latestCalls)
	require.Len(t, q.summaryCalls, 1)
	require.Len(t, q.statsCalls, 1)
	assert.Equal(t, date, q.summaryCalls[0].Date)
	assert.Equal(t, sqlcdb.GetYahooTeamSummaryStatsSnapshotParams(q.summaryCalls[0]), q.statsCalls[0])

	known := csvRecord(t, records, 1)
	assert.Equal(t, "2026-10-21", known["date"])
	assert.Equal(t, "2", known["team_id"])
	assert.Equal(t, "5", known["wins"])
	assert.Equal(t, "0.688", known["percentage"])
	assert.Equal(t, "1", known["stat_id"])
	assert.Equal(t, "Goals", known["category_name"])
	assert.Equal(t, "G", known["category_abbr"])
	assert.Equal(t, "12", known["value"])

	unknown := csvRecord(t, records, 2)
	assert.Equal(t, "999", unknown["stat_id"])
	assert.Empty(t, unknown["category_name"])
	assert.Empty(t, unknown["value"])
	assert.Equal(t, "-", unknown["raw_value"])

	withoutCategories := csvRecord(t, records, 3)
	assert.Equal(t, "7", withoutCategories["team_id"])
	assert.Empty(t, withoutCategories["stat_id"])
	assert.Empty(t, withoutCategories["raw_value"])
}

func TestYahooTeamStandingsUsesExplicitDateAndTeam(t *testing.T) {
	date := standingsTestDate()
	const teamID int32 = 7
	q := newTeamStandingsYahooQueries(pgtype.Date{})
	q.summaries = []sqlcdb.YahooTeamSummary{capturedSummary(date, teamID)}
	tool := yahooTestServer(q, nil).GetTool(yahooTeamStandingsToolName)

	result := callTool(t, tool.Handler, map[string]any{
		leagueIDArg: float64(allowedLeagueID), "date": "2026-10-21", "team_id": float64(teamID),
	})
	require.False(t, result.IsError, resultText(t, result))
	assert.Empty(t, q.latestCalls)
	require.Len(t, q.summaryCalls, 1)
	assert.Equal(t, date, q.summaryCalls[0].Date)
	assert.Equal(t, pgtype.Int4{Int32: teamID, Valid: true}, q.summaryCalls[0].TeamID)
	require.Len(t, parseCSV(t, resultText(t, result)), 2)
}

func TestYahooTeamStandingsReportsMissingSnapshotAsNoResults(t *testing.T) {
	date := standingsTestDate()
	t.Run("no latest snapshot", func(t *testing.T) {
		q := newTeamStandingsYahooQueries(pgtype.Date{})
		tool := yahooTestServer(q, nil).GetTool(yahooTeamStandingsToolName)
		result := callTool(t, tool.Handler, map[string]any{leagueIDArg: float64(allowedLeagueID)})
		assert.False(t, result.IsError, resultText(t, result))
		assert.Equal(t, noResultsText, resultText(t, result))
		assert.Empty(t, q.summaryCalls)
		assert.Empty(t, q.statsCalls)
	})

	t.Run("explicit date has stats but no capture", func(t *testing.T) {
		q := newTeamStandingsYahooQueries(pgtype.Date{})
		q.summaries = []sqlcdb.YahooTeamSummary{{LeagueID: allowedLeagueID, TeamID: 2, Date: date}}
		tool := yahooTestServer(q, nil).GetTool(yahooTeamStandingsToolName)
		result := callTool(t, tool.Handler, map[string]any{leagueIDArg: float64(allowedLeagueID), "date": "2026-10-21"})
		assert.False(t, result.IsError, resultText(t, result))
		assert.Equal(t, noResultsText, resultText(t, result))
		assert.Empty(t, q.latestCalls)
		assert.Empty(t, q.statsCalls)
	})
}

func TestYahooTeamStandingsValidatesOptionalArgumentsBeforeQueries(t *testing.T) {
	tests := map[string]map[string]any{
		"bad date":         {"date": "2026-02-30"},
		"fractional team":  {"team_id": 2.5},
		"zero limit":       {"limit": float64(0)},
		"excess limit":     {"limit": float64(maxYahooTeamStandingsLimit + 1)},
		"fractional limit": {"limit": 1.5},
	}
	for name, extra := range tests {
		t.Run(name, func(t *testing.T) {
			q := newTeamStandingsYahooQueries(standingsTestDate())
			args := map[string]any{leagueIDArg: float64(allowedLeagueID)}
			for key, value := range extra {
				args[key] = value
			}
			result := callTool(t, yahooTestServer(q, nil).GetTool(yahooTeamStandingsToolName).Handler, args)
			assert.True(t, result.IsError)
			assert.Empty(t, q.latestCalls)
			assert.Empty(t, q.summaryCalls)
			assert.Empty(t, q.statsCalls)
		})
	}
}

func TestYahooTeamStandingsAppliesRowLimit(t *testing.T) {
	date := standingsTestDate()
	q := newTeamStandingsYahooQueries(date)
	q.summaries = []sqlcdb.YahooTeamSummary{capturedSummary(date, 2)}
	q.stats = []sqlcdb.GetYahooTeamSummaryStatsSnapshotRow{
		{TeamID: 2, StatID: 1}, {TeamID: 2, StatID: 2}, {TeamID: 2, StatID: 3},
	}
	tool := yahooTestServer(q, nil).GetTool(yahooTeamStandingsToolName)
	result := callTool(t, tool.Handler, map[string]any{leagueIDArg: float64(allowedLeagueID), "limit": float64(2)})

	require.False(t, result.IsError, resultText(t, result))
	records := parseCSV(t, resultText(t, result))
	require.Len(t, records, 3)
	assert.Equal(t, "1", csvRecord(t, records, 1)["stat_id"])
	assert.Equal(t, "2", csvRecord(t, records, 2)["stat_id"])
}

func TestYahooTeamStandingsReportsQueryErrors(t *testing.T) {
	date := standingsTestDate()
	tests := map[string]struct {
		configure func(*teamStandingsYahooQueries)
		want      string
	}{
		"latest date": {func(q *teamStandingsYahooQueries) { q.latestErr = errors.New("date failed") }, "load latest standings date"},
		"summaries": {func(q *teamStandingsYahooQueries) {
			q.summariesErr = errors.New("summaries failed")
		}, "load standings of league"},
		"categories": {func(q *teamStandingsYahooQueries) {
			q.summaries = []sqlcdb.YahooTeamSummary{capturedSummary(date, 2)}
			q.statsErr = errors.New("stats failed")
		}, "load standings categories of league"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			q := newTeamStandingsYahooQueries(date)
			tc.configure(q)
			result := callTool(t, yahooTestServer(q, nil).GetTool(yahooTeamStandingsToolName).Handler,
				map[string]any{leagueIDArg: float64(allowedLeagueID)})
			assert.True(t, result.IsError)
			assert.Contains(t, resultText(t, result), tc.want)
		})
	}
}
