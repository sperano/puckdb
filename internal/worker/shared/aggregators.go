package shared

import (
	"go.temporal.io/sdk/workflow"
)

// Adder is implemented by types that can aggregate values.
type Adder[T any] interface {
	Add(T)
}

// AggregateInto returns a ResultHandler that decodes each activity result
// and adds it to the accumulator. The accumulator must implement Add(T).
// Like every handler here, it serves as PoolOptions.Handle for both pool
// entry points: tracker.RunWorkerPool with progress, RunWorkerPool without.
//
// Example:
//
//	counts := core.OriginCounts{}
//	err := tracker.RunWorkerPool(ctx, PoolOptions{
//	    Total: total, Concurrency: concurrency,
//	    Progress: SingleBar{Bar: BarRef{Group: group}},
//	    Start:    starter,
//	    Handle:   AggregateInto[core.OriginCounts](&counts),
//	})
func AggregateInto[T any](acc Adder[T]) ResultHandler {
	return func(ctx workflow.Context, _ int, f workflow.Future) error {
		var result T
		if err := f.Get(ctx, &result); err != nil {
			return err
		}
		acc.Add(result)
		return nil
	}
}

// CollectSlicesInto returns a ResultHandler that decodes each activity result
// (which must be a slice) and appends all elements to the target slice.
//
// Example:
//
//	var allPlayers []Player
//	err := tracker.RunWorkerPool(ctx, PoolOptions{
//	    Total: numBatches, Concurrency: concurrency,
//	    Progress: SingleBar{Bar: BarRef{Group: group}},
//	    Start:    starter,
//	    Handle:   CollectSlicesInto(&allPlayers),
//	})
func CollectSlicesInto[T any](target *[]T) ResultHandler {
	return func(ctx workflow.Context, _ int, f workflow.Future) error {
		var result []T
		if err := f.Get(ctx, &result); err != nil {
			return err
		}
		*target = append(*target, result...)
		return nil
	}
}
