package shared

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// poolValidationItems is the item count of the validation pools, and the bar
// count of group 1 of newPoolValidationTracker's report.
const poolValidationItems = 3

// newPoolValidationTracker returns a tracker over two groups: group 0 with
// one bar, group 1 with poolValidationItems bars.
func newPoolValidationTracker() *ReportTracker {
	perItem := make([]ProgressBar, poolValidationItems)
	for i := range perItem {
		perItem[i] = ProgressBar{Total: 1}
	}
	return &ReportTracker{report: &ProgressReport{Groups: []ProgressGroup{
		{Bars: []ProgressBar{{Total: poolValidationItems}}},
		{Bars: perItem},
	}}}
}

// poolRun is what one RunWorkerPool call did in a test workflow.
type poolRun struct {
	err        error
	dispatched int
	saves      atomic.Int32
}

// runPool runs tracker.RunWorkerPool(opts) in a test workflow. Unless
// nilStart, opts.Start is a starter that counts dispatches and returns
// already-settled successful futures; every progress Save is counted.
func runPool(t *testing.T, tracker *ReportTracker, opts PoolOptions, nilStart bool) *poolRun {
	t.Helper()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	run := &poolRun{}
	env.OnActivity(((*ProgressActivities)(nil)).Save, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(func(context.Context, string, string, []byte) error {
			run.saves.Add(1)
			return nil
		}).Maybe()
	if !nilStart {
		opts.Start = func(ctx workflow.Context, _ int) workflow.Future {
			run.dispatched++
			future, settable := workflow.NewFuture(ctx)
			settable.Set(nil, nil)
			return future
		}
	}

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		run.err = tracker.RunWorkerPool(ctx, opts)
		return nil
	})
	require.NoError(t, env.GetWorkflowError())
	return run
}

// TestRunWorkerPool_Validation checks every rejected PoolOptions: the call
// returns the error before issuing any command, so no item is dispatched and
// no progress Save runs. The accepted rows are controls for the counters.
func TestRunWorkerPool_Validation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name           string
		opts           PoolOptions
		nilStart       bool
		nilTracker     bool
		wantErr        string
		wantDispatched int
		wantSaves      int32
	}{
		{name: "valid single bar", opts: PoolOptions{Total: poolValidationItems, Concurrency: 2, Progress: SingleBar{}},
			wantDispatched: poolValidationItems, wantSaves: poolValidationItems},
		{name: "nil tracker without progress", nilTracker: true,
			opts: PoolOptions{Total: poolValidationItems, Concurrency: 2}, wantDispatched: poolValidationItems},
		{name: "zero total", opts: PoolOptions{Total: 0, Concurrency: 2, Progress: SingleBar{}}},
		{name: "negative total", opts: PoolOptions{Total: -1, Concurrency: 2, Progress: SingleBar{}},
			wantErr: "worker pool: total must not be negative"},
		{name: "zero concurrency", opts: PoolOptions{Total: poolValidationItems, Concurrency: 0, Progress: SingleBar{}},
			wantErr: "worker pool: concurrency must be positive, got 0 (total 3)"},
		{name: "negative concurrency", opts: PoolOptions{Total: poolValidationItems, Concurrency: -1, Progress: SingleBar{}},
			wantErr: "worker pool: concurrency must be positive, got -1 (total 3)"},
		{name: "nil start", nilStart: true, opts: PoolOptions{Total: poolValidationItems, Concurrency: 2, Progress: SingleBar{}},
			wantErr: "worker pool: Start must not be nil"},
		{name: "single bar past the group's bars",
			opts:    PoolOptions{Total: poolValidationItems, Concurrency: 2, Progress: SingleBar{Bar: BarRef{Group: 0, Bar: 1}}},
			wantErr: "worker pool: bar {group 0, bar 1} outside report (2 groups; group 0 has 1 bars)"},
		{name: "single bar negative bar",
			opts:    PoolOptions{Total: poolValidationItems, Concurrency: 2, Progress: SingleBar{Bar: BarRef{Group: 0, Bar: -1}}},
			wantErr: "worker pool: bar {group 0, bar -1} outside report (2 groups; group 0 has 1 bars)"},
		{name: "single bar past the report's groups",
			opts:    PoolOptions{Total: poolValidationItems, Concurrency: 2, Progress: SingleBar{Bar: BarRef{Group: 2}}},
			wantErr: "worker pool: bar {group 2, bar 0} outside report (2 groups)"},
		{name: "single bar negative group",
			opts:    PoolOptions{Total: poolValidationItems, Concurrency: 2, Progress: SingleBar{Bar: BarRef{Group: -1}}},
			wantErr: "worker pool: bar {group -1, bar 0} outside report (2 groups)"},
		{name: "bar per item negative first",
			opts:    PoolOptions{Total: poolValidationItems, Concurrency: 2, Progress: BarPerItem{Group: 1, First: -1}},
			wantErr: "worker pool: bars [-1, 2) of group 1 outside report (2 groups; group 1 has 3 bars)"},
		{name: "bar per item past the group's bars",
			opts:    PoolOptions{Total: poolValidationItems, Concurrency: 2, Progress: BarPerItem{Group: 1, First: 1}},
			wantErr: "worker pool: bars [1, 4) of group 1 outside report (2 groups; group 1 has 3 bars)"},
		{name: "bar per item past the report's groups",
			opts:    PoolOptions{Total: poolValidationItems, Concurrency: 2, Progress: BarPerItem{Group: 2}},
			wantErr: "worker pool: bars [0, 3) of group 2 outside report (2 groups)"},
		{name: "valid bars per item",
			opts: PoolOptions{Total: poolValidationItems, Concurrency: 2,
				Progress: BarsPerItem{Group: 1, Ranges: []BarRange{{Start: 0, Count: 2}, {Start: 2, Count: 1}, {Start: 3}}}},
			wantDispatched: poolValidationItems, wantSaves: 2 * poolValidationItems},
		{name: "bars per item fewer ranges than items",
			opts:    PoolOptions{Total: poolValidationItems, Concurrency: 2, Progress: BarsPerItem{Group: 1, Ranges: make([]BarRange, 2)}},
			wantErr: "worker pool: 2 bar ranges for 3 items"},
		{name: "bars per item more ranges than items",
			opts:    PoolOptions{Total: poolValidationItems, Concurrency: 2, Progress: BarsPerItem{Group: 1, Ranges: make([]BarRange, 4)}},
			wantErr: "worker pool: 4 bar ranges for 3 items"},
		{name: "bars per item past the report's groups",
			opts:    PoolOptions{Total: poolValidationItems, Concurrency: 2, Progress: BarsPerItem{Group: 2, Ranges: make([]BarRange, 3)}},
			wantErr: "worker pool: group 2 outside report (2 groups)"},
		{name: "bars per item negative group",
			opts:    PoolOptions{Total: poolValidationItems, Concurrency: 2, Progress: BarsPerItem{Group: -1, Ranges: make([]BarRange, 3)}},
			wantErr: "worker pool: group -1 outside report (2 groups)"},
		{name: "bars per item range past the group's bars",
			opts: PoolOptions{Total: poolValidationItems, Concurrency: 2,
				Progress: BarsPerItem{Group: 1, Ranges: []BarRange{{Start: 0, Count: 1}, {Start: 1, Count: 1}, {Start: 2, Count: 2}}}},
			wantErr: "worker pool: item 2 bars [2, 4) of group 1 outside report (2 groups; group 1 has 3 bars)"},
		{name: "bars per item range starting past the group's bars",
			opts: PoolOptions{Total: poolValidationItems, Concurrency: 2,
				Progress: BarsPerItem{Group: 1, Ranges: []BarRange{{Start: 0, Count: 1}, {Start: 4}, {Start: 2, Count: 1}}}},
			wantErr: "worker pool: item 1 bars [4, 4) of group 1 outside report (2 groups; group 1 has 3 bars)"},
		{name: "bars per item negative start",
			opts: PoolOptions{Total: poolValidationItems, Concurrency: 2,
				Progress: BarsPerItem{Group: 1, Ranges: []BarRange{{Start: -1, Count: 1}, {Start: 0, Count: 1}, {Start: 1, Count: 1}}}},
			wantErr: "worker pool: item 0 bars [-1, 0) of group 1 outside report (2 groups; group 1 has 3 bars)"},
		{name: "bars per item negative count",
			opts: PoolOptions{Total: poolValidationItems, Concurrency: 2,
				Progress: BarsPerItem{Group: 1, Ranges: []BarRange{{Start: 0, Count: 1}, {Start: 2, Count: -1}, {Start: 1, Count: 1}}}},
			wantErr: "worker pool: item 1 bars [2, 1) of group 1 outside report (2 groups; group 1 has 3 bars)"},
		{name: "bars per item on a nil tracker", nilTracker: true,
			opts:    PoolOptions{Total: poolValidationItems, Concurrency: 2, Progress: BarsPerItem{Ranges: make([]BarRange, 3)}},
			wantErr: "worker pool: progress needs a tracker, got a nil one"},
		{name: "OnlyOnSuccess with an invalid mode",
			opts: PoolOptions{Total: poolValidationItems, Concurrency: 2, OnlyOnSuccess: true,
				Progress: BarsPerItem{Group: 1, Ranges: make([]BarRange, 2)}},
			wantErr: "worker pool: 2 bar ranges for 3 items"},
		{name: "single bar pointer",
			opts:    PoolOptions{Total: poolValidationItems, Concurrency: 2, Progress: &SingleBar{}},
			wantErr: "worker pool: progress must be a SingleBar, BarPerItem or BarsPerItem value, got *shared.SingleBar"},
		{name: "typed nil single bar pointer",
			opts:    PoolOptions{Total: poolValidationItems, Concurrency: 2, Progress: (*SingleBar)(nil)},
			wantErr: "worker pool: progress must be a SingleBar, BarPerItem or BarsPerItem value, got *shared.SingleBar"},
		{name: "typed nil bar per item pointer",
			opts:    PoolOptions{Total: poolValidationItems, Concurrency: 2, Progress: (*BarPerItem)(nil)},
			wantErr: "worker pool: progress must be a SingleBar, BarPerItem or BarsPerItem value, got *shared.BarPerItem"},
		{name: "typed nil bars per item pointer",
			opts:    PoolOptions{Total: poolValidationItems, Concurrency: 2, Progress: (*BarsPerItem)(nil)},
			wantErr: "worker pool: progress must be a SingleBar, BarPerItem or BarsPerItem value, got *shared.BarsPerItem"},
		{name: "progress on a nil tracker", nilTracker: true,
			opts:    PoolOptions{Total: poolValidationItems, Concurrency: 2, Progress: SingleBar{}},
			wantErr: "worker pool: progress needs a tracker, got a nil one"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tracker := newPoolValidationTracker()
			if tc.nilTracker {
				tracker = nil
			}
			run := runPool(t, tracker, tc.opts, tc.nilStart)
			if tc.wantErr != "" {
				require.EqualError(t, run.err, tc.wantErr)
			} else {
				require.NoError(t, run.err)
			}
			assert.Equal(t, tc.wantDispatched, run.dispatched, "items dispatched")
			assert.Equal(t, tc.wantSaves, run.saves.Load(), "progress saves")
		})
	}
}

// TestRunWorkerPool_BarPerItemFromFirst checks that BarPerItem gives item i
// the bar First+i: with First 2, two items start and complete bars 2 and 3
// (one Save each way per item) and leave the bars around them untouched.
func TestRunWorkerPool_BarPerItemFromFirst(t *testing.T) {
	t.Parallel()
	const bars, first, items = 5, 2, 2
	report := &ProgressReport{Groups: []ProgressGroup{{Bars: make([]ProgressBar, bars)}}}
	for i := range report.Groups[0].Bars {
		report.Groups[0].Bars[i].Total = 1
	}
	tracker := &ReportTracker{report: report}

	run := runPool(t, tracker, PoolOptions{Total: items, Concurrency: 1, Progress: BarPerItem{First: first}}, false)

	require.NoError(t, run.err)
	assert.Equal(t, items, run.dispatched)
	assert.Equal(t, int32(2*items), run.saves.Load(), "StartBar and CompleteBar save once each per item")
	for i, bar := range report.Groups[0].Bars {
		if i >= first && i < first+items {
			assert.Truef(t, bar.Started, "bar %d started", i)
			assert.Equalf(t, bar.Total, bar.Current, "bar %d complete", i)
		} else {
			assert.Equalf(t, ProgressBar{Total: 1}, bar, "bar %d untouched", i)
		}
	}
}
