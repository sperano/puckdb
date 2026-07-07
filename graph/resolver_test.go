package graph

import (
	"context"
	"errors"
	"testing"

	"github.com/sperano/puckdb/graph/model"
	"github.com/sperano/puckdb/worker/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	temporalEnums "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
)

// MockTemporalClient is a mock implementation of client.Client
type MockTemporalClient struct {
	mock.Mock
	client.Client
}

// MockWorkflowRun is a mock implementation of client.WorkflowRun
type MockWorkflowRun struct {
	mock.Mock
	client.WorkflowRun
}

func (m *MockTemporalClient) ExecuteWorkflow(ctx context.Context, options client.StartWorkflowOptions, workflow any, args ...any) (client.WorkflowRun, error) {
	callArgs := m.Called(ctx, options, workflow, args)
	run := callArgs.Get(0)
	if run == nil {
		return nil, callArgs.Error(1)
	}
	return run.(client.WorkflowRun), callArgs.Error(1)
}

func (m *MockTemporalClient) CancelWorkflow(ctx context.Context, workflowID string, runID string) error {
	args := m.Called(ctx, workflowID, runID)
	return args.Error(0)
}

// TestFetchAssetsResolver_DispatchesOnAssetQueue verifies that the fetchAssets
// resolver routes the workflow start to puckdb-asset-tasks. Without this, the
// asset worker would never pick up the run.
//
// ExecuteWorkflow returns an error so executeAssetWorkflow short-circuits
// before the post-success Redis cleanup; the test's nil RedisClient is fine
// in that path. The mock matcher is what verifies queue routing.
func TestFetchAssetsResolver_DispatchesOnAssetQueue(t *testing.T) {
	t.Parallel()
	mc := &MockTemporalClient{}
	mc.On("ExecuteWorkflow",
		mock.Anything,
		mock.MatchedBy(func(opts client.StartWorkflowOptions) bool {
			return opts.ID == shared.WorkflowIDFetchAssets &&
				opts.TaskQueue == shared.TaskQueueAssets &&
				opts.WorkflowTaskTimeout > 0
		}),
		mock.Anything, mock.Anything,
	).Return(nil, errors.New("test-only abort"))

	r := &Resolver{TemporalClient: mc}
	ok, err := r.fetchAssets(context.Background(), &model.FetchAssetsInput{})
	require.Error(t, err)
	require.False(t, ok)
	mc.AssertExpectations(t)
}

// TestCancelFetchAssetsResolver pins the schema.resolvers.go CancelFetchAssets
// contract: it must cancel the well-known fetchAssets workflow ID. There is
// no dedicated resolver.go cancelFetchAssets wrapper (removed as a pure
// passthrough in the T3-A dedup pass) — CancelFetchAssets calls the shared
// cancelWorkflow helper directly with shared.WorkflowIDFetchAssets.
func TestCancelFetchAssetsResolver(t *testing.T) {
	t.Parallel()
	mc := &MockTemporalClient{}
	mc.On("CancelWorkflow", mock.Anything, shared.WorkflowIDFetchAssets, "").Return(nil)

	r := &Resolver{TemporalClient: mc}
	ok, err := r.cancelWorkflow(context.Background(), shared.WorkflowIDFetchAssets)
	require.NoError(t, err)
	require.True(t, ok)
	mc.AssertExpectations(t)
}

// TestIsTerminalFailureStatus pins B8: FailureReason must be fetched for every
// terminal non-success state (Failed, TimedOut, Terminated, Canceled), not
// just Failed. Success / in-flight / continued states must not.
func TestIsTerminalFailureStatus(t *testing.T) {
	t.Parallel()
	cases := []struct {
		status model.TemporalWorkflowStatus
		want   bool
	}{
		{model.TemporalWorkflowStatusFailed, true},
		{model.TemporalWorkflowStatusTimedOut, true},
		{model.TemporalWorkflowStatusTerminated, true},
		{model.TemporalWorkflowStatusCanceled, true},
		{model.TemporalWorkflowStatusCompleted, false},
		{model.TemporalWorkflowStatusRunning, false},
		{model.TemporalWorkflowStatusContinuedAsNew, false},
		{model.TemporalWorkflowStatusUnspecified, false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, isTerminalFailureStatus(tc.status), "status=%v", tc.status)
	}
}

// TestTemporalStatusMap_CoversAllKnownStatuses guards the B8 comma-ok lookup:
// every Temporal enum the SDK can report must map to a concrete GQL status, so
// getWorkflowResult only takes the "unknown status" error branch on a genuinely
// new enum rather than silently returning the zero value.
func TestTemporalStatusMap_CoversAllKnownStatuses(t *testing.T) {
	t.Parallel()
	known := []temporalEnums.WorkflowExecutionStatus{
		temporalEnums.WORKFLOW_EXECUTION_STATUS_UNSPECIFIED,
		temporalEnums.WORKFLOW_EXECUTION_STATUS_RUNNING,
		temporalEnums.WORKFLOW_EXECUTION_STATUS_COMPLETED,
		temporalEnums.WORKFLOW_EXECUTION_STATUS_FAILED,
		temporalEnums.WORKFLOW_EXECUTION_STATUS_CANCELED,
		temporalEnums.WORKFLOW_EXECUTION_STATUS_TERMINATED,
		temporalEnums.WORKFLOW_EXECUTION_STATUS_CONTINUED_AS_NEW,
		temporalEnums.WORKFLOW_EXECUTION_STATUS_TIMED_OUT,
	}
	for _, s := range known {
		_, ok := temporalStatusToGQL[s]
		assert.True(t, ok, "status %v must be mapped", s)
	}
}
