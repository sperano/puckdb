package graph

import (
	"context"

	"github.com/stretchr/testify/mock"
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
	callArgs := m.Called(ctx, options.ID, workflow, args)
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

