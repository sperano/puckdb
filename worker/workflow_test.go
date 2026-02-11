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

func TestWorkflowIDFetchGamesForSeason(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		season   int
		expected string
	}{
		{
			name:     "2023 season",
			season:   2023,
			expected: "fetch-games-for-season-2023",
		},
		{
			name:     "2022 season",
			season:   2022,
			expected: "fetch-games-for-season-2022",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := WorkflowIDFetchGamesForSeason(tt.season)
			assert.Equal(t, tt.expected, result)
		})
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
