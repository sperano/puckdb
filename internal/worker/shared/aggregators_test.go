package shared

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// testAdder is a simple type implementing Adder for testing.
type testAdder struct {
	sum int
}

func (a *testAdder) Add(v int) {
	a.sum += v
}

// Test activities that return various types
func returnInt(ctx context.Context, val int) (int, error) {
	return val, nil
}

func returnStrings(ctx context.Context, vals []string) ([]string, error) {
	return vals, nil
}

func TestAggregateInto(t *testing.T) {
	t.Parallel()

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()

	testWorkflow := func(ctx workflow.Context) (int, error) {
		ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			StartToCloseTimeout: time.Second,
		})

		acc := &testAdder{}
		handler := AggregateInto(acc)

		for _, val := range []int{10, 20, 30} {
			future := workflow.ExecuteActivity(ctx, returnInt, val)
			if err := handler(ctx, 0, future); err != nil {
				return 0, err
			}
		}
		return acc.sum, nil
	}

	env.RegisterWorkflow(testWorkflow)
	env.RegisterActivity(returnInt)
	env.ExecuteWorkflow(testWorkflow)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result int
	require.NoError(t, env.GetWorkflowResult(&result))
	assert.Equal(t, 60, result)
}

func TestCollectSlicesInto(t *testing.T) {
	t.Parallel()

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()

	testWorkflow := func(ctx workflow.Context) ([]string, error) {
		ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			StartToCloseTimeout: time.Second,
		})

		var collected []string
		handler := CollectSlicesInto(&collected)

		batches := [][]string{
			{"a", "b"},
			{"c"},
			{"d", "e", "f"},
		}

		for _, batch := range batches {
			future := workflow.ExecuteActivity(ctx, returnStrings, batch)
			if err := handler(ctx, 0, future); err != nil {
				return nil, err
			}
		}
		return collected, nil
	}

	env.RegisterWorkflow(testWorkflow)
	env.RegisterActivity(returnStrings)
	env.ExecuteWorkflow(testWorkflow)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result []string
	require.NoError(t, env.GetWorkflowResult(&result))
	assert.Equal(t, []string{"a", "b", "c", "d", "e", "f"}, result)
}

func TestCollectInto(t *testing.T) {
	t.Parallel()

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()

	testWorkflow := func(ctx workflow.Context) ([]int, error) {
		ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			StartToCloseTimeout: time.Second,
		})

		var collected []int
		handler := CollectInto(&collected)

		for _, val := range []int{1, 2, 3} {
			future := workflow.ExecuteActivity(ctx, returnInt, val)
			if err := handler(ctx, 0, future); err != nil {
				return nil, err
			}
		}
		return collected, nil
	}

	env.RegisterWorkflow(testWorkflow)
	env.RegisterActivity(returnInt)
	env.ExecuteWorkflow(testWorkflow)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result []int
	require.NoError(t, env.GetWorkflowResult(&result))
	assert.Equal(t, []int{1, 2, 3}, result)
}

func failingActivity(_ context.Context) (int, error) {
	return 0, assert.AnError
}

func TestAggregateInto_Error(t *testing.T) {
	t.Parallel()

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()

	testWorkflow := func(ctx workflow.Context) error {
		ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			StartToCloseTimeout: time.Second,
		})

		acc := &testAdder{}
		handler := AggregateInto(acc)

		future := workflow.ExecuteActivity(ctx, failingActivity)
		return handler(ctx, 0, future)
	}

	env.RegisterWorkflow(testWorkflow)
	env.RegisterActivityWithOptions(failingActivity, activity.RegisterOptions{
		DisableAlreadyRegisteredCheck: true,
	})
	env.ExecuteWorkflow(testWorkflow)

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
}
