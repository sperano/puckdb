package worker

import (
	"errors"
	"testing"
	"time"

	"github.com/sperano/puckdb/graph/model"
	"github.com/stretchr/testify/assert"
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

func TestWorkflowIDImportLeague(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		season   int
		leagueID int
		expected string
	}{
		{
			name:     "basic case",
			season:   2023,
			leagueID: 12345,
			expected: "league-2023-12345",
		},
		{
			name:     "different season",
			season:   2022,
			leagueID: 99999,
			expected: "league-2022-99999",
		},
		{
			name:     "zero values",
			season:   0,
			leagueID: 0,
			expected: "league-0-0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := WorkflowIDImportLeague(tt.season, tt.leagueID)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestWorkflowIDImportTeam(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		season   int
		leagueID int
		teamID   int
		expected string
	}{
		{
			name:     "basic case",
			season:   2023,
			leagueID: 12345,
			teamID:   1,
			expected: "team-2023-12345",
		},
		{
			name:     "different values",
			season:   2022,
			leagueID: 99999,
			teamID:   5,
			expected: "team-2022-99999",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := WorkflowIDImportTeam(tt.season, tt.leagueID, tt.teamID)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestWorkflowIDDownloadGamesForSeason(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		season   int
		expected string
	}{
		{
			name:     "2023 season",
			season:   2023,
			expected: "download-games-for-season-2023",
		},
		{
			name:     "2022 season",
			season:   2022,
			expected: "download-games-for-season-2022",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := WorkflowIDDownloadGamesForSeason(tt.season)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestWorkflowIDDownloadEverythingForSeason(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		season   int
		expected string
	}{
		{
			name:     "2023 season",
			season:   2023,
			expected: "download-everything-for-season-2023",
		},
		{
			name:     "2022 season",
			season:   2022,
			expected: "download-everything-for-season-2022",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := WorkflowIDDownloadEverythingForSeason(tt.season)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestWorkflowIDImportEverythingForSeason(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		season   int
		expected string
	}{
		{
			name:     "2023 season",
			season:   2023,
			expected: "import-everything-for-season-2023",
		},
		{
			name:     "2022 season",
			season:   2022,
			expected: "import-everything-for-season-2022",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := WorkflowIDImportEverythingForSeason(tt.season)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Workflow test suite for DownloadSeasons workflows
type DownloadSeasonsWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *DownloadSeasonsWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.RegisterWorkflow(DownloadSeasonsWorkflow)
	s.env.RegisterWorkflow(DownloadSeasonWorkflow)
	s.env.RegisterWorkflow(DownloadRosterForTeamWorkflow)
	s.env.RegisterWorkflow(DownloadTeamSummariesForTeamWorkflow)
}

func (s *DownloadSeasonsWorkflowTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

func TestDownloadSeasonsWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(DownloadSeasonsWorkflowTestSuite))
}

// Test DownloadSeasonsWorkflow with mocked activities
func (s *DownloadSeasonsWorkflowTestSuite) TestDownloadSeasonsWorkflow_Success() {
	input := &model.DownloadSeasonsInput{}
	seasons := []SeasonInfo{
		{StartYear: 2023, StartDate: mustParseDate("2024-04-14"), EndDate: mustParseDate("2024-04-15")},
	}

	s.env.OnActivity(FetchSeasonsDataActivity, mock.Anything, input).Return(seasons, nil)
	// Mock activities that collectDownloadFuturesForSeason calls
	s.env.OnActivity(DownloadDailySchedule, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(DownloadLeague, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(DownloadTeam, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnWorkflow(DownloadRosterForTeamWorkflow, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnWorkflow(DownloadTeamSummariesForTeamWorkflow, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	s.env.ExecuteWorkflow(DownloadSeasonsWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// Test DownloadSeasonsWorkflow handles activity error
func (s *DownloadSeasonsWorkflowTestSuite) TestDownloadSeasonsWorkflow_FetchSeasonsError() {
	input := &model.DownloadSeasonsInput{}
	expectedErr := errors.New("failed to fetch seasons")

	s.env.OnActivity(FetchSeasonsDataActivity, mock.Anything, input).Return(nil, expectedErr)

	s.env.ExecuteWorkflow(DownloadSeasonsWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

// Test DownloadSeasonsWorkflow handles activity error
func (s *DownloadSeasonsWorkflowTestSuite) TestDownloadSeasonsWorkflow_ActivityError() {
	input := &model.DownloadSeasonsInput{}
	seasons := []SeasonInfo{
		{StartYear: 2023, StartDate: mustParseDate("2024-04-14"), EndDate: mustParseDate("2024-04-15")},
	}
	expectedErr := errors.New("activity failed")

	s.env.OnActivity(FetchSeasonsDataActivity, mock.Anything, input).Return(seasons, nil)
	s.env.OnActivity(DownloadDailySchedule, mock.Anything, mock.Anything).Return(expectedErr)
	s.env.OnActivity(DownloadLeague, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(DownloadTeam, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnWorkflow(DownloadRosterForTeamWorkflow, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnWorkflow(DownloadTeamSummariesForTeamWorkflow, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	s.env.ExecuteWorkflow(DownloadSeasonsWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

// Test DownloadSeasonsWorkflow with no seasons
func (s *DownloadSeasonsWorkflowTestSuite) TestDownloadSeasonsWorkflow_NoSeasons() {
	input := &model.DownloadSeasonsInput{}
	seasons := []SeasonInfo{}

	s.env.OnActivity(FetchSeasonsDataActivity, mock.Anything, input).Return(seasons, nil)

	s.env.ExecuteWorkflow(DownloadSeasonsWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

/*
func newTestSeason(year int) config.Season {
	return config.Season{
		Start:   time.Date(year, 10, 1, 0, 0, 0, 0, time.UTC),
		End:     time.Date(year+1, 4, 30, 0, 0, 0, 0, time.UTC),
		GameKey: 400 + (year - 2020),
	}
}

func newTestLeague(leagueID int, teamIDs []int) config.League {
	return config.League{
		LeagueID: leagueID,
		TeamIDs:  teamIDs,
	}
}

func TestWorkflowIDImportRostersForTeam(t *testing.T) {
	t.Parallel()
	season := newTestSeason(2023)
	league := newTestLeague(12345, []int{1, 2, 3})

	tests := []struct {
		name     string
		season   config.Season
		league   config.League
		teamID   int
		expected string
	}{
		{
			name:     "basic case",
			season:   season,
			league:   league,
			teamID:   1,
			expected: "import-rosters-2023-12345-1",
		},
		{
			name:     "different team",
			season:   season,
			league:   league,
			teamID:   5,
			expected: "import-rosters-2023-12345-5",
		},
		{
			name:     "different season",
			season:   newTestSeason(2022),
			league:   newTestLeague(99999, []int{1}),
			teamID:   10,
			expected: "import-rosters-2022-99999-10",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := WorkflowIDImportRostersForTeam(tt.season, tt.league, tt.teamID)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestWorkflowIDImportTeamSummariesForTeam(t *testing.T) {
	t.Parallel()
	season := newTestSeason(2023)
	league := newTestLeague(12345, []int{1, 2, 3})

	tests := []struct {
		name     string
		season   config.Season
		league   config.League
		teamID   int
		expected string
	}{
		{
			name:     "basic case",
			season:   season,
			league:   league,
			teamID:   1,
			expected: "import-team-summary-2023-12345-1",
		},
		{
			name:     "different team",
			season:   season,
			league:   league,
			teamID:   5,
			expected: "import-team-summary-2023-12345-5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := WorkflowIDImportTeamSummariesForTeam(tt.season, tt.league, tt.teamID)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestWorkflowIDDownloadRostersForTeam(t *testing.T) {
	t.Parallel()
	season := newTestSeason(2023)
	league := newTestLeague(12345, []int{1, 2, 3})

	tests := []struct {
		name     string
		season   config.Season
		league   config.League
		teamID   int
		expected string
	}{
		{
			name:     "basic case",
			season:   season,
			league:   league,
			teamID:   1,
			expected: "download-rosters-2023-12345-1",
		},
		{
			name:     "different team",
			season:   season,
			league:   league,
			teamID:   5,
			expected: "download-rosters-2023-12345-5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := WorkflowIDDownloadRostersForTeam(tt.season, tt.league, tt.teamID)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestWorkflowIDDownloadTeamSummariesForTeam(t *testing.T) {
	t.Parallel()
	season := newTestSeason(2023)
	league := newTestLeague(12345, []int{1, 2, 3})

	tests := []struct {
		name     string
		season   config.Season
		league   config.League
		teamID   int
		expected string
	}{
		{
			name:     "basic case",
			season:   season,
			league:   league,
			teamID:   1,
			expected: "download-team-summary-2023-12345-1",
		},
		{
			name:     "different team",
			season:   season,
			league:   league,
			teamID:   5,
			expected: "download-team-summary-2023-12345-5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := WorkflowIDDownloadTeamSummariesForTeam(tt.season, tt.league, tt.teamID)
			assert.Equal(t, tt.expected, result)
		})
	}
}
*/

func TestWorkflowIDConstants(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "import-everything", WorkflowIDImportEverything)
	assert.Equal(t, "download-everything", WorkflowIDDownloadEverything)
	assert.Equal(t, "puckdb-tasks", TaskQueueName)
}

/*
func TestWorkflowIDsAreUnique(t *testing.T) {
	t.Parallel()
	season := newTestSeason(2023)
	league := newTestLeague(12345, []int{1, 2, 3})

	ids := []string{
		WorkflowIDImportEverything,
		WorkflowIDDownloadEverything,
		WorkflowIDImportLeague(2023, 12345),
		WorkflowIDImportTeam(2023, 12345, 1),
		WorkflowIDImportGamesForDay(2023, 11, 15),
		WorkflowIDImportGamesForSeason(2023),
		WorkflowIDImportRostersForTeam(season, league, 1),
		WorkflowIDImportTeamSummariesForTeam(season, league, 1),
		WorkflowIDDownloadGamesForSeason(2023),
		WorkflowIDDownloadRostersForTeam(season, league, 1),
		WorkflowIDDownloadTeamSummariesForTeam(season, league, 1),
		WorkflowIDDownloadEverythingForSeason(2023),
		WorkflowIDImportEverythingForSeason(2023),
	}

	seen := make(map[string]bool)
	for _, id := range ids {
		if seen[id] {
			t.Errorf("duplicate workflow ID found: %s", id)
		}
		seen[id] = true
	}
}
*/

/*
// WorkflowTestSuite is the test suite for Temporal workflows
type WorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *WorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	// Register all workflows that may be called as child workflows
	s.env.RegisterWorkflow(DownloadGamesForSeasonWorkflow)
	s.env.RegisterWorkflow(DownloadRosterForTeamWorkflow)
	s.env.RegisterWorkflow(DownloadTeamSummariesForTeamWorkflow)
	s.env.RegisterWorkflow(ImportGamesForSeasonWorkflow)
	s.env.RegisterWorkflow(ImportRosterForTeamWorkflow)
	s.env.RegisterWorkflow(ImportTeamSummariesForTeamWorkflow)
}

func (s *WorkflowTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

func TestWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(WorkflowTestSuite))
}

// Test ImportGamesForDayWorkflow
func (s *WorkflowTestSuite) TestImportGamesForDayWorkflow_Success() {
	day := time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC)

	s.env.OnActivity(ImportGameDay, mock.Anything, day).Return(nil)

	s.env.ExecuteWorkflow(ImportGamesForDayWorkflow, day)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *WorkflowTestSuite) TestImportGamesForDayWorkflow_ActivityError() {
	day := time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC)
	expectedErr := errors.New("failed to import games")

	s.env.OnActivity(ImportGameDay, mock.Anything, day).Return(expectedErr)

	s.env.ExecuteWorkflow(ImportGamesForDayWorkflow, day)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

// Test DownloadGamesForSeasonWorkflow
func (s *WorkflowTestSuite) TestDownloadGamesForSeasonWorkflow_Success() {
	// Use a very short season (just 2 days) for testing
	season := config.Season{
		Start:   time.Date(2023, 11, 14, 0, 0, 0, 0, time.UTC),
		End:     time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC),
		GameKey: 423,
	}

	// Mock activity for each day in the range
	s.env.OnActivity(DownloadGameDay, mock.Anything, mock.AnythingOfType("time.Time")).Return(nil)
	s.env.OnActivity(DownloadDailySchedule, mock.Anything, mock.AnythingOfType("time.Time")).Return(nil)

	s.env.ExecuteWorkflow(DownloadGamesForSeasonWorkflow, season)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *WorkflowTestSuite) TestDownloadGamesForSeasonWorkflow_ActivityError() {
	season := config.Season{
		Start:   time.Date(2023, 11, 14, 0, 0, 0, 0, time.UTC),
		End:     time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC),
		GameKey: 423,
	}
	expectedErr := errors.New("download failed")

	s.env.OnActivity(DownloadGameDay, mock.Anything, mock.AnythingOfType("time.Time")).Return(expectedErr)
	s.env.OnActivity(DownloadDailySchedule, mock.Anything, mock.AnythingOfType("time.Time")).Return(nil)

	s.env.ExecuteWorkflow(DownloadGamesForSeasonWorkflow, season)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

// Test ImportGamesForSeasonWorkflow
func (s *WorkflowTestSuite) TestImportGamesForSeasonWorkflow_Success() {
	season := config.Season{
		Start:   time.Date(2023, 11, 14, 0, 0, 0, 0, time.UTC),
		End:     time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC),
		GameKey: 423,
	}

	s.env.OnActivity(ImportGameDay, mock.Anything, mock.AnythingOfType("time.Time")).Return(nil)

	s.env.ExecuteWorkflow(ImportGamesForSeasonWorkflow, season)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// Test DownloadRosterForTeamWorkflow
func (s *WorkflowTestSuite) TestDownloadRosterForTeamWorkflow_Success() {
	season := config.Season{
		Start:   time.Date(2023, 11, 14, 0, 0, 0, 0, time.UTC),
		End:     time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC),
		GameKey: 423,
	}
	league := config.League{
		LeagueID: 12345,
		TeamIDs:  []int{1, 2, 3},
	}
	teamID := 1

	s.env.OnActivity(DownloadRosterForTeamOnDay, mock.Anything, season.GameKey, league.LeagueID, teamID, mock.AnythingOfType("time.Time")).Return(nil)

	s.env.ExecuteWorkflow(DownloadRosterForTeamWorkflow, season, league, teamID)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *WorkflowTestSuite) TestDownloadRosterForTeamWorkflow_ActivityError() {
	season := config.Season{
		Start:   time.Date(2023, 11, 14, 0, 0, 0, 0, time.UTC),
		End:     time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC),
		GameKey: 423,
	}
	league := config.League{
		LeagueID: 12345,
		TeamIDs:  []int{1, 2, 3},
	}
	teamID := 1
	expectedErr := errors.New("download roster failed")

	s.env.OnActivity(DownloadRosterForTeamOnDay, mock.Anything, season.GameKey, league.LeagueID, teamID, mock.AnythingOfType("time.Time")).Return(expectedErr)

	s.env.ExecuteWorkflow(DownloadRosterForTeamWorkflow, season, league, teamID)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

// Test ImportRosterForTeamWorkflow
func (s *WorkflowTestSuite) TestImportRosterForTeamWorkflow_Success() {
	season := config.Season{
		Start:   time.Date(2023, 11, 14, 0, 0, 0, 0, time.UTC),
		End:     time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC),
		GameKey: 423,
	}
	league := config.League{
		LeagueID: 12345,
		TeamIDs:  []int{1, 2, 3},
	}
	teamID := 1

	s.env.OnActivity(ImportRosterForTeamOnDay, mock.Anything, season.GameKey, league.LeagueID, teamID, mock.AnythingOfType("time.Time")).Return(database.RosterPlayers{}, nil)

	s.env.ExecuteWorkflow(ImportRosterForTeamWorkflow, season, league, teamID)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// Test DownloadTeamSummariesForTeamWorkflow
func (s *WorkflowTestSuite) TestDownloadTeamSummariesForTeamWorkflow_Success() {
	season := config.Season{
		Start:   time.Date(2023, 11, 14, 0, 0, 0, 0, time.UTC),
		End:     time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC),
		GameKey: 423,
	}
	league := config.League{
		LeagueID: 12345,
		TeamIDs:  []int{1, 2, 3},
	}
	teamID := 1

	s.env.OnActivity(DownloadTeamSummaryForTeamOnDay, mock.Anything, season.GameKey, league.LeagueID, teamID, mock.AnythingOfType("time.Time")).Return(nil)

	s.env.ExecuteWorkflow(DownloadTeamSummariesForTeamWorkflow, season, league, teamID)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// Test ImportTeamSummariesForTeamWorkflow
func (s *WorkflowTestSuite) TestImportTeamSummariesForTeamWorkflow_Success() {
	season := config.Season{
		Start:   time.Date(2023, 11, 14, 0, 0, 0, 0, time.UTC),
		End:     time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC),
		GameKey: 423,
	}
	league := config.League{
		LeagueID: 12345,
		TeamIDs:  []int{1, 2, 3},
	}
	teamID := 1

	s.env.OnActivity(ImportTeamSummaryForTeamOnDay, mock.Anything, season.GameKey, league.LeagueID, teamID, mock.AnythingOfType("time.Time")).Return(database.TeamSummary{}, nil)

	s.env.ExecuteWorkflow(ImportTeamSummariesForTeamWorkflow, season, league, teamID)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// Test DownloadEverythingForSeasonWorkflow
func (s *WorkflowTestSuite) TestDownloadEverythingForSeasonWorkflow_Success() {
	season := config.Season{
		Start:   time.Date(2023, 11, 14, 0, 0, 0, 0, time.UTC),
		End:     time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC),
		GameKey: 423,
		Leagues: []config.League{
			{LeagueID: 12345, TeamIDs: []int{1}},
		},
	}

	// Mock activities for downloading games (per-day activities)
	s.env.OnActivity(DownloadGameDay, mock.Anything, mock.AnythingOfType("time.Time")).Return(nil)
	s.env.OnActivity(DownloadDailySchedule, mock.Anything, mock.AnythingOfType("time.Time")).Return(nil)

	// Mock activities
	s.env.OnActivity(DownloadLeague, mock.Anything, season.StartYear(), season.GameKey, 12345).Return(nil)
	s.env.OnActivity(DownloadTeam, mock.Anything, season.StartYear(), season.GameKey, 12345, 1).Return(nil)

	// Mock child workflows for rosters and team summaries
	s.env.OnWorkflow(DownloadRosterForTeamWorkflow, mock.Anything, season, season.Leagues[0], 1).Return(nil)
	s.env.OnWorkflow(DownloadTeamSummariesForTeamWorkflow, mock.Anything, season, season.Leagues[0], 1).Return(nil)

	s.env.ExecuteWorkflow(DownloadEverythingForSeasonWorkflow, season)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *WorkflowTestSuite) TestDownloadEverythingForSeasonWorkflow_LeagueActivityError() {
	season := config.Season{
		Start:   time.Date(2023, 11, 14, 0, 0, 0, 0, time.UTC),
		End:     time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC),
		GameKey: 423,
		Leagues: []config.League{
			{LeagueID: 12345, TeamIDs: []int{1}},
		},
	}
	expectedErr := errors.New("download league failed")

	// Mock activities for downloading games (per-day activities)
	s.env.OnActivity(DownloadGameDay, mock.Anything, mock.AnythingOfType("time.Time")).Return(nil)
	s.env.OnActivity(DownloadDailySchedule, mock.Anything, mock.AnythingOfType("time.Time")).Return(nil)

	s.env.OnActivity(DownloadLeague, mock.Anything, season.StartYear(), season.GameKey, 12345).Return(expectedErr)
	s.env.OnActivity(DownloadTeam, mock.Anything, season.StartYear(), season.GameKey, 12345, 1).Return(nil)
	s.env.OnWorkflow(DownloadRosterForTeamWorkflow, mock.Anything, season, season.Leagues[0], 1).Return(nil)
	s.env.OnWorkflow(DownloadTeamSummariesForTeamWorkflow, mock.Anything, season, season.Leagues[0], 1).Return(nil)

	s.env.ExecuteWorkflow(DownloadEverythingForSeasonWorkflow, season)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

// Test ImportEverythingForSeasonWorkflow
func (s *WorkflowTestSuite) TestImportEverythingForSeasonWorkflow_Success() {
	season := config.Season{
		Start:   time.Date(2023, 11, 14, 0, 0, 0, 0, time.UTC),
		End:     time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC),
		GameKey: 423,
		Leagues: []config.League{
			{LeagueID: 12345, TeamIDs: []int{1}},
		},
	}

	// Mock child workflow for importing games
	s.env.OnWorkflow(ImportGamesForSeasonWorkflow, mock.Anything, season).Return(nil)

	// Mock activities
	s.env.OnActivity(ImportLeague, mock.Anything, season.StartYear(), season.GameKey, 12345).Return(database.League{}, nil)
	s.env.OnActivity(ImportTeam, mock.Anything, season.StartYear(), season.GameKey, 12345, 1).Return(database.Team{}, nil)

	// Mock child workflows for rosters and team summaries
	s.env.OnWorkflow(ImportRosterForTeamWorkflow, mock.Anything, season, season.Leagues[0], 1).Return(nil)
	s.env.OnWorkflow(ImportTeamSummariesForTeamWorkflow, mock.Anything, season, season.Leagues[0], 1).Return(nil)

	s.env.ExecuteWorkflow(ImportEverythingForSeasonWorkflow, season)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *WorkflowTestSuite) TestImportEverythingForSeasonWorkflow_ImportLeagueError() {
	season := config.Season{
		Start:   time.Date(2023, 11, 14, 0, 0, 0, 0, time.UTC),
		End:     time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC),
		GameKey: 423,
		Leagues: []config.League{
			{LeagueID: 12345, TeamIDs: []int{1}},
		},
	}
	expectedErr := errors.New("import league failed")

	s.env.OnWorkflow(ImportGamesForSeasonWorkflow, mock.Anything, season).Return(nil)
	s.env.OnActivity(ImportLeague, mock.Anything, season.StartYear(), season.GameKey, 12345).Return(database.League{}, expectedErr)
	s.env.OnActivity(ImportTeam, mock.Anything, season.StartYear(), season.GameKey, 12345, 1).Return(database.Team{}, nil)
	s.env.OnWorkflow(ImportRosterForTeamWorkflow, mock.Anything, season, season.Leagues[0], 1).Return(nil)
	s.env.OnWorkflow(ImportTeamSummariesForTeamWorkflow, mock.Anything, season, season.Leagues[0], 1).Return(nil)

	s.env.ExecuteWorkflow(ImportEverythingForSeasonWorkflow, season)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

func (s *WorkflowTestSuite) TestImportEverythingForSeasonWorkflow_MultipleTeams() {
	season := config.Season{
		Start:   time.Date(2023, 11, 14, 0, 0, 0, 0, time.UTC),
		End:     time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC),
		GameKey: 423,
		Leagues: []config.League{
			{LeagueID: 12345, TeamIDs: []int{1, 2, 3}},
		},
	}

	s.env.OnWorkflow(ImportGamesForSeasonWorkflow, mock.Anything, season).Return(nil)
	s.env.OnActivity(ImportLeague, mock.Anything, season.StartYear(), season.GameKey, 12345).Return(database.League{}, nil)

	// Mock for all 3 teams
	for _, teamID := range []int{1, 2, 3} {
		s.env.OnActivity(ImportTeam, mock.Anything, season.StartYear(), season.GameKey, 12345, teamID).Return(database.Team{}, nil)
		s.env.OnWorkflow(ImportRosterForTeamWorkflow, mock.Anything, season, season.Leagues[0], teamID).Return(nil)
		s.env.OnWorkflow(ImportTeamSummariesForTeamWorkflow, mock.Anything, season, season.Leagues[0], teamID).Return(nil)
	}

	s.env.ExecuteWorkflow(ImportEverythingForSeasonWorkflow, season)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *WorkflowTestSuite) TestImportEverythingForSeasonWorkflow_MultipleLeagues() {
	season := config.Season{
		Start:   time.Date(2023, 11, 14, 0, 0, 0, 0, time.UTC),
		End:     time.Date(2023, 11, 15, 0, 0, 0, 0, time.UTC),
		GameKey: 423,
		Leagues: []config.League{
			{LeagueID: 11111, TeamIDs: []int{1}},
			{LeagueID: 22222, TeamIDs: []int{2}},
		},
	}

	s.env.OnWorkflow(ImportGamesForSeasonWorkflow, mock.Anything, season).Return(nil)

	// Mock for all leagues and teams
	for i, league := range season.Leagues {
		s.env.OnActivity(ImportLeague, mock.Anything, season.StartYear(), season.GameKey, league.LeagueID).Return(database.League{}, nil)
		for _, teamID := range league.TeamIDs {
			s.env.OnActivity(ImportTeam, mock.Anything, season.StartYear(), season.GameKey, league.LeagueID, teamID).Return(database.Team{}, nil)
			s.env.OnWorkflow(ImportRosterForTeamWorkflow, mock.Anything, season, season.Leagues[i], teamID).Return(nil)
			s.env.OnWorkflow(ImportTeamSummariesForTeamWorkflow, mock.Anything, season, season.Leagues[i], teamID).Return(nil)
		}
	}

	s.env.ExecuteWorkflow(ImportEverythingForSeasonWorkflow, season)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// ActivityTestSuite for testing activities
type ActivityTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestActivityEnvironment
}

func (s *ActivityTestSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
}

func TestActivityTestSuite(t *testing.T) {
	suite.Run(t, new(ActivityTestSuite))
}

// Test activity function signatures are correct (basic smoke tests)
func (s *ActivityTestSuite) TestImportGameDaySignature() {
	// This tests that the activity function has the correct signature
	// The actual execution would require a database and file system
	var _ func(context.Context, time.Time) error = ImportGameDay
}

func (s *ActivityTestSuite) TestDownloadGameDaySignature() {
	var _ func(context.Context, time.Time) error = DownloadGameDay
}

func (s *ActivityTestSuite) TestDownloadLeagueSignature() {
	var _ func(context.Context, int, int, int) error = DownloadLeague
}

func (s *ActivityTestSuite) TestImportLeagueSignature() {
	var _ func(context.Context, int, int, int) (database.League, error) = ImportLeague
}

func (s *ActivityTestSuite) TestDownloadTeamSignature() {
	var _ func(context.Context, int, int, int, int) error = DownloadTeam
}

func (s *ActivityTestSuite) TestImportTeamSignature() {
	var _ func(context.Context, int, int, int, int) (database.Team, error) = ImportTeam
}

func (s *ActivityTestSuite) TestDownloadRosterForTeamOnDaySignature() {
	var _ func(context.Context, int, int, int, time.Time) error = DownloadRosterForTeamOnDay
}

func (s *ActivityTestSuite) TestImportRosterForTeamOnDaySignature() {
	var _ func(context.Context, int, int, int, time.Time) (database.RosterPlayers, error) = ImportRosterForTeamOnDay
}

func (s *ActivityTestSuite) TestDownloadTeamSummaryForTeamOnDaySignature() {
	var _ func(context.Context, int, int, int, time.Time) error = DownloadTeamSummaryForTeamOnDay
}

func (s *ActivityTestSuite) TestImportTeamSummaryForTeamOnDaySignature() {
	var _ func(context.Context, int, int, int, time.Time) (database.TeamSummary, error) = ImportTeamSummaryForTeamOnDay
}
*/
