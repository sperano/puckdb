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
//
// Example:
//
//	counts := core.OriginCounts{}
//	err := tracker.RunWorkerPool(ctx, group, 0, total, concurrency, starter,
//	    AggregateInto[core.OriginCounts](&counts))
func AggregateInto[T any, A Adder[T]](acc A) ResultHandler {
	return func(ctx workflow.Context, _ int, f workflow.Future) error {
		var result T
		if err := f.Get(ctx, &result); err != nil {
			return err
		}
		acc.Add(result)
		return nil
	}
}

// CollectInto returns a ResultHandler that decodes each activity result
// and appends it to the target slice. Use for collecting individual items.
//
// Example:
//
//	var players []Player
//	err := tracker.RunWorkerPool(ctx, group, 0, total, concurrency, starter,
//	    CollectInto(&players))
func CollectInto[T any](target *[]T) ResultHandler {
	return func(ctx workflow.Context, _ int, f workflow.Future) error {
		var result T
		if err := f.Get(ctx, &result); err != nil {
			return err
		}
		*target = append(*target, result)
		return nil
	}
}

// CollectSlicesInto returns a ResultHandler that decodes each activity result
// (which must be a slice) and appends all elements to the target slice.
//
// Example:
//
//	var allPlayers []Player
//	err := tracker.RunWorkerPool(ctx, group, 0, numBatches, concurrency, starter,
//	    CollectSlicesInto(&allPlayers))
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
