package shared

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

const (
	stopTestTotal       = 10
	stopTestConcurrency = 3
	// stopTestStopAfter is how many handled items stop dispatching.
	stopTestStopAfter = 2
	// stopTestStopAtOnce stops before the first dispatch.
	stopTestStopAtOnce = 0
	// stopTestNeverStop stops only once every item is handled, when nothing
	// is left to dispatch.
	stopTestNeverStop = stopTestTotal
	// dispatchedAtStopUnset marks that no handler made StopDispatch report true.
	dispatchedAtStopUnset = -1
)

// stopTestMode is how stopDispatchWorkflow runs its pool.
type stopTestMode int

const (
	// stopTestUntracked runs the package-level RunWorkerPool.
	stopTestUntracked stopTestMode = iota
	// stopTestSingleBar runs the tracker method with a SingleBar.
	stopTestSingleBar
	// stopTestOnlyOnSuccess adds OnlyOnSuccess to stopTestSingleBar.
	stopTestOnlyOnSuccess
)

// stopResult is what stopDispatchWorkflow observed.
type stopResult struct {
	Processed  []int
	Dispatched int
	// DispatchedAtStop is Dispatched when the handler that made StopDispatch
	// report true ran; dispatchedAtStopUnset when no handler did.
	DispatchedAtStop int
	// BarCurrent is the tracked pool's bar once the pool returned; zero for
	// an untracked pool.
	BarCurrent int
	Err        string
}

// stopDispatchWorkflow runs a pool whose StopDispatch reports true once
// stopAfter items have been handled, the way mode says. Counting
// handled items rather than waiting for a given index keeps the bounds
// independent of the order in which the local activities complete.
func stopDispatchWorkflow(ctx workflow.Context, stopAfter int, mode stopTestMode) (stopResult, error) {
	ctx = workflow.WithLocalActivityOptions(ctx, workflow.LocalActivityOptions{
		ScheduleToCloseTimeout: activityTestTimeout,
	})
	res := stopResult{DispatchedAtStop: dispatchedAtStopUnset}
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
	opts := PoolOptions{
		Total: stopTestTotal, Concurrency: stopTestConcurrency,
		Start: start, Handle: handler,
		StopDispatch: func() bool { return len(res.Processed) >= stopAfter },
	}
	var err error
	if mode != stopTestUntracked {
		tracker := &ReportTracker{report: &ProgressReport{Groups: []ProgressGroup{
			{Bars: []ProgressBar{{Total: stopTestTotal}}},
		}}}
		opts.Progress = SingleBar{}
		opts.OnlyOnSuccess = mode == stopTestOnlyOnSuccess
		err = tracker.RunWorkerPool(ctx, opts)
		res.BarCurrent = tracker.Bar(BarRef{}).Current
	} else {
		err = RunWorkerPool(ctx, opts)
	}
	res.Err = errString(err)
	return res, nil
}

func executeStopDispatch(t *testing.T, stopAfter int, mode stopTestMode) stopResult {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(stopDispatchWorkflow)
	env.RegisterActivity(workerPoolActivity)
	env.OnActivity(((*ProgressActivities)(nil)).Save, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Maybe()
	env.ExecuteWorkflow(stopDispatchWorkflow, stopAfter, mode)
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var result stopResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Empty(t, result.Err)
	return result
}

// Once StopDispatch reports true nothing new is dispatched, but every item
// already in flight is still handled, and on a tracked pool still advances
// its bar.
func TestRunWorkerPool_StopDispatchDrainsInFlight(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode stopTestMode
	}{
		{"untracked", stopTestUntracked},
		{"single bar", stopTestSingleBar},
		{"single bar only on success", stopTestOnlyOnSuccess},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := executeStopDispatch(t, stopTestStopAfter, tc.mode)

			require.NotEqual(t, dispatchedAtStopUnset, result.DispatchedAtStop, "the stopping handler ran")
			assert.Equal(t, result.DispatchedAtStop, result.Dispatched, "nothing is dispatched after stop")
			// Each handled item before the stopping one freed a slot that was
			// refilled, so the stopping handler runs with the pool full
			// whatever the order.
			assert.Equal(t, stopTestStopAfter-1+stopTestConcurrency, result.Dispatched)
			assert.Less(t, result.Dispatched, stopTestTotal)
			assert.ElementsMatch(t, indexes(result.Dispatched), result.Processed, "no dispatched item is abandoned")
			if tc.mode != stopTestUntracked {
				assert.Equal(t, result.Dispatched, result.BarCurrent, "every dispatched item advanced the bar")
			}
		})
	}
}

func TestRunWorkerPool_StopDispatchBeforeFirstDispatch(t *testing.T) {
	result := executeStopDispatch(t, stopTestStopAtOnce, stopTestUntracked)

	assert.Zero(t, result.Dispatched)
	assert.Empty(t, result.Processed)
}

func TestRunWorkerPool_StopDispatchNeverStopping(t *testing.T) {
	result := executeStopDispatch(t, stopTestNeverStop, stopTestUntracked)

	assert.Equal(t, stopTestTotal, result.Dispatched)
	assert.ElementsMatch(t, indexes(stopTestTotal), result.Processed)
}

// indexes returns 0..n-1, the items a pool dispatches first.
func indexes(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}
