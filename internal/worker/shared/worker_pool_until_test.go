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
	// untilTestStopAfter is how many handled items stop dispatching.
	untilTestStopAfter = 2
	// untilTestStopAtOnce stops before the first dispatch.
	untilTestStopAtOnce = 0
	// untilTestNeverStop stops only once every item is handled, when nothing
	// is left to dispatch.
	untilTestNeverStop = untilTestTotal
	// dispatchedAtStopUnset marks that no handler made stop report true.
	dispatchedAtStopUnset = -1
)

// untilResult is what runActivitiesUntilWorkflow observed.
type untilResult struct {
	Processed  []int
	Dispatched int
	// DispatchedAtStop is Dispatched when the handler that made stop report
	// true ran; dispatchedAtStopUnset when no handler did.
	DispatchedAtStop int
	Err              string
}

// runActivitiesUntilWorkflow runs RunActivitiesUntil, stopping once stopAfter
// items have been handled. Counting handled items rather than waiting for a
// given index keeps the bounds independent of the order in which the local
// activities complete.
func runActivitiesUntilWorkflow(ctx workflow.Context, stopAfter int) (untilResult, error) {
	ctx = workflow.WithLocalActivityOptions(ctx, workflow.LocalActivityOptions{
		ScheduleToCloseTimeout: activityTestTimeout,
	})
	res := untilResult{DispatchedAtStop: dispatchedAtStopUnset}
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
		if len(res.Processed) == stopAfter {
			res.DispatchedAtStop = res.Dispatched
		}
		return nil
	}
	stop := func() bool { return len(res.Processed) >= stopAfter }
	err := RunActivitiesUntil(ctx, untilTestTotal, untilTestConcurrency, start, handler, stop)
	res.Err = errString(err)
	return res, nil
}

func executeActivitiesUntil(t *testing.T, stopAfter int) untilResult {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(runActivitiesUntilWorkflow)
	env.RegisterActivity(workerPoolActivity)
	env.ExecuteWorkflow(runActivitiesUntilWorkflow, stopAfter)
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var result untilResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Empty(t, result.Err)
	return result
}

// Once stop reports true nothing new is dispatched, but every item already
// in flight is still handled.
func TestRunActivitiesUntil_StopsDispatchingAndDrainsInFlight(t *testing.T) {
	result := executeActivitiesUntil(t, untilTestStopAfter)

	require.NotEqual(t, dispatchedAtStopUnset, result.DispatchedAtStop, "the stopping handler ran")
	assert.Equal(t, result.DispatchedAtStop, result.Dispatched, "nothing is dispatched after stop")
	// Each handled item before the stopping one freed a slot that was refilled,
	// so the stopping handler runs with the pool full whatever the order.
	assert.Equal(t, untilTestStopAfter-1+untilTestConcurrency, result.Dispatched)
	assert.Less(t, result.Dispatched, untilTestTotal)
	assert.ElementsMatch(t, indexes(result.Dispatched), result.Processed, "no dispatched item is abandoned")
}

func TestRunActivitiesUntil_StopBeforeFirstDispatch(t *testing.T) {
	result := executeActivitiesUntil(t, untilTestStopAtOnce)

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
