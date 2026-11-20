package workflow

import (
	"testing"

	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

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

// Phase 1 builds the pool from the game player lists in one activity: one
// Yahoo bar step, and the pool's seasons reach the continuation
// state.
func (s *ProcessPlayersWorkflowTestSuite) TestProcessPlayersWorkflow_Phase1_GameLists() {
	const players = 2
	var playerAct *workplayer.Activities
	s.env.OnActivity(playerAct.CreateProcessPlayersManifest, mock.Anything, mock.Anything).Return(
		workplayer.ManifestRef{ID: "run", Count: players, Hash: "h"}, nil)
	pool := &workplayer.SaveYahooIDPoolResult{
		TotalPlayers: 3, AvailablePlayers: 3, PoolSize: 3,
		ListSeasons: []int{2026, 2025}, ListEntries: 2,
	}
	s.env.OnActivity(playerAct.BuildYahooIDPool, mock.Anything).Return(pool, nil).Once()
	lastSaved := recordLastSavedReport(s.env)

	s.env.ExecuteWorkflow(ProcessPlayersWorkflow, &ProcessPlayersInput{})

	s.True(s.env.IsWorkflowCompleted())
	var can *workflow.ContinueAsNewError
	s.Require().ErrorAs(s.env.GetWorkflowError(), &can)
	var state *processPlayersState
	s.Require().NoError(converter.GetDefaultDataConverter().FromPayloads(can.Input, &state))
	s.Equal(*pool, state.YahooPool)
	report := lastSaved()
	requireTotalIsBarSum(s.T(), report, processPlayersYahooEstimate+players+processPlayersUnmatchedEstimate)
	yahooBar := report.Groups[GroupLoadYahoo].Bars[0]
	s.Equal(processPlayersYahooEstimate, yahooBar.Current)
	s.Equal(processPlayersYahooEstimate, yahooBar.Total)
}

// A failed BuildYahooIDPool fails the chain.
func (s *ProcessPlayersWorkflowTestSuite) TestProcessPlayersWorkflow_Phase1_BuildPoolError() {
	var playerAct *workplayer.Activities
	s.env.OnActivity(playerAct.CreateProcessPlayersManifest, mock.Anything, mock.Anything).Return(
		workplayer.ManifestRef{ID: "run", Count: 1, Hash: "h"}, nil)
	s.env.OnActivity(playerAct.BuildYahooIDPool, mock.Anything).Return(
		(*workplayer.SaveYahooIDPoolResult)(nil), temporal.NewNonRetryableApplicationError("storage down", "test", nil))

	s.env.ExecuteWorkflow(ProcessPlayersWorkflow, &ProcessPlayersInput{})

	s.True(s.env.IsWorkflowCompleted())
	s.Require().Error(s.env.GetWorkflowError())
	s.Contains(s.env.GetWorkflowError().Error(), "build yahoo id pool")
	s.Contains(s.env.GetWorkflowError().Error(), "storage down")
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
