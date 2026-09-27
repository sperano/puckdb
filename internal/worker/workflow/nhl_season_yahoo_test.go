package workflow

import (
	"testing"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/core"
	worknhl "github.com/sperano/puckdb/internal/worker/nhl"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

// yahooSeasonForPerDayTest reuses standInYahooInput's mixed configuration:
// one stand-in league (no real Yahoo teams) and one real league with team 3.
// Only the real league's teams should ever reach ImportDay/FetchDay.
func yahooSeasonForPerDayTest() shared.YahooSeasonsSnapshot {
	return shared.YahooSeasonsSnapshot{
		Seasons: config.YahooSeasonsMap{preseasonTestYear: standInYahooInput().Season},
	}
}

// TestNHLSeasonWorkflows_PerDayYahooTeamIDs is the regression test for the
// bug fixed here: after the season-sync split (PR #77), ImportNHLSeasonWorkflow
// and FetchNHLSeasonWorkflow never passed any Yahoo team IDs to
// ImportDay/FetchDay, so yahoo_team_rosters/yahoo_team_summaries stayed
// empty. Both workflows must now pass the real league's team IDs (computed
// from the snapshotted Yahoo config, without re-importing league/team
// metadata) and skip the stand-in league entirely.
func TestNHLSeasonWorkflows_PerDayYahooTeamIDs(t *testing.T) {
	season := testSeasonW(preseasonTestYear, "2023-10-10", "2023-10-10")

	t.Run("import", func(t *testing.T) {
		previousLoader := loadYahooSeasons
		loadYahooSeasons = func() shared.YahooSeasonsSnapshot { return yahooSeasonForPerDayTest() }
		t.Cleanup(func() { loadYahooSeasons = previousLoader })

		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()
		env.RegisterWorkflow(ImportNHLSeasonWorkflow)

		var sa *worknhl.SeasonsActivities
		env.OnActivity(sa.ImportSeasonRosters, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity(sa.ImportClubStats, mock.Anything, mock.Anything).Return(nil)
		var pa *worknhl.PlayoffActivities
		env.OnActivity(pa.ListSeasonTeams, mock.Anything, mock.Anything).Return([]string{}, nil)

		var ia *worknhl.ImportActivities
		env.OnActivity(ia.ImportDay, mock.Anything, mock.MatchedBy(func(input worknhl.ImportDayInput) bool {
			return assert.ObjectsAreEqual(onlySecondLeagueTeams, input.TeamIDs)
		})).Return(core.OriginCounts{}, nil).Once()

		env.ExecuteWorkflow(ImportNHLSeasonWorkflow, season)

		require.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("fetch", func(t *testing.T) {
		previousLoader := loadYahooSeasons
		loadYahooSeasons = func() shared.YahooSeasonsSnapshot { return yahooSeasonForPerDayTest() }
		t.Cleanup(func() { loadYahooSeasons = previousLoader })

		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()
		env.RegisterWorkflow(FetchNHLSeasonWorkflow)

		var sa *worknhl.SeasonsActivities
		env.OnActivity(sa.FetchSeasonRosters, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity(sa.FetchClubStats, mock.Anything, mock.Anything).Return(nil)
		var pa *worknhl.PlayoffActivities
		env.OnActivity(pa.ListSeasonTeams, mock.Anything, mock.Anything).Return([]string{}, nil)

		var dsa *worknhl.DailyScheduleActivities
		env.OnActivity(dsa.FetchDay, mock.Anything, mock.MatchedBy(func(input worknhl.FetchDayInput) bool {
			return assert.ObjectsAreEqual(onlySecondLeagueTeams, input.TeamIDs)
		})).Return(core.OriginCounts{}, nil).Once()

		env.ExecuteWorkflow(FetchNHLSeasonWorkflow, season)

		require.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
}
