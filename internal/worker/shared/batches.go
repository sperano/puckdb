package shared

import "fmt"

// BatchCount returns the number of batches needed to process n items with the given batch size.
func BatchCount(n, batchSize int) int {
	return (n + batchSize - 1) / batchSize
}

// BatchRanges partitions the indexes [0, n) into consecutive batches of at
// most a fixed size; only the last batch may be smaller. It is the one place
// batch counts, batch bounds and per-batch progress increments come from, so
// a pool's Total, the data each item processes and what each item reports
// cannot disagree. Use it directly when the items are not a slice (an ID
// range); Batches wraps it for a slice.
type BatchRanges struct {
	n    int
	size int
}

// NewBatchRanges partitions n items into batches of at most size. It panics
// when size is not positive or n is negative.
func NewBatchRanges(n, size int) BatchRanges {
	if size <= 0 {
		panic(fmt.Sprintf("batch ranges: size %d must be positive", size))
	}
	if n < 0 {
		panic(fmt.Sprintf("batch ranges: item count %d must not be negative", n))
	}
	return BatchRanges{n: n, size: size}
}

// Count returns the number of batches: zero for no items.
func (r BatchRanges) Count() int {
	return BatchCount(r.n, r.size)
}

// Bounds returns the item indexes [start, end) of batch i. It panics when i
// is outside [0, Count()).
func (r BatchRanges) Bounds(i int) (start, end int) {
	if i < 0 || i >= r.Count() {
		panic(fmt.Sprintf("batch ranges: batch %d outside [0, %d)", i, r.Count()))
	}
	start = i * r.size
	return start, min(start+r.size, r.n)
}

// Len returns the number of items in batch i; it is an IncrementFunc.
func (r BatchRanges) Len(i int) int {
	start, end := r.Bounds(i)
	return end - start
}

// ItemProgress advances bar by the number of items in each completed batch,
// so a bar sized in items ends at exactly the item count.
func (r BatchRanges) ItemProgress(bar BarRef) SingleBar {
	return SingleBar{Bar: bar, Increment: r.Len}
}

// Batches partitions a slice into consecutive batches of at most a fixed
// size (see BatchRanges, whose methods it has).
type Batches[T any] struct {
	BatchRanges
	items []T
}

// NewBatches partitions items into batches of at most size. It panics when
// size is not positive.
func NewBatches[T any](items []T, size int) Batches[T] {
	return Batches[T]{BatchRanges: NewBatchRanges(len(items), size), items: items}
}

// Batch returns the items of batch i. Its capacity ends with the batch, so
// appending to it never overwrites the next batch.
func (b Batches[T]) Batch(i int) []T {
	start, end := b.Bounds(i)
	return b.items[start:end:end]
}
