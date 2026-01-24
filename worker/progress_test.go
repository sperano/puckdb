package worker

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewProgressTracker(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		total int
	}{
		{"zero total", 0},
		{"small total", 10},
		{"large total", 1000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracker := NewProgressTracker(tt.total)
			assert.NotNil(t, tracker)
			assert.Equal(t, tt.total, tracker.progress.Total)
			assert.Equal(t, 0, tracker.progress.Completed)
		})
	}
}

func TestNewProgressTrackerWithOffset(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		batchSize  int
		offset     int
		grandTotal int
	}{
		{"first batch", 100, 0, 500},
		{"middle batch", 100, 200, 500},
		{"last batch", 100, 400, 500},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracker := NewProgressTrackerWithOffset(tt.batchSize, tt.offset, tt.grandTotal)
			assert.NotNil(t, tracker)
			assert.Equal(t, tt.grandTotal, tracker.progress.Total)
			assert.Equal(t, tt.offset, tracker.progress.Completed)
		})
	}
}

func TestProgressTracker_Increment(t *testing.T) {
	t.Parallel()

	tracker := NewProgressTracker(10)
	assert.Equal(t, 0, tracker.progress.Completed)

	tracker.Increment()
	assert.Equal(t, 1, tracker.progress.Completed)

	tracker.Increment()
	tracker.Increment()
	assert.Equal(t, 3, tracker.progress.Completed)
}
