package shared

import (
	"errors"
	"fmt"
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
			assert.Equal(t, tt.expected, BatchSlice(tt.items, tt.batchIndex, tt.batchSize))
		})
	}
}

// mockBatch is a minimal implementation of the interface required by ExecBatch.
type mockBatch struct {
	rowCount int
	errOnRow int   // index at which to inject an error; -1 means no error
	injected error // the error to inject
}

func (m *mockBatch) Exec(fn func(int, error)) {
	for i := range m.rowCount {
		var err error
		if i == m.errOnRow {
			err = m.injected
		}
		fn(i, err)
	}
}

func TestExecBatch(t *testing.T) {
	t.Parallel()

	t.Run("no errors returns nil", func(t *testing.T) {
		t.Parallel()
		b := &mockBatch{rowCount: 3, errOnRow: -1}
		err := ExecBatch(b, func(i int) string {
			return fmt.Sprintf("row %d", i)
		})
		assert.NoError(t, err)
	})

	t.Run("error on row 2 returns formatted error for that row", func(t *testing.T) {
		t.Parallel()
		sentinel := errors.New("db constraint violation")
		b := &mockBatch{rowCount: 5, errOnRow: 2, injected: sentinel}
		err := ExecBatch(b, func(i int) string {
			return fmt.Sprintf("insert game %d", i)
		})
		require.Error(t, err)
		assert.ErrorIs(t, err, sentinel)
		assert.Contains(t, err.Error(), "insert game 2")
	})

	t.Run("only first error is returned when multiple rows fail", func(t *testing.T) {
		t.Parallel()
		// Use a batch that reports an error on every row.
		injected := errors.New("always fails")
		b := &mockBatch{rowCount: 4, errOnRow: 0, injected: injected}
		err := ExecBatch(b, func(i int) string {
			return fmt.Sprintf("row %d", i)
		})
		require.Error(t, err)
		assert.ErrorIs(t, err, injected)
		// Error message should reference the first failing row (index 0).
		assert.Contains(t, err.Error(), "row 0")
	})

	t.Run("empty batch returns nil", func(t *testing.T) {
		t.Parallel()
		b := &mockBatch{rowCount: 0, errOnRow: -1}
		err := ExecBatch(b, func(i int) string {
			return fmt.Sprintf("row %d", i)
		})
		assert.NoError(t, err)
	})
}
