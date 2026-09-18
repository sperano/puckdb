package workflow

import (
	"errors"
	"testing"

	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/worker/shared"
	"github.com/sperano/puckdb/worker/yahoo"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestLoadFetchYahooPlayersConfig(t *testing.T) {
	// Not parallel: viper.Set mutates global state and is not thread-safe.

	t.Run("defaults when nothing configured", func(t *testing.T) {
		// The snapshot is carried across every ContinueAsNew of a run, so an
		// unset flag must resolve to the package default rather than a zero
		// that a worker restart could no longer correct.
		got := loadFetchYahooPlayersConfig()
		require.Equal(t, config.DefaultMaxYahooPlayerID, got.MaxPlayerID)
		require.Equal(t, config.DefaultYahooPlayerBatchSize, got.Concurrency)
		require.Equal(t, config.DefaultYahooPlayerActivityBatchSize, got.ActivityBatchSize)
		require.Equal(t, config.DefaultYahooPlayersPerExecution, got.PlayersPerExecution)
	})

	t.Run("viper flags override defaults", func(t *testing.T) {
		setViperInt(t, config.FlagMaxYahooPlayerID, 111)
		setViperInt(t, config.FlagYahooPlayerBatchSize, 22)
		setViperInt(t, config.FlagYahooPlayerActivityBatchSize, 33)
		setViperInt(t, config.FlagYahooPlayersPerExecution, 444)

		got := loadFetchYahooPlayersConfig()
		require.Equal(t, 111, got.MaxPlayerID)
		require.Equal(t, 22, got.Concurrency)
		require.Equal(t, 33, got.ActivityBatchSize)
		require.Equal(t, 444, got.PlayersPerExecution)
	})
}

// TestFetchYahooPlayersWorkflow_ContinuesAsNewWithConfig verifies that the
// first execution of a logical run snapshots FetchYahooPlayersConfig from
// viper and carries it into the ContinueAsNew input, so replaying the
// recorded execution (or resuming it after a worker restart) does not depend
// on process-local viper state.
func TestFetchYahooPlayersWorkflow_ContinuesAsNewWithConfig(t *testing.T) {
	// Not parallel: viper.Set mutates global state and is not thread-safe.
	setViperInt(t, config.FlagMaxYahooPlayerID, 10)
	setViperInt(t, config.FlagYahooPlayersPerExecution, 5)
	setViperInt(t, config.FlagYahooPlayerActivityBatchSize, 5)
	setViperInt(t, config.FlagYahooPlayerBatchSize, 1)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(FetchYahooPlayersWorkflow)

	var yahooAct *yahoo.FetchActivities
	env.OnActivity(yahooAct.FetchYahooPlayerBatch, mock.Anything, mock.AnythingOfType("store.YahooPlayerID"), mock.AnythingOfType("store.YahooPlayerID")).
		Return(shared.FetchStats{Downloaded: 1}, nil)

	env.ExecuteWorkflow(FetchYahooPlayersWorkflow, (*FetchYahooPlayersInput)(nil))

	require.True(t, env.IsWorkflowCompleted())

	var contErr *workflow.ContinueAsNewError
	require.True(t, errors.As(env.GetWorkflowError(), &contErr),
		"expected a ContinueAsNewError, got %v", env.GetWorkflowError())

	var nextInput *FetchYahooPlayersInput
	require.NoError(t, converter.GetDefaultDataConverter().FromPayloads(contErr.Input, &nextInput))

	require.NotNil(t, nextInput.Config, "Config must be carried into the ContinueAsNew input")
	require.Equal(t, 10, nextInput.Config.MaxPlayerID)
	require.Equal(t, 1, nextInput.Config.Concurrency)
	require.Equal(t, 5, nextInput.Config.ActivityBatchSize)
	require.Equal(t, 5, nextInput.Config.PlayersPerExecution)
	require.Equal(t, 6, nextInput.StartPlayerID)
}

// TestFetchYahooPlayersWorkflow_ReusesCarriedConfig verifies that an
// execution started with a non-nil Config (i.e. a ContinueAsNew resumption)
// uses that snapshot instead of re-reading viper, even when the process-local
// flags have since changed.
func TestFetchYahooPlayersWorkflow_ReusesCarriedConfig(t *testing.T) {
	// Not parallel: viper.Set mutates global state and is not thread-safe.
	// Set viper to values that would produce a different (larger) run if the
	// workflow mistakenly re-snapshotted instead of using input.Config.
	setViperInt(t, config.FlagMaxYahooPlayerID, 999)
	setViperInt(t, config.FlagYahooPlayersPerExecution, 999)
	setViperInt(t, config.FlagYahooPlayerActivityBatchSize, 999)
	setViperInt(t, config.FlagYahooPlayerBatchSize, 999)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(FetchYahooPlayersWorkflow)

	var yahooAct *yahoo.FetchActivities
	env.OnActivity(yahooAct.FetchYahooPlayerBatch, mock.Anything, mock.AnythingOfType("store.YahooPlayerID"), mock.AnythingOfType("store.YahooPlayerID")).
		Return(shared.FetchStats{Downloaded: 1}, nil)

	input := &FetchYahooPlayersInput{
		StartPlayerID: 1,
		Config: &FetchYahooPlayersConfig{
			MaxPlayerID:         8,
			Concurrency:         1,
			ActivityBatchSize:   8,
			PlayersPerExecution: 8,
		},
	}
	env.ExecuteWorkflow(FetchYahooPlayersWorkflow, input)

	require.True(t, env.IsWorkflowCompleted())
	// With the carried Config, endID (8) equals MaxPlayerID (8): the run
	// completes without ContinueAsNew. If the workflow had re-snapshotted
	// from viper instead, MaxPlayerID=999 would force another ContinueAsNew
	// and this assertion would fail.
	require.NoError(t, env.GetWorkflowError())
}
