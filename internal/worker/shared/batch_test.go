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
	rowCount  int
	errOnRows []int // indices at which to inject errors
	injected  error // the error to inject
}

func (m *mockBatch) Exec(fn func(int, error)) {
	for i := range m.rowCount {
		var err error
		for _, errIdx := range m.errOnRows {
			if i == errIdx {
				err = m.injected
				break
			}
		}
		fn(i, err)
	}
}

func TestExecBatch(t *testing.T) {
	t.Parallel()

	t.Run("no errors returns nil", func(t *testing.T) {
		t.Parallel()
		b := &mockBatch{rowCount: 3, errOnRows: nil}
		err := ExecBatch(b, func(i int) string {
			return fmt.Sprintf("row %d", i)
		})
		assert.NoError(t, err)
	})

	t.Run("single error returns formatted error for that row", func(t *testing.T) {
		t.Parallel()
		sentinel := errors.New("db constraint violation")
		b := &mockBatch{rowCount: 5, errOnRows: []int{2}, injected: sentinel}
		err := ExecBatch(b, func(i int) string {
			return fmt.Sprintf("insert game %d", i)
		})
		require.Error(t, err)
		assert.ErrorIs(t, err, sentinel)
		assert.Contains(t, err.Error(), "insert game 2")
	})

	t.Run("multiple errors are all captured", func(t *testing.T) {
		t.Parallel()
		injected := errors.New("constraint violation")
		b := &mockBatch{rowCount: 5, errOnRows: []int{1, 3, 4}, injected: injected}
		err := ExecBatch(b, func(i int) string {
			return fmt.Sprintf("row %d", i)
		})
		require.Error(t, err)

		// Should be a BatchError with all 3 failures
		var batchErr *BatchError
		require.ErrorAs(t, err, &batchErr)
		assert.Len(t, batchErr.Errors, 3)
		assert.Equal(t, []int{1, 3, 4}, batchErr.Indices)

		// Error message shows first error + count
		assert.Contains(t, err.Error(), "row 1")
		assert.Contains(t, err.Error(), "and 2 more errors")

		// errors.Is works on any constituent error
		assert.ErrorIs(t, err, injected)
	})

	t.Run("empty batch returns nil", func(t *testing.T) {
		t.Parallel()
		b := &mockBatch{rowCount: 0, errOnRows: nil}
		err := ExecBatch(b, func(i int) string {
			return fmt.Sprintf("row %d", i)
		})
		assert.NoError(t, err)
	})
}

func TestBatchError(t *testing.T) {
	t.Parallel()

	t.Run("single error shows full message", func(t *testing.T) {
		t.Parallel()
		be := &BatchError{
			Errors:  []error{errors.New("row 5: duplicate key")},
			Indices: []int{5},
		}
		assert.Equal(t, "row 5: duplicate key", be.Error())
	})

	t.Run("multiple errors show count", func(t *testing.T) {
		t.Parallel()
		be := &BatchError{
			Errors: []error{
				errors.New("row 1: failed"),
				errors.New("row 3: failed"),
				errors.New("row 7: failed"),
			},
			Indices: []int{1, 3, 7},
		}
		assert.Equal(t, "row 1: failed (and 2 more errors)", be.Error())
	})

	t.Run("Unwrap returns all errors", func(t *testing.T) {
		t.Parallel()
		err1 := errors.New("first")
		err2 := errors.New("second")
		be := &BatchError{Errors: []error{err1, err2}}
		unwrapped := be.Unwrap()
		assert.Len(t, unwrapped, 2)
		assert.Equal(t, err1, unwrapped[0])
		assert.Equal(t, err2, unwrapped[1])
	})
}
