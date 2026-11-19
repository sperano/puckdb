package workflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/sperano/puckdb/internal/worker/yahoo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// A carried scan config: IDs up to 10, four per execution, batches of two.
const (
	scanTestMaxPlayerID      = 10
	scanTestPerExecution     = 4
	scanTestBatchSize        = 2
	scanTestMissingTTLDays   = 7
	scanTestSecondStartID    = 5
	scanTestFinalStartID     = 9
	scanTestBatchDownloaded  = 1
	scanTestBatchCacheHits   = 2
	scanTestBatchMissing     = 3
	scanTestCarriedFound     = 4
	scanTestCarriedDownloads = 2
	scanTestCarriedMissing   = 3
)

var scanTestStartedAt = time.Date(2026, time.November, 1, 0, 0, 0, 0, time.UTC)

func scanTestConfig() *FetchYahooPlayersScanConfig {
	return &FetchYahooPlayersScanConfig{
		MaxPlayerID: scanTestMaxPlayerID, Concurrency: 1, ActivityBatchSize: scanTestBatchSize,
		PlayersPerExecution: scanTestPerExecution, MissingTTLDays: scanTestMissingTTLDays,
	}
}

func scanTestInput(startID int) *FetchYahooPlayersInput {
	return &FetchYahooPlayersInput{
		StartPlayerID: startID, TotalCompleted: startID - 1, TotalFound: scanTestCarriedFound,
		TotalDownloaded: scanTestCarriedDownloads, TotalMissing: scanTestCarriedMissing,
		StartedAt: scanTestStartedAt, Config: scanTestConfig(),
	}
}

// scanBatch is one FetchYahooPlayerBatch call.
type scanBatch struct {
	start, end store.YahooPlayerID
	ttl        time.Duration
}

// runLegacyScan runs input on the scan path (no game lists marker), with the
// final execution's gate at finalVersion, and returns the batches scheduled.
func runLegacyScan(t *testing.T, input *FetchYahooPlayersInput, finalVersion workflow.Version) (*testsuite.TestWorkflowEnvironment, []scanBatch) {
	t.Helper()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(FetchYahooPlayersWorkflow)
	env.OnGetVersion(fetchYahooPlayersGameListsChange, workflow.DefaultVersion, fetchYahooPlayersGameListsVersion).
		Return(workflow.DefaultVersion)
	env.OnGetVersion(fetchYahooPlayersScanFinalChange, workflow.DefaultVersion, fetchYahooPlayersScanFinalVersion).
		Return(finalVersion)
	var batches []scanBatch
	var act *yahoo.FetchActivities
	env.OnActivity(act.FetchYahooPlayerBatch, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(
		func(_ context.Context, start, end store.YahooPlayerID, ttl time.Duration) (shared.FetchStats, error) {
			batches = append(batches, scanBatch{start, end, ttl})
			return shared.FetchStats{Downloaded: scanTestBatchDownloaded, CacheHits: scanTestBatchCacheHits, Missing: scanTestBatchMissing}, nil
		})
	env.ExecuteWorkflow(FetchYahooPlayersWorkflow, input)
	require.True(t, env.IsWorkflowCompleted())
	return env, batches
}

// continuedInput returns the input the execution continued as new with.
func continuedInput(t *testing.T, env *testsuite.TestWorkflowEnvironment) *FetchYahooPlayersInput {
	t.Helper()
	var can *workflow.ContinueAsNewError
	require.True(t, errors.As(env.GetWorkflowError(), &can), "expected ContinueAsNew, got %v", env.GetWorkflowError())
	require.Equal(t, "FetchYahooPlayersWorkflow", can.WorkflowType.Name)
	var next *FetchYahooPlayersInput
	require.NoError(t, converter.GetDefaultDataConverter().FromPayloads(can.Input, &next))
	return next
}

func TestFetchYahooPlayersScan_ContinuesWithCarriedConfigAndTotals(t *testing.T) {
	env, batches := runLegacyScan(t, scanTestInput(scanTestSecondStartID), fetchYahooPlayersScanFinalVersion)

	ttl := scanTestMissingTTLDays * hoursPerDay * time.Hour
	assert.Equal(t, []scanBatch{{5, 6, ttl}, {7, 8, ttl}}, batches, "the carried config sizes and times the batches")
	const executionBatches = 2
	assert.Equal(t, &FetchYahooPlayersInput{
		StartPlayerID:   scanTestSecondStartID + scanTestPerExecution,
		TotalCompleted:  scanTestSecondStartID - 1 + scanTestPerExecution,
		TotalFound:      scanTestCarriedFound + executionBatches*(scanTestBatchDownloaded+scanTestBatchCacheHits),
		TotalDownloaded: scanTestCarriedDownloads + executionBatches*scanTestBatchDownloaded,
		TotalMissing:    scanTestCarriedMissing + executionBatches*scanTestBatchMissing,
		StartedAt:       scanTestStartedAt,
		Config:          scanTestConfig(),
	}, continuedInput(t, env))
}

// The last execution continues as new, with no input, into the game list
// download.
func TestFetchYahooPlayersScan_FinalExecutionContinuesIntoGameLists(t *testing.T) {
	env, batches := runLegacyScan(t, scanTestInput(scanTestFinalStartID), fetchYahooPlayersScanFinalVersion)

	ttl := scanTestMissingTTLDays * hoursPerDay * time.Hour
	assert.Equal(t, []scanBatch{{9, 10, ttl}}, batches)
	assert.Nil(t, continuedInput(t, env))
	group := queryProgress(t, env).Groups[GroupFetchYahooPlayers]
	// 4 carried + one batch's download and cache hits, of all 10 IDs.
	assert.Contains(t, group.CompletedMsg, "Found 7/10 Yahoo! players in ")
}

// A last execution recorded before the gate completes, as it did.
func TestFetchYahooPlayersScan_FinalExecutionBeforeGateCompletes(t *testing.T) {
	env, _ := runLegacyScan(t, scanTestInput(scanTestFinalStartID), workflow.DefaultVersion)

	require.NoError(t, env.GetWorkflowError())
}
