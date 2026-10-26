package shared

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockBatch is a minimal implementation of the interface required by ExecBatch.
type mockBatch struct {
	rowCount  int
	errOnRows []int // indices at which to inject errors
	injected  error // the error to inject
	rowErrors map[int]error
}

func (m *mockBatch) Exec(fn func(int, error)) {
	for i := range m.rowCount {
		err := m.rowErrors[i]
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

	t.Run("batch close follow-ons do not obscure the primary error", func(t *testing.T) {
		t.Parallel()
		primary := errors.New("duplicate key")
		b := &mockBatch{
			rowCount: 3,
			rowErrors: map[int]error{
				0: primary,
				1: errors.New(batchAlreadyClosedMessage),
				2: errors.New(batchAlreadyClosedMessage),
			},
		}

		err := ExecBatch(b, func(i int) string { return fmt.Sprintf("row %d", i) })

		require.Error(t, err)
		assert.ErrorIs(t, err, primary)
		assert.Equal(t, "row 0: duplicate key", err.Error())
		var batchErr *BatchError
		require.ErrorAs(t, err, &batchErr)
		assert.Equal(t, []int{0}, batchErr.Indices)
		assert.Len(t, batchErr.Errors, 1)
	})
}

func TestBatchError(t *testing.T) {
	t.Parallel()

	t.Run("zero value is safe", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, emptyBatchErrorMessage, (&BatchError{}).Error())
		assert.Equal(t, emptyBatchErrorMessage, (*BatchError)(nil).Error())
		assert.Empty(t, (*BatchError)(nil).Unwrap())
	})

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
