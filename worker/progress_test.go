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

func TestProgressTracker_IncrementItem(t *testing.T) {
	t.Parallel()

	phases := []PhaseInfo{
		{ID: 1, Description: "Phase 1", Total: 100},
		{ID: 2, Description: "Phase 2", Total: 200},
	}
	tracker := NewProgressTrackerWithPhases(phases)

	assert.Equal(t, 0, tracker.progress.Completed)
	assert.Equal(t, 0, tracker.progress.Items[0].Completed)
	assert.Equal(t, 0, tracker.progress.Items[1].Completed)

	// Increment phase 1
	tracker.IncrementItem(1)
	assert.Equal(t, 1, tracker.progress.Completed)
	assert.Equal(t, 1, tracker.progress.Items[0].Completed)
	assert.Equal(t, 0, tracker.progress.Items[1].Completed)

	// Increment phase 2
	tracker.IncrementItem(2)
	tracker.IncrementItem(2)
	assert.Equal(t, 3, tracker.progress.Completed)
	assert.Equal(t, 1, tracker.progress.Items[0].Completed)
	assert.Equal(t, 2, tracker.progress.Items[1].Completed)

	// Increment non-existent phase (should still increment total)
	tracker.IncrementItem(999)
	assert.Equal(t, 4, tracker.progress.Completed)
}

func TestProgressTracker_IncrementItemBy(t *testing.T) {
	t.Parallel()

	phases := []PhaseInfo{
		{ID: 1, Description: "Phase 1", Total: 1000},
		{ID: 2, Description: "Phase 2", Total: 5000},
	}
	tracker := NewProgressTrackerWithPhases(phases)

	assert.Equal(t, 0, tracker.progress.Completed)
	assert.Equal(t, 6000, tracker.progress.Total)

	// Increment phase 1 by batch size (50)
	tracker.IncrementItemBy(1, 50)
	assert.Equal(t, 50, tracker.progress.Completed)
	assert.Equal(t, 50, tracker.progress.Items[0].Completed)
	assert.Equal(t, 0, tracker.progress.Items[1].Completed)

	// Increment phase 1 by another batch
	tracker.IncrementItemBy(1, 50)
	assert.Equal(t, 100, tracker.progress.Completed)
	assert.Equal(t, 100, tracker.progress.Items[0].Completed)

	// Increment phase 2 by larger batch
	tracker.IncrementItemBy(2, 500)
	assert.Equal(t, 600, tracker.progress.Completed)
	assert.Equal(t, 100, tracker.progress.Items[0].Completed)
	assert.Equal(t, 500, tracker.progress.Items[1].Completed)

	// Increment non-existent phase (should still increment total)
	tracker.IncrementItemBy(999, 25)
	assert.Equal(t, 625, tracker.progress.Completed)
}

func TestProgressTracker_IncrementItemBy_LastBatchSmaller(t *testing.T) {
	t.Parallel()

	// Simulate a scenario where total items = 182, batch size = 50
	// Last batch should be 182 - (3 * 50) = 32 items
	phases := []PhaseInfo{
		{ID: 1, Description: "Import players", Total: 182},
	}
	tracker := NewProgressTrackerWithPhases(phases)

	batchSize := 50
	totalItems := 182

	// Process 3 full batches
	for i := 0; i < 3; i++ {
		tracker.IncrementItemBy(1, batchSize)
	}
	assert.Equal(t, 150, tracker.progress.Completed)
	assert.Equal(t, 150, tracker.progress.Items[0].Completed)

	// Process last batch (smaller)
	lastBatchSize := totalItems - 3*batchSize // 32
	tracker.IncrementItemBy(1, lastBatchSize)
	assert.Equal(t, 182, tracker.progress.Completed)
	assert.Equal(t, 182, tracker.progress.Items[0].Completed)
}

func TestProgressTracker_SetItemTotal(t *testing.T) {
	t.Parallel()

	phases := []PhaseInfo{
		{ID: 1, Description: "Phase 1", Total: 0}, // Unknown initially
		{ID: 2, Description: "Phase 2", Total: 100},
	}
	tracker := NewProgressTrackerWithPhases(phases)

	assert.Equal(t, 100, tracker.progress.Total)
	assert.Equal(t, 0, tracker.progress.Items[0].Total)

	// Set phase 1 total after discovering it
	tracker.SetItemTotal(1, 9100)
	assert.Equal(t, 9200, tracker.progress.Total) // 9100 + 100
	assert.Equal(t, 9100, tracker.progress.Items[0].Total)

	// Update phase 1 total
	tracker.SetItemTotal(1, 9050)
	assert.Equal(t, 9150, tracker.progress.Total) // 9050 + 100
	assert.Equal(t, 9050, tracker.progress.Items[0].Total)
}
