package worker

import (
	"reflect"
	"time"

	"go.temporal.io/sdk/workflow"
)

// ItemProgress represents the progress for a single item (season, phase, batch, etc.).
type ItemProgress struct {
	ID          int    `json:"id"`
	Description string `json:"description,omitempty"`
	Total       int    `json:"total"`
	Completed   int    `json:"completed"`
	Started     bool   `json:"started"`
	StartedAt   string `json:"startedAt,omitempty"`   // RFC3339 timestamp
	CompletedAt string `json:"completedAt,omitempty"` // RFC3339 timestamp
}

// WorkflowProgress represents the progress of a workflow.
type WorkflowProgress struct {
	Total     int            `json:"total"`
	Completed int            `json:"completed"`
	Message   string         `json:"message,omitempty"`
	Items     []ItemProgress `json:"items,omitempty"`
}

// ProgressQueryName is the name of the query handler for progress.
const ProgressQueryName = "progress"

// ProgressTracker tracks workflow progress and handles completion via Selector.
type ProgressTracker struct {
	progress    WorkflowProgress
	itemIndex   map[int]int // maps item ID to index in Items slice
	futureToID  map[int]int // maps future index to item ID
}

// NewProgressTracker creates a new ProgressTracker with the given total count.
func NewProgressTracker(total int) *ProgressTracker {
	return &ProgressTracker{
		progress:   WorkflowProgress{Total: total, Completed: 0},
		itemIndex:  make(map[int]int),
		futureToID: make(map[int]int),
	}
}

// NewProgressTrackerWithOffset creates a ProgressTracker that reports cumulative progress.
// Used with ContinueAsNew to track progress across multiple workflow executions.
// - batchSize: number of items in this execution
// - offset: completed count from previous executions
// - grandTotal: total items across all executions
func NewProgressTrackerWithOffset(batchSize int, offset int, grandTotal int) *ProgressTracker {
	return &ProgressTracker{
		progress:   WorkflowProgress{Total: grandTotal, Completed: offset},
		itemIndex:  make(map[int]int),
		futureToID: make(map[int]int),
	}
}

// NewProgressTrackerWithSeasons creates a ProgressTracker that tracks per-season progress.
func NewProgressTrackerWithSeasons(seasons []SeasonInfo) *ProgressTracker {
	tracker := &ProgressTracker{
		itemIndex:  make(map[int]int),
		futureToID: make(map[int]int),
	}
	tracker.InitializeWithSeasons(seasons)
	return tracker
}

// PhaseInfo represents a workflow phase for progress tracking.
type PhaseInfo struct {
	ID          int    // Phase number (1, 2, 3, ...)
	Description string // Human-readable description
	Total       int    // Expected total items (0 if unknown initially)
}

// NewProgressTrackerWithPhases creates a ProgressTracker that tracks per-phase progress.
func NewProgressTrackerWithPhases(phases []PhaseInfo) *ProgressTracker {
	tracker := &ProgressTracker{
		itemIndex:  make(map[int]int),
		futureToID: make(map[int]int),
	}
	tracker.InitializeWithPhases(phases)
	return tracker
}

// InitializeWithPhases sets up per-phase progress tracking.
func (p *ProgressTracker) InitializeWithPhases(phases []PhaseInfo) {
	total := 0
	itemProgress := make([]ItemProgress, len(phases))

	for i, phase := range phases {
		itemProgress[i] = ItemProgress{
			ID:          phase.ID,
			Description: phase.Description,
			Total:       phase.Total,
			Completed:   0,
		}
		p.itemIndex[phase.ID] = i
		total += phase.Total
	}

	p.progress = WorkflowProgress{
		Total:     total,
		Completed: 0,
		Items:     itemProgress,
	}
}

// SetItemTotal updates the total for a specific item (useful when total is unknown at start).
func (p *ProgressTracker) SetItemTotal(itemID int, total int) {
	if idx, ok := p.itemIndex[itemID]; ok {
		oldTotal := p.progress.Items[idx].Total
		p.progress.Items[idx].Total = total
		p.progress.Total += (total - oldTotal)
	}
}

// InitializeWithSeasons sets up per-season progress tracking.
// Can be called after RegisterQueryHandler to update progress state once seasons are known.
func (p *ProgressTracker) InitializeWithSeasons(seasons []SeasonInfo) {
	total := 0
	itemProgress := make([]ItemProgress, len(seasons))

	for i, season := range seasons {
		count := countDownloadTasksForSeason(season)
		itemProgress[i] = ItemProgress{
			ID:          season.StartYear,
			Description: season.Label(),
			Total:       count,
			Completed:   0,
		}
		p.itemIndex[season.StartYear] = i
		total += count
	}

	p.progress = WorkflowProgress{
		Total:     total,
		Completed: 0,
		Items:     itemProgress,
	}
}

// SetFutureItem associates a future index with an item ID for tracking.
func (p *ProgressTracker) SetFutureItem(futureIndex int, itemID int) {
	p.futureToID[futureIndex] = itemID
}

// IncrementItem increments the completed count for a specific item.
func (p *ProgressTracker) IncrementItem(itemID int) {
	p.progress.Completed++
	if idx, ok := p.itemIndex[itemID]; ok {
		p.progress.Items[idx].Completed++
	}
}

// IncrementItemBy increments the completed count for a specific item by the given amount.
func (p *ProgressTracker) IncrementItemBy(itemID int, amount int) {
	p.progress.Completed += amount
	if idx, ok := p.itemIndex[itemID]; ok {
		p.progress.Items[idx].Completed += amount
	}
}

// MarkItemStarted marks an item as started (child workflow spawned).
func (p *ProgressTracker) MarkItemStarted(itemID int) {
	if idx, ok := p.itemIndex[itemID]; ok {
		p.progress.Items[idx].Started = true
		p.progress.Items[idx].StartedAt = time.Now().UTC().Format(time.RFC3339)
	}
}

// MarkItemCompleted marks an item as completed with timestamp.
func (p *ProgressTracker) MarkItemCompleted(itemID int) {
	if idx, ok := p.itemIndex[itemID]; ok {
		p.progress.Items[idx].CompletedAt = time.Now().UTC().Format(time.RFC3339)
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
	return p.WaitAllForItem(ctx, futures, 0)
}

// WaitAllForItem waits for all futures, updating per-item progress.
// If itemID is 0, only updates the total completed count.
func (p *ProgressTracker) WaitAllForItem(ctx workflow.Context, futures []workflow.Future, itemID int) error {
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
			if itemID != 0 {
				p.IncrementItem(itemID)
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

// WaitAllWithItems waits for all futures, updating per-item progress based on the futureItems slice.
// futureItems[i] contains the itemID for futures[i].
func (p *ProgressTracker) WaitAllWithItems(ctx workflow.Context, futures []workflow.Future, futureItems []int) error {
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
			p.IncrementItem(futureItems[idx])
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
