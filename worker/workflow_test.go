package worker

import (
	"bytes"
	"encoding/gob"
	"errors"
	"testing"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/graph/model"
	"github.com/sperano/puckdb/store"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
)

// gobEncodeProgressReport encodes a ProgressReport as gob bytes for use in test mocks.
// This simulates the bytes that LoadProgressReportActivity returns from Redis.
func gobEncodeProgressReport(report *ProgressReport) []byte {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(report); err != nil {
		panic("gobEncodeProgressReport: " + err.Error())
	}
	return buf.Bytes()
}

// testSeasonW creates a nhl.SeasonInfo for workflow tests.
func testSeasonW(year int, startDate, endDate string) nhl.SeasonInfo {
	return nhl.SeasonInfo{
		ID:             nhl.NewSeason(year),
		StandingsStart: nhl.MustParseDate(startDate),
		StandingsEnd:   nhl.MustParseDate(endDate),
	}
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
	input := &model.SeasonsInput{}
	result := FetchSeasonsManifestResult{
		Seasons: []nhl.SeasonInfo{
			testSeasonW(2023, "2024-04-14", "2024-04-15"),
		},
		Origin: core.OriginFileSystem,
	}

	var sa *SeasonsActivities
	s.env.OnActivity(sa.FetchSeasonsManifest, mock.Anything, input).Return(result, nil)
	// Mock the child workflow for each season
	s.env.OnWorkflow(FetchSeasonWorkflow, mock.Anything, mock.Anything).Return(core.OriginCounts{core.OriginRedis: 1}, nil)

	s.env.ExecuteWorkflow(FetchSeasonsWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// Test FetchSeasonsWorkflow handles activity error
func (s *FetchSeasonsWorkflowTestSuite) TestFetchSeasonsWorkflow_FetchSeasonsError() {
	input := &model.SeasonsInput{}
	expectedErr := errors.New("failed to fetch seasons")

	var sa *SeasonsActivities
	s.env.OnActivity(sa.FetchSeasonsManifest, mock.Anything, input).Return(FetchSeasonsManifestResult{}, expectedErr)

	s.env.ExecuteWorkflow(FetchSeasonsWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

// Test FetchSeasonsWorkflow handles child workflow error
func (s *FetchSeasonsWorkflowTestSuite) TestFetchSeasonsWorkflow_ChildWorkflowError() {
	input := &model.SeasonsInput{}
	result := FetchSeasonsManifestResult{
		Seasons: []nhl.SeasonInfo{
			testSeasonW(2023, "2024-04-14", "2024-04-15"),
		},
		Origin: core.OriginFileSystem,
	}
	expectedErr := errors.New("child workflow failed")

	var sa *SeasonsActivities
	s.env.OnActivity(sa.FetchSeasonsManifest, mock.Anything, input).Return(result, nil)
	// Mock the child workflow to return an error
	s.env.OnWorkflow(FetchSeasonWorkflow, mock.Anything, mock.Anything).Return((core.OriginCounts)(nil), expectedErr)

	s.env.ExecuteWorkflow(FetchSeasonsWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

// Test FetchSeasonsWorkflow with no seasons
func (s *FetchSeasonsWorkflowTestSuite) TestFetchSeasonsWorkflow_NoSeasons() {
	input := &model.SeasonsInput{}
	result := FetchSeasonsManifestResult{
		Seasons: []nhl.SeasonInfo{},
		Origin:  core.OriginFileSystem,
	}

	var sa *SeasonsActivities
	s.env.OnActivity(sa.FetchSeasonsManifest, mock.Anything, input).Return(result, nil)

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
	var fa *FranchiseActivities
	s.env.OnActivity(fa.FetchFranchises, mock.Anything).Return(
		FetchFranchisesResult{Origin: core.OriginRemoteNHLAPI}, nil)
	s.env.OnActivity(fa.UpsertFranchises, mock.Anything).Return(
		UpsertFranchisesResult{FranchisesUpserted: 32}, nil)
	var sa *SeasonsActivities
	s.env.OnActivity(sa.FetchSeasonsManifest, mock.Anything, mock.Anything).Return(
		FetchSeasonsManifestResult{
			Seasons: []nhl.SeasonInfo{
				testSeasonW(2022, "2022-10-07", "2023-04-14"),
				testSeasonW(2023, "2023-10-10", "2024-04-18"),
				testSeasonW(2024, "2024-10-04", "2025-04-17"),
			},
			Origin: core.OriginFileSystem,
		}, nil)
	s.env.OnActivity(sa.UpsertSeasons, mock.Anything).Return(
		UpsertSeasonsResult{SeasonsUpserted: 3}, nil)
	// Mock InitializeSeasonTeamsActivity - called for each season returned by FetchSeasonsManifest
	// Note: We must include valid Season values in ALL nested result structs, otherwise zero-value
	// Season{startYear: 0} serializes to "01" which fails to parse. The specific season values don't
	// matter here since the test only validates the aggregated TeamsUpserted count.
	s.env.OnActivity(sa.InitializeSeasonTeamsActivity, mock.Anything, mock.AnythingOfType("nhl.Season")).
		Return(InitializeSeasonTeamsResult{
			Season:         nhl.NewSeason(2022),
			DownloadResult: DownloadSeasonStandingsResult{Season: nhl.NewSeason(2022), TeamCount: 32},
			UpsertResult:   UpsertSeasonTeamsResult{Season: nhl.NewSeason(2022), TeamsUpserted: 30},
		}, nil)

	s.env.ExecuteWorkflow(InitializeWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result InitializeResult
	s.NoError(s.env.GetWorkflowResult(&result))
	s.Equal(core.OriginRemoteNHLAPI, result.FranchisesOrigin)
	s.Equal(32, result.FranchisesUpserted)
	s.Equal(core.OriginFileSystem, result.SeasonsOrigin)
	s.Equal(3, result.SeasonsUpserted)
	s.Equal(90, result.SeasonTeamsUpserted) // 3 seasons × 30 teams each
}

func (s *InitializeWorkflowTestSuite) TestInitializeWorkflow_FetchFranchisesError() {
	var fa *FranchiseActivities
	s.env.OnActivity(fa.FetchFranchises, mock.Anything).Return(
		FetchFranchisesResult{}, errors.New("NHL API error"))

	s.env.ExecuteWorkflow(InitializeWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
	s.Contains(s.env.GetWorkflowError().Error(), "NHL API error")
}

func (s *InitializeWorkflowTestSuite) TestInitializeWorkflow_UpsertFranchisesError() {
	var fa *FranchiseActivities
	s.env.OnActivity(fa.FetchFranchises, mock.Anything).Return(
		FetchFranchisesResult{}, nil)
	s.env.OnActivity(fa.UpsertFranchises, mock.Anything).Return(
		UpsertFranchisesResult{}, errors.New("database error"))

	s.env.ExecuteWorkflow(InitializeWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
	s.Contains(s.env.GetWorkflowError().Error(), "database error")
}

func (s *InitializeWorkflowTestSuite) TestInitializeWorkflow_FetchSeasonsError() {
	var fa *FranchiseActivities
	s.env.OnActivity(fa.FetchFranchises, mock.Anything).Return(
		FetchFranchisesResult{}, nil)
	s.env.OnActivity(fa.UpsertFranchises, mock.Anything).Return(
		UpsertFranchisesResult{FranchisesUpserted: 32}, nil)
	var sa *SeasonsActivities
	s.env.OnActivity(sa.FetchSeasonsManifest, mock.Anything, mock.Anything).Return(
		FetchSeasonsManifestResult{}, errors.New("manifest fetch error"))

	s.env.ExecuteWorkflow(InitializeWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
	s.Contains(s.env.GetWorkflowError().Error(), "manifest fetch error")
}

func (s *InitializeWorkflowTestSuite) TestInitializeWorkflow_UpsertSeasonsError() {
	var fa *FranchiseActivities
	s.env.OnActivity(fa.FetchFranchises, mock.Anything).Return(
		FetchFranchisesResult{}, nil)
	s.env.OnActivity(fa.UpsertFranchises, mock.Anything).Return(
		UpsertFranchisesResult{FranchisesUpserted: 32}, nil)
	var sa *SeasonsActivities
	s.env.OnActivity(sa.FetchSeasonsManifest, mock.Anything, mock.Anything).Return(
		FetchSeasonsManifestResult{
			Seasons: []nhl.SeasonInfo{testSeasonW(2023, "2023-10-10", "2024-04-18")},
			Origin:  core.OriginFileSystem,
		}, nil)
	s.env.OnActivity(sa.UpsertSeasons, mock.Anything).Return(
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
	input := &model.SeasonsInput{}
	result := FetchSeasonsManifestResult{
		Seasons: []nhl.SeasonInfo{
			testSeasonW(2023, "2023-10-10", "2023-10-12"),
		},
		Origin: core.OriginFileSystem,
	}

	var sa *SeasonsActivities
	s.env.OnActivity(sa.FetchSeasonsManifest, mock.Anything, input).Return(result, nil)
	s.env.OnWorkflow(ImportSeasonWorkflow, mock.Anything, mock.Anything).Return(core.OriginCounts{}, nil)

	s.env.ExecuteWorkflow(ImportSeasonsWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// Test ImportSeasonsWorkflow handles activity error
func (s *ImportSeasonsWorkflowTestSuite) TestImportSeasonsWorkflow_FetchSeasonsError() {
	input := &model.SeasonsInput{}
	expectedErr := errors.New("failed to fetch seasons")

	var sa *SeasonsActivities
	s.env.OnActivity(sa.FetchSeasonsManifest, mock.Anything, input).Return(FetchSeasonsManifestResult{}, expectedErr)

	s.env.ExecuteWorkflow(ImportSeasonsWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

// Test ImportSeasonsWorkflow handles child workflow error in Phase 1
func (s *ImportSeasonsWorkflowTestSuite) TestImportSeasonsWorkflow_ChildWorkflowError() {
	input := &model.SeasonsInput{}
	result := FetchSeasonsManifestResult{
		Seasons: []nhl.SeasonInfo{
			testSeasonW(2023, "2023-10-10", "2023-10-12"),
		},
		Origin: core.OriginFileSystem,
	}
	expectedErr := errors.New("child workflow failed")

	var sa *SeasonsActivities
	s.env.OnActivity(sa.FetchSeasonsManifest, mock.Anything, input).Return(result, nil)
	s.env.OnWorkflow(ImportSeasonWorkflow, mock.Anything, mock.Anything).Return(core.OriginCounts{}, expectedErr)

	s.env.ExecuteWorkflow(ImportSeasonsWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

// Test ImportSeasonsWorkflow with no seasons
func (s *ImportSeasonsWorkflowTestSuite) TestImportSeasonsWorkflow_NoSeasons() {
	input := &model.SeasonsInput{}
	result := FetchSeasonsManifestResult{
		Seasons: []nhl.SeasonInfo{},
		Origin:  core.OriginFileSystem,
	}

	var sa *SeasonsActivities
	s.env.OnActivity(sa.FetchSeasonsManifest, mock.Anything, input).Return(result, nil)

	s.env.ExecuteWorkflow(ImportSeasonsWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// Test ImportSeasonsWorkflow with multiple seasons (parallel processing)
func (s *ImportSeasonsWorkflowTestSuite) TestImportSeasonsWorkflow_MultipleSeasons() {
	input := &model.SeasonsInput{}
	result := FetchSeasonsManifestResult{
		Seasons: []nhl.SeasonInfo{
			testSeasonW(2022, "2022-10-07", "2022-10-09"),
			testSeasonW(2023, "2023-10-10", "2023-10-12"),
			testSeasonW(2024, "2024-10-08", "2024-10-10"),
		},
		Origin: core.OriginFileSystem,
	}

	var sa *SeasonsActivities
	s.env.OnActivity(sa.FetchSeasonsManifest, mock.Anything, input).Return(result, nil)
	s.env.OnWorkflow(ImportSeasonWorkflow, mock.Anything, mock.Anything).Return(core.OriginCounts{}, nil)

	s.env.ExecuteWorkflow(ImportSeasonsWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// Test ImportSeasonsWorkflow with custom concurrency
func (s *ImportSeasonsWorkflowTestSuite) TestImportSeasonsWorkflow_WithConcurrency() {
	concurrency := 2
	input := &model.SeasonsInput{
		SeasonConcurrency: &concurrency,
	}
	result := FetchSeasonsManifestResult{
		Seasons: []nhl.SeasonInfo{
			testSeasonW(2022, "2022-10-07", "2022-10-09"),
			testSeasonW(2023, "2023-10-10", "2023-10-12"),
		},
		Origin: core.OriginFileSystem,
	}

	var sa *SeasonsActivities
	s.env.OnActivity(sa.FetchSeasonsManifest, mock.Anything, input).Return(result, nil)
	s.env.OnWorkflow(ImportSeasonWorkflow, mock.Anything, mock.Anything).Return(core.OriginCounts{}, nil)

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
	season := testSeasonW(2023, "2023-10-10", "2023-10-12")

	var sa *SeasonsActivities
	s.env.OnActivity(sa.ImportDay, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(ImportSeasonWorkflow, season)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// Test ImportSeasonWorkflow handles activity error
func (s *ImportSeasonWorkflowTestSuite) TestImportSeasonWorkflow_ActivityError() {
	season := testSeasonW(2023, "2023-10-10", "2023-10-12")

	var sa *SeasonsActivities
	expectedErr := errors.New("database connection failed")
	s.env.OnActivity(sa.ImportDay, mock.Anything, mock.Anything).Return(expectedErr)

	s.env.ExecuteWorkflow(ImportSeasonWorkflow, season)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

// Test ImportSeasonWorkflow with zero days (start > end)
func (s *ImportSeasonWorkflowTestSuite) TestImportSeasonWorkflow_ZeroDays() {
	season := testSeasonW(2023, "2023-10-15", "2023-10-10")

	s.env.ExecuteWorkflow(ImportSeasonWorkflow, season)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// Test ImportSeasonWorkflow with single day
func (s *ImportSeasonWorkflowTestSuite) TestImportSeasonWorkflow_SingleDay() {
	season := testSeasonW(2023, "2023-10-10", "2023-10-10")

	var sa *SeasonsActivities
	s.env.OnActivity(sa.ImportDay, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(ImportSeasonWorkflow, season)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// Test ImportSeasonWorkflow with no games on some days
func (s *ImportSeasonWorkflowTestSuite) TestImportSeasonWorkflow_SomeDaysNoGames() {
	season := testSeasonW(2023, "2023-10-10", "2023-10-12")

	var sa *SeasonsActivities
	s.env.OnActivity(sa.ImportDay, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(ImportSeasonWorkflow, season)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// --- ImportSeasonPlayerLogsWorkflow tests ---

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

	var sa *SeasonsActivities
	s.env.OnActivity(sa.CollectSeasonPlayerIDs, mock.Anything, mock.Anything).Return([]int64{1, 2, 3}, nil)
	s.env.OnActivity(sa.ImportPlayerGameLogsBatch, mock.Anything, mock.Anything).Return(
		&ImportPlayerGameLogsBatchResult{PlayersProcessed: 3, GamesUpdated: 10}, nil)

	s.env.ExecuteWorkflow(ImportSeasonPlayerLogsWorkflow, season)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *ImportSeasonPlayerLogsWorkflowTestSuite) TestNoPlayers() {
	season := testSeasonW(2023, "2023-10-10", "2023-10-12")

	var sa *SeasonsActivities
	s.env.OnActivity(sa.CollectSeasonPlayerIDs, mock.Anything, mock.Anything).Return([]int64{}, nil)

	s.env.ExecuteWorkflow(ImportSeasonPlayerLogsWorkflow, season)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *ImportSeasonPlayerLogsWorkflowTestSuite) TestCollectError() {
	season := testSeasonW(2023, "2023-10-10", "2023-10-12")

	var sa *SeasonsActivities
	s.env.OnActivity(sa.CollectSeasonPlayerIDs, mock.Anything, mock.Anything).Return(
		([]int64)(nil), errors.New("redis error"))

	s.env.ExecuteWorkflow(ImportSeasonPlayerLogsWorkflow, season)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

func (s *ImportSeasonPlayerLogsWorkflowTestSuite) TestBatchError() {
	season := testSeasonW(2023, "2023-10-10", "2023-10-12")

	var sa *SeasonsActivities
	s.env.OnActivity(sa.CollectSeasonPlayerIDs, mock.Anything, mock.Anything).Return([]int64{1, 2, 3}, nil)
	s.env.OnActivity(sa.ImportPlayerGameLogsBatch, mock.Anything, mock.Anything).Return(
		(*ImportPlayerGameLogsBatchResult)(nil), errors.New("batch failed"))

	s.env.ExecuteWorkflow(ImportSeasonPlayerLogsWorkflow, season)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

// --- ImportPlayerLogsWorkflow tests ---

type ImportPlayerLogsWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *ImportPlayerLogsWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.RegisterWorkflow(ImportPlayerLogsWorkflow)
	s.env.RegisterWorkflow(ImportSeasonPlayerLogsWorkflow)
}

func (s *ImportPlayerLogsWorkflowTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

func TestImportPlayerLogsWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(ImportPlayerLogsWorkflowTestSuite))
}

func (s *ImportPlayerLogsWorkflowTestSuite) TestSuccess() {
	input := &model.SeasonsInput{}
	result := FetchSeasonsManifestResult{
		Seasons: []nhl.SeasonInfo{
			testSeasonW(2023, "2023-10-10", "2023-10-12"),
		},
		Origin: core.OriginFileSystem,
	}

	var sa *SeasonsActivities
	s.env.OnActivity(sa.FetchSeasonsManifest, mock.Anything, input).Return(result, nil)
	var pa *PlayerActivities
	s.env.OnActivity(pa.CountPlayersForAllSeasons, mock.Anything, mock.Anything).Return(map[int]int{2023: 100}, nil)
	s.env.OnWorkflow(ImportSeasonPlayerLogsWorkflow, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(ImportPlayerLogsWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *ImportPlayerLogsWorkflowTestSuite) TestNoSeasons() {
	input := &model.SeasonsInput{}
	result := FetchSeasonsManifestResult{
		Seasons: []nhl.SeasonInfo{},
		Origin:  core.OriginFileSystem,
	}

	var sa *SeasonsActivities
	s.env.OnActivity(sa.FetchSeasonsManifest, mock.Anything, input).Return(result, nil)

	s.env.ExecuteWorkflow(ImportPlayerLogsWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *ImportPlayerLogsWorkflowTestSuite) TestMultipleSeasons() {
	input := &model.SeasonsInput{}
	result := FetchSeasonsManifestResult{
		Seasons: []nhl.SeasonInfo{
			testSeasonW(2022, "2022-10-07", "2022-10-09"),
			testSeasonW(2023, "2023-10-10", "2023-10-12"),
		},
		Origin: core.OriginFileSystem,
	}

	var sa *SeasonsActivities
	s.env.OnActivity(sa.FetchSeasonsManifest, mock.Anything, input).Return(result, nil)
	var pa *PlayerActivities
	s.env.OnActivity(pa.CountPlayersForAllSeasons, mock.Anything, mock.Anything).Return(map[int]int{2022: 50, 2023: 100}, nil)
	s.env.OnWorkflow(ImportSeasonPlayerLogsWorkflow, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(ImportPlayerLogsWorkflow, input)

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
	season := testSeasonW(2023, "2023-10-10", "2023-10-12")

	// Mock FetchDay method for each day
	var dsa *DailyScheduleActivities
	s.env.OnActivity(dsa.FetchDay, mock.Anything, mock.Anything).Return(core.OriginCounts{core.OriginRedis: 1}, nil)

	s.env.ExecuteWorkflow(FetchSeasonWorkflow, season)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var counts core.OriginCounts
	s.NoError(s.env.GetWorkflowResult(&counts))
	s.Equal(3, counts[core.OriginRedis]) // 3 days × 1 per day
}

// Test FetchSeasonWorkflow handles activity error
func (s *FetchSeasonWorkflowTestSuite) TestFetchSeasonWorkflow_ActivityError() {
	season := testSeasonW(2023, "2023-10-10", "2023-10-12")

	var dsa *DailyScheduleActivities
	s.env.OnActivity(dsa.FetchDay, mock.Anything, mock.Anything).Return((core.OriginCounts)(nil), errors.New("network error"))

	s.env.ExecuteWorkflow(FetchSeasonWorkflow, season)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

// Test FetchSeasonWorkflow with single day
func (s *FetchSeasonWorkflowTestSuite) TestFetchSeasonWorkflow_SingleDay() {
	season := testSeasonW(2023, "2023-10-10", "2023-10-10")

	var dsa *DailyScheduleActivities
	s.env.OnActivity(dsa.FetchDay, mock.Anything, mock.Anything).Return(core.OriginCounts{core.OriginFileSystem: 1}, nil)

	s.env.ExecuteWorkflow(FetchSeasonWorkflow, season)

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
	var yahooAct *YahooActivities
	s.env.OnActivity(yahooAct.FetchYahooPlayerBatch, mock.Anything, mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Maybe().
		Return(FetchStats{Downloaded: 5, CacheHits: 3, Missing: 2}, nil)

	s.env.ExecuteWorkflow(FetchYahooPlayersWorkflow, (*FetchYahooPlayersInput)(nil))

	s.True(s.env.IsWorkflowCompleted())
	// May complete or ContinueAsNew - both are valid
}

// Test FetchYahooPlayersWorkflow handles activity error
func (s *FetchYahooPlayersWorkflowTestSuite) TestFetchYahooPlayersWorkflow_ActivityError() {
	// Activity takes (batchStartID int, batchEndID int)
	var yahooAct *YahooActivities
	s.env.OnActivity(yahooAct.FetchYahooPlayerBatch, mock.Anything, mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Maybe().
		Return(FetchStats{}, errors.New("download failed"))

	s.env.ExecuteWorkflow(FetchYahooPlayersWorkflow, (*FetchYahooPlayersInput)(nil))

	s.True(s.env.IsWorkflowCompleted())
	// With 0 max player ID (default), workflow may complete without calling activity
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
}

func (s *ProcessPlayersWorkflowTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

func TestProcessPlayersWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(ProcessPlayersWorkflowTestSuite))
}

// Test ProcessPlayersWorkflow Phase 1 (LoadYahoo) loads players from Redis and Yahoo files,
// then transitions to Phase 2 (ProcessPlayers) via ContinueAsNew.
func (s *ProcessPlayersWorkflowTestSuite) TestProcessPlayersWorkflow_Phase1_Success() {
	input := &ProcessPlayersInput{}

	var playerAct *PlayerActivities
	s.env.OnActivity(playerAct.LoadAllBoxscorePlayers, mock.Anything).Return(
		[]store.BoxscorePlayer{
			{ID: 8471214, FirstName: "Sidney", LastName: "Crosby"},
		}, nil)
	s.env.OnActivity(ListYahooPlayerFilesActivity, mock.Anything).Return(
		[]store.YahooPlayerID{1, 2, 3}, nil)
	s.env.OnActivity(ParseYahooPlayerBatchActivity, mock.Anything, mock.Anything).Return(
		[]store.YahooPlayer{{YahooID: 1, FirstName: "Test", LastName: "Player"}}, nil)
	s.env.OnActivity(SaveYahooPlayersToRedisActivity, mock.Anything, mock.Anything).Return(
		&SaveYahooIDPoolResult{TotalPlayers: 1, AvailablePlayers: 1}, nil)

	s.env.ExecuteWorkflow(ProcessPlayersWorkflow, input)

	// Phase 1 completes and ContinueAsNew into Phase 2.
	// In the test environment ContinueAsNew is treated as workflow completion.
	s.True(s.env.IsWorkflowCompleted())
}

// Test ProcessPlayersWorkflow handles LoadAllBoxscorePlayers failure in Phase 1.
func (s *ProcessPlayersWorkflowTestSuite) TestProcessPlayersWorkflow_Phase1_ChildError() {
	input := &ProcessPlayersInput{}

	var playerAct *PlayerActivities
	s.env.OnActivity(playerAct.LoadAllBoxscorePlayers, mock.Anything).Return(
		([]store.BoxscorePlayer)(nil), errors.New("redis unavailable"))

	s.env.ExecuteWorkflow(ProcessPlayersWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
	s.Contains(s.env.GetWorkflowError().Error(), "redis unavailable")
}

// Test ProcessPlayersWorkflow handles ListYahooPlayerFiles error during Phase 1 (LoadYahoo)
func (s *ProcessPlayersWorkflowTestSuite) TestProcessPlayersWorkflow_Phase1_ListYahooError() {
	input := &ProcessPlayersInput{}

	var playerAct *PlayerActivities
	s.env.OnActivity(playerAct.LoadAllBoxscorePlayers, mock.Anything).Return(
		[]store.BoxscorePlayer{{ID: 8471214, FirstName: "Sidney", LastName: "Crosby"}}, nil)
	s.env.OnActivity(ListYahooPlayerFilesActivity, mock.Anything).Return(
		([]store.YahooPlayerID)(nil), errors.New("file system error"))

	s.env.ExecuteWorkflow(ProcessPlayersWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
	s.Contains(s.env.GetWorkflowError().Error(), "file system error")
}

// Test ProcessPlayersWorkflow Phase 3 (ProcessPlayers) via ContinueAsNew entry point
func (s *ProcessPlayersWorkflowTestSuite) TestProcessPlayersWorkflow_Phase3_Success() {
	// Simulate state after Phases 1 and 2 completed
	input := &processPlayersInternalInput{
		Phase:           phaseProcessPlayers,
		BatchSize:       50,
		Concurrency:     10,
		Players:         []store.BoxscorePlayer{{ID: 8471214, FirstName: "Sidney", LastName: "Crosby"}},
		YahooPoolResult: &SaveYahooIDPoolResult{TotalPlayers: 100, AvailablePlayers: 95},
		StartIndex:      0,
		TotalCompleted:  0,
	}

	// Mock the Redis load — returns the pre-built progress report from Phase 1.
	reportBytes := gobEncodeProgressReport(NewProcessPlayersProgressReport(len(input.Players)))
	s.env.OnActivity(LoadProgressReportActivity, mock.Anything, mock.Anything).Return(reportBytes, nil)

	// Mock Phase 3 activities
	s.env.OnActivity(ProcessPlayerBatchActivity, mock.Anything, mock.Anything).Return(
		ProcessPlayerBatchResult{FetchStats: FetchStats{Downloaded: 1}, Imported: 1, Matched: 1}, nil)

	s.env.ExecuteWorkflow(ProcessPlayersWorkflowContinue, input)

	s.True(s.env.IsWorkflowCompleted())
	// Should ContinueAsNew to Phase 4
}

// Test ProcessPlayersWorkflow Phase 3 handles batch error
func (s *ProcessPlayersWorkflowTestSuite) TestProcessPlayersWorkflow_Phase3_BatchError() {
	input := &processPlayersInternalInput{
		Phase:           phaseProcessPlayers,
		BatchSize:       50,
		Concurrency:     10,
		Players:         []store.BoxscorePlayer{{ID: 8471214, FirstName: "Sidney", LastName: "Crosby"}},
		YahooPoolResult: &SaveYahooIDPoolResult{TotalPlayers: 100},
	}

	reportBytes := gobEncodeProgressReport(NewProcessPlayersProgressReport(len(input.Players)))
	s.env.OnActivity(LoadProgressReportActivity, mock.Anything, mock.Anything).Return(reportBytes, nil)

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
		Phase:           phaseVerifyUnmatched,
		BatchSize:       50,
		Concurrency:     10,
		Players:         []store.BoxscorePlayer{{ID: 8471214, FirstName: "Sidney", LastName: "Crosby"}},
		YahooPoolResult: &SaveYahooIDPoolResult{TotalPlayers: 100, AvailablePlayers: 95},
		TotalDownloaded: 1,
		TotalImported:   1,
		TotalMatched:    1,
	}

	// Mock the Redis load — returns the pre-built progress report from Phases 1-2.
	reportBytes := gobEncodeProgressReport(NewProcessPlayersProgressReport(len(input.Players)))
	s.env.OnActivity(LoadProgressReportActivity, mock.Anything, mock.Anything).Return(reportBytes, nil)

	// Mock Phase 4 activities - no unmatched players
	s.env.OnActivity(LoadUnmatchedYahooPlayersActivity, mock.Anything).Return(
		[]UnmatchedYahooPlayer{}, nil)
	s.env.OnActivity(CleanupYahooIDPoolActivity, mock.Anything).Maybe().Return(nil)

	s.env.ExecuteWorkflow(ProcessPlayersWorkflowContinue, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	// Phase 4 is the final phase - should return ProcessPlayersResult
	var res *ProcessPlayersResult
	s.NoError(s.env.GetWorkflowResult(&res))
	s.Equal(1, res.TotalPlayers)
	s.Equal(1, res.ImportedPlayers)
}

// Test ProcessPlayersWorkflow Phase 4 with unmatched players to verify
func (s *ProcessPlayersWorkflowTestSuite) TestProcessPlayersWorkflow_Phase4_WithUnmatchedPlayers() {
	input := &processPlayersInternalInput{
		Phase:           phaseVerifyUnmatched,
		BatchSize:       50,
		Concurrency:     10,
		Players:         []store.BoxscorePlayer{{ID: 8471214, FirstName: "Sidney", LastName: "Crosby"}},
		YahooPoolResult: &SaveYahooIDPoolResult{TotalPlayers: 100, AvailablePlayers: 95},
		TotalDownloaded: 1,
		TotalImported:   1,
		TotalMatched:    0,
	}

	// Mock the Redis load — returns the pre-built progress report from Phases 1-2.
	reportBytes := gobEncodeProgressReport(NewProcessPlayersProgressReport(len(input.Players)))
	s.env.OnActivity(LoadProgressReportActivity, mock.Anything, mock.Anything).Return(reportBytes, nil)

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

	var res *ProcessPlayersResult
	s.NoError(s.env.GetWorkflowResult(&res))
	s.Equal(1, res.VerifiedNonNHLThisRun)
}

// Test ProcessPlayersWorkflow invalid phase
func (s *ProcessPlayersWorkflowTestSuite) TestProcessPlayersWorkflow_InvalidPhase() {
	input := &processPlayersInternalInput{
		Phase: 99, // Invalid phase
	}

	// ContinueAsNew path loads tracker from Redis first
	reportBytes := gobEncodeProgressReport(NewProcessPlayersProgressReport(1))
	s.env.OnActivity(LoadProgressReportActivity, mock.Anything, mock.Anything).Return(reportBytes, nil)

	s.env.ExecuteWorkflow(ProcessPlayersWorkflowContinue, input)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
	s.Contains(s.env.GetWorkflowError().Error(), "unknown phase")
}
