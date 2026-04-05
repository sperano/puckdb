package worker

import "fmt"

// batchCount returns the number of batches needed to process n items with the given batch size.
func batchCount(n, batchSize int) int {
	return (n + batchSize - 1) / batchSize
}

// batchSlice returns the sub-slice for the given batch index and size, clamping to the slice length.
func batchSlice[T any](items []T, batchIndex, batchSize int) []T {
	start := batchIndex * batchSize
	end := start + batchSize
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}

// execBatch runs a sqlc batch operation and returns the first error encountered.
// errContext is called with the failing row index to produce a descriptive prefix
// for the error message. The caller's closure captures params for rich context.
func execBatch(br interface{ Exec(func(int, error)) }, errContext func(i int) string) error {
	var firstErr error
	br.Exec(func(i int, err error) {
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("%s: %w", errContext(i), err)
		}
	})
	return firstErr
}
