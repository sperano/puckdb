package workflow

import (
	"testing"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/store"
	workplayer "github.com/sperano/puckdb/internal/worker/player"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestLoadProcessPlayersConfig(t *testing.T) {
	// Not parallel: viper.Set mutates global state and is not thread-safe.

	t.Run("defaults when nothing configured", func(t *testing.T) {
		got := loadProcessPlayersConfig(nil, nil, nil)
		require.Equal(t, config.DefaultProcessPlayersBatchSize, got.BatchSize)
		require.Equal(t, config.DefaultProcessPlayersConcurrency, got.Concurrency)
		require.Equal(t, config.DefaultPlayerLandingPlayersPerExec, got.PlayersPerExec)
	})

	t.Run("viper flags override defaults", func(t *testing.T) {
		setViperInt(t, config.FlagProcessPlayersBatchSize, 77)
		setViperInt(t, config.FlagProcessPlayersConcurrency, 3)
		setViperInt(t, config.FlagPlayerLandingPlayersPerExec, 500)

		got := loadProcessPlayersConfig(nil, nil, nil)
		require.Equal(t, 77, got.BatchSize)
		require.Equal(t, 3, got.Concurrency)
		require.Equal(t, 500, got.PlayersPerExec)
	})

	t.Run("input overrides win for batch size and concurrency", func(t *testing.T) {
		setViperInt(t, config.FlagProcessPlayersBatchSize, 77)
		setViperInt(t, config.FlagProcessPlayersConcurrency, 3)

		got := loadProcessPlayersConfig(nil, intPtr(200), intPtr(9))
		require.Equal(t, 200, got.BatchSize)
		require.Equal(t, 9, got.Concurrency)
	})

	t.Run("PlayersPerExec has no override", func(t *testing.T) {
		// loadProcessPlayersConfig doesn't take a PlayersPerExec override; it
		// always resolves from shared.PlayerLandingPlayersPerExecParam.
		setViperInt(t, config.FlagPlayerLandingPlayersPerExec, 250)

		got := loadProcessPlayersConfig(nil, intPtr(1), intPtr(1))
		require.Equal(t, 250, got.PlayersPerExec)
	})
}

// yahooPlayersPerTestBatch splits the mocked Yahoo listing into one batch per
// player.
const yahooPlayersPerTestBatch = 1

// Phase 1 grows the Yahoo bar from one step to one per Yahoo player batch;
// the saved report's Total must follow, so Completed never exceeds it.
func (s *ProcessPlayersWorkflowTestSuite) TestProcessPlayersWorkflow_Phase1_SavedTotalMatchesBars() {
	players := []store.BoxscorePlayer{
		{ID: 8471214, FirstName: "Sidney", LastName: "Crosby"},
		{ID: 8478402, FirstName: "Connor", LastName: "McDavid"},
	}
	// One Yahoo player per batch, so the Yahoo bar needs more than one step.
	batchSize := yahooPlayersPerTestBatch
	yahooIDs := s.mockPhaseLoadYahooSuccess(players)
	lastSaved := recordLastSavedReport(s.env)

	s.env.ExecuteWorkflow(ProcessPlayersWorkflow, &ProcessPlayersInput{BatchSize: &batchSize})

	s.True(s.env.IsWorkflowCompleted())
	yahooBatches := shared.BatchCount(len(yahooIDs), batchSize)
	s.Require().Greater(yahooBatches, processPlayersYahooEstimate)
	report := lastSaved()
	requireTotalIsBarSum(s.T(), report, yahooBatches+len(players)+processPlayersUnmatchedEstimate)
	s.LessOrEqual(report.Completed, report.Total)
}

// Phase 3 grows the Unmatched bar from one step to one per unmatched player;
// the saved report's Total must follow, so Completed never exceeds it.
func (s *ProcessPlayersWorkflowTestSuite) TestProcessPlayersWorkflow_Phase3_SavedTotalMatchesBars() {
	input := &processPlayersInternalInput{
		Phase:   phaseVerifyUnmatched,
		Players: []store.BoxscorePlayer{{ID: 8471214, FirstName: "Sidney", LastName: "Crosby"}},
	}
	unmatched := []workplayer.UnmatchedYahooPlayer{
		{YahooID: 123, FirstName: "Unknown", LastName: "One"},
		{YahooID: 124, FirstName: "Unknown", LastName: "Two"},
		{YahooID: 125, FirstName: "Unknown", LastName: "Three"},
	}

	reportBytes := gobEncodeProgressReport(NewProcessPlayersProgressReport(len(input.Players)))
	s.env.OnActivity(((*shared.ProgressActivities)(nil)).Load, mock.Anything, mock.Anything).Return(reportBytes, nil)
	lastSaved := recordLastSavedReport(s.env)

	var playerAct *workplayer.Activities
	s.env.OnActivity(playerAct.LoadUnmatchedYahooPlayers, mock.Anything).Return(unmatched, nil)
	s.env.OnActivity(playerAct.VerifyUnmatchedBatch, mock.Anything, mock.Anything).Return(
		&workplayer.VerifyUnmatchedResult{VerifiedNonNHL: []store.YahooPlayerID{123, 124, 125}}, nil)
	s.env.OnActivity(playerAct.CleanupYahooIDPoolData, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(ProcessPlayersWorkflowContinue, input)

	s.True(s.env.IsWorkflowCompleted())
	s.Require().NoError(s.env.GetWorkflowError())
	report := lastSaved()
	requireTotalIsBarSum(s.T(), report, processPlayersYahooEstimate+len(input.Players)+len(unmatched))
	s.LessOrEqual(report.Completed, report.Total)
}

// mockPhaseLoadYahooSuccess mocks the activities ProcessPlayersWorkflow runs
// up to its ContinueAsNew into Phase 2, loading players from the boxscores,
// and returns the Yahoo player IDs the listing activity reports.
func (s *ProcessPlayersWorkflowTestSuite) mockPhaseLoadYahooSuccess(players []store.BoxscorePlayer) []store.YahooPlayerID {
	yahooIDs := []store.YahooPlayerID{1, 2, 3}
	var playerAct *workplayer.Activities
	s.env.OnActivity(playerAct.LoadAllBoxscorePlayers, mock.Anything).Return(players, nil)
	s.env.OnActivity(playerAct.ListYahooPlayerFiles, mock.Anything).Return(yahooIDs, nil)
	s.env.OnActivity(playerAct.ParseYahooPlayerBatch, mock.Anything, mock.Anything).Return(
		[]store.YahooPlayer{{YahooID: 1, FirstName: "Test", LastName: "Player"}}, nil)
	s.env.OnActivity(playerAct.SaveYahooPlayersToRedis, mock.Anything, mock.Anything).Return(
		&workplayer.SaveYahooIDPoolResult{TotalPlayers: 1, AvailablePlayers: 1}, nil)
	return yahooIDs
}
