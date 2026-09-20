package shared

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
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

// Save persists the current ProgressReport to Redis via a local activity.
// Call at key structural changes (group start, group complete) so the resolver
// can read progress without querying Temporal.
func (t *ReportTracker) Save(ctx workflow.Context) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(t.report); err != nil {
		workflow.GetLogger(ctx).Warn("Failed to encode progress report", "error", err)
		return
	}
	data := buf.Bytes()
	info := workflow.GetInfo(ctx)
	workflowID := info.WorkflowExecution.ID
	runID := progressRunStamp(info)
	localCtx := workflow.WithLocalActivityOptions(ctx, workflow.LocalActivityOptions{
		ScheduleToCloseTimeout: 5 * time.Second,
	})
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
	if info.FirstRunID != "" {
		return info.FirstRunID
	}
	return info.WorkflowExecution.RunID
}

// StartGroup marks a group as started with the current timestamp and saves to Redis.
func (t *ReportTracker) StartGroup(ctx workflow.Context, groupIdx int) {
	t.report.Groups[groupIdx].StartedAt = workflow.Now(ctx).UnixMilli()
	t.Save(ctx)
}

// CompleteGroup marks a group as completed with message and timestamp.
// Sets all bars to complete, recomputes the report's Completed count, and saves to Redis.
func (t *ReportTracker) CompleteGroup(ctx workflow.Context, groupIdx int, msg string) {
	g := &t.report.Groups[groupIdx]
	for i := range g.Bars {
		g.Bars[i].Current = g.Bars[i].Total
	}
	g.CompletedAt = workflow.Now(ctx).UnixMilli()
	g.CompletedMsg = msg
	t.report.Completed = t.completedFromBars()
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
	localCtx := workflow.WithLocalActivityOptions(ctx, workflow.LocalActivityOptions{
		ScheduleToCloseTimeout: 5 * time.Second,
	})
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
func (t *ReportTracker) IncrementBar(ctx workflow.Context, groupIdx, barIdx int) {
	t.report.Groups[groupIdx].Bars[barIdx].Current++
	t.Save(ctx)
}

// IncrementBarBy increments the current count of a specific bar by the given amount and saves to Redis.
func (t *ReportTracker) IncrementBarBy(ctx workflow.Context, groupIdx, barIdx, amount int) {
	t.report.Groups[groupIdx].Bars[barIdx].Current += amount
	t.Save(ctx)
}

// SetBarTotal sets the total for a specific bar.
func (t *ReportTracker) SetBarTotal(groupIdx, barIdx, total int) {
	t.report.Groups[groupIdx].Bars[barIdx].Total = total
}

// RecalcTotal recomputes the report-level Total from all bar totals.
// Call after SetBarTotal when a bar's size is only known at runtime
// (e.g., per-team playoff bars sized by a season's team count).
func (t *ReportTracker) RecalcTotal() {
	t.report.Total = t.totalFromBars()
}

// SetBarLabel sets the label for a specific bar.
func (t *ReportTracker) SetBarLabel(groupIdx, barIdx int, label string) {
	t.report.Groups[groupIdx].Bars[barIdx].Label = label
}

// StartBar marks a bar as started and saves to Redis.
func (t *ReportTracker) StartBar(ctx workflow.Context, groupIdx, barIdx int) {
	t.report.Groups[groupIdx].Bars[barIdx].Started = true
	t.Save(ctx)
}

// CompleteBar marks a specific bar as complete (sets Current = Total) and saves to Redis.
func (t *ReportTracker) CompleteBar(ctx workflow.Context, groupIdx, barIdx int) {
	bar := &t.report.Groups[groupIdx].Bars[barIdx]
	bar.Current = bar.Total
	t.Save(ctx)
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
		t.report.Groups[groupIdx].Bars = append(t.report.Groups[groupIdx].Bars, bar)
		barIndex[season.ID.StartYear()] = i
	}

	t.report.Total = t.totalFromBars()
	return barIndex, nil
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
	startedAt := t.report.Groups[groupIdx].StartedAt
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
