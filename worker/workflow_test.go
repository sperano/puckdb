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
}

func (s *DownloadSeasonsWorkflowTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

func TestDownloadSeasonsWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(DownloadSeasonsWorkflowTestSuite))
}

// Test DownloadSeasonsWorkflow with mocked child workflows
func (s *DownloadSeasonsWorkflowTestSuite) TestDownloadSeasonsWorkflow_Success() {
	input := &model.DownloadSeasonsInput{}
	seasons := []SeasonInfo{
		{StartYear: 2023, StartDate: mustParseDate("2024-04-14"), EndDate: mustParseDate("2024-04-15")},
	}

	s.env.OnActivity(FetchSeasonsDataActivity, mock.Anything, input).Return(seasons, nil)
	// Mock the child workflow for each season
	s.env.OnWorkflow(DownloadSeasonWorkflow, mock.Anything, mock.Anything).Return(nil)

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

// Test DownloadSeasonsWorkflow handles child workflow error
func (s *DownloadSeasonsWorkflowTestSuite) TestDownloadSeasonsWorkflow_ChildWorkflowError() {
	input := &model.DownloadSeasonsInput{}
	seasons := []SeasonInfo{
		{StartYear: 2023, StartDate: mustParseDate("2024-04-14"), EndDate: mustParseDate("2024-04-15")},
	}
	expectedErr := errors.New("child workflow failed")

	s.env.OnActivity(FetchSeasonsDataActivity, mock.Anything, input).Return(seasons, nil)
	// Mock the child workflow to return an error
	s.env.OnWorkflow(DownloadSeasonWorkflow, mock.Anything, mock.Anything).Return(expectedErr)

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
