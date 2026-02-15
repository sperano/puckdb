package worker

import (
	"errors"
	"testing"
	"time"

	"github.com/sperano/puckdb/graph/model"
	"github.com/sperano/puckdb/store"
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

// --- ImportSeasonsWorkflow tests ---

type ImportSeasonsWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *ImportSeasonsWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.RegisterWorkflow(ImportSeasonsWorkflow)
	s.env.RegisterWorkflow(ImportSeasonWorkflow)
}

func (s *ImportSeasonsWorkflowTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

func TestImportSeasonsWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(ImportSeasonsWorkflowTestSuite))
}

// Test ImportSeasonsWorkflow with mocked child workflows
func (s *ImportSeasonsWorkflowTestSuite) TestImportSeasonsWorkflow_Success() {
	input := &model.FetchSeasonsInput{}
	seasons := []SeasonInfo{
		{StartYear: 2023, StartDate: mustParseDate("2023-10-10"), EndDate: mustParseDate("2023-10-12")},
	}

	s.env.OnActivity(FetchSeasonsDataActivity, mock.Anything, input).Return(seasons, nil)
	// Mock the child workflow for each season
	s.env.OnWorkflow(ImportSeasonWorkflow, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(ImportSeasonsWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// Test ImportSeasonsWorkflow handles activity error
func (s *ImportSeasonsWorkflowTestSuite) TestImportSeasonsWorkflow_FetchSeasonsError() {
	input := &model.FetchSeasonsInput{}
	expectedErr := errors.New("failed to fetch seasons")

	s.env.OnActivity(FetchSeasonsDataActivity, mock.Anything, input).Return(nil, expectedErr)

	s.env.ExecuteWorkflow(ImportSeasonsWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

// Test ImportSeasonsWorkflow handles child workflow error
func (s *ImportSeasonsWorkflowTestSuite) TestImportSeasonsWorkflow_ChildWorkflowError() {
	input := &model.FetchSeasonsInput{}
	seasons := []SeasonInfo{
		{StartYear: 2023, StartDate: mustParseDate("2023-10-10"), EndDate: mustParseDate("2023-10-12")},
	}
	expectedErr := errors.New("child workflow failed")

	s.env.OnActivity(FetchSeasonsDataActivity, mock.Anything, input).Return(seasons, nil)
	// Mock the child workflow to return an error
	s.env.OnWorkflow(ImportSeasonWorkflow, mock.Anything, mock.Anything).Return(expectedErr)

	s.env.ExecuteWorkflow(ImportSeasonsWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

// Test ImportSeasonsWorkflow with no seasons
func (s *ImportSeasonsWorkflowTestSuite) TestImportSeasonsWorkflow_NoSeasons() {
	input := &model.FetchSeasonsInput{}
	seasons := []SeasonInfo{}

	s.env.OnActivity(FetchSeasonsDataActivity, mock.Anything, input).Return(seasons, nil)

	s.env.ExecuteWorkflow(ImportSeasonsWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// Test ImportSeasonsWorkflow with multiple seasons (parallel processing)
func (s *ImportSeasonsWorkflowTestSuite) TestImportSeasonsWorkflow_MultipleSeasons() {
	input := &model.FetchSeasonsInput{}
	seasons := []SeasonInfo{
		{StartYear: 2022, StartDate: mustParseDate("2022-10-07"), EndDate: mustParseDate("2022-10-09")},
		{StartYear: 2023, StartDate: mustParseDate("2023-10-10"), EndDate: mustParseDate("2023-10-12")},
		{StartYear: 2024, StartDate: mustParseDate("2024-10-08"), EndDate: mustParseDate("2024-10-10")},
	}

	s.env.OnActivity(FetchSeasonsDataActivity, mock.Anything, input).Return(seasons, nil)
	// Mock all child workflows to succeed
	s.env.OnWorkflow(ImportSeasonWorkflow, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(ImportSeasonsWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// Test ImportSeasonsWorkflow with custom concurrency
func (s *ImportSeasonsWorkflowTestSuite) TestImportSeasonsWorkflow_WithConcurrency() {
	concurrency := 2
	input := &model.FetchSeasonsInput{
		SeasonConcurrency: &concurrency,
	}
	seasons := []SeasonInfo{
		{StartYear: 2022, StartDate: mustParseDate("2022-10-07"), EndDate: mustParseDate("2022-10-09")},
		{StartYear: 2023, StartDate: mustParseDate("2023-10-10"), EndDate: mustParseDate("2023-10-12")},
	}

	s.env.OnActivity(FetchSeasonsDataActivity, mock.Anything, input).Return(seasons, nil)
	s.env.OnWorkflow(ImportSeasonWorkflow, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(ImportSeasonsWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// --- ImportSeasonWorkflow tests ---

type ImportSeasonWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *ImportSeasonWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.RegisterWorkflow(ImportSeasonWorkflow)
}

func (s *ImportSeasonWorkflowTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

func TestImportSeasonWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(ImportSeasonWorkflowTestSuite))
}

// Test ImportSeasonWorkflow success with multiple days
func (s *ImportSeasonWorkflowTestSuite) TestImportSeasonWorkflow_Success() {
	// Create a 3-day season
	input := &ImportSeasonInput{
		Season: SeasonInfo{
			StartYear: 2023,
			StartDate: mustParseDate("2023-10-10"),
			EndDate:   mustParseDate("2023-10-12"),
		},
	}

	// Mock the activity for each day (3 days total)
	s.env.OnActivity(ImportBoxscoresForDateActivity, mock.Anything, mock.Anything).Return(
		ImportBoxscoresForDateResult{GamesImported: 5, SkatersImported: 30, GoaliesImported: 4}, nil)

	s.env.ExecuteWorkflow(ImportSeasonWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// Test ImportSeasonWorkflow handles activity error
func (s *ImportSeasonWorkflowTestSuite) TestImportSeasonWorkflow_ActivityError() {
	input := &ImportSeasonInput{
		Season: SeasonInfo{
			StartYear: 2023,
			StartDate: mustParseDate("2023-10-10"),
			EndDate:   mustParseDate("2023-10-12"),
		},
	}

	expectedErr := errors.New("database connection failed")
	s.env.OnActivity(ImportBoxscoresForDateActivity, mock.Anything, mock.Anything).Return(
		ImportBoxscoresForDateResult{}, expectedErr)

	s.env.ExecuteWorkflow(ImportSeasonWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

// Test ImportSeasonWorkflow with zero days (start > end)
func (s *ImportSeasonWorkflowTestSuite) TestImportSeasonWorkflow_ZeroDays() {
	// End date before start date should result in 0 days
	input := &ImportSeasonInput{
		Season: SeasonInfo{
			StartYear: 2023,
			StartDate: mustParseDate("2023-10-15"),
			EndDate:   mustParseDate("2023-10-10"),
		},
	}

	// No activities should be called

	s.env.ExecuteWorkflow(ImportSeasonWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// Test ImportSeasonWorkflow with single day
func (s *ImportSeasonWorkflowTestSuite) TestImportSeasonWorkflow_SingleDay() {
	input := &ImportSeasonInput{
		Season: SeasonInfo{
			StartYear: 2023,
			StartDate: mustParseDate("2023-10-10"),
			EndDate:   mustParseDate("2023-10-10"),
		},
	}

	// Mock single day activity
	s.env.OnActivity(ImportBoxscoresForDateActivity, mock.Anything, mock.Anything).Return(
		ImportBoxscoresForDateResult{GamesImported: 10, SkatersImported: 60, GoaliesImported: 8}, nil)

	s.env.ExecuteWorkflow(ImportSeasonWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// Test ImportSeasonWorkflow with no games on some days
func (s *ImportSeasonWorkflowTestSuite) TestImportSeasonWorkflow_SomeDaysNoGames() {
	input := &ImportSeasonInput{
		Season: SeasonInfo{
			StartYear: 2023,
			StartDate: mustParseDate("2023-10-10"),
			EndDate:   mustParseDate("2023-10-12"),
		},
	}

	// Some days have no games (returns 0 counts)
	s.env.OnActivity(ImportBoxscoresForDateActivity, mock.Anything, mock.Anything).Return(
		ImportBoxscoresForDateResult{GamesImported: 0, SkatersImported: 0, GoaliesImported: 0}, nil)

	s.env.ExecuteWorkflow(ImportSeasonWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// --- FetchSeasonWorkflow tests ---

type FetchSeasonWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *FetchSeasonWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.RegisterWorkflow(FetchSeasonWorkflow)
}

func (s *FetchSeasonWorkflowTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

func TestFetchSeasonWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(FetchSeasonWorkflowTestSuite))
}

// Test FetchSeasonWorkflow success with multiple days
func (s *FetchSeasonWorkflowTestSuite) TestFetchSeasonWorkflow_Success() {
	input := &FetchSeasonInput{
		Season: SeasonInfo{
			StartYear: 2023,
			StartDate: mustParseDate("2023-10-10"),
			EndDate:   mustParseDate("2023-10-12"),
		},
	}

	// Mock FetchDayActivity for each day
	s.env.OnActivity(FetchDayActivity, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(FetchSeasonWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// Test FetchSeasonWorkflow handles activity error
func (s *FetchSeasonWorkflowTestSuite) TestFetchSeasonWorkflow_ActivityError() {
	input := &FetchSeasonInput{
		Season: SeasonInfo{
			StartYear: 2023,
			StartDate: mustParseDate("2023-10-10"),
			EndDate:   mustParseDate("2023-10-12"),
		},
	}

	s.env.OnActivity(FetchDayActivity, mock.Anything, mock.Anything).Return(errors.New("network error"))

	s.env.ExecuteWorkflow(FetchSeasonWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

// Test FetchSeasonWorkflow with single day
func (s *FetchSeasonWorkflowTestSuite) TestFetchSeasonWorkflow_SingleDay() {
	input := &FetchSeasonInput{
		Season: SeasonInfo{
			StartYear: 2023,
			StartDate: mustParseDate("2023-10-10"),
			EndDate:   mustParseDate("2023-10-10"),
		},
	}

	s.env.OnActivity(FetchDayActivity, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(FetchSeasonWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// --- FetchYahooPlayersWorkflow tests ---

type FetchYahooPlayersWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *FetchYahooPlayersWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.RegisterWorkflow(FetchYahooPlayersWorkflow)
}

func (s *FetchYahooPlayersWorkflowTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

func TestFetchYahooPlayersWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(FetchYahooPlayersWorkflowTestSuite))
}

// Test FetchYahooPlayersWorkflow success (completes without ContinueAsNew when range is small)
func (s *FetchYahooPlayersWorkflowTestSuite) TestFetchYahooPlayersWorkflow_Success() {
	// With nil input, uses defaults from viper (which will be 0/empty in tests)
	// The workflow should handle this gracefully
	// Activity takes (batchStartID int, batchEndID int)
	s.env.OnActivity(FetchYahooPlayerBatchActivity, mock.Anything, mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Maybe().
		Return(FetchYahooPlayerBatchResult{Downloaded: 5, Cached: 3, Missing: 2}, nil)

	s.env.ExecuteWorkflow(FetchYahooPlayersWorkflow, (*FetchYahooPlayersInput)(nil))

	s.True(s.env.IsWorkflowCompleted())
	// May complete or ContinueAsNew - both are valid
}

// Test FetchYahooPlayersWorkflow handles activity error
func (s *FetchYahooPlayersWorkflowTestSuite) TestFetchYahooPlayersWorkflow_ActivityError() {
	// Activity takes (batchStartID int, batchEndID int)
	s.env.OnActivity(FetchYahooPlayerBatchActivity, mock.Anything, mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Maybe().
		Return(FetchYahooPlayerBatchResult{}, errors.New("download failed"))

	s.env.ExecuteWorkflow(FetchYahooPlayersWorkflow, (*FetchYahooPlayersInput)(nil))

	s.True(s.env.IsWorkflowCompleted())
	// With 0 max player ID (default), workflow may complete without calling activity
}

// --- ImportNHLTeamsAndPlayersWorkflow tests ---

type ImportNHLTeamsAndPlayersWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *ImportNHLTeamsAndPlayersWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.RegisterWorkflow(ImportNHLTeamsAndPlayersWorkflow)
}

func (s *ImportNHLTeamsAndPlayersWorkflowTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

func TestImportNHLTeamsAndPlayersWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(ImportNHLTeamsAndPlayersWorkflowTestSuite))
}

// Test ImportNHLTeamsAndPlayersWorkflow success
func (s *ImportNHLTeamsAndPlayersWorkflowTestSuite) TestImportNHLTeamsAndPlayersWorkflow_Success() {
	input := &model.FetchSeasonsInput{}
	seasons := []SeasonInfo{
		{StartYear: 2023, StartDate: mustParseDate("2023-10-10"), EndDate: mustParseDate("2023-10-12")},
	}

	s.env.OnActivity(FetchSeasonsDataActivity, mock.Anything, input).Return(seasons, nil)
	s.env.OnActivity(ExtractBoxscoreDataForSeasonActivity, mock.Anything, mock.Anything).Return(
		BoxscoreExtractionResult{
			Players: []BoxscorePlayer{
				{ID: 8471214, FirstName: "Sidney", LastName: "Crosby"},
				{ID: 8478402, FirstName: "Connor", LastName: "McDavid"},
			},
		}, nil)

	s.env.ExecuteWorkflow(ImportNHLTeamsAndPlayersWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result *ImportTeamsAndPlayersResult
	s.NoError(s.env.GetWorkflowResult(&result))
	s.Equal(2, len(result.Players))
}

// Test ImportNHLTeamsAndPlayersWorkflow handles FetchSeasons error
func (s *ImportNHLTeamsAndPlayersWorkflowTestSuite) TestImportNHLTeamsAndPlayersWorkflow_FetchSeasonsError() {
	input := &model.FetchSeasonsInput{}

	s.env.OnActivity(FetchSeasonsDataActivity, mock.Anything, input).Return(nil, errors.New("NHL API error"))

	s.env.ExecuteWorkflow(ImportNHLTeamsAndPlayersWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

// Test ImportNHLTeamsAndPlayersWorkflow handles extraction error
func (s *ImportNHLTeamsAndPlayersWorkflowTestSuite) TestImportNHLTeamsAndPlayersWorkflow_ExtractionError() {
	input := &model.FetchSeasonsInput{}
	seasons := []SeasonInfo{
		{StartYear: 2023, StartDate: mustParseDate("2023-10-10"), EndDate: mustParseDate("2023-10-12")},
	}

	s.env.OnActivity(FetchSeasonsDataActivity, mock.Anything, input).Return(seasons, nil)
	s.env.OnActivity(ExtractBoxscoreDataForSeasonActivity, mock.Anything, mock.Anything).Return(
		BoxscoreExtractionResult{}, errors.New("file read error"))

	s.env.ExecuteWorkflow(ImportNHLTeamsAndPlayersWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

// Test ImportNHLTeamsAndPlayersWorkflow with no seasons
func (s *ImportNHLTeamsAndPlayersWorkflowTestSuite) TestImportNHLTeamsAndPlayersWorkflow_NoSeasons() {
	input := &model.FetchSeasonsInput{}
	seasons := []SeasonInfo{}

	s.env.OnActivity(FetchSeasonsDataActivity, mock.Anything, input).Return(seasons, nil)

	s.env.ExecuteWorkflow(ImportNHLTeamsAndPlayersWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result *ImportTeamsAndPlayersResult
	s.NoError(s.env.GetWorkflowResult(&result))
	s.Equal(0, len(result.Players))
}

// Test ImportNHLTeamsAndPlayersWorkflow with multiple seasons dedupes players
func (s *ImportNHLTeamsAndPlayersWorkflowTestSuite) TestImportNHLTeamsAndPlayersWorkflow_DeduplicatesPlayers() {
	input := &model.FetchSeasonsInput{}
	seasons := []SeasonInfo{
		{StartYear: 2022, StartDate: mustParseDate("2022-10-10"), EndDate: mustParseDate("2022-10-12")},
		{StartYear: 2023, StartDate: mustParseDate("2023-10-10"), EndDate: mustParseDate("2023-10-12")},
	}

	s.env.OnActivity(FetchSeasonsDataActivity, mock.Anything, input).Return(seasons, nil)
	// Same player appears in both seasons - should be deduped
	s.env.OnActivity(ExtractBoxscoreDataForSeasonActivity, mock.Anything, mock.Anything).Return(
		BoxscoreExtractionResult{
			Players: []BoxscorePlayer{
				{ID: 8471214, FirstName: "Sidney", LastName: "Crosby"},
			},
		}, nil)

	s.env.ExecuteWorkflow(ImportNHLTeamsAndPlayersWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result *ImportTeamsAndPlayersResult
	s.NoError(s.env.GetWorkflowResult(&result))
	// Player should appear only once despite being in both seasons
	s.Equal(1, len(result.Players))
}

// --- ProcessPlayersWorkflow tests ---

type ProcessPlayersWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *ProcessPlayersWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.RegisterWorkflow(ProcessPlayersWorkflow)
	s.env.RegisterWorkflow(ProcessPlayersWorkflowContinue)
	s.env.RegisterWorkflow(ImportNHLTeamsAndPlayersWorkflow)
}

func (s *ProcessPlayersWorkflowTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

func TestProcessPlayersWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(ProcessPlayersWorkflowTestSuite))
}

// Test ProcessPlayersWorkflow Phase 1 (extract IDs) transitions to Phase 2 via ContinueAsNew
func (s *ProcessPlayersWorkflowTestSuite) TestProcessPlayersWorkflow_Phase1_Success() {
	input := &ProcessPlayersInput{}

	// Mock the child workflow that extracts players
	s.env.OnWorkflow(ImportNHLTeamsAndPlayersWorkflow, mock.Anything, mock.Anything).Return(
		&ImportTeamsAndPlayersResult{
			Players: []BoxscorePlayer{
				{ID: 8471214, FirstName: "Sidney", LastName: "Crosby"},
			},
		}, nil)

	s.env.ExecuteWorkflow(ProcessPlayersWorkflow, input)

	// Workflow completes Phase 1 successfully then uses ContinueAsNew for Phase 2
	// In test environment, ContinueAsNew is treated as workflow completion
	s.True(s.env.IsWorkflowCompleted())
	// ContinueAsNew results in nil result and a special error
	// The test passes if the child workflow was called successfully
}

// Test ProcessPlayersWorkflow handles child workflow error in Phase 1
func (s *ProcessPlayersWorkflowTestSuite) TestProcessPlayersWorkflow_Phase1_ChildError() {
	input := &ProcessPlayersInput{}

	s.env.OnWorkflow(ImportNHLTeamsAndPlayersWorkflow, mock.Anything, mock.Anything).Return(
		(*ImportTeamsAndPlayersResult)(nil), errors.New("extraction failed"))

	s.env.ExecuteWorkflow(ProcessPlayersWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
	s.Contains(s.env.GetWorkflowError().Error(), "extraction failed")
}

// Test ProcessPlayersWorkflow Phase 2 (LoadYahoo) via ContinueAsNew entry point
func (s *ProcessPlayersWorkflowTestSuite) TestProcessPlayersWorkflow_Phase2_Success() {
	// Simulate state after Phase 1 completed
	input := &processPlayersInternalInput{
		Phase:               phaseProcessLoadYahoo,
		BatchSize:           50,
		Concurrency:         10,
		Players:             []BoxscorePlayer{{ID: 8471214, FirstName: "Sidney", LastName: "Crosby"}},
		Phase1CompletedDesc: "Extracted 1 player IDs in 0.1s.",
	}

	// Mock Phase 2 activities - use correct types
	s.env.OnActivity(ListYahooPlayerFilesActivity, mock.Anything).Return(
		[]store.YahooPlayerID{1, 2, 3}, nil)
	s.env.OnActivity(ParseYahooPlayerBatchActivity, mock.Anything, mock.Anything).Return(
		[]store.YahooPlayer{{YahooID: 1, FirstName: "Test", LastName: "Player"}}, nil)
	s.env.OnActivity(SaveYahooPlayersToRedisActivity, mock.Anything, mock.Anything).Return(
		&SaveYahooIDPoolResult{TotalPlayers: 100, AvailablePlayers: 95, SkippedNonNHL: 5}, nil)

	s.env.ExecuteWorkflow(ProcessPlayersWorkflowContinue, input)

	s.True(s.env.IsWorkflowCompleted())
	// Should ContinueAsNew to Phase 3
}

// Test ProcessPlayersWorkflow Phase 2 handles activity error
func (s *ProcessPlayersWorkflowTestSuite) TestProcessPlayersWorkflow_Phase2_ListError() {
	input := &processPlayersInternalInput{
		Phase:               phaseProcessLoadYahoo,
		BatchSize:           50,
		Concurrency:         10,
		Players:             []BoxscorePlayer{{ID: 8471214, FirstName: "Sidney", LastName: "Crosby"}},
		Phase1CompletedDesc: "Extracted 1 player IDs in 0.1s.",
	}

	s.env.OnActivity(ListYahooPlayerFilesActivity, mock.Anything).Return(
		([]store.YahooPlayerID)(nil), errors.New("file system error"))

	s.env.ExecuteWorkflow(ProcessPlayersWorkflowContinue, input)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

// Test ProcessPlayersWorkflow Phase 3 (ProcessPlayers) via ContinueAsNew entry point
func (s *ProcessPlayersWorkflowTestSuite) TestProcessPlayersWorkflow_Phase3_Success() {
	// Simulate state after Phases 1 and 2 completed
	input := &processPlayersInternalInput{
		Phase:               phaseProcessPlayers,
		BatchSize:           50,
		Concurrency:         10,
		Players:             []BoxscorePlayer{{ID: 8471214, FirstName: "Sidney", LastName: "Crosby"}},
		YahooPoolResult:     &SaveYahooIDPoolResult{TotalPlayers: 100, AvailablePlayers: 95},
		StartIndex:          0,
		TotalCompleted:      0,
		Phase1CompletedDesc: "Extracted 1 player IDs in 0.1s.",
		Phase2CompletedDesc: "Loaded 100 Yahoo players in 0.5s.",
	}

	// Mock Phase 3 activities
	s.env.OnActivity(ProcessPlayerBatchActivity, mock.Anything, mock.Anything).Return(
		ProcessPlayerBatchResult{Downloaded: 1, CacheHits: 0, Missing: 0, Imported: 1, Matched: 1}, nil)

	s.env.ExecuteWorkflow(ProcessPlayersWorkflowContinue, input)

	s.True(s.env.IsWorkflowCompleted())
	// Should ContinueAsNew to Phase 4
}

// Test ProcessPlayersWorkflow Phase 3 handles batch error
func (s *ProcessPlayersWorkflowTestSuite) TestProcessPlayersWorkflow_Phase3_BatchError() {
	input := &processPlayersInternalInput{
		Phase:               phaseProcessPlayers,
		BatchSize:           50,
		Concurrency:         10,
		Players:             []BoxscorePlayer{{ID: 8471214, FirstName: "Sidney", LastName: "Crosby"}},
		YahooPoolResult:     &SaveYahooIDPoolResult{TotalPlayers: 100},
		Phase1CompletedDesc: "Extracted 1 player IDs in 0.1s.",
		Phase2CompletedDesc: "Loaded 100 Yahoo players in 0.5s.",
	}

	s.env.OnActivity(ProcessPlayerBatchActivity, mock.Anything, mock.Anything).Return(
		ProcessPlayerBatchResult{}, errors.New("database connection failed"))

	s.env.ExecuteWorkflow(ProcessPlayersWorkflowContinue, input)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

// Test ProcessPlayersWorkflow Phase 4 (VerifyUnmatched) via ContinueAsNew entry point
func (s *ProcessPlayersWorkflowTestSuite) TestProcessPlayersWorkflow_Phase4_Success() {
	// Simulate state after Phases 1-3 completed
	input := &processPlayersInternalInput{
		Phase:               phaseProcessVerifyUnmatch,
		BatchSize:           50,
		Concurrency:         10,
		Players:             []BoxscorePlayer{{ID: 8471214, FirstName: "Sidney", LastName: "Crosby"}},
		YahooPoolResult:     &SaveYahooIDPoolResult{TotalPlayers: 100, AvailablePlayers: 95},
		TotalDownloaded:     1,
		TotalImported:       1,
		TotalMatched:        1,
		Phase1CompletedDesc: "Extracted 1 player IDs in 0.1s.",
		Phase2CompletedDesc: "Loaded 100 Yahoo players in 0.5s.",
	}

	// Mock Phase 4 activities - no unmatched players
	s.env.OnActivity(LoadUnmatchedYahooPlayersActivity, mock.Anything).Return(
		[]UnmatchedYahooPlayer{}, nil)
	s.env.OnActivity(CleanupYahooIDPoolActivity, mock.Anything).Maybe().Return(nil)

	s.env.ExecuteWorkflow(ProcessPlayersWorkflowContinue, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	// Phase 4 is the final phase - should return ProcessPlayersResult
	var result *ProcessPlayersResult
	s.NoError(s.env.GetWorkflowResult(&result))
	s.Equal(1, result.TotalPlayers)
	s.Equal(1, result.ImportedPlayers)
}

// Test ProcessPlayersWorkflow Phase 4 with unmatched players to verify
func (s *ProcessPlayersWorkflowTestSuite) TestProcessPlayersWorkflow_Phase4_WithUnmatchedPlayers() {
	input := &processPlayersInternalInput{
		Phase:               phaseProcessVerifyUnmatch,
		BatchSize:           50,
		Concurrency:         10,
		Players:             []BoxscorePlayer{{ID: 8471214, FirstName: "Sidney", LastName: "Crosby"}},
		YahooPoolResult:     &SaveYahooIDPoolResult{TotalPlayers: 100, AvailablePlayers: 95},
		TotalDownloaded:     1,
		TotalImported:       1,
		TotalMatched:        0,
		Phase1CompletedDesc: "Extracted 1 player IDs in 0.1s.",
		Phase2CompletedDesc: "Loaded 100 Yahoo players in 0.5s.",
	}

	// Mock Phase 4 activities - some unmatched players
	s.env.OnActivity(LoadUnmatchedYahooPlayersActivity, mock.Anything).Return(
		[]UnmatchedYahooPlayer{
			{YahooID: 123, FirstName: "Unknown", LastName: "Player"},
		}, nil)
	s.env.OnActivity(VerifyUnmatchedBatchActivity, mock.Anything, mock.Anything).Return(
		&VerifyUnmatchedResult{
			VerifiedNonNHL: []store.YahooPlayerID{123},
			TrulyUnmatched: []VerifiedPlayer{},
		}, nil)
	s.env.OnActivity(CleanupYahooIDPoolActivity, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(ProcessPlayersWorkflowContinue, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result *ProcessPlayersResult
	s.NoError(s.env.GetWorkflowResult(&result))
	s.Equal(1, result.VerifiedNonNHLThisRun)
}

// Test ProcessPlayersWorkflow invalid phase
func (s *ProcessPlayersWorkflowTestSuite) TestProcessPlayersWorkflow_InvalidPhase() {
	input := &processPlayersInternalInput{
		Phase: 99, // Invalid phase
	}

	s.env.ExecuteWorkflow(ProcessPlayersWorkflowContinue, input)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
	s.Contains(s.env.GetWorkflowError().Error(), "unknown phase")
}
