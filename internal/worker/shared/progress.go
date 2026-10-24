package shared

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/config"
	"go.temporal.io/sdk/workflow"
)

// ProgressBar represents a single progress bar within a group.
type ProgressBar struct {
	Label   string `json:"label,omitempty"`
	Current int    `json:"current"`
	Total   int    `json:"total"`
	Started bool   `json:"started,omitempty"`
	// ProgressSourceKey is the Redis key from which the resolver merges this bar's
	// live progress. It may hold a real child workflow ID (when the bar corresponds
	// to a spawned child workflow) or a synthetic key published by an activity that
	// writes its own ProgressReport (see SaveActivityProgress). Empty means no merge.
	ProgressSourceKey string `json:"progressSourceKey,omitempty"`
	// MirrorSourceBar makes this bar mirror one bar of the source report, the
	// one at index ProgressSourceBar counted across the source's groups in
	// order: both its Current and its Total. When false the resolver uses the
	// default merge: the sum of every source bar's Current, capped at this
	// bar's Total. (A flag rather than a nil-able index because gob drops a
	// pointer to zero, which would turn index 0 into "unset".)
	MirrorSourceBar   bool `json:"mirrorSourceBar,omitempty"`
	ProgressSourceBar int  `json:"progressSourceBar,omitempty"`
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

// ProgressReportQueryName is the query name for the new group-based progress.
const ProgressReportQueryName = "progressReport"

// ReportTracker wraps ProgressReport and provides helper methods for workflows.
type ReportTracker struct {
	report *ProgressReport
	// saving, once SerializeSaves sets it, is held by Save from encoding the
	// report until its local activity returns.
	saving workflow.Mutex
}

// SerializeSaves makes Save hold a workflow mutex while it writes the report,
// for a workflow that updates one tracker from several coroutines. Each Save
// encodes the whole report and writes it in a local activity; two in flight
// at once could reach Redis out of order and leave an older report there.
// An uncontended Lock never yields, so a single coroutine issues the same
// commands as without it.
func (t *ReportTracker) SerializeSaves(ctx workflow.Context) {
	if t.saving == nil {
		t.saving = workflow.NewMutex(ctx)
	}
}

// RegisterQueryHandler registers the progressReport query handler.
// Called by InitTracker and after LoadReportTracker (for ContinueAsNew).
func (t *ReportTracker) RegisterQueryHandler(ctx workflow.Context) error {
	return workflow.SetQueryHandler(ctx, ProgressReportQueryName, func() (*ProgressReport, error) {
		return t.report, nil
	})
}

// InitTracker creates a ReportTracker and registers the query handler.
func InitTracker(ctx workflow.Context, report *ProgressReport) (*ReportTracker, error) {
	tracker := &ReportTracker{report: report}
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return nil, err
	}
	return tracker, nil
}

// WithProgressLocalActivityOptions returns ctx with the local activity
// options every progress persistence call shares (Save, LoadReportTracker and
// the stale-report deletion in the workflow package): only a
// ScheduleToCloseTimeout of config.DefaultProgressLocalActivityTimeout, so an
// unreachable Redis delays a workflow task by at most that long per update.
func WithProgressLocalActivityOptions(ctx workflow.Context) workflow.Context {
	return workflow.WithLocalActivityOptions(ctx, workflow.LocalActivityOptions{
		ScheduleToCloseTimeout: config.DefaultProgressLocalActivityTimeout,
	})
}

// Save persists the current ProgressReport to Redis via a local activity.
// Call at key structural changes (group start, group complete) so the resolver
// can read progress without querying Temporal.
//
// After SerializeSaves, Save waits for the previous one to finish. When the
// workflow is canceled while another Save holds the lock, it saves anyway:
// the run is ending and its progress is no longer read.
func (t *ReportTracker) Save(ctx workflow.Context) {
	if t.saving != nil {
		if err := t.saving.Lock(ctx); err != nil {
			workflow.GetLogger(ctx).Debug("Saving progress report without the save lock", "error", err)
		} else {
			defer t.saving.Unlock()
		}
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(t.report); err != nil {
		workflow.GetLogger(ctx).Warn("Failed to encode progress report", "error", err)
		return
	}
	data := buf.Bytes()
	info := workflow.GetInfo(ctx)
	workflowID := info.WorkflowExecution.ID
	runID := progressRunStamp(info)
	localCtx := WithProgressLocalActivityOptions(ctx)
	if err := workflow.ExecuteLocalActivity(localCtx, ((*ProgressActivities)(nil)).Save, workflowID, runID, data).Get(ctx, nil); err != nil {
		workflow.GetLogger(ctx).Warn("Failed to save progress report to Redis", "error", err)
	}
}

// progressRunStamp is the run identity a workflow stamps on its saved
// reports, compared by the API's cache.DeleteStaleProgressReport against
// the run ExecuteWorkflow returned. FirstRunID, not the current RunID: it
// survives ContinueAsNew, so the whole chain stamps its reports identically
// and a continued run's report is never mistaken for a previous start's.
// Falls back to the current RunID where FirstRunID is unset (the SDK test
// environment); an empty stamp would read as stale on every start.
func progressRunStamp(info *workflow.Info) string {
	return ChainRunID(info)
}

// ChainRunID identifies a workflow's ContinueAsNew chain: its FirstRunID,
// which every continuation shares and every new start changes, or the
// current RunID where FirstRunID is unset (the SDK test environment).
// Workflow code may call it: both come from workflow.GetInfo.
func ChainRunID(info *workflow.Info) string {
	if info.FirstRunID != "" {
		return info.FirstRunID
	}
	return info.WorkflowExecution.RunID
}

// StartGroup marks a group as started with the current timestamp and saves to Redis.
func (t *ReportTracker) StartGroup(ctx workflow.Context, groupIdx int) {
	g := t.group(groupIdx)
	g.StartedAt = workflow.Now(ctx).UnixMilli()
	t.Save(ctx)
}

// CompleteGroup marks a group as completed with message and timestamp.
// Sets all bars to complete, recomputes the report's Completed count, and saves to Redis.
func (t *ReportTracker) CompleteGroup(ctx workflow.Context, groupIdx int, msg string) {
	g := t.group(groupIdx)
	for i := range g.Bars {
		g.Bars[i].Current = g.Bars[i].Total
	}
	g.CompletedAt = workflow.Now(ctx).UnixMilli()
	g.CompletedMsg = msg
	t.report.Completed = t.completedFromBars()
	t.Save(ctx)
}

// SetMessage records a workflow-level status message and persists the report.
// Use it for selection outcomes and failure context that apply across groups.
func (t *ReportTracker) SetMessage(ctx workflow.Context, msg string) {
	t.report.Message = msg
	t.Save(ctx)
}

// completedFromBars computes Completed by summing all bar currents across all groups.
func (t *ReportTracker) completedFromBars() int {
	completed := 0
	for _, g := range t.report.Groups {
		for _, bar := range g.Bars {
			completed += bar.Current
		}
	}
	return completed
}

// LoadReportTracker loads a ProgressReport from Redis and returns a tracker wrapping it.
// The workflow ID is taken from the workflow execution info. This is used after ContinueAsNew
// so the new execution inherits the progress state persisted by the prior execution.
// Returns an error if Redis has no report for this workflow ID.
func LoadReportTracker(ctx workflow.Context) (*ReportTracker, error) {
	workflowID := workflow.GetInfo(ctx).WorkflowExecution.ID
	localCtx := WithProgressLocalActivityOptions(ctx)
	var data []byte
	if err := workflow.ExecuteLocalActivity(localCtx, ((*ProgressActivities)(nil)).Load, workflowID).Get(ctx, &data); err != nil {
		return nil, fmt.Errorf("load progress report from redis: %w", err)
	}
	if data == nil {
		return nil, fmt.Errorf("no progress report found in redis for workflow %s", workflowID)
	}
	var report ProgressReport
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&report); err != nil {
		return nil, fmt.Errorf("decode progress report: %w", err)
	}
	return &ReportTracker{report: &report}, nil
}

// IncrementBar increments the current count of a specific bar and saves to Redis.
func (t *ReportTracker) IncrementBar(ctx workflow.Context, ref BarRef) {
	t.incrementBarByNoSave(ref, 1)
	t.Save(ctx)
}

// IncrementBarBy increments the current count of a specific bar by the given amount and saves to Redis.
func (t *ReportTracker) IncrementBarBy(ctx workflow.Context, ref BarRef, amount int) {
	t.incrementBarByNoSave(ref, amount)
	t.Save(ctx)
}

func (t *ReportTracker) incrementBarByNoSave(ref BarRef, amount int) {
	t.bar(ref).Current += amount
}

// IncrementBars increments several bars of a group by one each and saves to
// Redis once. Use it when one unit of work counts toward several bars.
func (t *ReportTracker) IncrementBars(ctx workflow.Context, groupIdx int, barIdxs []int) {
	t.incrementBarsNoSave(groupIdx, barIdxs)
	t.Save(ctx)
}

func (t *ReportTracker) incrementBarsNoSave(groupIdx int, barIdxs []int) {
	t.group(groupIdx) // panics on a bad group even when no bar is named
	for _, barIdx := range barIdxs {
		t.bar(BarRef{Group: groupIdx, Bar: barIdx}).Current++
	}
}

// Bar returns a copy of a specific bar.
func (t *ReportTracker) Bar(ref BarRef) ProgressBar {
	return *t.bar(ref)
}

// GroupBarTotals returns the Total of every bar in a group, in order.
func (t *ReportTracker) GroupBarTotals(groupIdx int) []int {
	bars := t.group(groupIdx).Bars
	totals := make([]int, len(bars))
	for i, bar := range bars {
		totals[i] = bar.Total
	}
	return totals
}

// SetBarTotal sets the total for a specific bar and nothing else: unlike
// AddBar it does not refresh the report-level Total (call RecalcTotal after
// the last SetBarTotal), and unlike IncrementBar it does not save (the next
// saving call, or an explicit Save, writes the new size to Redis). For one
// bar resized at runtime, use ResizeBar; SetBarTotal is for resizing several
// bars before a single RecalcTotal and save.
func (t *ReportTracker) SetBarTotal(ref BarRef, total int) {
	t.bar(ref).Total = total
}

// ResizeBar sets a bar's Total, refreshes the report Total and saves.
func (t *ReportTracker) ResizeBar(ctx workflow.Context, ref BarRef, total int) {
	t.SetBarTotal(ref, total)
	t.RecalcTotal()
	t.Save(ctx)
}

// RecalcTotal recomputes the report-level Total from all bar totals.
// Call after the last SetBarTotal when several bars are sized at runtime
// (e.g., one bar per Yahoo league); ResizeBar calls it for a single bar.
func (t *ReportTracker) RecalcTotal() {
	t.report.Total = t.totalFromBars()
}

// SetBarLabel sets the label for a specific bar.
func (t *ReportTracker) SetBarLabel(ref BarRef, label string) {
	t.bar(ref).Label = label
}

// StartBar marks a bar as started and saves to Redis.
func (t *ReportTracker) StartBar(ctx workflow.Context, ref BarRef) {
	t.startBarNoSave(ref)
	t.Save(ctx)
}

func (t *ReportTracker) startBarNoSave(ref BarRef) {
	t.bar(ref).Started = true
}

// CompleteBar marks a specific bar as complete (sets Current = Total) and saves to Redis.
func (t *ReportTracker) CompleteBar(ctx workflow.Context, ref BarRef) {
	t.completeBarNoSave(ref)
	t.Save(ctx)
}

func (t *ReportTracker) completeBarNoSave(ref BarRef) {
	bar := t.bar(ref)
	bar.Current = bar.Total
}

// BarRange is a contiguous run of Count bars of one group, from Start.
type BarRange struct {
	Start int
	Count int
}

// StartBars marks every bar of a range as started and saves to Redis once.
func (t *ReportTracker) StartBars(ctx workflow.Context, groupIdx int, bars BarRange) {
	t.startBarsNoSave(groupIdx, bars)
	t.Save(ctx)
}

func (t *ReportTracker) startBarsNoSave(groupIdx int, bars BarRange) {
	t.group(groupIdx) // panics on a bad group even when no bar is named
	for i := bars.Start; i < bars.Start+bars.Count; i++ {
		t.bar(BarRef{Group: groupIdx, Bar: i}).Started = true
	}
}

// CompleteBars sets Current = Total on every bar of a range and saves to
// Redis once.
func (t *ReportTracker) CompleteBars(ctx workflow.Context, groupIdx int, bars BarRange) {
	t.completeBarsNoSave(groupIdx, bars)
	t.Save(ctx)
}

func (t *ReportTracker) completeBarsNoSave(groupIdx int, bars BarRange) {
	t.group(groupIdx) // panics on a bad group even when no bar is named
	for i := bars.Start; i < bars.Start+bars.Count; i++ {
		bar := t.bar(BarRef{Group: groupIdx, Bar: i})
		bar.Current = bar.Total
	}
}

// ProgressSourceKeyFunc maps a season to the Redis key under which its live
// progress report is stored (either a real child workflow ID or a synthetic
// key written by an activity via SaveActivityProgress).
type ProgressSourceKeyFunc func(startYear int) string

// ChildWorkflowStarter starts a child workflow for a season and returns its future.
// Each caller constructs its own input, child options, and workflow ID.
// isLatest is true for the last season in the run's manifest — the season still
// accruing data — so starters can scope refresh-style flags to it without
// consulting the calendar.
type ChildWorkflowStarter func(ctx workflow.Context, season nhl.SeasonInfo, isLatest bool) workflow.ChildWorkflowFuture

// AddBarsForSeasons adds one bar per season to a group.
// If sourceKeyFunc is provided, sets each bar's ProgressSourceKey so the resolver
// can merge per-season progress from Redis at query time.
// Returns a map of startYear -> barIdx for looking up bars later.
func (t *ReportTracker) AddBarsForSeasons(ctx workflow.Context, groupIdx int, seasons []nhl.SeasonInfo, counter SeasonCounterFunc, sourceKeyFunc ProgressSourceKeyFunc) (map[int]int, error) {
	g := t.group(groupIdx)
	barIndex := make(map[int]int)

	for i, season := range seasons {
		count, err := counter(ctx, season)
		if err != nil {
			return nil, err
		}
		bar := ProgressBar{
			Label: season.Label(),
			Total: count,
		}
		if sourceKeyFunc != nil {
			bar.ProgressSourceKey = sourceKeyFunc(season.ID.StartYear())
		}
		g.Bars = append(g.Bars, bar)
		barIndex[season.ID.StartYear()] = i
	}

	t.report.Total = t.totalFromBars()
	return barIndex, nil
}

// AddBar appends a bar to a group and refreshes the report-level Total.
// Use it when bars are derived from runtime input (e.g. one per Yahoo season).
func (t *ReportTracker) AddBar(groupIdx int, bar ProgressBar) {
	g := t.group(groupIdx)
	g.Bars = append(g.Bars, bar)
	t.report.Total = t.totalFromBars()
}

// totalFromBars computes Total by summing all bar totals across all groups.
func (t *ReportTracker) totalFromBars() int {
	total := 0
	for _, g := range t.report.Groups {
		for _, bar := range g.Bars {
			total += bar.Total
		}
	}
	return total
}

// GetElapsed returns the formatted elapsed time since group started.
func (t *ReportTracker) GetElapsed(ctx workflow.Context, groupIdx int) string {
	startedAt := t.group(groupIdx).StartedAt
	if startedAt == 0 {
		return ""
	}
	elapsed := workflow.Now(ctx).UnixMilli() - startedAt
	return FormatElapsedMilli(elapsed)
}

// FormatElapsedMilli formats milliseconds as a human-readable duration.
func FormatElapsedMilli(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	return FormatDuration(d)
}

// FormatDuration formats a duration in a human-readable way.
func FormatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
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

// SaveActivityProgress saves a simple current/total ProgressReport to Redis from an activity.
// This allows activities (not just workflows) to publish progress in the ProgressReport format
// that the resolver can read via mergeChildWorkflowProgress.
//
// startedAt is a UnixMilli timestamp captured once per activity invocation and passed
// in unchanged on every call, so the field stays stable across loop iterations. On
// activity retry the caller captures a fresh value, which matches the reset of
// per-iteration counters (Temporal re-runs the function from the top on retry).
//
// The report is saved without a run stamp: these synthetic keys are cleared
// only by the parent workflow (cleanupStaleChildReports), which runs before
// the activity can write, so the API's run-aware cleanup never sees them.
func SaveActivityProgress(ctx context.Context, client *redis.Client, workflowID string, startedAt int64, current, total int) error {
	report := &ProgressReport{
		Total:     total,
		Completed: current,
		Groups: []ProgressGroup{
			{Bars: []ProgressBar{{Current: current, Total: total}}, StartedAt: startedAt},
		},
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(report); err != nil {
		return fmt.Errorf("encode activity progress: %w", err)
	}
	const noRunStamp = ""
	return cache.SaveProgressReport(ctx, client, workflowID, noRunStamp, buf.Bytes())
}

// SeasonCounterFunc returns the total count for a season's progress tracking.
// Accepts workflow.Context to enable deterministic time calculation using workflow.Now().
type SeasonCounterFunc func(ctx workflow.Context, season nhl.SeasonInfo) (int, error)
