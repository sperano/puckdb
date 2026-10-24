package shared

import (
	"errors"
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

// workerPoolHooks are the callbacks the scheduler invokes.
// onStart runs immediately before an item is dispatched; onComplete runs after
// the item's result handler. afterComplete runs after those two hooks for every
// observed completion, including a failed one. stopDispatch, checked before
// each dispatch, ends dispatching for good once it reports true (it is not
// called again); items already in flight still complete.
// Any may be nil. All run on the workflow goroutine, so any side effects they
// emit (Save local activities, etc.) are recorded in deterministic order.
type workerPoolHooks struct {
	onStart       func(ctx workflow.Context, index int)
	onComplete    func(ctx workflow.Context, index int)
	afterComplete func(ctx workflow.Context)
	stopDispatch  func() bool
}

// stopped reports whether stopDispatch asks to dispatch no further item.
func (h workerPoolHooks) stopped() bool {
	return h.stopDispatch != nil && h.stopDispatch()
}

// PoolOptions configures one pool, run by RunWorkerPool or, with progress,
// by (*ReportTracker).RunWorkerPool.
type PoolOptions struct {
	// Total is the number of work items, run as indexes [0, Total).
	Total int
	// Concurrency is the most items in flight at once.
	Concurrency int
	// Start starts item i and returns its future.
	Start ActivityStarter
	// Handle receives each completed item's future; nil means future.Get(ctx, nil).
	Handle ResultHandler
	// Progress is how the items advance the tracker's report; nil means no
	// progress. Only (*ReportTracker).RunWorkerPool accepts a non-nil one.
	Progress PoolProgress
	// OnlyOnSuccess advances Progress only after Handle (or future.Get) succeeded;
	// a failed item's bar stays as it was. Default false: advance regardless.
	// With a nil Progress it is accepted and does nothing.
	OnlyOnSuccess bool
	// SaveEvery batches progress persistence while keeping every in-memory
	// mutation. Values 0 and 1 save on every progress hook. A value greater
	// than 1 saves after every SaveEvery-th completion, after the final
	// completion, and before returning an item error; dispatch hooks wait for
	// the next batched save. The progressReport Temporal query registered by
	// ReportTracker.RegisterQueryHandler reads the in-memory report and remains
	// current. The Redis copy used by GraphQL *Progress fields may lag by at
	// most SaveEvery-1 completed items. A negative value is invalid.
	SaveEvery int
	// StopDispatch, checked before every dispatch, ends dispatching once it
	// returns true; in-flight items still complete and reach Handle. The first
	// true is final: StopDispatch is not called again.
	StopDispatch func() bool
}

// RunWorkerPool runs a pool that reports no progress. opts.Progress must be
// nil; a pool with progress runs on its tracker through
// (*ReportTracker).RunWorkerPool. The Progress check comes first, so even a
// zero Total with a Progress is an error. Otherwise it behaves like the
// method: opts is validated before any command, the first item error stops
// dispatching and is returned, and a zero Total returns nil.
func RunWorkerPool(ctx workflow.Context, opts PoolOptions) error {
	if opts.Progress != nil {
		return fmt.Errorf("worker pool: progress %T without a tracker; use (*ReportTracker).RunWorkerPool for progress", opts.Progress)
	}
	return runPoolOn(ctx, nil, opts)
}

// RunWorkerPool runs opts.Total work items with at most opts.Concurrency in
// flight, reporting them on the tracker as opts.Progress says. It validates
// opts before issuing any command, so a rejected call records no activity and
// no Save; the first item error stops dispatching and is returned. A zero
// Total returns nil without validating its other options. A pool without
// progress needs no tracker: use the package-level RunWorkerPool.
func (t *ReportTracker) RunWorkerPool(ctx workflow.Context, opts PoolOptions) error {
	return runPoolOn(ctx, t, opts)
}

// runPoolOn validates opts, resolves opts.Progress against t (which may be nil
// when Progress is) and runs the items on the scheduler. Every entry point
// goes through it, so the validation order is the same for all of them.
func runPoolOn(ctx workflow.Context, t *ReportTracker, opts PoolOptions) error {
	if opts.Total == 0 {
		return nil
	}
	if opts.SaveEvery < 0 {
		return errors.New("worker pool: SaveEvery must not be negative")
	}
	if err := validatePoolOptions(opts); err != nil {
		return err
	}
	batched := opts.Progress != nil && opts.SaveEvery > 1
	hooks, err := t.progressHooks(opts.Progress, opts.Total, !batched)
	if err != nil {
		return err
	}
	hooks.stopDispatch = opts.StopDispatch
	handle := opts.Handle
	if opts.OnlyOnSuccess {
		handle, hooks = completeOnlyOnSuccess(handle, hooks)
	}
	var saver *poolProgressSaver
	if batched {
		saver = &poolProgressSaver{tracker: t, every: opts.SaveEvery}
		hooks.afterComplete = saver.onCompletion
	}
	err = runWorkerPool(ctx, opts.Total, opts.Concurrency, opts.Start, handle, hooks)
	if saver != nil {
		saver.finish(ctx)
	}
	return err
}

// poolProgressSaver publishes mutation-only progress hooks at completion
// boundaries. lastSaved avoids a duplicate save when the final or failing
// completion is itself an exact batch boundary.
type poolProgressSaver struct {
	tracker   *ReportTracker
	every     int
	completed int
	lastSaved int
}

func (s *poolProgressSaver) onCompletion(ctx workflow.Context) {
	s.completed++
	if s.completed%s.every == 0 {
		s.save(ctx)
	}
}

func (s *poolProgressSaver) finish(ctx workflow.Context) {
	if s.completed > s.lastSaved {
		s.save(ctx)
	}
}

func (s *poolProgressSaver) save(ctx workflow.Context) {
	s.tracker.Save(ctx)
	s.lastSaved = s.completed
}

// validatePoolOptions checks the fields of opts other than Progress.
func validatePoolOptions(opts PoolOptions) error {
	switch {
	case opts.Total < 0:
		return errors.New("worker pool: total must not be negative")
	case opts.Concurrency <= 0:
		return concurrencyError(opts.Total, opts.Concurrency)
	case opts.Start == nil:
		return errors.New("worker pool: Start must not be nil")
	}
	return nil
}

// completeOnlyOnSuccess moves hooks.onComplete into the result handler: it
// runs after handle (or future.Get(ctx, nil) when handle is nil), and only
// when that succeeded. The command order is the scheduler's own (handler,
// then completion), minus the completion of a failed item. Hooks without an
// onComplete come back unchanged.
func completeOnlyOnSuccess(handle ResultHandler, hooks workerPoolHooks) (ResultHandler, workerPoolHooks) {
	onComplete := hooks.onComplete
	if onComplete == nil {
		return handle, hooks
	}
	hooks.onComplete = nil
	return func(ctx workflow.Context, index int, future workflow.Future) error {
		if handle != nil {
			if err := handle(ctx, index, future); err != nil {
				return err
			}
		} else if err := future.Get(ctx, nil); err != nil {
			return err
		}
		onComplete(ctx, index)
		return nil
	}, hooks
}

// progressHooks validates progress against the tracker's report for total
// items and returns the hooks of its mode; a nil progress needs no tracker
// and has no hooks.
func (t *ReportTracker) progressHooks(progress PoolProgress, total int, saveEach bool) (workerPoolHooks, error) {
	switch {
	case progress == nil:
		return workerPoolHooks{}, nil
	case t == nil:
		return workerPoolHooks{}, errors.New("worker pool: progress needs a tracker, got a nil one")
	}
	switch progress.(type) {
	case SingleBar, BarPerItem, BarsPerItem:
		return progress.hooks(t, total, saveEach)
	default:
		// A pointer mode satisfies PoolProgress too, and a typed nil one would
		// panic in hooks: reject it as an error like every other bad option.
		return workerPoolHooks{}, fmt.Errorf("worker pool: progress must be a SingleBar, BarPerItem or BarsPerItem value, got %T", progress)
	}
}

// runWorkerPool is the single scheduler behind both RunWorkerPool entry
// points. It dispatches work items [0, total) through startActivity with at most
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
		return concurrencyError(total, concurrency)
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
	// dispatchStopped latches the first true stopDispatch: once it is set, stopDispatch
	// is not called again and nothing more is dispatched, even if it would
	// later report false.
	var dispatchStopped bool

	dispatch := func() {
		for !dispatchStopped && len(activeIndexes) < concurrency && nextIndex < total {
			if hooks.stopped() {
				dispatchStopped = true
				return
			}
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
			selector.AddFuture(futures[idx], func(f workflow.Future) {
				if handler != nil {
					if err := handler(ctx, idx, f); err != nil && firstErr == nil {
						firstErr = err
					}
				} else if err := f.Get(ctx, nil); err != nil && firstErr == nil {
					firstErr = err
				}
				if hooks.onComplete != nil {
					hooks.onComplete(ctx, idx)
				}
				if hooks.afterComplete != nil {
					hooks.afterComplete(ctx)
				}
				delete(futures, idx)
				activeIndexes = removeActiveIndex(activeIndexes, idx)
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

// concurrencyError reports a nonpositive concurrency for total items.
func concurrencyError(total, concurrency int) error {
	return fmt.Errorf("worker pool: concurrency must be positive, got %d (total %d)", concurrency, total)
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
