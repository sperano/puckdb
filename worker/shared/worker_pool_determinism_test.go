package shared

// Determinism-focused worker pool tests and the bookkeeping benchmark. The
// behavioral tests live in worker_pool_test.go; this file pins the properties
// replay compatibility depends on: lowest-index tie-breaking at a decision
// point and the exact hook/dispatch command ordering.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// presettledErrorStarter returns a startActivity that hands back futures already
// resolved with an index-tagged error. Pre-settling removes real-time local
// activity scheduling from the picture, so with concurrency == total every
// future is ready at the first Selector.Select. That is exactly the "multiple
// activities complete at one decision point" condition the determinism fix
// targets: the returned firstErr must be the lowest-index one (error-0),
// independent of Go's randomized map iteration order.
func presettledErrorStarter(ctx workflow.Context) ActivityStarter {
	return func(_ workflow.Context, index int) workflow.Future {
		future, settable := workflow.NewFuture(ctx)
		settable.SetError(fmt.Errorf("error-%d", index))
		return future
	}
}

// runWorkerPoolIndexErrorWorkflow drives RunWorkerPool with pre-settled,
// simultaneously-ready error futures. See presettledErrorStarter.
func runWorkerPoolIndexErrorWorkflow(ctx workflow.Context, total int) (workerPoolResult, error) {
	report := &ProgressReport{
		Groups: []ProgressGroup{
			{Bars: []ProgressBar{{Total: total}}},
		},
	}
	tracker := &ReportTracker{report: report}

	err := tracker.RunWorkerPool(ctx, 0, 0, total, total, presettledErrorStarter(ctx), nil)
	return workerPoolResult{Err: errString(err)}, nil
}

// runWorkerPoolMultiBarIndexErrorWorkflow is the RunWorkerPoolMultiBar analogue
// of runWorkerPoolIndexErrorWorkflow.
func runWorkerPoolMultiBarIndexErrorWorkflow(ctx workflow.Context, total int) (workerPoolResult, error) {
	bars := make([]ProgressBar, total)
	for i := range bars {
		bars[i] = ProgressBar{Total: 1}
	}
	report := &ProgressReport{
		Groups: []ProgressGroup{
			{Bars: bars},
		},
	}
	tracker := &ReportTracker{report: report}

	err := tracker.RunWorkerPoolMultiBar(ctx, 0, 0, total, total, presettledErrorStarter(ctx), nil)
	return workerPoolResult{Err: errString(err)}, nil
}

// runWorkerPoolEventOrderWorkflow drives the scheduler directly with logging
// hooks, a logging starter, and pre-settled success futures, returning the
// exact command sequence. Pre-settled futures make every future ready at each
// Selector.Select, so the sequence below is fully determined by the
// scheduler's dispatch and registration order.
func runWorkerPoolEventOrderWorkflow(ctx workflow.Context, total, concurrency int) ([]string, error) {
	var events []string

	startActivity := func(_ workflow.Context, index int) workflow.Future {
		events = append(events, fmt.Sprintf("dispatch:%d", index))
		future, settable := workflow.NewFuture(ctx)
		settable.Set(nil, nil)
		return future
	}

	hooks := workerPoolHooks{
		onStart: func(_ workflow.Context, index int) {
			events = append(events, fmt.Sprintf("start:%d", index))
		},
		onComplete: func(_ workflow.Context, index int) {
			events = append(events, fmt.Sprintf("complete:%d", index))
		},
	}

	err := runWorkerPool(ctx, total, concurrency, startActivity, nil, hooks)
	return events, err
}

// runWorkerPoolBookkeepingWorkflow drives the scheduler with pre-settled
// success futures and no hooks, isolating dispatch/selector bookkeeping from
// activity execution. Used by BenchmarkRunWorkerPool_Bookkeeping.
func runWorkerPoolBookkeepingWorkflow(ctx workflow.Context, total, concurrency int) error {
	startActivity := func(_ workflow.Context, _ int) workflow.Future {
		future, settable := workflow.NewFuture(ctx)
		settable.Set(nil, nil)
		return future
	}
	return runWorkerPool(ctx, total, concurrency, startActivity, nil, workerPoolHooks{})
}

// determinismRuns is how many fresh environments each determinism test executes.
// Go randomizes map iteration order per range statement, so with the old
// map-ranging Selector registration the lowest-index-error assertion would fail
// within a handful of runs; the ascending-index fix makes it hold every time.
const determinismRuns = 50

// mockSave stubs the ProgressActivities.Save local activity so the pre-settled
// determinism workflows don't hit the (unregistered) real Save.
func mockSave(env *testsuite.TestWorkflowEnvironment) {
	env.OnActivity(((*ProgressActivities)(nil)).Save, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
}

// TestRunWorkerPool_FirstErrorIsLowestIndex asserts that when several activities
// are ready at the same decision point, RunWorkerPool deterministically returns
// the lowest-index error (error-0) rather than a map-iteration-order-dependent one.
func (s *WorkerPoolTestSuite) TestRunWorkerPool_FirstErrorIsLowestIndex() {
	const total = 8
	for run := range determinismRuns {
		env := s.NewTestWorkflowEnvironment()
		env.RegisterWorkflow(runWorkerPoolIndexErrorWorkflow)
		mockSave(env)
		env.ExecuteWorkflow(runWorkerPoolIndexErrorWorkflow, total)

		s.True(env.IsWorkflowCompleted())
		s.NoError(env.GetWorkflowError())

		var result workerPoolResult
		s.NoError(env.GetWorkflowResult(&result))
		s.Truef(strings.HasSuffix(result.Err, "error-0"), "run %d: expected lowest-index error, got %q", run, result.Err)
	}
}

// TestRunWorkerPoolMultiBar_FirstErrorIsLowestIndex is the RunWorkerPoolMultiBar
// analogue of TestRunWorkerPool_FirstErrorIsLowestIndex.
func (s *WorkerPoolTestSuite) TestRunWorkerPoolMultiBar_FirstErrorIsLowestIndex() {
	const total = 8
	for run := range determinismRuns {
		env := s.NewTestWorkflowEnvironment()
		env.RegisterWorkflow(runWorkerPoolMultiBarIndexErrorWorkflow)
		mockSave(env)
		env.ExecuteWorkflow(runWorkerPoolMultiBarIndexErrorWorkflow, total)

		s.True(env.IsWorkflowCompleted())
		s.NoError(env.GetWorkflowError())

		var result workerPoolResult
		s.NoError(env.GetWorkflowResult(&result))
		s.Truef(strings.HasSuffix(result.Err, "error-0"), "run %d: expected lowest-index error, got %q", run, result.Err)
	}
}

// TestRunWorkerPool_CommandOrdering pins the scheduler's exact command
// sequence — onStart before dispatch, completions in ascending index order,
// refill after each completion — which replay-compatible histories depend on.
// A change that reorders hooks relative to dispatch, or breaks the
// ascending-index registration, fails this test.
func TestRunWorkerPool_CommandOrdering(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(runWorkerPoolEventOrderWorkflow)
	env.ExecuteWorkflow(runWorkerPoolEventOrderWorkflow, 4, 2)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var events []string
	require.NoError(t, env.GetWorkflowResult(&events))
	require.Equal(t, []string{
		"start:0", "dispatch:0",
		"start:1", "dispatch:1",
		"complete:0",
		"start:2", "dispatch:2",
		"complete:1",
		"start:3", "dispatch:3",
		"complete:2",
		"complete:3",
	}, events)
}

// BenchmarkRunWorkerPool_Bookkeeping is a one-off complexity check, not a
// tracked benchmark: with concurrency fixed and futures pre-settled (so
// activity execution is excluded), ns/op should grow roughly linearly with
// total. Superlinear growth across the sizes below means per-completion
// bookkeeping has regressed to scanning all dispatched indexes instead of
// only the active ones. Environment setup inside the loop adds a constant
// per-op cost that does not affect the scaling comparison.
func BenchmarkRunWorkerPool_Bookkeeping(b *testing.B) {
	const concurrency = 8
	for _, total := range []int{1000, 2000, 4000} {
		b.Run(fmt.Sprintf("total=%d", total), func(b *testing.B) {
			var ts testsuite.WorkflowTestSuite
			for i := 0; i < b.N; i++ {
				env := ts.NewTestWorkflowEnvironment()
				env.RegisterWorkflow(runWorkerPoolBookkeepingWorkflow)
				env.ExecuteWorkflow(runWorkerPoolBookkeepingWorkflow, total, concurrency)
				if !env.IsWorkflowCompleted() || env.GetWorkflowError() != nil {
					b.Fatalf("workflow failed: %v", env.GetWorkflowError())
				}
			}
		})
	}
}
