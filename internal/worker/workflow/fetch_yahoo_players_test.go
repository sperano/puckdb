package workflow

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/sperano/puckdb/internal/worker/yahoo"
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
		require.Equal(t, config.DefaultYahooPlayerMissingTTLDays, got.MissingTTLDays)
	})

	t.Run("viper flags override defaults", func(t *testing.T) {
		setViperInt(t, config.FlagMaxYahooPlayerID, 111)
		setViperInt(t, config.FlagYahooPlayerBatchSize, 22)
		setViperInt(t, config.FlagYahooPlayerActivityBatchSize, 33)
		setViperInt(t, config.FlagYahooPlayersPerExecution, 444)
		setViperInt(t, config.FlagYahooPlayerMissingTTLDays, 7)

		got := loadFetchYahooPlayersConfig()
		require.Equal(t, 111, got.MaxPlayerID)
		require.Equal(t, 22, got.Concurrency)
		require.Equal(t, 33, got.ActivityBatchSize)
		require.Equal(t, 444, got.PlayersPerExecution)
		require.Equal(t, 7, got.MissingTTLDays)
	})

	t.Run("missing TTL capped below Duration overflow", func(t *testing.T) {
		// Above ~106,751 days, days*24h wraps to a negative Duration and
		// every marker would be ignored.
		setViperInt(t, config.FlagYahooPlayerMissingTTLDays, math.MaxInt32)

		got := loadFetchYahooPlayersConfig()
		require.Equal(t, config.DefaultMaxYahooPlayerMissingTTLDays, got.MissingTTLDays)
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
	env.OnActivity(yahooAct.FetchYahooPlayerBatch, mock.Anything, mock.AnythingOfType("store.YahooPlayerID"), mock.AnythingOfType("store.YahooPlayerID"), mock.AnythingOfType("time.Duration")).
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
	env.OnActivity(yahooAct.FetchYahooPlayerBatch, mock.Anything, mock.AnythingOfType("store.YahooPlayerID"), mock.AnythingOfType("store.YahooPlayerID"), mock.AnythingOfType("time.Duration")).
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

// TestFetchYahooPlayersWorkflow_PartialFinalBatchProgress verifies that an
// execution whose last activity batch is smaller than ActivityBatchSize
// advances the bar by the IDs it fetched. The execution continues as new, so
// no CompleteGroup fills the bar: it must read exactly the IDs processed
// (1-7), not three full batches (9).
func TestFetchYahooPlayersWorkflow_PartialFinalBatchProgress(t *testing.T) {
	const (
		maxPlayerID         = 10
		playersPerExecution = 7
		activityBatchSize   = 3
	)
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(FetchYahooPlayersWorkflow)
	mockProgressSaves(env)

	var yahooAct *yahoo.FetchActivities
	var fetched [][2]store.YahooPlayerID
	env.OnActivity(yahooAct.FetchYahooPlayerBatch, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(func(_ context.Context, first, last store.YahooPlayerID, _ time.Duration) (shared.FetchStats, error) {
			fetched = append(fetched, [2]store.YahooPlayerID{first, last})
			return shared.FetchStats{}, nil
		})

	env.ExecuteWorkflow(FetchYahooPlayersWorkflow, &FetchYahooPlayersInput{
		StartPlayerID: 1,
		Config: &FetchYahooPlayersConfig{
			MaxPlayerID:         maxPlayerID,
			Concurrency:         1,
			ActivityBatchSize:   activityBatchSize,
			PlayersPerExecution: playersPerExecution,
		},
	})

	require.True(t, env.IsWorkflowCompleted())
	var contErr *workflow.ContinueAsNewError
	require.ErrorAs(t, env.GetWorkflowError(), &contErr)
	require.Equal(t, [][2]store.YahooPlayerID{{1, 3}, {4, 6}, {7, 7}}, fetched)
	bar := queryProgress(t, env).Groups[GroupFetchYahooPlayers].Bars[0]
	require.Equal(t, playersPerExecution, bar.Current)
	require.Equal(t, maxPlayerID, bar.Total)
}

// The snapshotted TTL reaches every batch, and per-batch stats accumulate into
// the totals carried across ContinueAsNew.
func TestFetchYahooPlayersWorkflow_PassesTTLAndCarriesTotals(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(FetchYahooPlayersWorkflow)
	mockProgressSaves(env)

	const ttlDays = 9
	var yahooAct *yahoo.FetchActivities
	var gotTTL time.Duration
	env.OnActivity(yahooAct.FetchYahooPlayerBatch, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(func(_ context.Context, _, _ store.YahooPlayerID, ttl time.Duration) (shared.FetchStats, error) {
			gotTTL = ttl
			return shared.FetchStats{Downloaded: 1, CacheHits: 2, Missing: 3}, nil
		})

	// Totals from earlier executions: this execution must add to them.
	env.ExecuteWorkflow(FetchYahooPlayersWorkflow, &FetchYahooPlayersInput{
		StartPlayerID:   1,
		TotalDownloaded: 100,
		TotalMissing:    50,
		TotalFound:      200,
		Config: &FetchYahooPlayersConfig{
			MaxPlayerID: 10, Concurrency: 1, ActivityBatchSize: 5, PlayersPerExecution: 5,
			MissingTTLDays: ttlDays,
		},
	})

	var contErr *workflow.ContinueAsNewError
	require.ErrorAs(t, env.GetWorkflowError(), &contErr)
	require.Equal(t, ttlDays*24*time.Hour, gotTTL)
	var next FetchYahooPlayersInput
	require.NoError(t, converter.GetDefaultDataConverter().FromPayloads(contErr.Input, &next))
	require.Equal(t, 101, next.TotalDownloaded)
	require.Equal(t, 53, next.TotalMissing)
	require.Equal(t, 203, next.TotalFound)
}

// A config carried from before MissingTTLDays existed holds 0: the batches
// still get the default TTL, so markers keep expiring.
func TestFetchYahooPlayersWorkflow_CarriedZeroTTLUsesDefault(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(FetchYahooPlayersWorkflow)
	mockProgressSaves(env)

	var yahooAct *yahoo.FetchActivities
	var gotTTL time.Duration
	env.OnActivity(yahooAct.FetchYahooPlayerBatch, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(func(_ context.Context, _, _ store.YahooPlayerID, ttl time.Duration) (shared.FetchStats, error) {
			gotTTL = ttl
			return shared.FetchStats{}, nil
		})

	env.ExecuteWorkflow(FetchYahooPlayersWorkflow, &FetchYahooPlayersInput{
		StartPlayerID: 1,
		Config: &FetchYahooPlayersConfig{
			MaxPlayerID: 5, Concurrency: 1, ActivityBatchSize: 5, PlayersPerExecution: 5,
		},
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.Equal(t, config.DefaultYahooPlayerMissingTTLDays*24*time.Hour, gotTTL)
}

func TestLogFetchYahooPlayersTotals_WarnsWhenNothingDownloaded(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name                          string
		downloaded, missing, wantWarn int
	}{
		{"all missing, nothing downloaded", 0, 90, 1},
		{"downloads happened", 5, 90, 0},
		{"mostly cached", 0, 10, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			l := &recordingLogger{}
			logFetchYahooPlayersTotals(l, 100, 100, 100-tc.missing, tc.downloaded, tc.missing)
			require.Equal(t, tc.wantWarn, l.warns)
			require.Equal(t, 1, l.infos)
		})
	}
}

type recordingLogger struct{ infos, warns int }

func (l *recordingLogger) Debug(string, ...interface{}) {}
func (l *recordingLogger) Info(string, ...interface{})  { l.infos++ }
func (l *recordingLogger) Warn(string, ...interface{})  { l.warns++ }
func (l *recordingLogger) Error(string, ...interface{}) {}
