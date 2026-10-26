package shared

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBatchCount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		n         int
		batchSize int
		expected  int
	}{
		{name: "zero items", n: 0, batchSize: 10, expected: 0},
		{name: "one item", n: 1, batchSize: 10, expected: 1},
		{name: "exactly one full batch", n: 10, batchSize: 10, expected: 1},
		{name: "one item over one batch", n: 11, batchSize: 10, expected: 2},
		{name: "exactly ten full batches", n: 100, batchSize: 10, expected: 10},
		{name: "non-divisible batch size", n: 25, batchSize: 7, expected: 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, BatchCount(tt.n, tt.batchSize))
		})
	}
}

// TestBatches checks that the batch count, the batch data and the per-batch
// progress increments agree for empty, exact and partial final batches: the
// batches cover the items once, in order, and the increments sum to the item
// count (no last-batch overcount).
func TestBatches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		n       int
		size    int
		wantLen []int
	}{
		{name: "empty", n: 0, size: 3, wantLen: nil},
		{name: "exact final batch", n: 6, size: 3, wantLen: []int{3, 3}},
		{name: "partial final batch", n: 7, size: 3, wantLen: []int{3, 3, 1}},
		{name: "fewer items than one batch", n: 2, size: 3, wantLen: []int{2}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			items := make([]int, tt.n)
			for i := range items {
				items[i] = i
			}
			b := NewBatches(items, tt.size)
			require.Equal(t, len(tt.wantLen), b.Count())

			var processed []int
			var gotLen []int
			reported := 0
			for i := range b.Count() {
				batch := b.Batch(i)
				processed = append(processed, batch...)
				gotLen = append(gotLen, len(batch))
				reported += b.ItemProgress(BarRef{}).Increment(i)
				assert.Equal(t, len(batch), b.Len(i))
			}
			assert.Equal(t, tt.wantLen, gotLen)
			assert.Equal(t, tt.n, reported)
			if tt.n == 0 {
				assert.Empty(t, processed)
			} else {
				assert.Equal(t, items, processed)
			}
		})
	}
}

func TestBatchRangesBounds(t *testing.T) {
	t.Parallel()

	r := NewBatchRanges(7, 3)
	var got [][2]int
	for i := range r.Count() {
		start, end := r.Bounds(i)
		got = append(got, [2]int{start, end})
	}
	assert.Equal(t, [][2]int{{0, 3}, {3, 6}, {6, 7}}, got)
}

func TestBatchAppendDoesNotOverwriteNextBatch(t *testing.T) {
	t.Parallel()

	b := NewBatches([]int{1, 2, 3, 4}, 2)
	first := b.Batch(0)
	_ = append(first, 99)
	assert.Equal(t, []int{3, 4}, b.Batch(1))
}

func TestBatchRangesPanics(t *testing.T) {
	t.Parallel()

	assert.PanicsWithValue(t, "batch ranges: size 0 must be positive", func() { NewBatchRanges(1, 0) })
	assert.PanicsWithValue(t, "batch ranges: item count -1 must not be negative", func() { NewBatchRanges(-1, 2) })
	r := NewBatchRanges(4, 2)
	assert.PanicsWithValue(t, "batch ranges: batch 2 outside [0, 2)", func() { r.Bounds(2) })
	assert.PanicsWithValue(t, "batch ranges: batch -1 outside [0, 2)", func() { r.Bounds(-1) })
}
