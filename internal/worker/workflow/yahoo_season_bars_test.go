package workflow

import (
	"testing"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/worker/nhl"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

const (
	barsTestNextYear      = preseasonTestYear + 1
	barsTestStandInLeague = 1003
	// barsTestFinalTotal1001/1002 are the totals the 2026 child reports it
	// finished with, unrelated to the parent's estimates on purpose.
	barsTestFinalTotal1001 = 40
	barsTestFinalTotal1002 = 7
)

// barsTestSnapshot has a two-league season and a one-league next season.
func barsTestSnapshot() shared.YahooSeasonsSnapshot {
	return shared.YahooSeasonsSnapshot{Seasons: config.YahooSeasonsMap{
		preseasonTestYear: {Leagues: []config.League{
			{LeagueID: preseasonTestLeagueID, TeamIDs: []int{1, 2}},
			{LeagueID: preseasonSecondLeagueID},
		}},
		barsTestNextYear: {Leagues: []config.League{standInLeague(barsTestStandInLeague)}},
	}}
}

// runSeasonSyncWithYahooChildren runs the parent in mode with mocked Yahoo
// children: the 2026 child reports final per-league totals, the 2027 child
// (like a result from before LeagueTotals existed) none.
func runSeasonSyncWithYahooChildren(t *testing.T, mode seasonSyncMode) shared.ProgressReport {
	t.Helper()
	withYahooSnapshot(t, barsTestSnapshot())
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetStartTime(preseasonTestNow)
	env.RegisterWorkflow(FetchSeasonsWorkflow)
	env.RegisterWorkflow(ImportSeasonsWorkflow)
	env.RegisterWorkflow(FetchYahooSeasonWorkflow)
	env.RegisterWorkflow(ImportYahooSeasonWorkflow)
	mockProgressSaves(env)
	mockNoUpcomingSeason(env)
	var activities *nhl.SeasonsActivities
	env.OnActivity(activities.FetchSeasonsManifest, mock.Anything, mock.Anything).
		Return(nhl.FetchSeasonsManifestResult{Origin: core.OriginFileSystem}, nil)
	child, workflowFn := any(FetchYahooSeasonWorkflow), any(FetchSeasonsWorkflow)
	if mode == seasonSyncImport {
		child, workflowFn = ImportYahooSeasonWorkflow, ImportSeasonsWorkflow
	}
	env.OnWorkflow(child, mock.Anything, mock.MatchedBy(func(input YahooSeasonWorkflowInput) bool {
		return input.StartYear == preseasonTestYear
	})).Return(YahooSeasonSyncResult{LeagueTotals: []int{barsTestFinalTotal1001, barsTestFinalTotal1002}}, nil).Once()
	env.OnWorkflow(child, mock.Anything, mock.MatchedBy(func(input YahooSeasonWorkflowInput) bool {
		return input.StartYear == barsTestNextYear
	})).Return(YahooSeasonSyncResult{}, nil).Once()

	env.ExecuteWorkflow(workflowFn, preseasonInput())

	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
	return queryProgress(t, env)
}

func TestSeasonSync_YahooBarsPerLeagueMirrorChildBars(t *testing.T) {
	for _, mode := range []seasonSyncMode{seasonSyncFetch, seasonSyncImport} {
		t.Run(seasonSyncVerb(mode), func(t *testing.T) {
			report := runSeasonSyncWithYahooChildren(t, mode)

			sourceKey := yahooSeasonSourceKey(mode)
			bars := report.Groups[groupYahooMetadata].Bars
			require.Len(t, bars, 3)
			assert.Equal(t, []string{"2026-27 · 1001", "2026-27 · 1002", "2027-28 · 1003"}, barLabels(bars))
			for i, wantSource := range []struct {
				key string
				bar int
			}{
				{sourceKey(preseasonTestYear), 0},
				{sourceKey(preseasonTestYear), 1},
				{sourceKey(barsTestNextYear), 0},
			} {
				assert.Equal(t, wantSource.key, bars[i].ProgressSourceKey)
				assert.True(t, bars[i].MirrorSourceBar)
				assert.Equal(t, wantSource.bar, bars[i].ProgressSourceBar)
				assert.True(t, bars[i].Started)
				assert.Equal(t, bars[i].Total, bars[i].Current, "bar %d completes", i)
			}
			// The child's final totals replace the estimates; without them
			// the estimate stays.
			assert.Equal(t, barsTestFinalTotal1001, bars[0].Total)
			assert.Equal(t, barsTestFinalTotal1002, bars[1].Total)
			assert.Equal(t, yahooLeagueUnits(standInLeague(barsTestStandInLeague), mode), bars[2].Total)
			assert.Equal(t, bars[0].Total+bars[1].Total+bars[2].Total+upcomingSeasonBarTotal(report),
				report.Total, "report Total follows the bars")
		})
	}
}

// upcomingSeasonBarTotal is the Total of the bars outside the Yahoo group.
func upcomingSeasonBarTotal(report shared.ProgressReport) int {
	total := 0
	for groupIdx, group := range report.Groups {
		if groupIdx == groupYahooMetadata {
			continue
		}
		for _, bar := range group.Bars {
			total += bar.Total
		}
	}
	return total
}
