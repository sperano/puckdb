package worker

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWorkflowIDEnrichPlayers(t *testing.T) {
	result := WorkflowIDEnrichPlayers()
	assert.Equal(t, "enrich-players", result)
}

// Tests for splitIntoBatches

func TestSplitIntoBatches(t *testing.T) {
	tests := []struct {
		name      string
		ids       []int64
		batchSize int
		expected  [][]int64
	}{
		{
			name:      "empty input",
			ids:       []int64{},
			batchSize: 10,
			expected:  [][]int64{},
		},
		{
			name:      "single batch - exact fit",
			ids:       []int64{1, 2, 3},
			batchSize: 3,
			expected:  [][]int64{{1, 2, 3}},
		},
		{
			name:      "single batch - underfill",
			ids:       []int64{1, 2},
			batchSize: 5,
			expected:  [][]int64{{1, 2}},
		},
		{
			name:      "multiple batches - exact fit",
			ids:       []int64{1, 2, 3, 4, 5, 6},
			batchSize: 2,
			expected:  [][]int64{{1, 2}, {3, 4}, {5, 6}},
		},
		{
			name:      "multiple batches - partial last batch",
			ids:       []int64{1, 2, 3, 4, 5},
			batchSize: 2,
			expected:  [][]int64{{1, 2}, {3, 4}, {5}},
		},
		{
			name:      "batch size of 1",
			ids:       []int64{1, 2, 3},
			batchSize: 1,
			expected:  [][]int64{{1}, {2}, {3}},
		},
		{
			name:      "large batch size",
			ids:       []int64{1, 2, 3},
			batchSize: 100,
			expected:  [][]int64{{1, 2, 3}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := splitIntoBatches(tt.ids, tt.batchSize)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSplitIntoBatches_PreservesOrder(t *testing.T) {
	ids := []int64{100, 50, 200, 25, 300}
	batches := splitIntoBatches(ids, 2)

	// Flatten back
	var flattened []int64
	for _, batch := range batches {
		flattened = append(flattened, batch...)
	}

	assert.Equal(t, ids, flattened)
}

/*
// Workflow test suite for player extraction

type ExtractPlayersWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *ExtractPlayersWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	// Register all workflows that may be called as child workflows
	s.env.RegisterWorkflow(ExtractUniquePlayersWorkflow)
	s.env.RegisterWorkflow(ExtractPlayersForSeasonWorkflow)
	s.env.RegisterWorkflow(ExtractYahooPlayersForSeasonWorkflow)
	s.env.RegisterWorkflow(ExtractBoxscorePlayersForSeasonWorkflow)
	s.env.RegisterWorkflow(EnrichPlayersWorkflow)
}

func (s *ExtractPlayersWorkflowTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

func TestExtractPlayersWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(ExtractPlayersWorkflowTestSuite))
}
*/

/*
// Test EnrichPlayersWorkflow

func (s *ExtractPlayersWorkflowTestSuite) TestEnrichPlayersWorkflow_EmptyInput() {
	playerIDs := []int64{}
	redisKey := "test-key"

	s.env.ExecuteWorkflow(EnrichPlayersWorkflow, playerIDs, redisKey)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result int
	s.NoError(s.env.GetWorkflowResult(&result))
	s.Equal(0, result)
}

func (s *ExtractPlayersWorkflowTestSuite) TestEnrichPlayersWorkflow_SingleBatch() {
	playerIDs := []int64{8476453, 8477934, 8478402}
	redisKey := "test-key"

	// One batch activity should be called
	s.env.OnActivity(EnrichPlayerBatchActivity, mock.Anything, playerIDs, redisKey).Return(3, nil)

	s.env.ExecuteWorkflow(EnrichPlayersWorkflow, playerIDs, redisKey)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result int
	s.NoError(s.env.GetWorkflowResult(&result))
	s.Equal(3, result)
}

func (s *ExtractPlayersWorkflowTestSuite) TestEnrichPlayersWorkflow_ActivityError() {
	playerIDs := []int64{8476453}
	redisKey := "test-key"
	expectedErr := errors.New("enrichment failed")

	s.env.OnActivity(EnrichPlayerBatchActivity, mock.Anything, playerIDs, redisKey).Return(0, expectedErr)

	s.env.ExecuteWorkflow(EnrichPlayersWorkflow, playerIDs, redisKey)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

// Test ExtractPlayersForSeasonWorkflow

func (s *ExtractPlayersWorkflowTestSuite) TestExtractPlayersForSeasonWorkflow_Success() {
	season := config.Season{
		Start:   time.Date(2023, 10, 1, 0, 0, 0, 0, time.UTC),
		End:     time.Date(2024, 4, 30, 0, 0, 0, 0, time.UTC),
		GameKey: 423,
	}

	yahooPlayers := map[int64]PartialPlayer{
		1001: {ID: 1001, FirstName: "Connor", LastName: "McDavid", HasYahooData: true},
	}
	boxscorePlayers := map[int64]PartialPlayer{
		8476453: {ID: 8476453, FirstName: "Connor", LastName: "McDavid", HasBoxscoreData: true},
	}
	mergedPlayers := map[int64]PartialPlayer{
		8476453: {ID: 8476453, FirstName: "Connor", LastName: "McDavid", HasYahooData: true, HasBoxscoreData: true},
	}
	expectedResult := BatchResult{RedisKey: "season-2023-merged", PlayerCount: 1}

	// Mock child workflows for Yahoo and Boxscore extraction
	s.env.OnWorkflow(ExtractYahooPlayersForSeasonWorkflow, mock.Anything, season).Return(yahooPlayers, nil)
	s.env.OnWorkflow(ExtractBoxscorePlayersForSeasonWorkflow, mock.Anything, season).Return(boxscorePlayers, nil)

	// Mock merge activity
	s.env.OnActivity(MergeSeasonPlayersActivity, mock.Anything, yahooPlayers, boxscorePlayers).Return(mergedPlayers, nil)

	// Mock store activity
	s.env.OnActivity(StoreSeasonResultActivity, mock.Anything, season, mergedPlayers).Return(expectedResult, nil)

	s.env.ExecuteWorkflow(ExtractPlayersForSeasonWorkflow, season)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result BatchResult
	s.NoError(s.env.GetWorkflowResult(&result))
	s.Equal("season-2023-merged", result.RedisKey)
	s.Equal(1, result.PlayerCount)
}

func (s *ExtractPlayersWorkflowTestSuite) TestExtractPlayersForSeasonWorkflow_YahooError() {
	season := config.Season{
		Start:   time.Date(2023, 10, 1, 0, 0, 0, 0, time.UTC),
		End:     time.Date(2024, 4, 30, 0, 0, 0, 0, time.UTC),
		GameKey: 423,
	}

	boxscorePlayers := map[int64]PartialPlayer{}

	s.env.OnWorkflow(ExtractYahooPlayersForSeasonWorkflow, mock.Anything, season).Return(nil, errors.New("yahoo failed"))
	s.env.OnWorkflow(ExtractBoxscorePlayersForSeasonWorkflow, mock.Anything, season).Return(boxscorePlayers, nil)

	s.env.ExecuteWorkflow(ExtractPlayersForSeasonWorkflow, season)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}
*/
