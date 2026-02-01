package worker

import (
	"reflect"
	"time"

	"go.temporal.io/sdk/workflow"
)

// SeasonProgress represents the progress for a single season.
type SeasonProgress struct {
	StartYear   int    `json:"startYear"`
	Total       int    `json:"total"`
	Completed   int    `json:"completed"`
	Started     bool   `json:"started"`
	StartedAt   string `json:"startedAt,omitempty"`   // RFC3339 timestamp
	CompletedAt string `json:"completedAt,omitempty"` // RFC3339 timestamp
}

// WorkflowProgress represents the progress of a workflow.
type WorkflowProgress struct {
	Total     int              `json:"total"`
	Completed int              `json:"completed"`
	Message   string           `json:"message,omitempty"`
	Seasons   []SeasonProgress `json:"seasons,omitempty"`
}

// ProgressQueryName is the name of the query handler for progress.
const ProgressQueryName = "progress"

// ProgressTracker tracks workflow progress and handles completion via Selector.
type ProgressTracker struct {
	progress     WorkflowProgress
	seasonIndex  map[int]int // maps startYear to index in Seasons slice
	futureToYear map[int]int // maps future index to startYear
}

// NewProgressTracker creates a new ProgressTracker with the given total count.
func NewProgressTracker(total int) *ProgressTracker {
	return &ProgressTracker{
		progress:     WorkflowProgress{Total: total, Completed: 0},
		seasonIndex:  make(map[int]int),
		futureToYear: make(map[int]int),
	}
}

// NewProgressTrackerWithOffset creates a ProgressTracker that reports cumulative progress.
// Used with ContinueAsNew to track progress across multiple workflow executions.
// - batchSize: number of items in this execution
// - offset: completed count from previous executions
// - grandTotal: total items across all executions
func NewProgressTrackerWithOffset(batchSize int, offset int, grandTotal int) *ProgressTracker {
	return &ProgressTracker{
		progress:     WorkflowProgress{Total: grandTotal, Completed: offset},
		seasonIndex:  make(map[int]int),
		futureToYear: make(map[int]int),
	}
}

// NewProgressTrackerWithSeasons creates a ProgressTracker that tracks per-season progress.
func NewProgressTrackerWithSeasons(seasons []SeasonInfo) *ProgressTracker {
	tracker := &ProgressTracker{
		seasonIndex:  make(map[int]int),
		futureToYear: make(map[int]int),
	}
	tracker.InitializeWithSeasons(seasons)
	return tracker
}

// InitializeWithSeasons sets up per-season progress tracking.
// Can be called after RegisterQueryHandler to update progress state once seasons are known.
func (p *ProgressTracker) InitializeWithSeasons(seasons []SeasonInfo) {
	total := 0
	seasonProgress := make([]SeasonProgress, len(seasons))

	for i, season := range seasons {
		count := countDownloadTasksForSeason(season)
		seasonProgress[i] = SeasonProgress{
			StartYear: season.StartYear,
			Total:     count,
			Completed: 0,
		}
		p.seasonIndex[season.StartYear] = i
		total += count
	}

	p.progress = WorkflowProgress{
		Total:     total,
		Completed: 0,
		Seasons:   seasonProgress,
	}
}

// SetFutureSeason associates a future index with a season year for tracking.
func (p *ProgressTracker) SetFutureSeason(futureIndex int, startYear int) {
	p.futureToYear[futureIndex] = startYear
}

// IncrementSeason increments the completed count for a specific season.
func (p *ProgressTracker) IncrementSeason(startYear int) {
	p.progress.Completed++
	if idx, ok := p.seasonIndex[startYear]; ok {
		p.progress.Seasons[idx].Completed++
	}
}

// MarkSeasonStarted marks a season as started (child workflow spawned).
func (p *ProgressTracker) MarkSeasonStarted(startYear int) {
	if idx, ok := p.seasonIndex[startYear]; ok {
		p.progress.Seasons[idx].Started = true
		p.progress.Seasons[idx].StartedAt = time.Now().UTC().Format(time.RFC3339)
	}
}

// MarkSeasonCompleted marks a season as completed with timestamp.
func (p *ProgressTracker) MarkSeasonCompleted(startYear int) {
	if idx, ok := p.seasonIndex[startYear]; ok {
		p.progress.Seasons[idx].CompletedAt = time.Now().UTC().Format(time.RFC3339)
	}
}

// RegisterQueryHandler registers the progress query handler on the workflow context.
func (p *ProgressTracker) RegisterQueryHandler(ctx workflow.Context) error {
	return workflow.SetQueryHandler(ctx, ProgressQueryName, func() (WorkflowProgress, error) {
		return p.progress, nil
	})
}

// WaitAll waits for all futures, updating progress as each completes.
// Returns the first error encountered, but continues tracking progress for all futures.
func (p *ProgressTracker) WaitAll(ctx workflow.Context, futures []workflow.Future) error {
	return p.WaitAllForSeason(ctx, futures, 0)
}

// WaitAllForSeason waits for all futures, updating per-season progress.
// If startYear is 0, only updates the total completed count.
func (p *ProgressTracker) WaitAllForSeason(ctx workflow.Context, futures []workflow.Future, startYear int) error {
	if len(futures) == 0 {
		return nil
	}

	selector := workflow.NewSelector(ctx)
	var firstErr error

	for _, f := range futures {
		future := f // capture for closure
		selector.AddFuture(future, func(f workflow.Future) {
			if err := f.Get(ctx, nil); err != nil && firstErr == nil {
				firstErr = err
			}
			if startYear != 0 {
				p.IncrementSeason(startYear)
			} else {
				p.progress.Completed++
			}
		})
	}

	for range futures {
		selector.Select(ctx)
		if firstErr != nil {
			return firstErr
		}
	}

	return nil
}

// WaitAllWithSeasons waits for all futures, updating per-season progress based on the futureSeasons slice.
// futureSeasons[i] contains the startYear for futures[i].
func (p *ProgressTracker) WaitAllWithSeasons(ctx workflow.Context, futures []workflow.Future, futureSeasons []int) error {
	if len(futures) == 0 {
		return nil
	}

	selector := workflow.NewSelector(ctx)
	var firstErr error

	for i, f := range futures {
		idx := i
		future := f
		selector.AddFuture(future, func(f workflow.Future) {
			if err := f.Get(ctx, nil); err != nil && firstErr == nil {
				firstErr = err
			}
			p.IncrementSeason(futureSeasons[idx])
		})
	}

	for range futures {
		selector.Select(ctx)
		if firstErr != nil {
			return firstErr
		}
	}

	return nil
}

// WaitAllWithResults waits for all futures using a Selector, returning immediately on the first error.
// Results are stored in the results slice at the corresponding index.
// Progress is updated as each future completes.
func (p *ProgressTracker) WaitAllWithResults(ctx workflow.Context, futures []workflow.Future, results any) error {
	if len(futures) == 0 {
		return nil
	}

	selector := workflow.NewSelector(ctx)
	var firstErr error

	for i, f := range futures {
		idx := i
		future := f
		selector.AddFuture(future, func(f workflow.Future) {
			if err := f.Get(ctx, getResultPtr(results, idx)); err != nil && firstErr == nil {
				firstErr = err
			}
			p.progress.Completed++
		})
	}

	for range futures {
		selector.Select(ctx)
		if firstErr != nil {
			return firstErr
		}
	}

	return nil
}

// Increment manually increments the completed count by 1.
func (p *ProgressTracker) Increment() {
	p.progress.Completed++
}

// SetMessage sets the progress message describing the current phase.
func (p *ProgressTracker) SetMessage(message string) {
	p.progress.Message = message
}

// ActivityStarter is a function that starts an activity for a given index and returns a future.
type ActivityStarter func(ctx workflow.Context, index int) workflow.Future

// RunWorkerPool runs activities with a fixed concurrency, always keeping `concurrency` activities in flight.
// As each activity completes, immediately starts the next one until all `total` items are processed.
func (p *ProgressTracker) RunWorkerPool(ctx workflow.Context, total int, concurrency int, startActivity ActivityStarter) error {
	if total == 0 {
		return nil
	}

	nextIndex := 0
	var firstErr error

	// Track active futures by index
	active := make(map[int]workflow.Future)

	// Start initial batch
	for len(active) < concurrency && nextIndex < total {
		idx := nextIndex
		active[idx] = startActivity(ctx, idx)
		nextIndex++
	}

	// Process until all work is done
	for len(active) > 0 {
		// Create a new selector each iteration (required for Temporal determinism)
		selector := workflow.NewSelector(ctx)

		// Add all active futures to selector
		for idx, future := range active {
			capturedIdx := idx
			capturedFuture := future
			selector.AddFuture(capturedFuture, func(f workflow.Future) {
				if err := f.Get(ctx, nil); err != nil && firstErr == nil {
					firstErr = err
				}
				p.Increment()
				delete(active, capturedIdx)
			})
		}

		// Wait for any future to complete
		selector.Select(ctx)

		if firstErr != nil {
			return firstErr
		}

		// Start new activities to replace completed ones
		for len(active) < concurrency && nextIndex < total {
			idx := nextIndex
			active[idx] = startActivity(ctx, idx)
			nextIndex++
		}
	}

	return nil
}

// getResultPtr returns a pointer to the element at index i in the slice.
// results must be a slice.
func getResultPtr(results any, i int) any {
	v := reflect.ValueOf(results)
	if v.Kind() != reflect.Slice {
		return nil
	}
	return v.Index(i).Addr().Interface()
}
