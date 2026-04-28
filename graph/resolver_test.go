package graph

import (
	"context"
	"errors"
	"testing"

	"github.com/sperano/puckdb/graph/model"
	"github.com/sperano/puckdb/worker/shared"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
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

func (m *MockTemporalClient) ExecuteWorkflow(ctx context.Context, options client.StartWorkflowOptions, workflow interface{}, args ...interface{}) (client.WorkflowRun, error) {
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

func TestCancelFetchAssetsResolver(t *testing.T) {
	t.Parallel()
	mc := &MockTemporalClient{}
	mc.On("CancelWorkflow", mock.Anything, shared.WorkflowIDFetchAssets, "").Return(nil)

	r := &Resolver{TemporalClient: mc}
	ok, err := r.cancelFetchAssets(context.Background())
	require.NoError(t, err)
	require.True(t, ok)
	mc.AssertExpectations(t)
}

