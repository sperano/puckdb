package shared

import (
	"bytes"
	"context"
	"encoding/gob"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

const (
	saveEveryTestItems = 10
	saveEveryTestBatch = 3
	queryItemDuration  = time.Hour
)

type savedProgressReports struct {
	mu      sync.Mutex
	reports []ProgressReport
}

func (s *savedProgressReports) record(_ context.Context, _, _ string, data []byte) error {
	var report ProgressReport
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&report); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reports = append(s.reports, report)
	return nil
}

func (s *savedProgressReports) currents() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]int, len(s.reports))
	for i, report := range s.reports {
		result[i] = reportCurrent(report)
	}
	return result
}

func reportCurrent(report ProgressReport) int {
	current := 0
	for _, group := range report.Groups {
		for _, bar := range group.Bars {
			current += bar.Current
		}
	}
	return current
}

func saveEveryReport(progress PoolProgress) *ProgressReport {
	switch progress.(type) {
	case BarPerItem, BarsPerItem:
		bars := make([]ProgressBar, saveEveryTestItems)
		for i := range bars {
			bars[i].Total = 1
		}
		return &ProgressReport{Total: saveEveryTestItems, Groups: []ProgressGroup{{Bars: bars}}}
	}
	return &ProgressReport{Total: saveEveryTestItems, Groups: []ProgressGroup{{Bars: []ProgressBar{{Total: saveEveryTestItems}}}}}
}

func runSaveEveryPool(t *testing.T, progress PoolProgress) *savedProgressReports {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	saved := &savedProgressReports{}
	env.OnActivity(((*ProgressActivities)(nil)).Save, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(saved.record)
	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		tracker := &ReportTracker{report: saveEveryReport(progress)}
		tracker.StartGroup(ctx, 0)
		if err := tracker.RunWorkerPool(ctx, PoolOptions{
			Total: saveEveryTestItems, Concurrency: 1,
			Progress: progress, SaveEvery: saveEveryTestBatch,
			Start: settledStarter(nil),
		}); err != nil {
			return err
		}
		tracker.CompleteGroup(ctx, 0, "done")
		return nil
	})
	require.NoError(t, env.GetWorkflowError())
	return saved
}

// TestRunWorkerPool_SaveEveryBatchesProgress checks both dispatch-free and
// dispatch-mutating modes. Group start and completion still save separately.
func TestRunWorkerPool_SaveEveryBatchesProgress(t *testing.T) {
	t.Parallel()
	ranges := make([]BarRange, saveEveryTestItems)
	for i := range ranges {
		ranges[i] = BarRange{Start: i, Count: 1}
	}
	for _, tc := range []struct {
		name     string
		progress PoolProgress
	}{
		{name: "SingleBar", progress: SingleBar{}},
		{name: "BarPerItem", progress: BarPerItem{}},
		{name: "BarsPerItem", progress: BarsPerItem{Ranges: ranges}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			saved := runSaveEveryPool(t, tc.progress)
			assert.Equal(t, []int{0, 3, 6, 9, 10, 10}, saved.currents())
		})
	}
}

// TestRunWorkerPool_SaveEveryFlushesBeforeError checks that a partial batch
// is published before the pool returns an item error. OnlyOnSuccess leaves the
// failed item's unit uncounted.
func TestRunWorkerPool_SaveEveryFlushesBeforeError(t *testing.T) {
	t.Parallel()
	const items = 5
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	saved := &savedProgressReports{}
	env.OnActivity(((*ProgressActivities)(nil)).Save, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(saved.record)
	report := &ProgressReport{Total: items, Groups: []ProgressGroup{{Bars: []ProgressBar{{Total: items}}}}}
	var poolErr error
	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		poolErr = (&ReportTracker{report: report}).RunWorkerPool(ctx, PoolOptions{
			Total: items, Concurrency: 1,
			Progress: SingleBar{}, OnlyOnSuccess: true, SaveEvery: saveEveryTestBatch,
			Start: settledStarter(func(index int) bool { return index == items-1 }),
		})
		return nil
	})
	require.NoError(t, env.GetWorkflowError())
	require.ErrorIs(t, poolErr, errSuccessRunItem)
	assert.Equal(t, []int{3, 4}, saved.currents())
}

// TestRunWorkerPool_SaveEveryQuerySeesUnsavedProgress checks that the
// progressReport query reads the live in-memory report between batch saves.
func TestRunWorkerPool_SaveEveryQuerySeesUnsavedProgress(t *testing.T) {
	t.Parallel()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	var saves atomic.Int32
	env.OnActivity(((*ProgressActivities)(nil)).Save, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(func(context.Context, string, string, []byte) error {
			saves.Add(1)
			return nil
		})
	var (
		queryReport ProgressReport
		queryErr    error
		querySaves  int32
	)
	env.RegisterDelayedCallback(func() {
		encoded, err := env.QueryWorkflow(ProgressReportQueryName)
		queryErr = err
		if queryErr == nil {
			queryErr = encoded.Get(&queryReport)
		}
		querySaves = saves.Load()
	}, 2*queryItemDuration+queryItemDuration/2)
	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		tracker, err := InitTracker(ctx, &ProgressReport{
			Total:  saveEveryTestBatch,
			Groups: []ProgressGroup{{Bars: []ProgressBar{{Total: saveEveryTestBatch}}}},
		})
		if err != nil {
			return err
		}
		return tracker.RunWorkerPool(ctx, PoolOptions{
			Total: saveEveryTestBatch, Concurrency: 1,
			Progress: SingleBar{}, SaveEvery: saveEveryTestBatch,
			Start: func(ctx workflow.Context, _ int) workflow.Future {
				return workflow.NewTimer(ctx, queryItemDuration)
			},
		})
	})
	require.NoError(t, env.GetWorkflowError())
	require.NoError(t, queryErr)
	require.Len(t, queryReport.Groups, 1)
	require.Len(t, queryReport.Groups[0].Bars, 1)
	assert.Equal(t, 2, queryReport.Groups[0].Bars[0].Current)
	assert.Zero(t, querySaves)
	assert.Equal(t, int32(1), saves.Load())
}
