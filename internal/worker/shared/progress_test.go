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

func TestFormatDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		d        time.Duration
		expected string
	}{
		{
			name:     "zero duration shows milliseconds",
			d:        0,
			expected: "0ms",
		},
		{
			name:     "500 ms shows milliseconds",
			d:        500 * time.Millisecond,
			expected: "500ms",
		},
		{
			name:     "999 ms stays in milliseconds (below 1 second)",
			d:        999 * time.Millisecond,
			expected: "999ms",
		},
		{
			name:     "exactly 1 second shows seconds",
			d:        time.Second,
			expected: "1.0s",
		},
		{
			name:     "3.5 seconds shows one decimal",
			d:        3500 * time.Millisecond,
			expected: "3.5s",
		},
		{
			name:     "59 seconds stays in seconds (below 1 minute)",
			d:        59 * time.Second,
			expected: "59.0s",
		},
		{
			name:     "exactly 1 minute shows Xm Ys format",
			d:        time.Minute,
			expected: "1m0s",
		},
		{
			name:     "1 minute 30 seconds shows Xm Ys format",
			d:        90 * time.Second,
			expected: "1m30s",
		},
		{
			name:     "59 minutes 59 seconds stays in minutes format",
			d:        59*time.Minute + 59*time.Second,
			expected: "59m59s",
		},
		{
			name:     "exactly 1 hour shows XhYm format",
			d:        time.Hour,
			expected: "1h0m",
		},
		{
			name:     "1 hour 30 minutes shows XhYm format",
			d:        90 * time.Minute,
			expected: "1h30m",
		},
		{
			name:     "2 hours 5 minutes shows XhYm format",
			d:        2*time.Hour + 5*time.Minute,
			expected: "2h5m",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, FormatDuration(tt.d))
		})
	}
}

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

	tracker.SetBarTotal(0, 0, 42)
	tracker.SetBarTotal(0, 1, 7)

	assert.Equal(t, 42, report.Groups[0].Bars[0].Total)
	assert.Equal(t, 7, report.Groups[0].Bars[1].Total)
}

func TestSetBarLabel(t *testing.T) {
	t.Parallel()

	report := &ProgressReport{
		Groups: []ProgressGroup{
			{
				Bars: []ProgressBar{
					{Label: "original", Total: 10},
				},
			},
		},
	}
	tracker := &ReportTracker{report: report}

	tracker.SetBarLabel(0, 0, "updated label")

	assert.Equal(t, "updated label", report.Groups[0].Bars[0].Label)
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
					tracker.IncrementBar(ctx, 0, 0)
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
