package shared

import "fmt"

// BatchCount returns the number of batches needed to process n items with the given batch size.
func BatchCount(n, batchSize int) int {
	return (n + batchSize - 1) / batchSize
}

// BatchSlice returns the sub-slice for the given batch index and size, clamping to the slice length.
func BatchSlice[T any](items []T, batchIndex, batchSize int) []T {
	start := batchIndex * batchSize
	end := start + batchSize
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}

// BatchError aggregates multiple errors from a batch operation.
// It implements error and provides Unwrap() []error for Go 1.20+ error inspection.
//
// Invariant: Errors must contain at least one error. An empty BatchError is a
// programming bug — callers never need to construct one directly; ExecBatch
// returns nil on success and allocates a populated *BatchError on failure.
type BatchError struct {
	Errors  []error // All errors encountered (length ≥ 1)
	Indices []int   // Row indices that failed
}

// Error returns a summary message. For a single failure, shows the full error.
// For multiple failures, shows the first error plus a count.
// Panics if Errors is empty — see the invariant on BatchError.
func (be *BatchError) Error() string {
	if len(be.Errors) == 0 {
		panic("shared: BatchError.Error called with no errors (invariant violation)")
	}
	if len(be.Errors) == 1 {
		return be.Errors[0].Error()
	}
	return fmt.Sprintf("%s (and %d more errors)", be.Errors[0].Error(), len(be.Errors)-1)
}

// Unwrap returns all errors for use with errors.Is/As on any constituent error.
func (be *BatchError) Unwrap() []error {
	return be.Errors
}

// Execer abstracts sqlc's generated *FooBatchResults types so ExecBatch is
// decoupled from any specific batch type. Every sqlc batch result satisfies it
// implicitly via its Exec(func(int, error)) method.
type Execer interface {
	Exec(func(int, error))
}

// ExecBatch runs a sqlc batch operation and returns all errors encountered.
// errContext is called with the failing row index to produce a descriptive prefix
// for the error message. The caller's closure captures params for rich context.
// Returns nil if no errors, or a *BatchError containing all failures.
func ExecBatch(br Execer, errContext func(i int) string) error {
	var batchErr *BatchError
	br.Exec(func(i int, err error) {
		if err != nil {
			if batchErr == nil {
				batchErr = &BatchError{}
			}
			batchErr.Errors = append(batchErr.Errors, fmt.Errorf("%s: %w", errContext(i), err))
			batchErr.Indices = append(batchErr.Indices, i)
		}
	})
	if batchErr != nil {
		return batchErr
	}
	return nil
}
