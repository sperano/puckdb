package worker

import (
	"reflect"

	"go.temporal.io/sdk/workflow"
)

// SeasonProgress represents the progress for a single season.
type SeasonProgress struct {
	StartYear int `json:"startYear"`
	Total     int `json:"total"`
	Completed int `json:"completed"`
}

// WorkflowProgress represents the progress of a workflow.
type WorkflowProgress struct {
	Total     int              `json:"total"`
	Completed int              `json:"completed"`
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

// NewProgressTrackerWithSeasons creates a ProgressTracker that tracks per-season progress.
func NewProgressTrackerWithSeasons(seasons []SeasonInfo) *ProgressTracker {
	total := 0
	seasonProgress := make([]SeasonProgress, len(seasons))
	seasonIndex := make(map[int]int)

	for i, season := range seasons {
		count := countDownloadTasksForSeason(season)
		seasonProgress[i] = SeasonProgress{
			StartYear: season.StartYear,
			Total:     count,
			Completed: 0,
		}
		seasonIndex[season.StartYear] = i
		total += count
	}

	return &ProgressTracker{
		progress: WorkflowProgress{
			Total:     total,
			Completed: 0,
			Seasons:   seasonProgress,
		},
		seasonIndex:  seasonIndex,
		futureToYear: make(map[int]int),
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

// getResultPtr returns a pointer to the element at index i in the slice.
// results must be a slice.
func getResultPtr(results any, i int) any {
	v := reflect.ValueOf(results)
	if v.Kind() != reflect.Slice {
		return nil
	}
	return v.Index(i).Addr().Interface()
}
