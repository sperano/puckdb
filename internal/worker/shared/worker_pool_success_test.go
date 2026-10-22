package shared

// PoolOptions.OnlyOnSuccess tests: each progress mode on a run whose last item
// fails, with and without the option, and the command trace it must keep
// (handler, then the completion Save, and nothing for a failed item).

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

const (
	// successRunItems is the item count of the failing runs.
	successRunItems = 3
	// successRunFailingItem is the item that fails: the last one, so with
	// concurrency 1 every other item has completed before the pool stops.
	successRunFailingItem = successRunItems - 1
)

// errSuccessRunItem is the failing item's error.
var errSuccessRunItem = errors.New("item failed")

// settledStarter returns a Start whose futures are already settled: with
// errSuccessRunItem when fails(i) is true, successfully otherwise. A nil
// fails settles every item successfully.
func settledStarter(fails func(int) bool) ActivityStarter {
	return func(ctx workflow.Context, index int) workflow.Future {
		future, settable := workflow.NewFuture(ctx)
		if fails != nil && fails(index) {
			settable.SetError(errSuccessRunItem)
		} else {
			settable.Set(nil, nil)
		}
		return future
	}
}

// failsLast reports whether index is successRunFailingItem.
func failsLast(index int) bool { return index == successRunFailingItem }

// successRunReport has one group per mode: group 0 a single bar of
// successRunItems units (SingleBar), group 1 one bar per item (BarPerItem),
// group 2 the bars of successRunRanges (BarsPerItem).
func successRunReport() *ProgressReport {
	perItem := make([]ProgressBar, successRunItems)
	for i := range perItem {
		perItem[i] = ProgressBar{Total: 1}
	}
	return &ProgressReport{Groups: []ProgressGroup{
		{Bars: []ProgressBar{{Total: successRunItems}}},
		{Bars: perItem},
		{Bars: []ProgressBar{{Total: 1}, {Total: 2}, {Total: 3}, {Total: 4}}},
	}}
}

// successRunRanges gives the last item two bars, so BarsPerItem shows a
// failed item leaving every bar of its range incomplete.
var successRunRanges = []BarRange{{Start: 0, Count: 1}, {Start: 1, Count: 1}, {Start: 2, Count: 2}}

// runFailingPool runs opts over successRunReport with concurrency 1 and the
// last item failing, and returns the report after the run.
func runFailingPool(t *testing.T, opts PoolOptions) *ProgressReport {
	t.Helper()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	mockSave(env)
	report := successRunReport()
	opts.Total, opts.Concurrency, opts.Start = successRunItems, 1, settledStarter(failsLast)
	var runErr error
	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		runErr = (&ReportTracker{report: report}).RunWorkerPool(ctx, opts)
		return nil
	})
	require.NoError(t, env.GetWorkflowError())
	require.ErrorIs(t, runErr, errSuccessRunItem)
	return report
}

// TestRunWorkerPool_OnlyOnSuccess runs each progress mode on a pool whose last
// item fails. The successful items advance either way; the failed item
// advances only without OnlyOnSuccess (the default).
func TestRunWorkerPool_OnlyOnSuccess(t *testing.T) {
	t.Parallel()
	modes := []struct {
		name     string
		progress PoolProgress
		// check asserts the report; failedAdvanced is whether the failed
		// item's progress is expected to have advanced.
		check func(t *testing.T, report *ProgressReport, failedAdvanced bool)
	}{
		{name: "SingleBar", progress: SingleBar{Bar: BarRef{Group: 0}},
			check: func(t *testing.T, report *ProgressReport, failedAdvanced bool) {
				want := successRunItems - 1
				if failedAdvanced {
					want = successRunItems
				}
				assert.Equal(t, want, report.Groups[0].Bars[0].Current, "units advanced")
			}},
		{name: "BarPerItem", progress: BarPerItem{Group: 1},
			check: func(t *testing.T, report *ProgressReport, failedAdvanced bool) {
				for i, bar := range report.Groups[1].Bars {
					assertItemBar(t, bar, i, i != successRunFailingItem || failedAdvanced)
				}
			}},
		{name: "BarsPerItem", progress: BarsPerItem{Group: 2, Ranges: successRunRanges},
			check: func(t *testing.T, report *ProgressReport, failedAdvanced bool) {
				for item, r := range successRunRanges {
					for i := r.Start; i < r.Start+r.Count; i++ {
						assertItemBar(t, report.Groups[2].Bars[i], i, item != successRunFailingItem || failedAdvanced)
					}
				}
			}},
	}
	for _, mode := range modes {
		for _, onlyOnSuccess := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/OnlyOnSuccess=%t", mode.name, onlyOnSuccess), func(t *testing.T) {
				t.Parallel()
				report := runFailingPool(t, PoolOptions{Progress: mode.progress, OnlyOnSuccess: onlyOnSuccess})
				mode.check(t, report, !onlyOnSuccess)
			})
		}
	}
}

// assertItemBar checks that a per-item bar was started on dispatch and is
// complete exactly when complete is true.
func assertItemBar(t *testing.T, bar ProgressBar, idx int, complete bool) {
	t.Helper()
	assert.Truef(t, bar.Started, "bar %d started", idx)
	want := 0
	if complete {
		want = bar.Total
	}
	assert.Equalf(t, want, bar.Current, "bar %d current", idx)
}

// TestRunWorkerPool_OnlyOnSuccessWithoutProgress checks that OnlyOnSuccess
// with a nil Progress is accepted and changes nothing: every item runs, its
// handler sees its future, and no progress Save is issued.
func TestRunWorkerPool_OnlyOnSuccessWithoutProgress(t *testing.T) {
	t.Parallel()
	var handled []int
	run := runPool(t, nil, PoolOptions{
		Total: poolValidationItems, Concurrency: 1, OnlyOnSuccess: true,
		Handle: func(ctx workflow.Context, index int, future workflow.Future) error {
			handled = append(handled, index)
			return future.Get(ctx, nil)
		},
	}, false)
	require.NoError(t, run.err)
	assert.Equal(t, []int{0, 1, 2}, handled)
	assert.Zero(t, run.saves.Load(), "progress saves")
}

// TestRunWorkerPool_OnlyOnSuccessCommandOrder pins the trace of a BarPerItem
// pool whose last item fails, with and without OnlyOnSuccess. Each item saves
// its started bar before its handler runs and its completed bar after it;
// OnlyOnSuccess drops only the failed item's completion Save.
func TestRunWorkerPool_OnlyOnSuccessCommandOrder(t *testing.T) {
	t.Parallel()
	itemTrace := func(i int) []string {
		return []string{"save", fmt.Sprintf("handle:%d", i), "save"}
	}
	var succeeded []string
	for i := range successRunFailingItem {
		succeeded = append(succeeded, itemTrace(i)...)
	}
	failedStart := []string{"save", fmt.Sprintf("handle:%d", successRunFailingItem)}

	for _, tc := range []struct {
		onlyOnSuccess bool
		want          []string
	}{
		{onlyOnSuccess: false, want: append(append(append([]string{}, succeeded...), failedStart...), "save")},
		{onlyOnSuccess: true, want: append(append([]string{}, succeeded...), failedStart...)},
	} {
		t.Run(fmt.Sprintf("OnlyOnSuccess=%t", tc.onlyOnSuccess), func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, failingPoolTrace(t, tc.onlyOnSuccess))
		})
	}
}

// failingPoolTrace runs a BarPerItem pool whose last item fails and returns
// its handler calls and progress Saves in the order they ran.
func failingPoolTrace(t *testing.T, onlyOnSuccess bool) []string {
	t.Helper()
	var (
		mu     sync.Mutex
		events []string
	)
	record := func(event string) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, event)
	}
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.OnActivity(((*ProgressActivities)(nil)).Save, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(func(context.Context, string, string, []byte) error {
			record("save")
			return nil
		}).Maybe()
	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		tracker := &ReportTracker{report: successRunReport()}
		return tracker.RunWorkerPool(ctx, PoolOptions{
			Total: successRunItems, Concurrency: 1,
			Progress:      BarPerItem{Group: 1},
			OnlyOnSuccess: onlyOnSuccess,
			Start:         settledStarter(failsLast),
			Handle: func(ctx workflow.Context, index int, future workflow.Future) error {
				record(fmt.Sprintf("handle:%d", index))
				return future.Get(ctx, nil)
			},
		})
	})
	require.Error(t, env.GetWorkflowError())
	mu.Lock()
	defer mu.Unlock()
	return events
}
