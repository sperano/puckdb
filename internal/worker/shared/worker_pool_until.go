package shared

import "go.temporal.io/sdk/workflow"

// RunActivitiesUntil runs work items [0, total) with at most concurrency in
// flight, like RunWorkerPool but without a progress bar, and dispatches no
// further item once stop reports true. Items already in flight still complete
// and reach handler, so a caller that only needs a prefix of the items can
// stop the rest from being scheduled without abandoning any it started.
// stop is checked on the workflow goroutine before every dispatch, typically
// against state handler updates. A handler error ends the run as in
// RunWorkerPool.
func RunActivitiesUntil(ctx workflow.Context, total, concurrency int, startActivity ActivityStarter,
	handler ResultHandler, stop func() bool) error {
	return runWorkerPool(ctx, total, concurrency, startActivity, handler, workerPoolHooks{stopDispatch: stop})
}
