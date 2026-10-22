package yahoo

// PostgreSQL-backed tests of the H2H standings and normalized category
// import: ImportYahooDataForDate writing through the real queries and
// migrations, and the snapshot reads the MCP standings tool uses. They skip
// unless PUCKDB_TEST_PG_URL names a test database.

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/fixtures/yahoofixtures"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

// standingsPGOtherLeague is a second league of the fixture's season, its
// standings relabeled from the points league's.
const standingsPGOtherLeague = 88888

var (
	standingsPGDay1 = time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	standingsPGDay2 = time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	// standingsPGCaptured is the standings files' modification time.
	standingsPGCaptured = time.Date(2026, 11, 14, 16, 30, 0, 0, time.UTC)
)

type standingsPGFixture struct {
	pool    *pgxpool.Pool
	q       *sqlcdb.Queries
	storage *store.MemStorage
	acts    *ImportActivities
	env     *testsuite.TestActivityEnvironment
}

// newStandingsPGFixture seeds the points league (teams 1-4) and a second
// league (team 1), and the points league's Goals category definition.
func newStandingsPGFixture(t *testing.T) *standingsPGFixture {
	pool := openDraftTestDB(t)
	ctx := context.Background()
	for _, leagueID := range []int{yahoofixtures.PointsLeagueID, standingsPGOtherLeague} {
		key := fmt.Sprintf("%d.l.%d", yahoofixtures.PointsGameKey, leagueID)
		_, err := pool.Exec(ctx, `INSERT INTO yahoo_leagues (id, league_key, name, season) VALUES ($1, $2, 'League', $3)`,
			leagueID, key, yahoofixtures.PointsSeason)
		require.NoError(t, err)
	}
	for _, team := range []TeamInfo{pointsTeam(1), pointsTeam(2), pointsTeam(3), pointsTeam(4), {LeagueID: standingsPGOtherLeague, TeamID: 1}} {
		key := fmt.Sprintf("%d.l.%d.t.%d", yahoofixtures.PointsGameKey, team.LeagueID, team.TeamID)
		_, err := pool.Exec(ctx, `INSERT INTO yahoo_teams (league_id, id, team_key, name) VALUES ($1, $2, $3, 'Team')`,
			team.LeagueID, team.TeamID, key)
		require.NoError(t, err)
	}
	_, err := pool.Exec(ctx, `INSERT INTO yahoo_league_stat_categories (league_id, stat_id, name, abbr, display_name, position_type, sort_order)
		VALUES ($1, 1, 'Goals', 'G', 'G', 'P', 1)`, yahoofixtures.PointsLeagueID)
	require.NoError(t, err)

	mem := store.NewMemStorage()
	acts := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil), Queries: sqlcdb.New(pool)}
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(acts.ImportYahooDataForDate)
	return &standingsPGFixture{pool: pool, q: sqlcdb.New(pool), storage: mem, acts: acts, env: env}
}

// writeSummary caches a team's daily stats file with stats as stat ID/value pairs.
func (f *standingsPGFixture) writeSummary(team TeamInfo, day time.Time, stats ...[2]string) {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><fantasy_content><team><team_stats><coverage_type>date</coverage_type><stats>`)
	for _, stat := range stats {
		fmt.Fprintf(&b, `<stat><stat_id>%s</stat_id><value>%s</value></stat>`, stat[0], stat[1])
	}
	b.WriteString(`</stats></team_stats></team></fantasy_content>`)
	f.storage.SetFile(resource.TeamSummary{LeagueID: team.LeagueID, TeamID: team.TeamID, Date: day}.Path(), []byte(b.String()))
}

// writeStandings caches a league's standings snapshot of day from a points
// league fixture, relabeled to leagueID.
func (f *standingsPGFixture) writeStandings(leagueID int, day time.Time, fixture string, captured time.Time) {
	data := yahoofixtures.Read(fixture)
	data = bytes.ReplaceAll(data, []byte(fmt.Sprint(yahoofixtures.PointsLeagueID)), []byte(fmt.Sprint(leagueID)))
	f.storage.SetFileWithTime(standingsResource(yahoofixtures.PointsSeason, leagueID, day).Path(), data, captured)
}

func (f *standingsPGFixture) importDay(t *testing.T, day time.Time, teams ...TeamInfo) ImportYahooDataForDateResult {
	t.Helper()
	val, err := f.env.ExecuteActivity(f.acts.ImportYahooDataForDate,
		ImportYahooDataForDateInput{Season: yahoofixtures.PointsSeason, Teams: teams, Date: day})
	require.NoError(t, err)
	var result ImportYahooDataForDateResult
	require.NoError(t, val.Get(&result))
	require.Zero(t, result.ReadFailures)
	return result
}

func (f *standingsPGFixture) summaries(t *testing.T, leagueID int, day time.Time, team pgtype.Int4) []sqlcdb.YahooTeamSummary {
	t.Helper()
	rows, err := f.q.GetYahooTeamSummarySnapshot(context.Background(), sqlcdb.GetYahooTeamSummarySnapshotParams{
		LeagueID: int32(leagueID), Date: pgDate(day), TeamID: team,
	})
	require.NoError(t, err)
	return rows
}

// statValues is a team's category rows of a date as stat ID -> raw value.
func (f *standingsPGFixture) statValues(t *testing.T, team TeamInfo, day time.Time) map[int32]string {
	t.Helper()
	rows, err := f.q.GetYahooTeamSummaryStatsSnapshot(context.Background(), sqlcdb.GetYahooTeamSummaryStatsSnapshotParams{
		LeagueID: int32(team.LeagueID), Date: pgDate(day), TeamID: pgtype.Int4{Int32: int32(team.TeamID), Valid: true},
	})
	require.NoError(t, err)
	values := make(map[int32]string, len(rows))
	for _, row := range rows {
		values[row.StatID] = row.RawValue
	}
	return values
}

func (f *standingsPGFixture) latest(t *testing.T, leagueID int) pgtype.Date {
	t.Helper()
	date, err := f.q.GetLatestYahooStandingsSnapshotDate(context.Background(), int32(leagueID))
	require.NoError(t, err)
	return date
}

func int4(n int32) pgtype.Int4 { return pgtype.Int4{Int32: n, Valid: true} }

func TestPg_StandingsAndCategoriesForTeamsAndDates(t *testing.T) {
	f := newStandingsPGFixture(t)
	teams := []TeamInfo{pointsTeam(1), pointsTeam(2), pointsTeam(3)}
	for _, day := range []time.Time{standingsPGDay1, standingsPGDay2} {
		f.writeSummary(pointsTeam(1), day, [2]string{"1", "2"}, [2]string{"999", "7"}, [2]string{"26", ".915"})
		f.writeSummary(pointsTeam(2), day, [2]string{"1", "0"}, [2]string{"1234", "-"})
	}
	// Day 1 has a standings snapshot; day 2 has none (a capture gap).
	f.writeStandings(yahoofixtures.PointsLeagueID, standingsPGDay1, yahoofixtures.H2HStandings, standingsPGCaptured)

	day1 := f.importDay(t, standingsPGDay1, teams...)
	f.importDay(t, standingsPGDay2, teams...)

	assert.Equal(t, 2, day1.SummariesImported)
	assert.Equal(t, 3, day1.StandingsImported, "team 4 is ranked but not configured")
	rows := f.summaries(t, yahoofixtures.PointsLeagueID, standingsPGDay1, pgtype.Int4{})
	require.Len(t, rows, 3)
	byTeam := map[int32]sqlcdb.YahooTeamSummary{}
	for _, row := range rows {
		byTeam[row.TeamID] = row
	}
	team1 := byTeam[1]
	assert.Equal(t, int4(2), team1.StandingsRank)
	assert.Equal(t, []pgtype.Int4{int4(3), int4(1), int4(1)}, []pgtype.Int4{team1.H2hWins, team1.H2hLosses, team1.H2hTies})
	assert.InDelta(t, 0.7, team1.H2hPercentage.Float64, 0)
	assert.Equal(t, int4(2), team1.H2hDivisionWins)
	assert.True(t, team1.StandingsCapturedAt.Time.Equal(standingsPGCaptured))
	assert.InDelta(t, 2, team1.Goals.Float32, 0, "the fixed category columns are still filled")
	assert.Equal(t, int4(4), byTeam[2].StandingsRank)
	assert.Equal(t, int4(1), byTeam[3].StandingsRank, "standings without a stats file still make a row")
	assert.False(t, byTeam[3].Goals.Valid)

	only2 := f.summaries(t, yahoofixtures.PointsLeagueID, standingsPGDay1, int4(2))
	require.Len(t, only2, 1)
	assert.EqualValues(t, 2, only2[0].TeamID)

	gap := f.summaries(t, yahoofixtures.PointsLeagueID, standingsPGDay2, pgtype.Int4{})
	require.Len(t, gap, 2, "day 2 has stats for teams 1 and 2 only")
	for _, row := range gap {
		assert.False(t, row.StandingsCapturedAt.Valid, "no snapshot was captured on day 2")
		assert.False(t, row.StandingsRank.Valid)
	}

	assert.Equal(t, map[int32]string{1: "2", 26: ".915", 999: "7"}, f.statValues(t, pointsTeam(1), standingsPGDay1))
	assert.Equal(t, map[int32]string{1: "0", 1234: "-"}, f.statValues(t, pointsTeam(2), standingsPGDay2))

	stats, err := f.q.GetYahooTeamSummaryStatsSnapshot(context.Background(), sqlcdb.GetYahooTeamSummaryStatsSnapshotParams{
		LeagueID: yahoofixtures.PointsLeagueID, Date: pgDate(standingsPGDay1),
	})
	require.NoError(t, err)
	require.Len(t, stats, 5, "both teams' categories")
	goals, unknown, missing := stats[0], stats[2], stats[4]
	assert.Equal(t, [2]int32{1, 1}, [2]int32{goals.TeamID, goals.StatID}, "ordered by team, then stat")
	assert.Equal(t, pgtype.Text{String: "Goals", Valid: true}, goals.Name)
	assert.Equal(t, pgtype.Float8{Float64: 2, Valid: true}, goals.Value)
	assert.EqualValues(t, 999, unknown.StatID)
	assert.False(t, unknown.Name.Valid, "a stat ID the league settings do not list has no definition")
	assert.EqualValues(t, 1234, missing.StatID)
	assert.False(t, missing.Value.Valid, `"-" is no value`)
	assert.Equal(t, "-", missing.RawValue)
}

func TestPg_ReimportReplacesTheSnapshotAndCategorySet(t *testing.T) {
	f := newStandingsPGFixture(t)
	teams := []TeamInfo{pointsTeam(1), pointsTeam(2), pointsTeam(3)}
	f.writeSummary(pointsTeam(1), standingsPGDay1, [2]string{"1", "2"}, [2]string{"999", "7"}, [2]string{"26", ".915"})
	f.writeSummary(pointsTeam(2), standingsPGDay1, [2]string{"1", "0"})
	f.writeStandings(yahoofixtures.PointsLeagueID, standingsPGDay1, yahoofixtures.H2HStandings, standingsPGCaptured)
	f.importDay(t, standingsPGDay1, teams...)

	// The files change: team 1 loses two categories, changes one and gains
	// one; the snapshot is re-captured before the first game and ranks teams
	// 1 and 2 only.
	recaptured := standingsPGCaptured.Add(time.Hour)
	f.writeSummary(pointsTeam(1), standingsPGDay1, [2]string{"1", "5"}, [2]string{"31", "3"})
	f.writeStandings(yahoofixtures.PointsLeagueID, standingsPGDay1, yahoofixtures.PreseasonStandings, recaptured)
	result := f.importDay(t, standingsPGDay1, teams...)

	assert.Equal(t, 2, result.StandingsImported)
	assert.Equal(t, map[int32]string{1: "5", 31: "3"}, f.statValues(t, pointsTeam(1), standingsPGDay1),
		"no category of the earlier file is left")
	assert.Equal(t, map[int32]string{1: "0"}, f.statValues(t, pointsTeam(2), standingsPGDay1))
	rows := f.summaries(t, yahoofixtures.PointsLeagueID, standingsPGDay1, pgtype.Int4{})
	require.Len(t, rows, 3)
	team1, team3 := rows[0], rows[2]
	assert.InDelta(t, 5, team1.Goals.Float32, 0)
	assert.False(t, team1.StandingsRank.Valid, "the preseason snapshot has no rank")
	assert.Equal(t, int4(0), team1.H2hWins)
	assert.False(t, team1.H2hPercentage.Valid)
	assert.False(t, team1.H2hDivisionWins.Valid, "the new snapshot has no divisional record")
	assert.True(t, team1.StandingsCapturedAt.Time.Equal(recaptured))
	assert.EqualValues(t, 3, team3.TeamID)
	assert.False(t, team3.StandingsCapturedAt.Valid, "a team the new snapshot does not list keeps no standing")
	assert.False(t, team3.StandingsRank.Valid)

	// An identical re-import changes nothing.
	before := f.summaries(t, yahoofixtures.PointsLeagueID, standingsPGDay1, pgtype.Int4{})
	f.importDay(t, standingsPGDay1, teams...)
	assert.Equal(t, before, f.summaries(t, yahoofixtures.PointsLeagueID, standingsPGDay1, pgtype.Int4{}))
}

func TestPg_LatestSnapshotIsTheLeaguesOwn(t *testing.T) {
	f := newStandingsPGFixture(t)
	other := TeamInfo{LeagueID: standingsPGOtherLeague, TeamID: 1}
	assert.False(t, f.latest(t, yahoofixtures.PointsLeagueID).Valid, "no snapshot yet")

	f.writeStandings(yahoofixtures.PointsLeagueID, standingsPGDay1, yahoofixtures.H2HStandings, standingsPGCaptured)
	f.writeSummary(pointsTeam(1), standingsPGDay2, [2]string{"1", "1"}) // stats only on day 2
	f.writeStandings(standingsPGOtherLeague, standingsPGDay2, yahoofixtures.PreseasonStandings, standingsPGCaptured)
	for _, day := range []time.Time{standingsPGDay1, standingsPGDay2} {
		f.importDay(t, day, pointsTeam(1), pointsTeam(2), other)
	}

	assert.Equal(t, pgDate(standingsPGDay1), f.latest(t, yahoofixtures.PointsLeagueID),
		"neither day 2's stats-only row nor the other league's later capture moves it")
	assert.Equal(t, pgDate(standingsPGDay2), f.latest(t, standingsPGOtherLeague))
	latest := f.summaries(t, standingsPGOtherLeague, standingsPGDay2, pgtype.Int4{})
	require.Len(t, latest, 1)
	assert.EqualValues(t, standingsPGOtherLeague, latest[0].LeagueID)
}
