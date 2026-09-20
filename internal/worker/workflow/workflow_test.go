package workflow

import (
	"bytes"
	"encoding/gob"
	"errors"
	"testing"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/sperano/puckdb/internal/store"
	worknhl "github.com/sperano/puckdb/internal/worker/nhl"
	workplayer "github.com/sperano/puckdb/internal/worker/player"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/sperano/puckdb/internal/worker/yahoo"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
)

// gobEncodeProgressReport encodes a shared.ProgressReport as gob bytes for use in test mocks.
// This simulates the bytes that ProgressActivities.Load returns from Redis.
func gobEncodeProgressReport(report *shared.ProgressReport) []byte {
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
	// The per-season counter sizes playoff bars via ListSeasonTeams.
	var pa *worknhl.PlayoffActivities
	s.env.OnActivity(pa.ListSeasonTeams, mock.Anything, mock.Anything).Return([]string{"TOR", "MTL"}, nil).Maybe()
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
	result := worknhl.FetchSeasonsManifestResult{
		Seasons: []nhl.SeasonInfo{
			testSeasonW(2023, "2024-04-14", "2024-04-15"),
		},
		Origin: core.OriginFileSystem,
	}

	var sa *worknhl.SeasonsActivities
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

	var sa *worknhl.SeasonsActivities
	s.env.OnActivity(sa.FetchSeasonsManifest, mock.Anything, input).Return(worknhl.FetchSeasonsManifestResult{}, expectedErr)

	s.env.ExecuteWorkflow(FetchSeasonsWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

// Test FetchSeasonsWorkflow handles child workflow error
func (s *FetchSeasonsWorkflowTestSuite) TestFetchSeasonsWorkflow_ChildWorkflowError() {
	input := &model.SeasonsInput{}
	result := worknhl.FetchSeasonsManifestResult{
		Seasons: []nhl.SeasonInfo{
			testSeasonW(2023, "2024-04-14", "2024-04-15"),
		},
		Origin: core.OriginFileSystem,
	}
	expectedErr := errors.New("child workflow failed")

	var sa *worknhl.SeasonsActivities
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
	result := worknhl.FetchSeasonsManifestResult{
		Seasons: []nhl.SeasonInfo{},
		Origin:  core.OriginFileSystem,
	}

	var sa *worknhl.SeasonsActivities
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
	var fa *worknhl.FranchiseActivities
	s.env.OnActivity(fa.FetchFranchises, mock.Anything).Return(
		worknhl.FetchFranchisesResult{Origin: core.OriginRemoteNHLAPI}, nil)
	s.env.OnActivity(fa.UpsertFranchises, mock.Anything).Return(
		worknhl.UpsertFranchisesResult{FranchisesUpserted: 32}, nil)
	var sa *worknhl.SeasonsActivities
	s.env.OnActivity(sa.FetchSeasonsManifest, mock.Anything, mock.Anything).Return(
		worknhl.FetchSeasonsManifestResult{
			Seasons: []nhl.SeasonInfo{
				testSeasonW(2022, "2022-10-07", "2023-04-14"),
				testSeasonW(2023, "2023-10-10", "2024-04-18"),
				testSeasonW(2024, "2024-10-04", "2025-04-17"),
			},
			Origin: core.OriginFileSystem,
		}, nil)
	s.env.OnActivity(sa.UpsertSeasons, mock.Anything).Return(
		worknhl.UpsertSeasonsResult{SeasonsUpserted: 3}, nil)
	// Mock InitializeSeasonTeamsActivity - called for each season returned by FetchSeasonsManifest
	// Note: We must include valid Season values in ALL nested result structs, otherwise zero-value
	// Season{startYear: 0} serializes to "01" which fails to parse. The specific season values don't
	// matter here since the test only validates the aggregated TeamsUpserted count.
	s.env.OnActivity(sa.InitializeSeasonTeamsActivity, mock.Anything, mock.AnythingOfType("nhl.Season")).
		Return(worknhl.InitializeSeasonTeamsResult{
			Season:         nhl.NewSeason(2022),
			DownloadResult: worknhl.DownloadSeasonStandingsResult{Season: nhl.NewSeason(2022), TeamCount: 32},
			UpsertResult:   worknhl.UpsertSeasonTeamsResult{Season: nhl.NewSeason(2022), TeamsUpserted: 30},
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
	var fa *worknhl.FranchiseActivities
	s.env.OnActivity(fa.FetchFranchises, mock.Anything).Return(
		worknhl.FetchFranchisesResult{}, errors.New("NHL API error"))

	s.env.ExecuteWorkflow(InitializeWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
	s.Contains(s.env.GetWorkflowError().Error(), "NHL API error")
}

func (s *InitializeWorkflowTestSuite) TestInitializeWorkflow_UpsertFranchisesError() {
	var fa *worknhl.FranchiseActivities
	s.env.OnActivity(fa.FetchFranchises, mock.Anything).Return(
		worknhl.FetchFranchisesResult{}, nil)
	s.env.OnActivity(fa.UpsertFranchises, mock.Anything).Return(
		worknhl.UpsertFranchisesResult{}, errors.New("database error"))

	s.env.ExecuteWorkflow(InitializeWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
	s.Contains(s.env.GetWorkflowError().Error(), "database error")
}

func (s *InitializeWorkflowTestSuite) TestInitializeWorkflow_FetchSeasonsError() {
	var fa *worknhl.FranchiseActivities
	s.env.OnActivity(fa.FetchFranchises, mock.Anything).Return(
		worknhl.FetchFranchisesResult{}, nil)
	s.env.OnActivity(fa.UpsertFranchises, mock.Anything).Return(
		worknhl.UpsertFranchisesResult{FranchisesUpserted: 32}, nil)
	var sa *worknhl.SeasonsActivities
	s.env.OnActivity(sa.FetchSeasonsManifest, mock.Anything, mock.Anything).Return(
		worknhl.FetchSeasonsManifestResult{}, errors.New("manifest fetch error"))

	s.env.ExecuteWorkflow(InitializeWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
	s.Contains(s.env.GetWorkflowError().Error(), "manifest fetch error")
}

func (s *InitializeWorkflowTestSuite) TestInitializeWorkflow_UpsertSeasonsError() {
	var fa *worknhl.FranchiseActivities
	s.env.OnActivity(fa.FetchFranchises, mock.Anything).Return(
		worknhl.FetchFranchisesResult{}, nil)
	s.env.OnActivity(fa.UpsertFranchises, mock.Anything).Return(
		worknhl.UpsertFranchisesResult{FranchisesUpserted: 32}, nil)
	var sa *worknhl.SeasonsActivities
	s.env.OnActivity(sa.FetchSeasonsManifest, mock.Anything, mock.Anything).Return(
		worknhl.FetchSeasonsManifestResult{
			Seasons: []nhl.SeasonInfo{testSeasonW(2023, "2023-10-10", "2024-04-18")},
			Origin:  core.OriginFileSystem,
		}, nil)
	s.env.OnActivity(sa.UpsertSeasons, mock.Anything).Return(
		worknhl.UpsertSeasonsResult{}, errors.New("seasons db error"))

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
	// The per-season counter sizes playoff bars via ListSeasonTeams.
	var pa *worknhl.PlayoffActivities
	s.env.OnActivity(pa.ListSeasonTeams, mock.Anything, mock.Anything).Return([]string{"TOR", "MTL"}, nil).Maybe()
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
	result := worknhl.FetchSeasonsManifestResult{
		Seasons: []nhl.SeasonInfo{
			testSeasonW(2023, "2023-10-10", "2023-10-12"),
		},
		Origin: core.OriginFileSystem,
	}

	var sa *worknhl.SeasonsActivities
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

	var sa *worknhl.SeasonsActivities
	s.env.OnActivity(sa.FetchSeasonsManifest, mock.Anything, input).Return(worknhl.FetchSeasonsManifestResult{}, expectedErr)

	s.env.ExecuteWorkflow(ImportSeasonsWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

// Test ImportSeasonsWorkflow handles child workflow error in Phase 1
func (s *ImportSeasonsWorkflowTestSuite) TestImportSeasonsWorkflow_ChildWorkflowError() {
	input := &model.SeasonsInput{}
	result := worknhl.FetchSeasonsManifestResult{
		Seasons: []nhl.SeasonInfo{
			testSeasonW(2023, "2023-10-10", "2023-10-12"),
		},
		Origin: core.OriginFileSystem,
	}
	expectedErr := errors.New("child workflow failed")

	var sa *worknhl.SeasonsActivities
	s.env.OnActivity(sa.FetchSeasonsManifest, mock.Anything, input).Return(result, nil)
	s.env.OnWorkflow(ImportSeasonWorkflow, mock.Anything, mock.Anything).Return(core.OriginCounts{}, expectedErr)

	s.env.ExecuteWorkflow(ImportSeasonsWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

// Test ImportSeasonsWorkflow with no seasons
func (s *ImportSeasonsWorkflowTestSuite) TestImportSeasonsWorkflow_NoSeasons() {
	input := &model.SeasonsInput{}
	result := worknhl.FetchSeasonsManifestResult{
		Seasons: []nhl.SeasonInfo{},
		Origin:  core.OriginFileSystem,
	}

	var sa *worknhl.SeasonsActivities
	s.env.OnActivity(sa.FetchSeasonsManifest, mock.Anything, input).Return(result, nil)

	s.env.ExecuteWorkflow(ImportSeasonsWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// Test ImportSeasonsWorkflow with multiple seasons (parallel processing)
func (s *ImportSeasonsWorkflowTestSuite) TestImportSeasonsWorkflow_MultipleSeasons() {
	input := &model.SeasonsInput{}
	result := worknhl.FetchSeasonsManifestResult{
		Seasons: []nhl.SeasonInfo{
			testSeasonW(2022, "2022-10-07", "2022-10-09"),
			testSeasonW(2023, "2023-10-10", "2023-10-12"),
			testSeasonW(2024, "2024-10-08", "2024-10-10"),
		},
		Origin: core.OriginFileSystem,
	}

	var sa *worknhl.SeasonsActivities
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
	result := worknhl.FetchSeasonsManifestResult{
		Seasons: []nhl.SeasonInfo{
			testSeasonW(2022, "2022-10-07", "2022-10-09"),
			testSeasonW(2023, "2023-10-10", "2023-10-12"),
		},
		Origin: core.OriginFileSystem,
	}

	var sa *worknhl.SeasonsActivities
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

// mockImportSeasonActivities registers mock expectations for all season-level
// activities that run before the day loop in ImportSeasonWorkflow.
func (s *ImportSeasonWorkflowTestSuite) mockImportSeasonActivities() {
	var sa *worknhl.SeasonsActivities
	s.env.OnActivity(sa.ImportSeasonRosters, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(sa.ImportClubStats, mock.Anything, mock.Anything).Return(nil)
	var pa *worknhl.PlayoffActivities
	s.env.OnActivity(pa.ListSeasonTeams, mock.Anything, mock.Anything).Return([]string{"TOR", "MTL"}, nil).Maybe()
	s.env.OnActivity(pa.ImportTeamPlayoffGames, mock.Anything, mock.Anything).Return(worknhl.ImportTeamPlayoffGamesResult{}, nil).Maybe()
}

// Test ImportSeasonWorkflow success with multiple days
func (s *ImportSeasonWorkflowTestSuite) TestImportSeasonWorkflow_Success() {
	season := testSeasonW(2023, "2023-10-10", "2023-10-12")

	s.mockImportSeasonActivities()
	var ia *worknhl.ImportActivities
	s.env.OnActivity(ia.ImportDay, mock.Anything, mock.Anything).Return(core.OriginCounts{}, nil)

	s.env.ExecuteWorkflow(ImportSeasonWorkflow, season)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// Test ImportSeasonWorkflow handles activity error
func (s *ImportSeasonWorkflowTestSuite) TestImportSeasonWorkflow_ActivityError() {
	season := testSeasonW(2023, "2023-10-10", "2023-10-12")

	s.mockImportSeasonActivities()
	var ia *worknhl.ImportActivities
	expectedErr := errors.New("database connection failed")
	s.env.OnActivity(ia.ImportDay, mock.Anything, mock.Anything).Return(core.OriginCounts{}, expectedErr)

	s.env.ExecuteWorkflow(ImportSeasonWorkflow, season)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

// Test ImportSeasonWorkflow with zero days (start > end)
func (s *ImportSeasonWorkflowTestSuite) TestImportSeasonWorkflow_ZeroDays() {
	season := testSeasonW(2023, "2023-10-15", "2023-10-10")

	s.mockImportSeasonActivities()
	s.env.ExecuteWorkflow(ImportSeasonWorkflow, season)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// Test ImportSeasonWorkflow with single day
func (s *ImportSeasonWorkflowTestSuite) TestImportSeasonWorkflow_SingleDay() {
	season := testSeasonW(2023, "2023-10-10", "2023-10-10")

	s.mockImportSeasonActivities()
	var ia *worknhl.ImportActivities
	s.env.OnActivity(ia.ImportDay, mock.Anything, mock.Anything).Return(core.OriginCounts{}, nil)

	s.env.ExecuteWorkflow(ImportSeasonWorkflow, season)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// Test ImportSeasonWorkflow with no games on some days
func (s *ImportSeasonWorkflowTestSuite) TestImportSeasonWorkflow_SomeDaysNoGames() {
	season := testSeasonW(2023, "2023-10-10", "2023-10-12")

	s.mockImportSeasonActivities()
	var ia *worknhl.ImportActivities
	s.env.OnActivity(ia.ImportDay, mock.Anything, mock.Anything).Return(core.OriginCounts{}, nil)

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
	result := worknhl.FetchSeasonsManifestResult{
		Seasons: []nhl.SeasonInfo{
			testSeasonW(2023, "2023-10-10", "2023-10-12"),
		},
		Origin: core.OriginFileSystem,
	}

	var sa *worknhl.SeasonsActivities
	s.env.OnActivity(sa.FetchSeasonsManifest, mock.Anything, input).Return(result, nil)
	var pa *workplayer.Activities
	s.env.OnActivity(pa.CountPlayersForAllSeasons, mock.Anything, mock.Anything).Return(map[int]int{2023: 100}, nil)
	s.env.OnWorkflow(ImportSeasonPlayerLogsWorkflow, mock.Anything, mock.Anything).Return(core.OriginCounts{}, nil)

	s.env.ExecuteWorkflow(ImportPlayerLogsWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *ImportPlayerLogsWorkflowTestSuite) TestNoSeasons() {
	input := &model.SeasonsInput{}
	result := worknhl.FetchSeasonsManifestResult{
		Seasons: []nhl.SeasonInfo{},
		Origin:  core.OriginFileSystem,
	}

	var sa *worknhl.SeasonsActivities
	s.env.OnActivity(sa.FetchSeasonsManifest, mock.Anything, input).Return(result, nil)

	s.env.ExecuteWorkflow(ImportPlayerLogsWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *ImportPlayerLogsWorkflowTestSuite) TestMultipleSeasons() {
	input := &model.SeasonsInput{}
	result := worknhl.FetchSeasonsManifestResult{
		Seasons: []nhl.SeasonInfo{
			testSeasonW(2022, "2022-10-07", "2022-10-09"),
			testSeasonW(2023, "2023-10-10", "2023-10-12"),
		},
		Origin: core.OriginFileSystem,
	}

	var sa *worknhl.SeasonsActivities
	s.env.OnActivity(sa.FetchSeasonsManifest, mock.Anything, input).Return(result, nil)
	var pa *workplayer.Activities
	s.env.OnActivity(pa.CountPlayersForAllSeasons, mock.Anything, mock.Anything).Return(map[int]int{2022: 50, 2023: 100}, nil)
	s.env.OnWorkflow(ImportSeasonPlayerLogsWorkflow, mock.Anything, mock.Anything).Return(core.OriginCounts{}, nil)

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

// mockFetchSeasonActivities registers mock expectations for all season-level
// activities that run before the day loop in FetchSeasonWorkflow.
func (s *FetchSeasonWorkflowTestSuite) mockFetchSeasonActivities() {
	var sa *worknhl.SeasonsActivities
	s.env.OnActivity(sa.FetchSeasonRosters, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(sa.FetchClubStats, mock.Anything, mock.Anything).Return(nil)
	var pa *worknhl.PlayoffActivities
	s.env.OnActivity(pa.ListSeasonTeams, mock.Anything, mock.Anything).Return([]string{"TOR", "MTL"}, nil).Maybe()
	s.env.OnActivity(pa.FetchTeamPlayoffGames, mock.Anything, mock.Anything).Return(worknhl.FetchTeamPlayoffGamesResult{}, nil).Maybe()
}

// Test FetchSeasonWorkflow success with multiple days
func (s *FetchSeasonWorkflowTestSuite) TestFetchSeasonWorkflow_Success() {
	season := testSeasonW(2023, "2023-10-10", "2023-10-12")

	s.mockFetchSeasonActivities()
	// Mock FetchDay method for each day
	var dsa *worknhl.DailyScheduleActivities
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

	s.mockFetchSeasonActivities()
	var dsa *worknhl.DailyScheduleActivities
	s.env.OnActivity(dsa.FetchDay, mock.Anything, mock.Anything).Return((core.OriginCounts)(nil), errors.New("network error"))

	s.env.ExecuteWorkflow(FetchSeasonWorkflow, season)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

// Test FetchSeasonWorkflow with single day
func (s *FetchSeasonWorkflowTestSuite) TestFetchSeasonWorkflow_SingleDay() {
	season := testSeasonW(2023, "2023-10-10", "2023-10-10")

	s.mockFetchSeasonActivities()
	var dsa *worknhl.DailyScheduleActivities
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
	var yahooAct *yahoo.FetchActivities
	s.env.OnActivity(yahooAct.FetchYahooPlayerBatch, mock.Anything, mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Maybe().
		Return(shared.FetchStats{Downloaded: 5, CacheHits: 3, Missing: 2}, nil)

	s.env.ExecuteWorkflow(FetchYahooPlayersWorkflow, (*FetchYahooPlayersInput)(nil))

	s.True(s.env.IsWorkflowCompleted())
	// May complete or ContinueAsNew - both are valid
}

// Test FetchYahooPlayersWorkflow handles activity error
func (s *FetchYahooPlayersWorkflowTestSuite) TestFetchYahooPlayersWorkflow_ActivityError() {
	// Activity takes (batchStartID int, batchEndID int)
	var yahooAct *yahoo.FetchActivities
	s.env.OnActivity(yahooAct.FetchYahooPlayerBatch, mock.Anything, mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Maybe().
		Return(shared.FetchStats{}, errors.New("download failed"))

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

	var playerAct *workplayer.Activities
	s.env.OnActivity(playerAct.LoadAllBoxscorePlayers, mock.Anything).Return(
		[]store.BoxscorePlayer{
			{ID: 8471214, FirstName: "Sidney", LastName: "Crosby"},
		}, nil)
	s.env.OnActivity(playerAct.ListYahooPlayerFiles, mock.Anything).Return(
		[]store.YahooPlayerID{1, 2, 3}, nil)
	s.env.OnActivity(playerAct.ParseYahooPlayerBatch, mock.Anything, mock.Anything).Return(
		[]store.YahooPlayer{{YahooID: 1, FirstName: "Test", LastName: "Player"}}, nil)
	s.env.OnActivity(playerAct.SaveYahooPlayersToRedis, mock.Anything, mock.Anything).Return(
		&workplayer.SaveYahooIDPoolResult{TotalPlayers: 1, AvailablePlayers: 1}, nil)

	s.env.ExecuteWorkflow(ProcessPlayersWorkflow, input)

	// Phase 1 completes and ContinueAsNew into Phase 2.
	// In the test environment ContinueAsNew is treated as workflow completion.
	s.True(s.env.IsWorkflowCompleted())
}

// Test ProcessPlayersWorkflow handles LoadAllBoxscorePlayers failure in Phase 1.
func (s *ProcessPlayersWorkflowTestSuite) TestProcessPlayersWorkflow_Phase1_ChildError() {
	input := &ProcessPlayersInput{}

	var playerAct *workplayer.Activities
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

	var playerAct *workplayer.Activities
	s.env.OnActivity(playerAct.LoadAllBoxscorePlayers, mock.Anything).Return(
		[]store.BoxscorePlayer{{ID: 8471214, FirstName: "Sidney", LastName: "Crosby"}}, nil)
	s.env.OnActivity(playerAct.ListYahooPlayerFiles, mock.Anything).Return(
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
		YahooPoolResult: &workplayer.SaveYahooIDPoolResult{TotalPlayers: 100, AvailablePlayers: 95},
		StartIndex:      0,
		TotalCompleted:  0,
	}

	// Mock the Redis load — returns the pre-built progress report from Phase 1.
	reportBytes := gobEncodeProgressReport(NewProcessPlayersProgressReport(len(input.Players)))
	s.env.OnActivity(((*shared.ProgressActivities)(nil)).Load, mock.Anything, mock.Anything).Return(reportBytes, nil)

	// Mock Phase 3 activities
	var playerAct *workplayer.Activities
	s.env.OnActivity(playerAct.ProcessPlayerBatch, mock.Anything, mock.Anything).Return(
		workplayer.ProcessPlayerBatchResult{FetchStats: shared.FetchStats{Downloaded: 1}, Imported: 1, Matched: 1}, nil)

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
		YahooPoolResult: &workplayer.SaveYahooIDPoolResult{TotalPlayers: 100},
	}

	reportBytes := gobEncodeProgressReport(NewProcessPlayersProgressReport(len(input.Players)))
	s.env.OnActivity(((*shared.ProgressActivities)(nil)).Load, mock.Anything, mock.Anything).Return(reportBytes, nil)

	var playerAct *workplayer.Activities
	s.env.OnActivity(playerAct.ProcessPlayerBatch, mock.Anything, mock.Anything).Return(
		workplayer.ProcessPlayerBatchResult{}, errors.New("database connection failed"))

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
		YahooPoolResult: &workplayer.SaveYahooIDPoolResult{TotalPlayers: 100, AvailablePlayers: 95},
		TotalDownloaded: 1,
		TotalImported:   1,
		TotalMatched:    1,
	}

	// Mock the Redis load — returns the pre-built progress report from Phases 1-2.
	reportBytes := gobEncodeProgressReport(NewProcessPlayersProgressReport(len(input.Players)))
	s.env.OnActivity(((*shared.ProgressActivities)(nil)).Load, mock.Anything, mock.Anything).Return(reportBytes, nil)

	// Mock Phase 4 activities - no unmatched players
	var playerAct *workplayer.Activities
	s.env.OnActivity(playerAct.LoadUnmatchedYahooPlayers, mock.Anything).Return(
		[]workplayer.UnmatchedYahooPlayer{}, nil)
	s.env.OnActivity(playerAct.CleanupYahooIDPoolData, mock.Anything).Maybe().Return(nil)

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
		YahooPoolResult: &workplayer.SaveYahooIDPoolResult{TotalPlayers: 100, AvailablePlayers: 95},
		TotalDownloaded: 1,
		TotalImported:   1,
		TotalMatched:    0,
	}

	// Mock the Redis load — returns the pre-built progress report from Phases 1-2.
	reportBytes := gobEncodeProgressReport(NewProcessPlayersProgressReport(len(input.Players)))
	s.env.OnActivity(((*shared.ProgressActivities)(nil)).Load, mock.Anything, mock.Anything).Return(reportBytes, nil)

	// Mock Phase 4 activities - some unmatched players
	var playerAct *workplayer.Activities
	s.env.OnActivity(playerAct.LoadUnmatchedYahooPlayers, mock.Anything).Return(
		[]workplayer.UnmatchedYahooPlayer{
			{YahooID: 123, FirstName: "Unknown", LastName: "Player"},
		}, nil)
	s.env.OnActivity(playerAct.VerifyUnmatchedBatch, mock.Anything, mock.Anything).Return(
		&workplayer.VerifyUnmatchedResult{
			VerifiedNonNHL: []store.YahooPlayerID{123},
			TrulyUnmatched: []workplayer.VerifiedPlayer{},
		}, nil)
	s.env.OnActivity(playerAct.CleanupYahooIDPoolData, mock.Anything).Return(nil)

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
	s.env.OnActivity(((*shared.ProgressActivities)(nil)).Load, mock.Anything, mock.Anything).Return(reportBytes, nil)

	s.env.ExecuteWorkflow(ProcessPlayersWorkflowContinue, input)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
	s.Contains(s.env.GetWorkflowError().Error(), "unknown phase")
}
