package worker

import (
	"errors"
	"testing"
	"time"

	"github.com/sperano/puckdb/graph/model"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
)

func mustParseDate(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

// Workflow test suite for FetchSeasons workflows
type FetchSeasonsWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *FetchSeasonsWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.RegisterWorkflow(FetchSeasonsWorkflow)
	s.env.RegisterWorkflow(FetchSeasonWorkflow)
}

func (s *FetchSeasonsWorkflowTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

func TestFetchSeasonsWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(FetchSeasonsWorkflowTestSuite))
}

// Test FetchSeasonsWorkflow with mocked child workflows
func (s *FetchSeasonsWorkflowTestSuite) TestFetchSeasonsWorkflow_Success() {
	input := &model.FetchSeasonsInput{}
	seasons := []SeasonInfo{
		{StartYear: 2023, StartDate: mustParseDate("2024-04-14"), EndDate: mustParseDate("2024-04-15")},
	}

	s.env.OnActivity(FetchSeasonsDataActivity, mock.Anything, input).Return(seasons, nil)
	// Mock the child workflow for each season
	s.env.OnWorkflow(FetchSeasonWorkflow, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(FetchSeasonsWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// Test FetchSeasonsWorkflow handles activity error
func (s *FetchSeasonsWorkflowTestSuite) TestFetchSeasonsWorkflow_FetchSeasonsError() {
	input := &model.FetchSeasonsInput{}
	expectedErr := errors.New("failed to fetch seasons")

	s.env.OnActivity(FetchSeasonsDataActivity, mock.Anything, input).Return(nil, expectedErr)

	s.env.ExecuteWorkflow(FetchSeasonsWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

// Test FetchSeasonsWorkflow handles child workflow error
func (s *FetchSeasonsWorkflowTestSuite) TestFetchSeasonsWorkflow_ChildWorkflowError() {
	input := &model.FetchSeasonsInput{}
	seasons := []SeasonInfo{
		{StartYear: 2023, StartDate: mustParseDate("2024-04-14"), EndDate: mustParseDate("2024-04-15")},
	}
	expectedErr := errors.New("child workflow failed")

	s.env.OnActivity(FetchSeasonsDataActivity, mock.Anything, input).Return(seasons, nil)
	// Mock the child workflow to return an error
	s.env.OnWorkflow(FetchSeasonWorkflow, mock.Anything, mock.Anything).Return(expectedErr)

	s.env.ExecuteWorkflow(FetchSeasonsWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

// Test FetchSeasonsWorkflow with no seasons
func (s *FetchSeasonsWorkflowTestSuite) TestFetchSeasonsWorkflow_NoSeasons() {
	input := &model.FetchSeasonsInput{}
	seasons := []SeasonInfo{}

	s.env.OnActivity(FetchSeasonsDataActivity, mock.Anything, input).Return(seasons, nil)

	s.env.ExecuteWorkflow(FetchSeasonsWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// --- InitializeWorkflow tests ---

type InitializeWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *InitializeWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.RegisterWorkflow(InitializeWorkflow)
}

func (s *InitializeWorkflowTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

func TestInitializeWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(InitializeWorkflowTestSuite))
}

func (s *InitializeWorkflowTestSuite) TestInitializeWorkflow_Success() {
	// Mock all activities in the workflow
	s.env.OnActivity(DownloadFranchisesActivity, mock.Anything).Return(
		DownloadFranchisesResult{Count: 32, FromCache: false}, nil)
	s.env.OnActivity(UpsertFranchisesActivity, mock.Anything).Return(
		UpsertFranchisesResult{FranchisesUpserted: 32}, nil)
	s.env.OnActivity(DownloadSeasonsManifestActivity, mock.Anything).Return(
		DownloadSeasonsManifestResult{Count: 107, FromCache: false}, nil)
	s.env.OnActivity(UpsertSeasonsActivity, mock.Anything).Return(
		UpsertSeasonsResult{SeasonsUpserted: 107}, nil)
	// Mock InitializeSeasonTeamsActivity - will be called for each season read from cache
	// Use .Maybe() since the number of seasons comes from SideEffect reading real cache
	s.env.OnActivity(InitializeSeasonTeamsActivity, mock.Anything, mock.AnythingOfType("int")).
		Maybe().
		Return(InitializeSeasonTeamsResult{
			UpsertResult: UpsertSeasonTeamsResult{TeamsUpserted: 30},
		}, nil)

	s.env.ExecuteWorkflow(InitializeWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result InitializeResult
	s.NoError(s.env.GetWorkflowResult(&result))
	s.Equal(32, result.FranchisesFetched)
	s.Equal(32, result.FranchisesUpserted)
	s.Equal(107, result.SeasonsFetched)
	s.Equal(107, result.SeasonsUpserted)
}

func (s *InitializeWorkflowTestSuite) TestInitializeWorkflow_FetchFranchisesError() {
	s.env.OnActivity(DownloadFranchisesActivity, mock.Anything).Return(
		DownloadFranchisesResult{}, errors.New("NHL API error"))

	s.env.ExecuteWorkflow(InitializeWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
	s.Contains(s.env.GetWorkflowError().Error(), "NHL API error")
}

func (s *InitializeWorkflowTestSuite) TestInitializeWorkflow_UpsertFranchisesError() {
	s.env.OnActivity(DownloadFranchisesActivity, mock.Anything).Return(
		DownloadFranchisesResult{Count: 32}, nil)
	s.env.OnActivity(UpsertFranchisesActivity, mock.Anything).Return(
		UpsertFranchisesResult{}, errors.New("database error"))

	s.env.ExecuteWorkflow(InitializeWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
	s.Contains(s.env.GetWorkflowError().Error(), "database error")
}

func (s *InitializeWorkflowTestSuite) TestInitializeWorkflow_FetchSeasonsError() {
	s.env.OnActivity(DownloadFranchisesActivity, mock.Anything).Return(
		DownloadFranchisesResult{Count: 32}, nil)
	s.env.OnActivity(UpsertFranchisesActivity, mock.Anything).Return(
		UpsertFranchisesResult{FranchisesUpserted: 32}, nil)
	s.env.OnActivity(DownloadSeasonsManifestActivity, mock.Anything).Return(
		DownloadSeasonsManifestResult{}, errors.New("manifest fetch error"))

	s.env.ExecuteWorkflow(InitializeWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
	s.Contains(s.env.GetWorkflowError().Error(), "manifest fetch error")
}

func (s *InitializeWorkflowTestSuite) TestInitializeWorkflow_UpsertSeasonsError() {
	s.env.OnActivity(DownloadFranchisesActivity, mock.Anything).Return(
		DownloadFranchisesResult{Count: 32}, nil)
	s.env.OnActivity(UpsertFranchisesActivity, mock.Anything).Return(
		UpsertFranchisesResult{FranchisesUpserted: 32}, nil)
	s.env.OnActivity(DownloadSeasonsManifestActivity, mock.Anything).Return(
		DownloadSeasonsManifestResult{Count: 107}, nil)
	s.env.OnActivity(UpsertSeasonsActivity, mock.Anything).Return(
		UpsertSeasonsResult{}, errors.New("seasons db error"))

	s.env.ExecuteWorkflow(InitializeWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
	s.Contains(s.env.GetWorkflowError().Error(), "seasons db error")
}
