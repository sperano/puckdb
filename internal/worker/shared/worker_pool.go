package shared

import (
	"fmt"

	"go.temporal.io/sdk/workflow"
)

// IncrementFunc returns the progress increment for a given work item index.
type IncrementFunc func(index int) int

// ActivityStarter is a function that starts an activity for a given index and returns a future.
type ActivityStarter func(ctx workflow.Context, index int) workflow.Future

// ResultHandler is called for each completed activity with its index.
// The handler receives the future to extract the result.
type ResultHandler func(ctx workflow.Context, index int, future workflow.Future) error

// workerPoolHooks are the progress-tracking callbacks the scheduler invokes.
// onStart runs immediately before an item is dispatched; onComplete runs after
// the item's result handler. Either may be nil. Both run on the workflow
// goroutine, so any side effects they emit (Save local activities, etc.) are
// recorded in deterministic order.
type workerPoolHooks struct {
	onStart    func(ctx workflow.Context, index int)
	onComplete func(ctx workflow.Context, index int)
}

// RunWorkerPool runs activities concurrently with a fixed concurrency limit.
// All activities update a SINGLE bar at barIdx, incrementing by 1 on each completion.
// Use RunWorkerPoolMultiBar if each activity should have its own bar.
func (t *ReportTracker) RunWorkerPool(ctx workflow.Context, groupIdx, barIdx, total, concurrency int, startActivity ActivityStarter, handler ResultHandler) error {
	return t.RunWorkerPoolWithIncrement(ctx, groupIdx, barIdx, total, concurrency, func(_ int) int { return 1 }, startActivity, handler)
}

// RunWorkerPoolWithIncrement is like RunWorkerPool but uses incrementFunc to determine
// how much to advance the progress bar for each completed work item.
func (t *ReportTracker) RunWorkerPoolWithIncrement(ctx workflow.Context, groupIdx, barIdx, total, concurrency int, incrementFunc IncrementFunc, startActivity ActivityStarter, handler ResultHandler) error {
	return runWorkerPool(ctx, total, concurrency, startActivity, handler, workerPoolHooks{
		onComplete: func(ctx workflow.Context, index int) {
			t.IncrementBarBy(ctx, groupIdx, barIdx, incrementFunc(index))
		},
	})
}

// RunWorkerPoolMultiBar runs activities concurrently where each activity has its own bar.
// Bar[barStart + i] corresponds to activity i. Marks bars as Started when dispatched
// and sets them complete on activity completion.
func (t *ReportTracker) RunWorkerPoolMultiBar(ctx workflow.Context, groupIdx, barStart, total, concurrency int, startActivity ActivityStarter, handler ResultHandler) error {
	return runWorkerPool(ctx, total, concurrency, startActivity, handler, workerPoolHooks{
		onStart: func(ctx workflow.Context, index int) {
			t.StartBar(ctx, groupIdx, barStart+index)
		},
		onComplete: func(ctx workflow.Context, index int) {
			t.CompleteBar(ctx, groupIdx, barStart+index)
		},
	})
}

// runWorkerPool is the single scheduler behind the RunWorkerPool* variants.
// It dispatches work items [0, total) through startActivity with at most
// concurrency futures in flight, invoking hooks around each item's lifecycle.
//
// The first error observed (from handler, or from the future itself when
// handler is nil) stops refilling and is returned once observed at a decision
// point; in-flight futures are abandoned.
//
// Deliberately one function despite its length: the dispatch/selector/
// completion sequence is replay-determinism-critical, and keeping it in a
// single block makes the command ordering auditable at a glance.
func runWorkerPool(ctx workflow.Context, total, concurrency int, startActivity ActivityStarter, handler ResultHandler, hooks workerPoolHooks) error {
	if total == 0 {
		return nil
	}
	if concurrency <= 0 {
		return fmt.Errorf("worker pool: concurrency must be positive, got %d (total %d)", concurrency, total)
	}

	nextIndex := 0
	// activeIndexes holds the indexes of in-flight futures in ascending order:
	// dispatch appends the monotonically increasing nextIndex, and completion
	// removes in place. Bookkeeping therefore costs O(concurrency) per
	// completion, independent of how many items have already finished.
	activeIndexes := make([]int, 0, concurrency)
	futures := make(map[int]workflow.Future, concurrency)
	// firstErr captures the first error across all completions. Safe without
	// synchronization: workflow.Selector invokes its callbacks cooperatively
	// (one at a time on the workflow goroutine), so reads/writes never race.
	var firstErr error

	dispatch := func() {
		for len(activeIndexes) < concurrency && nextIndex < total {
			idx := nextIndex
			if hooks.onStart != nil {
				hooks.onStart(ctx, idx)
			}
			futures[idx] = startActivity(ctx, idx)
			activeIndexes = append(activeIndexes, idx)
			nextIndex++
		}
	}

	dispatch()
	for len(activeIndexes) > 0 {
		selector := workflow.NewSelector(ctx)

		// Register futures in ascending index order. Selector.Select fires the
		// first-registered ready branch, so a nondeterministic registration
		// order (e.g. ranging over the futures map) would make the
		// side-effecting hook/Save commands and the firstErr choice
		// non-deterministic across replay.
		for _, idx := range activeIndexes {
			capturedIdx := idx
			selector.AddFuture(futures[idx], func(f workflow.Future) {
				if handler != nil {
					if err := handler(ctx, capturedIdx, f); err != nil && firstErr == nil {
						firstErr = err
					}
				} else if err := f.Get(ctx, nil); err != nil && firstErr == nil {
					firstErr = err
				}
				if hooks.onComplete != nil {
					hooks.onComplete(ctx, capturedIdx)
				}
				delete(futures, capturedIdx)
				activeIndexes = removeActiveIndex(activeIndexes, capturedIdx)
			})
		}

		selector.Select(ctx)
		if firstErr != nil {
			return firstErr
		}

		dispatch()
	}

	return nil
}

// removeActiveIndex removes idx from the sorted slice, preserving order.
func removeActiveIndex(indexes []int, idx int) []int {
	for i, v := range indexes {
		if v == idx {
			return append(indexes[:i], indexes[i+1:]...)
		}
	}
	return indexes
}
