package shared

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

const (
	untilTestTotal       = 10
	untilTestConcurrency = 3
	// untilTestStopIndex is the item whose handling stops dispatching.
	untilTestStopIndex = 4
	// untilTestNeverStop disables stopping.
	untilTestNeverStop = untilTestTotal
)

// runActivitiesUntilWorkflow runs RunActivitiesUntil, stopping once the item
// at stopIndex has been handled (immediately when stopIndex is negative).
func runActivitiesUntilWorkflow(ctx workflow.Context, stopIndex int) (workerPoolResult, error) {
	ctx = workflow.WithLocalActivityOptions(ctx, workflow.LocalActivityOptions{
		ScheduleToCloseTimeout: activityTestTimeout,
	})
	res := workerPoolResult{}
	stopped := stopIndex < 0
	start := func(c workflow.Context, index int) workflow.Future {
		res.Dispatched++
		return workflow.ExecuteLocalActivity(c, workerPoolActivity, index)
	}
	handler := func(c workflow.Context, _ int, f workflow.Future) error {
		var index int
		if err := f.Get(c, &index); err != nil {
			return err
		}
		res.Processed = append(res.Processed, index)
		stopped = stopped || index == stopIndex
		return nil
	}
	err := RunActivitiesUntil(ctx, untilTestTotal, untilTestConcurrency, start, handler, func() bool { return stopped })
	res.Err = errString(err)
	return res, nil
}

func executeActivitiesUntil(t *testing.T, stopIndex int) workerPoolResult {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(runActivitiesUntilWorkflow)
	env.RegisterActivity(workerPoolActivity)
	env.ExecuteWorkflow(runActivitiesUntilWorkflow, stopIndex)
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var result workerPoolResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Empty(t, result.Err)
	return result
}

// Once stop reports true nothing new is dispatched, but every item already
// in flight is still handled.
func TestRunActivitiesUntil_StopsDispatchingAndDrainsInFlight(t *testing.T) {
	result := executeActivitiesUntil(t, untilTestStopIndex)

	assert.Greater(t, result.Dispatched, untilTestStopIndex)
	assert.LessOrEqual(t, result.Dispatched, untilTestStopIndex+untilTestConcurrency,
		"at most the items in flight when the stop item was handled follow it")
	assert.Less(t, result.Dispatched, untilTestTotal)
	assert.ElementsMatch(t, indexes(result.Dispatched), result.Processed, "no dispatched item is abandoned")
}

func TestRunActivitiesUntil_StopBeforeFirstDispatch(t *testing.T) {
	result := executeActivitiesUntil(t, -1)

	assert.Zero(t, result.Dispatched)
	assert.Empty(t, result.Processed)
}

func TestRunActivitiesUntil_NeverStopping(t *testing.T) {
	result := executeActivitiesUntil(t, untilTestNeverStop)

	assert.Equal(t, untilTestTotal, result.Dispatched)
	assert.ElementsMatch(t, indexes(untilTestTotal), result.Processed)
}

// indexes returns 0..n-1, the items a pool dispatches first.
func indexes(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}
