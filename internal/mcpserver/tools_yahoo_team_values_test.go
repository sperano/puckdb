package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/projection"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	valuesTestStart      = "2026-10-07"
	valuesTestSkaterA    = 11
	valuesTestSkaterB    = 12
	valuesTestDropped    = 13
	valuesTestUnmatched  = 14
	valuesTestOtherSktr  = 15
	valuesTestGoalieE    = 21
	valuesTestOtherGoal  = 22
	valuesTestNHLOffset  = 8400000
	valuesTestProjGames  = 80
	valuesTestProjGoals  = 40
	valuesTestGoalieTOI  = 3600
	valuesTestSkaterGP   = 10
	valuesTestSeasonYear = 2026
)

var (
	valuesTestProjectionID = uuid.MustParse("c3e60f01-d2e6-4349-96eb-a3d1b4b55983")
	valuesTestProjectionAt = time.Date(2026, time.September, 28, 0, 0, 0, 0, time.UTC)
)

// teamValuesQueries serves get_yahoo_team_player_values's queries.
type teamValuesQueries struct {
	*fakeYahooQueries
	team      sqlcdb.YahooTeam
	teamErr   error
	latest    pgtype.Date
	latestErr error
	// rosterDays are the league's imported roster days that
	// GetLatestYahooLeagueRosterDateOnOrBefore searches, or onOrBeforeErr.
	rosterDays     []pgtype.Date
	onOrBeforeErr  error
	onOrBeforeArgs []sqlcdb.GetLatestYahooLeagueRosterDateOnOrBeforeParams
	categories     []sqlcdb.YahooLeagueStatCategory
	categoriesErr  error
	rows           []sqlcdb.ListYahooLeaguePlayerWindowTotalsRow
	rowsErr        error
	totalsCalls    []sqlcdb.ListYahooLeaguePlayerWindowTotalsParams
	projection     []sqlcdb.ListProjectionValuesByNHLPlayersRow
	projectionErr  error
	projectionArgs []sqlcdb.ListProjectionValuesByNHLPlayersParams
}

func (q *teamValuesQueries) GetYahooTeam(_ context.Context, arg sqlcdb.GetYahooTeamParams) (sqlcdb.YahooTeam, error) {
	if q.teamErr != nil {
		return sqlcdb.YahooTeam{}, q.teamErr
	}
	if arg.LeagueID != q.team.LeagueID || arg.ID != q.team.ID {
		return sqlcdb.YahooTeam{}, pgx.ErrNoRows
	}
	return q.team, nil
}

// errWrongLeague fails a lookup made for another league than the team's, so
// a cross-league query cannot pass unnoticed.
var errWrongLeague = errors.New("query for another league than the team's")

func (q *teamValuesQueries) checkLeague(leagueID int32) error {
	if leagueID != q.team.LeagueID {
		return fmt.Errorf("%w: got %d, want %d", errWrongLeague, leagueID, q.team.LeagueID)
	}
	return nil
}

func (q *teamValuesQueries) GetLatestYahooLeagueRosterDate(_ context.Context, leagueID int32) (pgtype.Date, error) {
	if err := q.checkLeague(leagueID); err != nil {
		return pgtype.Date{}, err
	}
	return q.latest, q.latestErr
}

func (q *teamValuesQueries) GetLatestYahooLeagueRosterDateOnOrBefore(_ context.Context, arg sqlcdb.GetLatestYahooLeagueRosterDateOnOrBeforeParams) (pgtype.Date, error) {
	q.onOrBeforeArgs = append(q.onOrBeforeArgs, arg)
	if err := q.checkLeague(arg.LeagueID); err != nil {
		return pgtype.Date{}, err
	}
	var found pgtype.Date
	for _, day := range q.rosterDays {
		if !day.Time.After(arg.OnOrBefore.Time) && (!found.Valid || day.Time.After(found.Time)) {
			found = day
		}
	}
	return found, q.onOrBeforeErr
}

func (q *teamValuesQueries) GetYahooLeagueStatCategories(_ context.Context, leagueID int32) ([]sqlcdb.YahooLeagueStatCategory, error) {
	if err := q.checkLeague(leagueID); err != nil {
		return nil, err
	}
	return q.categories, q.categoriesErr
}

func (q *teamValuesQueries) ListYahooLeaguePlayerWindowTotals(_ context.Context, arg sqlcdb.ListYahooLeaguePlayerWindowTotalsParams) ([]sqlcdb.ListYahooLeaguePlayerWindowTotalsRow, error) {
	q.totalsCalls = append(q.totalsCalls, arg)
	if err := q.checkLeague(arg.LeagueID); err != nil {
		return nil, err
	}
	return q.rows, q.rowsErr
}

func (q *teamValuesQueries) ListProjectionValuesByNHLPlayers(_ context.Context, arg sqlcdb.ListProjectionValuesByNHLPlayersParams) ([]sqlcdb.ListProjectionValuesByNHLPlayersRow, error) {
	q.projectionArgs = append(q.projectionArgs, arg)
	return q.projection, q.projectionErr
}

// fakeRankingReader returns read (or err) and records the references read.
type fakeRankingReader struct {
	read  draftrank.Read
	err   error
	calls []draftrank.LeagueRef
}

func (f *fakeRankingReader) Read(_ context.Context, ref draftrank.LeagueRef, _ uuid.UUID) (draftrank.Read, error) {
	f.calls = append(f.calls, ref)
	return f.read, f.err
}

func servedProjection() *fakeRankingReader {
	snapshot := &draftrank.Snapshot{}
	snapshot.Projection = draftrank.ProjectionInfo{SnapshotID: valuesTestProjectionID, AsOf: valuesTestProjectionAt}
	return &fakeRankingReader{read: draftrank.Read{Status: draftrank.StatusReady, Snapshot: snapshot}}
}

func valuesRow(team, yahooID int32, positionType, last string, days int32) sqlcdb.ListYahooLeaguePlayerWindowTotalsRow {
	row := windowRow(team, yahooID, int64(valuesTestNHLOffset+yahooID), positionType, last)
	row.PlayerKey, row.Name, row.Days, row.ActiveDays = fmt.Sprintf("465.p.%d", yahooID), fmt.Sprintf("Player %d", yahooID), days, days
	return row
}

func skaterRow(team, yahooID int32, last string, goals int64) sqlcdb.ListYahooLeaguePlayerWindowTotalsRow {
	row := valuesRow(team, yahooID, skaterPositionType, last, valuesTestSkaterGP)
	row.SkaterGames, row.Goals = valuesTestSkaterGP, goals
	return row
}

func goalieRow(team, yahooID int32, goalsAgainst, wins int64) sqlcdb.ListYahooLeaguePlayerWindowTotalsRow {
	row := valuesRow(team, yahooID, goaliePositionType, valuesTestToString, 1)
	row.GoalieGames, row.GoalsAgainst, row.Wins, row.GoalieTOISeconds = 1, goalsAgainst, wins, valuesTestGoalieTOI
	return row
}

func newTeamValuesQueries() *teamValuesQueries {
	base := newFakeYahooQueries()
	for i := range base.leagues {
		base.leagues[i].Season = valuesTestSeasonYear
		base.leagues[i].StartDate = mustDate(valuesTestStart)
	}
	unmatched := valuesRow(valuesTestTeam, valuesTestUnmatched, skaterPositionType, valuesTestToString, 2)
	unmatched.NhlPlayerID, unmatched.Name = pgtype.Int8{}, "Pool Only"
	return &teamValuesQueries{
		fakeYahooQueries: base,
		team:             sqlcdb.YahooTeam{LeagueID: allowedLeagueID, ID: valuesTestTeam, Name: "My Team"},
		latest:           valuesTestTo,
		rosterDays:       []pgtype.Date{mustDate(valuesTestStart), mustDate("2026-11-08"), mustDate("2026-11-10"), valuesTestTo},
		categories: []sqlcdb.YahooLeagueStatCategory{
			category(statGAA, "GAA", "G", sortLower, true), category(statGoals, "G", "P", sortHigher, true),
			category(statWins, "W", "G", sortHigher, true), category(statUnsupported, "XYZ", "P", sortHigher, true),
		},
		// Compared skaters A (6), B (2) and the other team's (4): mean 4,
		// standard deviation sqrt(8/3). Compared goalies E (2 GA, a win) and
		// the other team's (4 GA, no win) in an hour each.
		rows: []sqlcdb.ListYahooLeaguePlayerWindowTotalsRow{
			skaterRow(valuesTestTeam, valuesTestSkaterB, valuesTestToString, 2),
			skaterRow(valuesTestTeam, valuesTestSkaterA, valuesTestToString, 6),
			skaterRow(valuesTestTeam, valuesTestDropped, "2026-11-01", 1),
			unmatched,
			skaterRow(valuesTestOtherTm, valuesTestOtherSktr, valuesTestToString, 4),
			goalieRow(valuesTestTeam, valuesTestGoalieE, 2, 1),
			goalieRow(valuesTestOtherTm, valuesTestOtherGoal, 4, 0),
		},
		projection: []sqlcdb.ListProjectionValuesByNHLPlayersRow{
			{NhlPlayerID: valuesTestNHLOffset + valuesTestSkaterA, PlayerKey: "nhl.p.a", Stat: string(projection.StatGamesPlayed), Mean: valuesTestProjGames},
			{NhlPlayerID: valuesTestNHLOffset + valuesTestSkaterA, PlayerKey: "nhl.p.a", Stat: string(projection.StatGoals), Mean: valuesTestProjGoals},
		},
	}
}

func callTeamValues(t *testing.T, q yahooQueries, rankings draftRankingReader, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	tool := yahooTeamPlayerValuesTool(q, newLeagueGuard(q, []string{allowedLeagueKey}), rankings)
	full := map[string]any{leagueIDArg: float64(allowedLeagueID), yahooTeamIDParam.name: float64(valuesTestTeam)}
	for key, value := range args {
		full[key] = value
	}
	return callTool(t, tool.Handler, full)
}

func TestTeamValuesRanksTheTeamInTheLeaguesCategories(t *testing.T) {
	q, rankings := newTeamValuesQueries(), servedProjection()
	result := callTeamValues(t, q, rankings, nil)
	require.False(t, result.IsError, resultText(t, result))
	want := strings.Join([]string{
		fmt.Sprintf("# league_id=%d league_key=%s season=2026 team_id=%d team=\"My Team\" from=%s to=%s "+
			"latest_roster_date=%s active_only=false compared_skaters=3 compared_goalies=2 projection=%s "+
			"projection_as_of=2026-09-28T00:00:00Z unsupported_categories=XYZ",
			allowedLeagueID, allowedLeagueKey, valuesTestTeam, valuesTestStart, valuesTestToString, valuesTestToString, valuesTestProjectionID),
		"# categories",
		"column,stat_id,name,position_type,direction,numerator,denominator,in_value",
		"G,1,G name,P,higher,,,true",
		"W,19,W name,G,higher,,,true",
		"GAA,23,GAA name,G,lower,goals_against,toi_seconds,true",
		"# skaters",
		"rank,player_key,nhl_player_id,name,on_roster,days,active_days,games,value,G,G_z,G_pace",
		// A: (6-4)/sqrt(8/3) = 1.22; pace 40/80 goals a game × 10 games.
		"1,465.p.11,8400011,Player 11,true,10,10,10,1.2,6,1.2,5",
		"2,465.p.12,8400012,Player 12,true,10,10,10,-1.2,2,-1.2,",
		",465.p.13,8400013,Player 13,false,10,10,10,-1.8,1,-1.8,",
		"# goalies",
		"rank,player_key,nhl_player_id,name,on_roster,days,active_days,games,value,W,W_z,W_pace,GAA,GAA_num,GAA_den,GAA_z,GAA_pace",
		// W: (1-0.5)/0.5; GAA: 1 goal under the pooled 3 an hour, spread 1.
		"1,465.p.21,8400021,Player 21,true,1,1,1,2,1,1,,2.000,2,3600,1,",
		"# unmatched",
		"player_key,yahoo_player_id,name,position_type,on_roster,days,active_days",
		"465.p.14,14,Pool Only,P,true,2,2",
		"",
	}, "\n")
	assert.Equal(t, want, resultText(t, result))
	assert.Equal(t, []draftrank.LeagueRef{{Season: valuesTestSeasonYear, LeagueID: int(allowedLeagueID)}}, rankings.calls)
	require.Len(t, q.projectionArgs, 1)
	assert.Equal(t, pgtype.UUID{Bytes: valuesTestProjectionID, Valid: true}, q.projectionArgs[0].SnapshotID)
	assert.ElementsMatch(t, []int64{8400011, 8400012, 8400013, 8400021}, q.projectionArgs[0].NhlPlayerIds,
		"only the team's matched players")
	require.Len(t, q.totalsCalls, 1)
	assert.Equal(t, sqlcdb.ListYahooLeaguePlayerWindowTotalsParams{
		LeagueKey: allowedLeagueKey, InactiveSlots: []string{"BN", "IR", "IR+", "NA"}, LeagueID: allowedLeagueID,
		FromDate: mustDate(valuesTestStart), ToDate: valuesTestTo, MinimumShutoutToiSeconds: projection.DefaultGoalieShutoutMinTOI,
	}, q.totalsCalls[0])
}

func TestTeamValuesWindow(t *testing.T) {
	for _, tc := range []struct {
		name              string
		args              map[string]any
		wantFrom, wantTo  string
		wantHeaderSnippet string
	}{
		{"defaults", nil, valuesTestStart, valuesTestToString, " to=" + valuesTestToString + " latest_roster_date="},
		{"to after the latest roster", map[string]any{teamValuesToArg: "2026-11-25"}, valuesTestStart, valuesTestToString,
			" to=" + valuesTestToString + " requested_to=2026-11-25 latest_roster_date="},
		{"explicit window", map[string]any{teamValuesFromArg: "2026-11-01", teamValuesToArg: "2026-11-10", activeOnlyArg: true},
			"2026-11-01", "2026-11-10", " from=2026-11-01 to=2026-11-10 latest_roster_date=2026-11-19 active_only=true "},
		{"to on a day without a roster", map[string]any{teamValuesFromArg: "2026-11-01", teamValuesToArg: "2026-11-09"},
			"2026-11-01", "2026-11-08", " from=2026-11-01 to=2026-11-08 requested_to=2026-11-09 latest_roster_date=2026-11-19 "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := newTeamValuesQueries()
			result := callTeamValues(t, q, servedProjection(), tc.args)
			require.False(t, result.IsError, resultText(t, result))
			assert.Contains(t, resultText(t, result), tc.wantHeaderSnippet)
			require.Len(t, q.totalsCalls, 1)
			assert.Equal(t, mustDate(tc.wantFrom), q.totalsCalls[0].FromDate)
			assert.Equal(t, mustDate(tc.wantTo), q.totalsCalls[0].ToDate)
			assert.Equal(t, tc.args[activeOnlyArg] == true, q.totalsCalls[0].ActiveOnly)
		})
	}
}

func TestTeamValuesErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   map[string]any
		change func(*teamValuesQueries, *fakeRankingReader)
		want   string
	}{
		{"from after to", map[string]any{teamValuesFromArg: "2026-11-15", teamValuesToArg: "2026-11-10"}, nil,
			"from 2026-11-15 is after to 2026-11-10"},
		{"from after a to moved back", map[string]any{teamValuesFromArg: "2026-11-09", teamValuesToArg: "2026-11-09"}, nil,
			"from 2026-11-09 is after to 2026-11-09, moved back to 2026-11-08, the latest imported roster day on or before it"},
		{"from after a to past the latest roster", map[string]any{teamValuesFromArg: "2026-11-21", teamValuesToArg: "2026-11-25"}, nil,
			"from 2026-11-21 is after to 2026-11-25, moved back to " + valuesTestToString + ", the latest imported roster day on or before it"},
		{"no season start", nil, func(q *teamValuesQueries, _ *fakeRankingReader) {
			q.leagues[0].StartDate = pgtype.Date{}
		}, fmt.Sprintf("league %d has no season start date: pass from", allowedLeagueID)},
		{"no rosters", nil, func(q *teamValuesQueries, _ *fakeRankingReader) { q.latest = pgtype.Date{} },
			fmt.Sprintf("league %d: no roster of this league has been imported", allowedLeagueID)},
		{"unknown team", map[string]any{yahooTeamIDParam.name: float64(valuesTestOtherTm)}, nil,
			fmt.Sprintf("team %d not found in league %d (use get_yahoo_teams_by_league)", valuesTestOtherTm, allowedLeagueID)},
		{"team error", nil, func(q *teamValuesQueries, _ *fakeRankingReader) { q.teamErr = errors.New("boom") },
			fmt.Sprintf("load team %d of league %d: boom", valuesTestTeam, allowedLeagueID)},
		{"to before the first roster", map[string]any{teamValuesFromArg: "2026-10-01", teamValuesToArg: "2026-10-06"}, nil,
			fmt.Sprintf("league %d has no roster imported on or before to 2026-10-06; its first imported rosters come later",
				allowedLeagueID)},
		{"roster date error", map[string]any{teamValuesToArg: "2026-11-09"},
			func(q *teamValuesQueries, _ *fakeRankingReader) { q.onOrBeforeErr = errors.New("boom") },
			fmt.Sprintf("load roster date of league %d on or before 2026-11-09: boom", allowedLeagueID)},
		{"latest error", nil, func(q *teamValuesQueries, _ *fakeRankingReader) { q.latestErr = errors.New("boom") },
			fmt.Sprintf("load latest roster date of league %d: boom", allowedLeagueID)},
		{"categories error", nil, func(q *teamValuesQueries, _ *fakeRankingReader) { q.categoriesErr = errors.New("boom") },
			fmt.Sprintf("load stat categories of league %d: boom", allowedLeagueID)},
		{"totals error", nil, func(q *teamValuesQueries, _ *fakeRankingReader) { q.rowsErr = errors.New("boom") },
			fmt.Sprintf("load player totals of league %d: boom", allowedLeagueID)},
		{"ranking error", nil, func(_ *teamValuesQueries, r *fakeRankingReader) { r.err = errors.New("boom") },
			fmt.Sprintf("load draft ranking of league %d: boom", allowedLeagueID)},
		{"projection error", nil, func(q *teamValuesQueries, _ *fakeRankingReader) { q.projectionErr = errors.New("boom") },
			fmt.Sprintf("load projection %s: boom", valuesTestProjectionID)},
		{"team_id missing", map[string]any{yahooTeamIDParam.name: nil}, nil, "team_id is required"},
		{"bad from", map[string]any{teamValuesFromArg: "2026-13-01"}, nil, `invalid from "2026-13-01": want a date as YYYY-MM-DD`},
		{"bad active_only", map[string]any{activeOnlyArg: "maybe"}, nil, `invalid active_only "maybe": want true or false`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, rankings := newTeamValuesQueries(), servedProjection()
			if tc.change != nil {
				tc.change(q, rankings)
			}
			result := callTeamValues(t, q, rankings, tc.args)
			assert.True(t, result.IsError)
			assert.Equal(t, tc.want, resultText(t, result))
		})
	}
}

func TestTeamValuesRefusesUnknownLeagueBeforeQuerying(t *testing.T) {
	q := newTeamValuesQueries()
	q.leagues = q.leagues[1:] // the allowlisted league has no yahoo_leagues row
	result := callTeamValues(t, q, servedProjection(), nil)
	assert.True(t, result.IsError)
	assert.Equal(t, fmt.Sprintf("unknown league_id %d", allowedLeagueID), resultText(t, result))
	assert.Empty(t, q.totalsCalls)
}

func TestTeamValuesWithoutProjection(t *testing.T) {
	for name, rankings := range map[string]*fakeRankingReader{
		"no snapshot":   {read: draftrank.Read{Status: draftrank.StatusNotComputed}},
		"no rules":      {err: fmt.Errorf("%w: no rules", draftrank.ErrUnknownLeague)},
		"no projection": {read: draftrank.Read{Status: draftrank.StatusReady, Snapshot: &draftrank.Snapshot{}}},
	} {
		t.Run(name, func(t *testing.T) {
			q := newTeamValuesQueries()
			result := callTeamValues(t, q, rankings, nil)
			text := resultText(t, result)
			require.False(t, result.IsError, text)
			assert.Contains(t, text, " projection=none ")
			assert.Contains(t, text, "\nrank,player_key,nhl_player_id,name,on_roster,days,active_days,games,value,G,G_z\n")
			assert.NotContains(t, text, paceSuffix)
			assert.Empty(t, q.projectionArgs)
		})
	}
}

func TestTeamValuesEmptySections(t *testing.T) {
	q := newTeamValuesQueries()
	q.rows = nil
	result := callTeamValues(t, q, servedProjection(), nil)
	text := resultText(t, result)
	require.False(t, result.IsError, text)
	assert.Contains(t, text, " compared_skaters=0 compared_goalies=0 ")
	assert.True(t, strings.HasSuffix(text, "# skaters\nno results\n# goalies\nno results\n# unmatched\nno results\n"), text)
}

func TestTeamValuesRefusesAnOversizedResult(t *testing.T) {
	q := newTeamValuesQueries()
	const droppedPlayers = 3000
	for i := range droppedPlayers {
		q.rows = append(q.rows, skaterRow(valuesTestTeam, int32(1000+i), "2026-10-10", int64(i)))
	}
	result := callTeamValues(t, q, servedProjection(), nil)
	assert.True(t, result.IsError)
	assert.Regexp(t, `^team values result is \d+ bytes; maximum is 65536: shorten the window with from$`, resultText(t, result))
}
