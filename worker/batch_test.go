package worker

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
			assert.Equal(t, tt.expected, batchCount(tt.n, tt.batchSize))
		})
	}
}

func TestBatchSlice(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		items      []int
		batchIndex int
		batchSize  int
		expected   []int
	}{
		{
			name:       "first batch of five with size two",
			items:      []int{1, 2, 3, 4, 5},
			batchIndex: 0,
			batchSize:  2,
			expected:   []int{1, 2},
		},
		{
			name:       "second batch of five with size two",
			items:      []int{1, 2, 3, 4, 5},
			batchIndex: 1,
			batchSize:  2,
			expected:   []int{3, 4},
		},
		{
			name:       "third batch partial remainder",
			items:      []int{1, 2, 3, 4, 5},
			batchIndex: 2,
			batchSize:  2,
			expected:   []int{5},
		},
		{
			name:       "single item slice",
			items:      []int{1},
			batchIndex: 0,
			batchSize:  10,
			expected:   []int{1},
		},
		{
			name:       "full batch exactly fitting",
			items:      []int{1, 2, 3},
			batchIndex: 0,
			batchSize:  3,
			expected:   []int{1, 2, 3},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, batchSlice(tt.items, tt.batchIndex, tt.batchSize))
		})
	}
}
