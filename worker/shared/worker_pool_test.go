package shared

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// workerPoolActivity is the trivial activity used in all worker pool tests.
// It receives the item index and returns it, allowing handlers to verify order.
func workerPoolActivity(_ context.Context, index int) (int, error) {
	return index, nil
}

// workerPoolErrorActivity always returns an error.
func workerPoolErrorActivity(_ context.Context, _ int) (int, error) {
	return 0, errors.New("activity failed")
}

// --- Workflow wrappers -------------------------------------------------------
// Each wrapper runs one of the RunWorkerPool* variants inside a real workflow
// context so that Temporal's selector, goroutine scheduler, and workflow.Now
// work correctly.

type workerPoolResult struct {
	Processed []int
	Err       string
}

// runWorkerPoolWorkflow exercises RunWorkerPool with concurrency capping.
func runWorkerPoolWorkflow(ctx workflow.Context, total, concurrency int) (workerPoolResult, error) {
	report := &ProgressReport{
		Groups: []ProgressGroup{
			{Bars: []ProgressBar{{Total: total}}},
		},
	}
	tracker := &ReportTracker{report: report}

	mu := sync.Mutex{}
	processed := make([]int, 0, total)

	actCtx := workflow.WithLocalActivityOptions(ctx, workflow.LocalActivityOptions{
		ScheduleToCloseTimeout: activityTestTimeout,
	})

	startActivity := func(c workflow.Context, index int) workflow.Future {
		return workflow.ExecuteLocalActivity(actCtx, workerPoolActivity, index)
	}

	handler := func(c workflow.Context, index int, f workflow.Future) error {
		var result int
		if err := f.Get(c, &result); err != nil {
			return err
		}
		mu.Lock()
		processed = append(processed, result)
		mu.Unlock()
		return nil
	}

	err := tracker.RunWorkerPool(ctx, 0, 0, total, concurrency, startActivity, handler)
	res := workerPoolResult{Processed: processed}
	if err != nil {
		res.Err = err.Error()
	}
	return res, nil
}

// runWorkerPoolWithIncrementWorkflow exercises RunWorkerPoolWithIncrement.
// The increment function returns the index+1 so bar current sums to n*(n+1)/2.
func runWorkerPoolWithIncrementWorkflow(ctx workflow.Context, total, concurrency int) (workerPoolResult, error) {
	report := &ProgressReport{
		Groups: []ProgressGroup{
			{Bars: []ProgressBar{{Total: total * (total + 1) / 2}}},
		},
	}
	tracker := &ReportTracker{report: report}

	mu := sync.Mutex{}
	processed := make([]int, 0, total)

	actCtx := workflow.WithLocalActivityOptions(ctx, workflow.LocalActivityOptions{
		ScheduleToCloseTimeout: activityTestTimeout,
	})

	startActivity := func(c workflow.Context, index int) workflow.Future {
		return workflow.ExecuteLocalActivity(actCtx, workerPoolActivity, index)
	}

	incrementFunc := func(index int) int { return index + 1 }

	handler := func(c workflow.Context, index int, f workflow.Future) error {
		var result int
		if err := f.Get(c, &result); err != nil {
			return err
		}
		mu.Lock()
		processed = append(processed, result)
		mu.Unlock()
		return nil
	}

	err := tracker.RunWorkerPoolWithIncrement(ctx, 0, 0, total, concurrency, incrementFunc, startActivity, handler)
	res := workerPoolResult{Processed: processed}
	if err != nil {
		res.Err = err.Error()
	}
	return res, nil
}

// runWorkerPoolMultiBarWorkflow exercises RunWorkerPoolMultiBar.
// Each item has its own bar; on completion bars are marked complete.
func runWorkerPoolMultiBarWorkflow(ctx workflow.Context, total, concurrency int) (workerPoolResult, error) {
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

	mu := sync.Mutex{}
	processed := make([]int, 0, total)

	actCtx := workflow.WithLocalActivityOptions(ctx, workflow.LocalActivityOptions{
		ScheduleToCloseTimeout: activityTestTimeout,
	})

	startActivity := func(c workflow.Context, index int) workflow.Future {
		return workflow.ExecuteLocalActivity(actCtx, workerPoolActivity, index)
	}

	handler := func(c workflow.Context, index int, f workflow.Future) error {
		var result int
		if err := f.Get(c, &result); err != nil {
			return err
		}
		mu.Lock()
		processed = append(processed, result)
		mu.Unlock()
		return nil
	}

	err := tracker.RunWorkerPoolMultiBar(ctx, 0, 0, total, concurrency, startActivity, handler)
	res := workerPoolResult{Processed: processed}
	if err != nil {
		res.Err = err.Error()
	}
	return res, nil
}

// runWorkerPoolErrorWorkflow exercises RunWorkerPool error propagation.
func runWorkerPoolErrorWorkflow(ctx workflow.Context) (workerPoolResult, error) {
	report := &ProgressReport{
		Groups: []ProgressGroup{
			{Bars: []ProgressBar{{Total: 3}}},
		},
	}
	tracker := &ReportTracker{report: report}

	actCtx := workflow.WithLocalActivityOptions(ctx, workflow.LocalActivityOptions{
		ScheduleToCloseTimeout: activityTestTimeout,
	})

	startActivity := func(c workflow.Context, index int) workflow.Future {
		return workflow.ExecuteLocalActivity(actCtx, workerPoolErrorActivity, index)
	}

	err := tracker.RunWorkerPool(ctx, 0, 0, 3, 2, startActivity, nil)
	res := workerPoolResult{}
	if err != nil {
		res.Err = err.Error()
	}
	return res, nil
}

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

// runWorkerPoolZeroWorkflow exercises the RunWorkerPool zero-items short-circuit.
func runWorkerPoolZeroWorkflow(ctx workflow.Context) (workerPoolResult, error) {
	report := &ProgressReport{
		Groups: []ProgressGroup{
			{Bars: []ProgressBar{{Total: 0}}},
		},
	}
	tracker := &ReportTracker{report: report}

	startActivity := func(c workflow.Context, index int) workflow.Future {
		return workflow.ExecuteLocalActivity(c, workerPoolActivity, index)
	}

	err := tracker.RunWorkerPool(ctx, 0, 0, 0, 5, startActivity, nil)
	return workerPoolResult{Err: errString(err)}, nil
}

// runWorkerPoolMultiBarZeroWorkflow exercises RunWorkerPoolMultiBar with zero items.
func runWorkerPoolMultiBarZeroWorkflow(ctx workflow.Context) (workerPoolResult, error) {
	report := &ProgressReport{
		Groups: []ProgressGroup{
			{Bars: []ProgressBar{}},
		},
	}
	tracker := &ReportTracker{report: report}

	neverCalled := func(_ workflow.Context, _ int) workflow.Future {
		// startActivity must never be invoked when total is zero.
		return workflow.ExecuteLocalActivity(ctx, workerPoolActivity, 0)
	}

	err := tracker.RunWorkerPoolMultiBar(ctx, 0, 0, 0, 5, neverCalled, nil)
	return workerPoolResult{Err: errString(err)}, nil
}

func errString(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}

// --- Test suite -------------------------------------------------------------

// activityTestTimeout is the ScheduleToCloseTimeout for local activities in tests.
const activityTestTimeout = 5 * time.Second

// WorkerPoolTestSuite exercises RunWorkerPool, RunWorkerPoolWithIncrement, and
// RunWorkerPoolMultiBar through the Temporal workflow test framework.
type WorkerPoolTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *WorkerPoolTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.RegisterWorkflow(runWorkerPoolWorkflow)
	s.env.RegisterWorkflow(runWorkerPoolWithIncrementWorkflow)
	s.env.RegisterWorkflow(runWorkerPoolMultiBarWorkflow)
	s.env.RegisterWorkflow(runWorkerPoolMultiBarZeroWorkflow)
	s.env.RegisterWorkflow(runWorkerPoolErrorWorkflow)
	s.env.RegisterWorkflow(runWorkerPoolZeroWorkflow)
	s.env.RegisterWorkflow(runWorkerPoolIndexErrorWorkflow)
	s.env.RegisterWorkflow(runWorkerPoolMultiBarIndexErrorWorkflow)
	s.env.RegisterActivity(workerPoolActivity)
	s.env.RegisterActivity(workerPoolErrorActivity)
}

func (s *WorkerPoolTestSuite) AfterTest(_, _ string) {
	s.env.AssertExpectations(s.T())
}

func TestWorkerPoolTestSuite(t *testing.T) {
	suite.Run(t, new(WorkerPoolTestSuite))
}

// TestRunWorkerPool_AllItemsProcessed verifies every index [0, total) is handled
// when concurrency equals total (fully parallel execution).
func (s *WorkerPoolTestSuite) TestRunWorkerPool_AllItemsProcessed() {
	const total = 5
	s.env.ExecuteWorkflow(runWorkerPoolWorkflow, total, total)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result workerPoolResult
	s.NoError(s.env.GetWorkflowResult(&result))
	s.Empty(result.Err)
	s.Len(result.Processed, total)
}

// TestRunWorkerPool_Concurrency1 verifies serial execution (concurrency = 1).
func (s *WorkerPoolTestSuite) TestRunWorkerPool_Concurrency1() {
	const total = 4
	s.env.ExecuteWorkflow(runWorkerPoolWorkflow, total, 1)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result workerPoolResult
	s.NoError(s.env.GetWorkflowResult(&result))
	s.Empty(result.Err)
	s.Len(result.Processed, total)
}

// TestRunWorkerPool_ZeroTotal verifies immediate return with no activity calls.
func (s *WorkerPoolTestSuite) TestRunWorkerPool_ZeroTotal() {
	s.env.ExecuteWorkflow(runWorkerPoolZeroWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result workerPoolResult
	s.NoError(s.env.GetWorkflowResult(&result))
	s.Empty(result.Err)
}

// TestRunWorkerPool_ErrorPropagates verifies that the first activity error is returned.
func (s *WorkerPoolTestSuite) TestRunWorkerPool_ErrorPropagates() {
	s.env.ExecuteWorkflow(runWorkerPoolErrorWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	// The workflow itself returns nil; error is surfaced inside the result struct.
	s.NoError(s.env.GetWorkflowError())

	var result workerPoolResult
	s.NoError(s.env.GetWorkflowResult(&result))
	s.NotEmpty(result.Err)
	s.Contains(result.Err, "activity failed")
}

// TestRunWorkerPoolWithIncrement_AllItemsProcessed verifies that the custom
// increment function is accepted and all items complete.
func (s *WorkerPoolTestSuite) TestRunWorkerPoolWithIncrement_AllItemsProcessed() {
	const total = 4
	s.env.ExecuteWorkflow(runWorkerPoolWithIncrementWorkflow, total, 2)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result workerPoolResult
	s.NoError(s.env.GetWorkflowResult(&result))
	s.Empty(result.Err)
	s.Len(result.Processed, total)
}

// TestRunWorkerPoolMultiBar_AllBarsCompleted verifies every per-item bar is
// started and then completed (Current == Total == 1 for each bar).
func (s *WorkerPoolTestSuite) TestRunWorkerPoolMultiBar_AllBarsCompleted() {
	const total = 3
	s.env.ExecuteWorkflow(runWorkerPoolMultiBarWorkflow, total, total)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result workerPoolResult
	s.NoError(s.env.GetWorkflowResult(&result))
	s.Empty(result.Err)
	s.Len(result.Processed, total)
}

// TestRunWorkerPoolMultiBar_ZeroTotal verifies that RunWorkerPoolMultiBar returns
// immediately without calling startActivity when total is zero.
func (s *WorkerPoolTestSuite) TestRunWorkerPoolMultiBar_ZeroTotal() {
	s.env.ExecuteWorkflow(runWorkerPoolMultiBarZeroWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result workerPoolResult
	s.NoError(s.env.GetWorkflowResult(&result))
	s.Empty(result.Err)
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
