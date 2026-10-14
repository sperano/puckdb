package workflow

import (
	"errors"
	"testing"

	worknhl "github.com/sperano/puckdb/internal/worker/nhl"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
)

type ImportSeasonPlayerLogsWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *ImportSeasonPlayerLogsWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.RegisterWorkflow(ImportSeasonPlayerLogsWorkflow)
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
