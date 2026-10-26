package shared

import (
	"bytes"
	"context"
	"encoding/gob"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestFormatElapsedMilli(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		ms       int64
		expected string
	}{
		{
			name:     "zero milliseconds",
			ms:       0,
			expected: "0ms",
		},
		{
			name:     "750 milliseconds",
			ms:       750,
			expected: "750ms",
		},
		{
			name:     "5000 ms is 5 seconds",
			ms:       5000,
			expected: "5.0s",
		},
		{
			name:     "90000 ms is 1 minute 30 seconds",
			ms:       90_000,
			expected: "1m30s",
		},
		{
			name:     "3600000 ms is 1 hour",
			ms:       3_600_000,
			expected: "1h0m",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, FormatElapsedMilli(tt.ms))
		})
	}
}

func TestSetBarTotal(t *testing.T) {
	t.Parallel()

	report := &ProgressReport{
		Groups: []ProgressGroup{
			{
				Bars: []ProgressBar{
					{Label: "season 1", Total: 0},
					{Label: "season 2", Total: 0},
				},
			},
		},
	}
	tracker := &ReportTracker{report: report}

	tracker.SetBarTotal(BarRef{Group: 0}, 42)
	tracker.SetBarTotal(BarRef{Group: 0, Bar: 1}, 7)

	assert.Equal(t, 42, report.Groups[0].Bars[0].Total)
	assert.Equal(t, 7, report.Groups[0].Bars[1].Total)
}

const (
	concurrentSaveCoroutines = 2
	concurrentSaveRounds     = 3
	// concurrentSaveDuration keeps each save in flight long enough for the
	// other coroutine to start one.
	concurrentSaveDuration = 20 * time.Millisecond
)

// saveRecorder records how many progress saves overlap and the bar progress
// of each saved report, in the order the saves finished.
type saveRecorder struct {
	mu          sync.Mutex
	inFlight    int
	maxInFlight int
	saved       []int
}

func (r *saveRecorder) save(_ context.Context, _, _ string, data []byte) error {
	var report ProgressReport
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&report); err != nil {
		return err
	}
	r.mu.Lock()
	r.inFlight++
	r.maxInFlight = max(r.maxInFlight, r.inFlight)
	r.mu.Unlock()
	time.Sleep(concurrentSaveDuration)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inFlight--
	r.saved = append(r.saved, report.Groups[0].Bars[0].Current)
	return nil
}

// runConcurrentSaves advances one bar from several coroutines, each
// increment saving the report, and returns what the saves recorded.
func runConcurrentSaves(t *testing.T, serialize bool) *saveRecorder {
	t.Helper()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	recorder := &saveRecorder{}
	env.OnActivity(((*ProgressActivities)(nil)).Save, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(recorder.save)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		total := concurrentSaveCoroutines * concurrentSaveRounds
		tracker, err := InitTracker(ctx, &ProgressReport{Groups: []ProgressGroup{{Bars: []ProgressBar{{Total: total}}}}})
		if err != nil {
			return err
		}
		if serialize {
			tracker.SerializeSaves(ctx)
		}
		wg := workflow.NewWaitGroup(ctx)
		for range concurrentSaveCoroutines {
			wg.Add(1)
			workflow.Go(ctx, func(ctx workflow.Context) {
				defer wg.Done()
				for range concurrentSaveRounds {
					tracker.IncrementBar(ctx, BarRef{Group: 0})
				}
			})
		}
		wg.Wait(ctx)
		return nil
	})
	require.NoError(t, env.GetWorkflowError())
	return recorder
}

// Without SerializeSaves, two coroutines' saves overlap, so an older report
// can reach Redis last. With it, one save runs at a time, each saved report
// is at least as far along as the one before, and the last is the final one.
func TestSerializeSaves_OneSaveAtATimeInOrder(t *testing.T) {
	t.Parallel()

	unserialized := runConcurrentSaves(t, false)
	assert.Greater(t, unserialized.maxInFlight, 1, "control: saves from two coroutines overlap")

	serialized := runConcurrentSaves(t, true)
	assert.Equal(t, 1, serialized.maxInFlight)
	require.Len(t, serialized.saved, concurrentSaveCoroutines*concurrentSaveRounds)
	assert.IsNonDecreasing(t, serialized.saved)
	assert.Equal(t, concurrentSaveCoroutines*concurrentSaveRounds, serialized.saved[len(serialized.saved)-1])
}

// A Save whose context is canceled while another Save holds the lock goes
// ahead without it: it must neither unlock a mutex it never held (a panic)
// nor wait for the holder to finish.
func TestSerializeSaves_CanceledWaiterSavesWithoutLock(t *testing.T) {
	t.Parallel()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	recorder := &saveRecorder{}
	env.OnActivity(((*ProgressActivities)(nil)).Save, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(recorder.save)
	var holderStillSaving bool

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		tracker, err := InitTracker(ctx, &ProgressReport{Groups: []ProgressGroup{{Bars: []ProgressBar{{Total: 1}}}}})
		if err != nil {
			return err
		}
		tracker.SerializeSaves(ctx)
		holderDone := workflow.NewWaitGroup(ctx)
		holderDone.Add(1)
		workflow.Go(ctx, func(ctx workflow.Context) {
			defer holderDone.Done()
			tracker.IncrementBar(ctx, BarRef{Group: 0})
		})
		if err := workflow.Await(ctx, tracker.saving.IsLocked); err != nil {
			return err
		}
		waiterCtx, cancel := workflow.WithCancel(ctx)
		cancel()
		tracker.Save(waiterCtx)
		holderStillSaving = tracker.saving.IsLocked()
		holderDone.Wait(ctx)
		return nil
	})

	require.NoError(t, env.GetWorkflowError())
	assert.True(t, holderStillSaving, "the canceled Save returned while the holder still held the lock")
	assert.Contains(t, recorder.saved, 1, "the holder's save completed")
}
