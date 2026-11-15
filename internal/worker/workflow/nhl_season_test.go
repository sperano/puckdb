package workflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/core"
	worknhl "github.com/sperano/puckdb/internal/worker/nhl"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

const (
	// nhlSeasonTestDayReads is what every stub day activity reads.
	nhlSeasonTestDayReads = 2
	// nhlSeasonTestActivityDuration is how long each stub season activity
	// takes, so the day group's start time shows which side of them it is.
	nhlSeasonTestActivityDuration = time.Minute
	// nhlSeasonTestFailingTeam is the team whose playoff activity fails in
	// the error cases.
	nhlSeasonTestFailingTeam = "TOR"
)

var (
	errNHLSeasonStub      = errors.New("stub failure")
	nhlSeasonTestStart    = time.Date(2026, time.November, 1, 12, 0, 0, 0, time.UTC)
	nhlSeasonTestTeams    = []string{"TOR", "MTL", "SEA"}
	nhlSeasonFetchResults = map[string]worknhl.FetchTeamPlayoffGamesResult{
		"TOR": {GamesFound: 2, GamesFetched: 2, Origins: core.OriginCounts{core.OriginRemoteNHLAPI: 1}},
		"MTL": {GamesFound: 1, GamesFetched: 1, Origins: core.OriginCounts{core.OriginFileSystem: 1}},
		"SEA": {}, // no club schedule
	}
	nhlSeasonImportResults = map[string]worknhl.ImportTeamPlayoffGamesResult{
		"TOR": {GamesImported: 1, SkatersImported: 2, GoaliesImported: 1, Origins: core.OriginCounts{core.OriginFileSystem: 4}},
		"MTL": {GamesImported: 2, SkatersImported: 3, GoaliesImported: 2, Origins: core.OriginCounts{core.OriginFileSystem: 5}},
		"SEA": {Origins: core.OriginCounts{}},
	}
)

// newNHLSeasonEnv registers both NHL season workflows over stub activities
// that succeed, except the one named failing, which returns errNHLSeasonStub
// (a playoff activity fails for nhlSeasonTestFailingTeam only). Each season
// activity takes nhlSeasonTestActivityDuration; FetchDay checks its input.
func newNHLSeasonEnv(t *testing.T, failing string) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	withYahooSnapshot(t, shared.YahooSeasonsSnapshot{})
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetStartTime(nhlSeasonTestStart)
	env.RegisterWorkflow(FetchNHLSeasonWorkflow)
	env.RegisterWorkflow(ImportNHLSeasonWorkflow)
	mockProgressSaves(env)
	fail := func(name string) error {
		if name == failing {
			return errNHLSeasonStub
		}
		return nil
	}

	var sa *worknhl.SeasonsActivities
	for name, activity := range map[string]any{
		"FetchSeasonRosters": sa.FetchSeasonRosters, "FetchClubStats": sa.FetchClubStats,
		"ImportSeasonRosters": sa.ImportSeasonRosters, "ImportClubStats": sa.ImportClubStats,
	} {
		env.OnActivity(activity, mock.Anything, mock.Anything).
			After(nhlSeasonTestActivityDuration).Return(fail(name)).Maybe()
	}
	var dsa *worknhl.DailyScheduleActivities
	seasonStart := testSeasonW(2023, daysTestStart, daysTestEnd).StandingsStart.Time
	env.OnActivity(dsa.FetchDay, mock.Anything, mock.Anything).Return(
		func(_ context.Context, in worknhl.FetchDayInput) (core.OriginCounts, error) {
			assert.Equal(t, seasonStart.AddDate(0, 0, in.DayIndex), in.Day, "FetchDay day of its index")
			assert.Equal(t, 2023, in.StartSeason)
			assert.Equal(t, daysTestCount, in.TotalDays)
			return core.OriginCounts{core.OriginFileSystem: nhlSeasonTestDayReads}, fail("FetchDay")
		}).Maybe()
	var ia *worknhl.ImportActivities
	env.OnActivity(ia.ImportDayIfChanged, mock.Anything, mock.Anything).Return(worknhl.ImportDayIfChangedResult{
		Origins: core.OriginCounts{core.OriginFileSystem: nhlSeasonTestDayReads},
	}, fail("ImportDayIfChanged")).Maybe()

	var pa *worknhl.PlayoffActivities
	env.OnActivity(pa.ListSeasonTeams, mock.Anything, mock.Anything).
		Return(nhlSeasonTestTeams, fail("ListSeasonTeams")).Maybe()
	teamFail := func(name, team string) error {
		if team == nhlSeasonTestFailingTeam {
			return fail(name)
		}
		return nil
	}
	env.OnActivity(pa.FetchTeamPlayoffGames, mock.Anything, mock.Anything).Return(
		func(_ context.Context, in worknhl.FetchTeamPlayoffGamesInput) (worknhl.FetchTeamPlayoffGamesResult, error) {
			return nhlSeasonFetchResults[in.TeamAbbrev], teamFail("FetchTeamPlayoffGames", in.TeamAbbrev)
		}).Maybe()
	env.OnActivity(pa.ImportTeamPlayoffGames, mock.Anything, mock.Anything).Return(
		func(_ context.Context, in worknhl.ImportTeamPlayoffGamesInput) (worknhl.ImportTeamPlayoffGamesResult, error) {
			return nhlSeasonImportResults[in.TeamAbbrev], teamFail("ImportTeamPlayoffGames", in.TeamAbbrev)
		}).Maybe()
	return env
}

func executeNHLSeason(env *testsuite.TestWorkflowEnvironment, mode seasonSyncMode) {
	season := testSeasonW(2023, daysTestStart, daysTestEnd)
	if mode == seasonSyncImport {
		env.ExecuteWorkflow(ImportNHLSeasonWorkflow, season, ImportNHLSeasonOptions{})
		return
	}
	env.ExecuteWorkflow(FetchNHLSeasonWorkflow, season)
}

// Fetch counts one origin per unit of progress: each day's daily schedule
// and each team's club schedule (none for a team without one), which it
// dropped before the shared runner.
func TestFetchNHLSeasonWorkflow_CountsDayAndClubScheduleOrigins(t *testing.T) {
	env := newNHLSeasonEnv(t, "")
	executeNHLSeason(env, seasonSyncFetch)

	require.NoError(t, env.GetWorkflowError())
	var counts core.OriginCounts
	require.NoError(t, env.GetWorkflowResult(&counts))
	assert.Equal(t, core.OriginCounts{
		core.OriginFileSystem:   daysTestCount*nhlSeasonTestDayReads + 1,
		core.OriginRemoteNHLAPI: 1,
	}, counts)
	report := queryProgress(t, env)
	assert.Regexp(t, `^Fetched 3 days for 2023-24 in \S+\.$`, report.Groups[GroupFetchSeasonData].CompletedMsg)
	assert.Regexp(t, `^Fetched 3 playoff games for 2023-24 in \S+\.$`, report.Groups[GroupFetchSeasonPlayoff].CompletedMsg)
}

func TestImportNHLSeasonWorkflow_CountsDayAndPlayoffOrigins(t *testing.T) {
	env := newNHLSeasonEnv(t, "")
	executeNHLSeason(env, seasonSyncImport)

	require.NoError(t, env.GetWorkflowError())
	var counts core.OriginCounts
	require.NoError(t, env.GetWorkflowResult(&counts))
	assert.Equal(t, core.OriginCounts{core.OriginFileSystem: daysTestCount*nhlSeasonTestDayReads + 4 + 5}, counts)
	report := queryProgress(t, env)
	assert.Regexp(t, `^Imported 3 days for 2023-24 in \S+\.$`, report.Groups[GroupImportDays].CompletedMsg)
	assert.Regexp(t, `^Imported 3 playoff games \(5 skaters, 3 goalies\) for 2023-24 in \S+\.$`,
		report.Groups[GroupImportPlayoff].CompletedMsg)
}

// Fetch starts its day group before the season activities, import after
// them, as their histories recorded it.
func TestNHLSeasonWorkflows_DayGroupStartPlacement(t *testing.T) {
	seasonActivities := 2 * nhlSeasonTestActivityDuration
	for _, tc := range []struct {
		name string
		mode seasonSyncMode
		want time.Time
	}{
		{"fetch", seasonSyncFetch, nhlSeasonTestStart},
		{"import", seasonSyncImport, nhlSeasonTestStart.Add(seasonActivities)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newNHLSeasonEnv(t, "")
			executeNHLSeason(env, tc.mode)

			require.NoError(t, env.GetWorkflowError())
			report := queryProgress(t, env)
			assert.Equal(t, tc.want.UnixMilli(), report.Groups[groupSeasonDays].StartedAt)
		})
	}
}

// Every failure ends the workflow with the activity's error, wrapped with
// what failed; day failures are returned as the pool reported them.
func TestNHLSeasonWorkflows_WrapActivityErrors(t *testing.T) {
	for _, tc := range []struct {
		mode    seasonSyncMode
		failing string
		want    string
	}{
		{seasonSyncFetch, "FetchSeasonRosters", "fetch season rosters: activity error (type: FetchSeasonRosters,"},
		{seasonSyncFetch, "FetchClubStats", "fetch club stats: activity error (type: FetchClubStats,"},
		{seasonSyncFetch, "FetchDay", "): activity error (type: FetchDay,"},
		{seasonSyncFetch, "ListSeasonTeams", "list season teams: activity error (type: ListSeasonTeams,"},
		{seasonSyncFetch, "FetchTeamPlayoffGames",
			"fetch playoff games for TOR: activity error (type: FetchTeamPlayoffGames,"},
		{seasonSyncImport, "ImportSeasonRosters", "import season rosters: activity error (type: ImportSeasonRosters,"},
		{seasonSyncImport, "ImportClubStats", "import club stats: activity error (type: ImportClubStats,"},
		{seasonSyncImport, "ImportDayIfChanged", "): activity error (type: ImportDayIfChanged,"},
		{seasonSyncImport, "ListSeasonTeams", "list season teams: activity error (type: ListSeasonTeams,"},
		{seasonSyncImport, "ImportTeamPlayoffGames",
			"import playoff games for TOR: activity error (type: ImportTeamPlayoffGames,"},
	} {
		t.Run(seasonSyncAction(tc.mode)+"/"+tc.failing, func(t *testing.T) {
			env := newNHLSeasonEnv(t, tc.failing)
			executeNHLSeason(env, tc.mode)

			err := env.GetWorkflowError()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.Contains(t, err.Error(), errNHLSeasonStub.Error())
		})
	}
}
