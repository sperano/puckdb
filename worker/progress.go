package worker

import (
	"fmt"
	"reflect"
	"time"

	"go.temporal.io/sdk/workflow"
)

// ItemProgress represents the progress for a single item (season, phase, batch, etc.).
type ItemProgress struct {
	ID                   int    `json:"id"`
	Description          string `json:"description,omitempty"`
	CompletedDescription string `json:"completedDescription,omitempty"` // Past tense for completion display
	Total                int    `json:"total"`
	Completed            int    `json:"completed"`
	Started              bool   `json:"started"`
	StartedAt            string `json:"startedAt,omitempty"`   // RFC3339 timestamp
	CompletedAt          string `json:"completedAt,omitempty"` // RFC3339 timestamp
}

// ProgressDisplayStyle determines how items are rendered in the CLI.
type ProgressDisplayStyle string

const (
	// DisplayStyleSequential shows items as sequential steps with ✓/▶ prefixes, completed items remain visible
	DisplayStyleSequential ProgressDisplayStyle = "SEQUENTIAL"
	// DisplayStyleParallel shows concurrent items with progress bars, completed items hidden
	DisplayStyleParallel ProgressDisplayStyle = "PARALLEL"
)

// WorkflowProgress represents the progress of a workflow.
// DEPRECATED: Use ProgressReport for new workflows.
type WorkflowProgress struct {
	Total           int                  `json:"total"`
	Completed       int                  `json:"completed"`
	Message         string               `json:"message,omitempty"`
	Header          string               `json:"header,omitempty"`
	CompletedHeader string               `json:"completedHeader,omitempty"`
	Items           []ItemProgress       `json:"items,omitempty"`
	DisplayStyle    ProgressDisplayStyle `json:"displayStyle,omitempty"`
}

// ProgressBar represents a single progress bar within a group.
type ProgressBar struct {
	Label           string `json:"label,omitempty"`
	Current         int    `json:"current"`
	Total           int    `json:"total"`
	Started         bool   `json:"started,omitempty"`
	ChildWorkflowID string `json:"childWorkflowID,omitempty"` // If set, resolver queries this child for progress
}

// ProgressGroup is a unit of display: header + bars + completion message.
type ProgressGroup struct {
	Header       string        `json:"header"`
	CompletedMsg string        `json:"completedMsg"`
	Bars         []ProgressBar `json:"bars"`
	StartedAt    int64         `json:"startedAt"`
	CompletedAt  int64         `json:"completedAt"`
}

// ProgressReport is the new group-based progress structure.
type ProgressReport struct {
	Total     int             `json:"total"`
	Completed int             `json:"completed"`
	Message   string          `json:"message,omitempty"`
	Groups    []ProgressGroup `json:"groups"`
}

// ProgressQueryName is the name of the query handler for progress.
// DEPRECATED: Use ProgressReportQueryName for new workflows.
const ProgressQueryName = "progress"

// ProgressReportQueryName is the query name for the new group-based progress.
const ProgressReportQueryName = "progressReport"

// ReportTracker wraps ProgressReport and provides helper methods for workflows.
type ReportTracker struct {
	report *ProgressReport
}

// NewReportTracker creates a tracker wrapping the given report.
func NewReportTracker(report *ProgressReport) *ReportTracker {
	return &ReportTracker{report: report}
}

// RegisterQueryHandler registers the progressReport query handler.
func (t *ReportTracker) RegisterQueryHandler(ctx workflow.Context) error {
	return workflow.SetQueryHandler(ctx, ProgressReportQueryName, func() (*ProgressReport, error) {
		return t.report, nil
	})
}

// StartGroup marks a group as started with the current timestamp.
func (t *ReportTracker) StartGroup(ctx workflow.Context, groupIdx int) {
	t.report.Groups[groupIdx].StartedAt = workflow.Now(ctx).UnixMilli()
}

// CompleteGroup marks a group as completed with message and timestamp.
// Sets all bars to complete and increments the report's Completed count.
func (t *ReportTracker) CompleteGroup(ctx workflow.Context, groupIdx int, msg string) {
	g := &t.report.Groups[groupIdx]
	for i := range g.Bars {
		g.Bars[i].Current = g.Bars[i].Total
	}
	g.CompletedAt = workflow.Now(ctx).UnixMilli()
	g.CompletedMsg = msg
	t.report.Completed++
}

// IncrementBar increments the current count of a specific bar.
func (t *ReportTracker) IncrementBar(groupIdx, barIdx int) {
	t.report.Groups[groupIdx].Bars[barIdx].Current++
}

// IncrementBarBy increments the current count of a specific bar by the given amount.
func (t *ReportTracker) IncrementBarBy(groupIdx, barIdx, amount int) {
	t.report.Groups[groupIdx].Bars[barIdx].Current += amount
}

// SetBarTotal sets the total for a specific bar.
func (t *ReportTracker) SetBarTotal(groupIdx, barIdx, total int) {
	t.report.Groups[groupIdx].Bars[barIdx].Total = total
}

// SetBarLabel sets the label for a specific bar.
func (t *ReportTracker) SetBarLabel(groupIdx, barIdx int, label string) {
	t.report.Groups[groupIdx].Bars[barIdx].Label = label
}

// StartBar marks a bar as started.
func (t *ReportTracker) StartBar(groupIdx, barIdx int) {
	t.report.Groups[groupIdx].Bars[barIdx].Started = true
}

// CompleteBar marks a specific bar as complete (sets Current = Total).
func (t *ReportTracker) CompleteBar(groupIdx, barIdx int) {
	bar := &t.report.Groups[groupIdx].Bars[barIdx]
	bar.Current = bar.Total
}

// ChildWorkflowIDFunc maps a season to its child workflow ID.
type ChildWorkflowIDFunc func(startYear int) string

// AddBarsForSeasons adds one bar per season to a group.
// If childIDFunc is provided, sets ChildWorkflowID so resolver can query children for progress.
// Returns a map of startYear -> barIdx for looking up bars later.
func (t *ReportTracker) AddBarsForSeasons(groupIdx int, seasons []SeasonInfo, counter SeasonCounterFunc, childIDFunc ChildWorkflowIDFunc) map[int]int {
	barIndex := make(map[int]int)
	total := 0

	for i, season := range seasons {
		count := counter(season)
		bar := ProgressBar{
			Label: season.Label(),
			Total: count,
		}
		if childIDFunc != nil {
			bar.ChildWorkflowID = childIDFunc(season.StartYear())
		}
		t.report.Groups[groupIdx].Bars = append(t.report.Groups[groupIdx].Bars, bar)
		barIndex[season.StartYear()] = i
		total += count
	}

	t.report.Total = total
	return barIndex
}

// GetElapsed returns the formatted elapsed time since group started.
func (t *ReportTracker) GetElapsed(ctx workflow.Context, groupIdx int) string {
	startedAt := t.report.Groups[groupIdx].StartedAt
	if startedAt == 0 {
		return ""
	}
	elapsed := workflow.Now(ctx).UnixMilli() - startedAt
	return formatElapsedMilli(elapsed)
}

// formatElapsedMilli formats milliseconds as a human-readable duration.
func formatElapsedMilli(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	return formatDuration(d)
}

// RunWorkerPool runs activities concurrently with a fixed concurrency limit.
// All activities update a SINGLE bar at barIdx, incrementing by 1 on each completion.
// Use RunWorkerPoolMultiBar if each activity should have its own bar.
func (t *ReportTracker) RunWorkerPool(ctx workflow.Context, groupIdx, barIdx, total, concurrency int, startActivity ActivityStarter, handler ResultHandler) error {
	return t.RunWorkerPoolBy(ctx, groupIdx, barIdx, total, concurrency, 1, startActivity, handler)
}

// RunWorkerPoolBy is like RunWorkerPool but increments the bar by a custom amount per completion.
func (t *ReportTracker) RunWorkerPoolBy(ctx workflow.Context, groupIdx, barIdx, total, concurrency, incrementBy int, startActivity ActivityStarter, handler ResultHandler) error {
	if total == 0 {
		return nil
	}

	nextIndex := 0
	active := make(map[int]workflow.Future)
	var firstErr error

	// Start initial batch
	for len(active) < concurrency && nextIndex < total {
		idx := nextIndex
		active[idx] = startActivity(ctx, idx)
		nextIndex++
	}

	// Process until all work is done
	for len(active) > 0 {
		selector := workflow.NewSelector(ctx)

		for idx, future := range active {
			capturedIdx := idx
			capturedFuture := future
			selector.AddFuture(capturedFuture, func(f workflow.Future) {
				if handler != nil {
					if err := handler(ctx, capturedIdx, f); err != nil && firstErr == nil {
						firstErr = err
					}
				} else if err := f.Get(ctx, nil); err != nil && firstErr == nil {
					firstErr = err
				}
				t.IncrementBarBy(groupIdx, barIdx, incrementBy)
				delete(active, capturedIdx)
			})
		}

		selector.Select(ctx)
		if firstErr != nil {
			return firstErr
		}

		// Start next batch
		for len(active) < concurrency && nextIndex < total {
			idx := nextIndex
			active[idx] = startActivity(ctx, idx)
			nextIndex++
		}
	}

	return nil
}

// RunWorkerPoolMultiBar runs activities concurrently where each activity has its own bar.
// Bar[barStart + i] corresponds to activity i. Marks bars as Started when dispatched
// and sets them complete on activity completion.
func (t *ReportTracker) RunWorkerPoolMultiBar(ctx workflow.Context, groupIdx, barStart, total, concurrency int, startActivity ActivityStarter, handler ResultHandler) error {
	if total == 0 {
		return nil
	}

	nextIndex := 0
	active := make(map[int]workflow.Future)
	var firstErr error

	// Start initial batch
	for len(active) < concurrency && nextIndex < total {
		idx := nextIndex
		t.StartBar(groupIdx, barStart+idx)
		active[idx] = startActivity(ctx, idx)
		nextIndex++
	}

	// Process until all work is done
	for len(active) > 0 {
		selector := workflow.NewSelector(ctx)

		for idx, future := range active {
			capturedIdx := idx
			capturedFuture := future
			selector.AddFuture(capturedFuture, func(f workflow.Future) {
				if handler != nil {
					if err := handler(ctx, capturedIdx, f); err != nil && firstErr == nil {
						firstErr = err
					}
				} else if err := f.Get(ctx, nil); err != nil && firstErr == nil {
					firstErr = err
				}
				t.IncrementBarBy(groupIdx, barStart+capturedIdx, 1)
				delete(active, capturedIdx)
			})
		}

		selector.Select(ctx)
		if firstErr != nil {
			return firstErr
		}

		// Start next batch
		for len(active) < concurrency && nextIndex < total {
			idx := nextIndex
			t.StartBar(groupIdx, barStart+idx)
			active[idx] = startActivity(ctx, idx)
			nextIndex++
		}
	}

	return nil
}

// ProgressTracker tracks workflow progress and handles completion via Selector.
type ProgressTracker struct {
	progress   WorkflowProgress
	itemIndex  map[int]int // maps item ID to index in Items slice
	futureToID map[int]int // maps future index to item ID
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

// NewProgressTrackerSinglePhase creates a ProgressTracker with a single phase for display.
// Combines phase-based display with ContinueAsNew offset support.
// CompletedDescription should be set dynamically via SetItemCompletedDescription.
func NewProgressTrackerSinglePhase(description string, total, completed int) *ProgressTracker {
	const phaseID = 1
	return &ProgressTracker{
		progress: WorkflowProgress{
			Total:     total,
			Completed: completed,
			Items: []ItemProgress{{
				ID:          phaseID,
				Description: description,
				Total:       total,
				Completed:   completed,
				Started:     true,
			}},
		},
		itemIndex:  map[int]int{phaseID: 0},
		futureToID: make(map[int]int),
	}
}

// NewProgressTrackerWithSeasons creates a ProgressTracker that tracks per-season progress.
func NewProgressTrackerWithSeasons(seasons []SeasonInfo, counter SeasonCounterFunc, header, completedHeader string) *ProgressTracker {
	tracker := &ProgressTracker{
		itemIndex:  make(map[int]int),
		futureToID: make(map[int]int),
	}
	tracker.InitializeWithSeasons(seasons, counter, header, completedHeader)
	return tracker
}

// PhaseInfo represents a workflow phase for progress tracking.
type PhaseInfo struct {
	ID                   int    // Phase number (1, 2, 3, ...)
	Description          string // In-progress description (e.g., "Downloading...")
	CompletedDescription string // Completion description (e.g., "Downloaded.")
	Total                int    // Expected total items (0 if unknown initially)
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
			ID:                   phase.ID,
			Description:          phase.Description,
			CompletedDescription: phase.CompletedDescription,
			Total:                phase.Total,
			Completed:            0,
		}
		p.itemIndex[phase.ID] = i
		total += phase.Total
	}

	p.progress = WorkflowProgress{
		Total:        total,
		Completed:    0,
		Items:        itemProgress,
		DisplayStyle: DisplayStyleSequential,
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

// SetItemCompletedDescription updates the completed description for a specific item.
// Useful for including dynamic values like counts in the completion message.
func (p *ProgressTracker) SetItemCompletedDescription(itemID int, description string) {
	if idx, ok := p.itemIndex[itemID]; ok {
		p.progress.Items[idx].CompletedDescription = description
	}
}

// GetItemElapsed returns the elapsed time since the item started as a formatted string.
// Returns empty string if item not found or not started.
func (p *ProgressTracker) GetItemElapsed(ctx workflow.Context, itemID int) string {
	idx, ok := p.itemIndex[itemID]
	if !ok {
		return ""
	}
	item := p.progress.Items[idx]
	if item.StartedAt == "" {
		return ""
	}
	startTime, err := time.Parse(time.RFC3339, item.StartedAt)
	if err != nil {
		return ""
	}
	elapsed := workflow.Now(ctx).Sub(startTime)
	return formatDuration(elapsed)
}

// formatDuration formats a duration in a human-readable way.
func formatDuration(d time.Duration) string {
	if d < time.Second {
		return "0.0s"
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	if d < time.Hour {
		minutes := int(d.Minutes())
		seconds := int(d.Seconds()) % 60
		return fmt.Sprintf("%dm%ds", minutes, seconds)
	}
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	return fmt.Sprintf("%dh%dm", hours, minutes)
}

// SeasonCounterFunc returns the total count for a season's progress tracking.
type SeasonCounterFunc func(season SeasonInfo) int

// InitializeWithSeasons sets up per-season progress tracking.
// The counter function determines each season's total (e.g., countDaysInSeason).
// Can be called after RegisterQueryHandler to update progress state once seasons are known.
func (p *ProgressTracker) InitializeWithSeasons(seasons []SeasonInfo, counter SeasonCounterFunc, header, completedHeader string) {
	total := 0
	itemProgress := make([]ItemProgress, len(seasons))

	for i, season := range seasons {
		count := counter(season)
		itemProgress[i] = ItemProgress{
			ID:          season.StartYear(),
			Description: season.Label(),
			Total:       count,
			Completed:   0,
		}
		p.itemIndex[season.StartYear()] = i
		total += count
	}

	p.progress = WorkflowProgress{
		Total:           total,
		Completed:       0,
		Header:          header,
		CompletedHeader: completedHeader,
		Items:           itemProgress,
		DisplayStyle:    DisplayStyleParallel,
	}
}

// CountDeferredTasks returns 0, indicating totals will be populated from child workflows.
// Use for workflows where the parent doesn't know totals upfront (e.g., player logs).
func CountDeferredTasks(_ SeasonInfo) int {
	return 0
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
// Uses workflow.Now for deterministic time during replays.
func (p *ProgressTracker) MarkItemStarted(ctx workflow.Context, itemID int) {
	if idx, ok := p.itemIndex[itemID]; ok {
		p.progress.Items[idx].Started = true
		p.progress.Items[idx].StartedAt = workflow.Now(ctx).UTC().Format(time.RFC3339)
	}
}

// IsItemStarted returns whether an item has been marked as started.
func (p *ProgressTracker) IsItemStarted(itemID int) bool {
	if idx, ok := p.itemIndex[itemID]; ok {
		return p.progress.Items[idx].Started
	}
	return false
}

// GetItemTotal returns the total for a specific item.
func (p *ProgressTracker) GetItemTotal(itemID int) int {
	if idx, ok := p.itemIndex[itemID]; ok {
		return p.progress.Items[idx].Total
	}
	return -1
}

// MarkItemCompleted marks an item as completed with timestamp.
// Sets Completed = Total to ensure display logic shows completion checkmark.
// Uses workflow.Now for deterministic time during replays.
func (p *ProgressTracker) MarkItemCompleted(ctx workflow.Context, itemID int) {
	if idx, ok := p.itemIndex[itemID]; ok {
		item := &p.progress.Items[idx]
		// Sync overall progress if item's Completed was behind Total
		if item.Completed < item.Total {
			p.progress.Completed += item.Total - item.Completed
		}
		item.Completed = item.Total
		item.CompletedAt = workflow.Now(ctx).UTC().Format(time.RFC3339)
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

// IncrementBy manually increments the completed count by the given amount.
func (p *ProgressTracker) IncrementBy(amount int) {
	p.progress.Completed += amount
}

// SetMessage sets the progress message describing the current phase.
func (p *ProgressTracker) SetMessage(message string) {
	p.progress.Message = message
}

// ActivityStarter is a function that starts an activity for a given index and returns a future.
type ActivityStarter func(ctx workflow.Context, index int) workflow.Future

// ResultHandler is called for each completed activity with its index.
// The handler receives the future to extract the result.
type ResultHandler func(ctx workflow.Context, index int, future workflow.Future) error

// RunWorkerPool runs activities with a fixed concurrency, always keeping `concurrency` activities in flight.
// As each activity completes, immediately starts the next one until all `total` items are processed.
func (p *ProgressTracker) RunWorkerPool(ctx workflow.Context, total int, concurrency int, startActivity ActivityStarter) error {
	return p.RunWorkerPoolWithHandler(ctx, total, concurrency, startActivity, nil)
}

// RunWorkerPoolWithHandler is like RunWorkerPool but calls handler for each completed activity.
// The handler can extract and aggregate results from each future.
func (p *ProgressTracker) RunWorkerPoolWithHandler(ctx workflow.Context, total int, concurrency int, startActivity ActivityStarter, handler ResultHandler) error {
	return p.RunWorkerPoolForItem(ctx, total, concurrency, 0, startActivity, handler)
}

// RunWorkerPoolForItem is like RunWorkerPoolWithHandler but increments a specific item's progress.
// Use itemID=0 to only increment the overall total.
func (p *ProgressTracker) RunWorkerPoolForItem(ctx workflow.Context, total int, concurrency int, itemID int, startActivity ActivityStarter, handler ResultHandler) error {
	return p.RunWorkerPoolForItemBy(ctx, total, concurrency, itemID, 1, startActivity, handler)
}

// RunWorkerPoolForItemBy is like RunWorkerPoolForItem but increments by a custom amount per completion.
// Use for batch processing where each activity handles multiple items.
func (p *ProgressTracker) RunWorkerPoolForItemBy(ctx workflow.Context, total int, concurrency int, itemID int, incrementBy int, startActivity ActivityStarter, handler ResultHandler) error {
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
				if handler != nil {
					if err := handler(ctx, capturedIdx, f); err != nil && firstErr == nil {
						firstErr = err
					}
				} else {
					if err := f.Get(ctx, nil); err != nil && firstErr == nil {
						firstErr = err
					}
				}
				if itemID != 0 {
					p.IncrementItemBy(itemID, incrementBy)
				} else {
					p.progress.Completed += incrementBy
				}
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
