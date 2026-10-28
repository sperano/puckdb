package workflow

import (
	"errors"
	"testing"

	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/graph/model"
	worknhl "github.com/sperano/puckdb/internal/worker/nhl"
	workplayer "github.com/sperano/puckdb/internal/worker/player"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// ImportSeasonPlayerLogsWorkflowTestSuite covers the legacy child, which
// lists its season's skaters itself.
type ImportSeasonPlayerLogsWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *ImportSeasonPlayerLogsWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.RegisterWorkflow(ImportSeasonPlayerLogsWorkflow)
	s.env.OnActivity(((*shared.ProgressActivities)(nil)).Save,
		mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
}

func (s *ImportSeasonPlayerLogsWorkflowTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

func TestImportSeasonPlayerLogsWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(ImportSeasonPlayerLogsWorkflowTestSuite))
}

func (s *ImportSeasonPlayerLogsWorkflowTestSuite) TestSuccess() {
	season := testSeasonW(2023, "2023-10-10", "2023-10-12")

	var ia *worknhl.ImportActivities
	s.env.OnActivity(ia.CollectSeasonPlayerIDs, mock.Anything, mock.Anything).Return([]int64{1, 2, 3}, nil)
	s.env.OnActivity(ia.ImportPlayerGameLogsBatch, mock.Anything, mock.Anything).Return(
		&worknhl.ImportPlayerGameLogsBatchResult{PlayersProcessed: 3, GamesUpdated: 10}, nil)

	s.env.ExecuteWorkflow(ImportSeasonPlayerLogsWorkflow, season)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *ImportSeasonPlayerLogsWorkflowTestSuite) TestNoPlayers() {
	season := testSeasonW(2023, "2023-10-10", "2023-10-12")

	var ia *worknhl.ImportActivities
	s.env.OnActivity(ia.CollectSeasonPlayerIDs, mock.Anything, mock.Anything).Return([]int64{}, nil)

	s.env.ExecuteWorkflow(ImportSeasonPlayerLogsWorkflow, season)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *ImportSeasonPlayerLogsWorkflowTestSuite) TestCollectError() {
	season := testSeasonW(2023, "2023-10-10", "2023-10-12")

	var ia *worknhl.ImportActivities
	s.env.OnActivity(ia.CollectSeasonPlayerIDs, mock.Anything, mock.Anything).Return(
		([]int64)(nil), errors.New("redis error"))

	s.env.ExecuteWorkflow(ImportSeasonPlayerLogsWorkflow, season)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

func (s *ImportSeasonPlayerLogsWorkflowTestSuite) TestBatchError() {
	season := testSeasonW(2023, "2023-10-10", "2023-10-12")

	var ia *worknhl.ImportActivities
	s.env.OnActivity(ia.CollectSeasonPlayerIDs, mock.Anything, mock.Anything).Return([]int64{1, 2, 3}, nil)
	s.env.OnActivity(ia.ImportPlayerGameLogsBatch, mock.Anything, mock.Anything).Return(
		(*worknhl.ImportPlayerGameLogsBatchResult)(nil), errors.New("batch failed"))

	s.env.ExecuteWorkflow(ImportSeasonPlayerLogsWorkflow, season)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

// TestImportPlayerLogsLegacyHistoryKeepsRedisCount replays the path of
// histories recorded before the skater manifest.
func TestImportPlayerLogsLegacyHistoryKeepsRedisCount(t *testing.T) {
	env := newImportPlayerLogsEnv(t, playerLogsFixtureSeason())
	env.RegisterWorkflow(ImportSeasonPlayerLogsWorkflow)
	recordSavedReports(env)
	env.OnGetVersion(importPlayerLogsManifestChangeID, workflow.DefaultVersion, importPlayerLogsManifestVersion).
		Return(workflow.DefaultVersion)
	env.OnActivity(((*workplayer.Activities)(nil)).CountPlayersForAllSeasons, mock.Anything, []int{playerLogsFixtureYear}).
		Return(map[int]int{playerLogsFixtureYear: seasonFanOutTestPlayers}, nil).Once()
	env.OnWorkflow(ImportSeasonPlayerLogsWorkflow, mock.Anything, playerLogsFixtureSeason()).
		Return(core.OriginCounts{}, nil).Once()

	env.ExecuteWorkflow(ImportPlayerLogsWorkflow, &model.SeasonsInput{})
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)

	report := queryProgressReport(t, env)
	require.Len(t, report.Groups, 1)
	assert.Equal(t, seasonFanOutTestPlayers, report.Groups[groupImportPlayerLogsLegacy].Bars[0].Total)
}
