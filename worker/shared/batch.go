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

// ExecBatch runs a sqlc batch operation and returns the first error encountered.
// errContext is called with the failing row index to produce a descriptive prefix
// for the error message. The caller's closure captures params for rich context.
func ExecBatch(br interface{ Exec(func(int, error)) }, errContext func(i int) string) error {
	var firstErr error
	br.Exec(func(i int, err error) {
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("%s: %w", errContext(i), err)
		}
	})
	return firstErr
}
