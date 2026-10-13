package workflow

import (
	"context"
	"testing"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/graph/model"
	worknhl "github.com/sperano/puckdb/internal/worker/nhl"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

const (
	daysTestStart = "2023-10-10"
	daysTestEnd   = "2023-10-12"
	daysTestCount = 3
)

// newImportSeasonDaysEnv registers ImportNHLSeasonWorkflow with no Yahoo
// leagues, season-level imports that succeed and no playoff teams.
func newImportSeasonDaysEnv(t *testing.T) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	previousLoader := loadYahooSeasons
	loadYahooSeasons = func() shared.YahooSeasonsSnapshot { return shared.YahooSeasonsSnapshot{} }
	t.Cleanup(func() { loadYahooSeasons = previousLoader })

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(ImportNHLSeasonWorkflow)
	var sa *worknhl.SeasonsActivities
	env.OnActivity(sa.ImportSeasonRosters, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(sa.ImportClubStats, mock.Anything, mock.Anything).Return(nil)
	var pa *worknhl.PlayoffActivities
	env.OnActivity(pa.ListSeasonTeams, mock.Anything, mock.Anything).Return([]string{}, nil)
	return env
}

func TestImportNHLSeasonWorkflow_ReportsUnchangedDays(t *testing.T) {
	env := newImportSeasonDaysEnv(t)
	changedDay := time.Date(2023, 10, 11, 0, 0, 0, 0, time.UTC)
	var ia *worknhl.ImportActivities
	env.OnActivity(ia.ImportDayIfChanged, mock.Anything, mock.Anything).Return(
		func(_ context.Context, input worknhl.ImportDayIfChangedInput) (worknhl.ImportDayIfChangedResult, error) {
			assert.False(t, input.Force)
			if input.Date.Equal(changedDay) {
				return worknhl.ImportDayIfChangedResult{
					Origins: core.OriginCounts{core.OriginFileSystem: 2}, SkippedRows: 1,
				}, nil
			}
			return worknhl.ImportDayIfChangedResult{Origins: core.OriginCounts{}, Skipped: true}, nil
		}).Times(daysTestCount)

	env.ExecuteWorkflow(ImportNHLSeasonWorkflow, testSeasonW(2023, daysTestStart, daysTestEnd), ImportNHLSeasonOptions{})

	require.NoError(t, env.GetWorkflowError())
	var counts core.OriginCounts
	require.NoError(t, env.GetWorkflowResult(&counts))
	assert.Equal(t, 2, counts[core.OriginFileSystem])
	report := queryProgress(t, env)
	assert.Contains(t, report.Groups[GroupImportDays].CompletedMsg, "Imported 1 of 3 days for 2023-24 ")
	assert.Contains(t, report.Groups[GroupImportDays].CompletedMsg,
		"2 unchanged since their last import; 1 with skipped rows, retried next run.")
	env.AssertExpectations(t)
}

func TestImportNHLSeasonWorkflow_ReimportDaysForcesEveryDay(t *testing.T) {
	env := newImportSeasonDaysEnv(t)
	var ia *worknhl.ImportActivities
	env.OnActivity(ia.ImportDayIfChanged, mock.Anything, mock.MatchedBy(func(input worknhl.ImportDayIfChangedInput) bool {
		return input.Force
	})).Return(worknhl.ImportDayIfChangedResult{Origins: core.OriginCounts{}}, nil).Times(daysTestCount)

	env.ExecuteWorkflow(ImportNHLSeasonWorkflow, testSeasonW(2023, daysTestStart, daysTestEnd),
		ImportNHLSeasonOptions{ReimportDays: true})

	require.NoError(t, env.GetWorkflowError())
	report := queryProgress(t, env)
	assert.Contains(t, report.Groups[GroupImportDays].CompletedMsg, "Imported 3 days for 2023-24 ")
	env.AssertExpectations(t)
}

// A history recorded before the change has no version marker and must keep
// scheduling the unconditional ImportDay.
func TestImportNHLSeasonWorkflow_LegacyHistoryImportsEveryDay(t *testing.T) {
	env := newImportSeasonDaysEnv(t)
	env.OnGetVersion(importDaySkipChangeID, workflow.DefaultVersion, importDaySkipVersion).Return(workflow.DefaultVersion)
	var ia *worknhl.ImportActivities
	env.OnActivity(ia.ImportDay, mock.Anything, mock.Anything).
		Return(core.OriginCounts{core.OriginFileSystem: 1}, nil).Times(daysTestCount)

	env.ExecuteWorkflow(ImportNHLSeasonWorkflow, testSeasonW(2023, daysTestStart, daysTestEnd), ImportNHLSeasonOptions{})

	require.NoError(t, env.GetWorkflowError())
	var counts core.OriginCounts
	require.NoError(t, env.GetWorkflowResult(&counts))
	assert.Equal(t, daysTestCount, counts[core.OriginFileSystem])
	env.AssertExpectations(t)
}

func TestImportSeasonsWorkflow_PassesReimportDaysToSeasonChild(t *testing.T) {
	reimport := true
	for _, tc := range []struct {
		name  string
		input *bool
		want  bool
	}{
		{name: "omitted", input: nil, want: false},
		{name: "set", input: &reimport, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previousLoader := loadYahooSeasons
			loadYahooSeasons = func() shared.YahooSeasonsSnapshot { return shared.YahooSeasonsSnapshot{} }
			t.Cleanup(func() { loadYahooSeasons = previousLoader })
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			env.RegisterWorkflow(ImportSeasonsWorkflow)
			env.RegisterWorkflow(ImportNHLSeasonWorkflow)
			var pa *worknhl.PlayoffActivities
			env.OnActivity(pa.ListSeasonTeams, mock.Anything, mock.Anything).Return([]string{}, nil).Maybe()
			mockNoUpcomingSeason(env)
			input := &model.SeasonsInput{ReimportDays: tc.input}
			var sa *worknhl.SeasonsActivities
			env.OnActivity(sa.FetchSeasonsManifest, mock.Anything, input).Return(worknhl.FetchSeasonsManifestResult{
				Seasons: []nhl.SeasonInfo{testSeasonW(2023, daysTestStart, daysTestEnd)}, Origin: core.OriginFileSystem,
			}, nil)
			env.OnWorkflow(ImportNHLSeasonWorkflow, mock.Anything, mock.Anything,
				ImportNHLSeasonOptions{ReimportDays: tc.want}).Return(core.OriginCounts{}, nil).Once()

			env.ExecuteWorkflow(ImportSeasonsWorkflow, input)

			require.NoError(t, env.GetWorkflowError())
			env.AssertExpectations(t)
		})
	}
}
