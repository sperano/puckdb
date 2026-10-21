package workflow

import (
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/core"
	worknhl "github.com/sperano/puckdb/internal/worker/nhl"
	"github.com/stretchr/testify/mock"
)

// initializeSingleStepGroups is the number of InitializeWorkflow groups
// (fetch and upsert franchises, fetch and upsert seasons) sized to one step;
// the season-teams group has one step per season.
const initializeSingleStepGroups = 4

// The last saved report's Total must count every group's bars, not stay at
// the constructor's zero while SetBarTotal sizes each group.
func (s *InitializeWorkflowTestSuite) TestInitializeWorkflow_SavedTotalMatchesBars() {
	seasons := initializeTestSeasons()
	s.mockInitializeSuccess(seasons)
	lastSaved := recordLastSavedReport(s.env)

	s.env.ExecuteWorkflow(InitializeWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.Require().NoError(s.env.GetWorkflowError())
	requireTotalIsBarSum(s.T(), lastSaved(), initializeSingleStepGroups+len(seasons))
}

// mockInitializeSuccess mocks every InitializeWorkflow activity to succeed,
// with the manifest returning seasons.
func (s *InitializeWorkflowTestSuite) mockInitializeSuccess(seasons []nhl.SeasonInfo) {
	var fa *worknhl.FranchiseActivities
	s.env.OnActivity(fa.FetchFranchises, mock.Anything).Return(
		worknhl.FetchFranchisesResult{Origin: core.OriginRemoteNHLAPI}, nil)
	s.env.OnActivity(fa.UpsertFranchises, mock.Anything).Return(
		worknhl.UpsertFranchisesResult{FranchisesUpserted: 32}, nil)
	var sa *worknhl.SeasonsActivities
	s.env.OnActivity(sa.FetchSeasonsManifest, mock.Anything, mock.Anything).Return(
		worknhl.FetchSeasonsManifestResult{Seasons: seasons, Origin: core.OriginFileSystem}, nil)
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
}

// initializeTestSeasons is the three-season manifest the InitializeWorkflow
// success tests use.
func initializeTestSeasons() []nhl.SeasonInfo {
	return []nhl.SeasonInfo{
		testSeasonW(2022, "2022-10-07", "2023-04-14"),
		testSeasonW(2023, "2023-10-10", "2024-04-18"),
		testSeasonW(2024, "2024-10-04", "2025-04-17"),
	}
}
