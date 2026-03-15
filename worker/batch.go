package worker

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
