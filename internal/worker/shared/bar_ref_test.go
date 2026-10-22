package shared

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.temporal.io/sdk/workflow"
)

// barRefTestReport has three groups of 2, 0 and 4 bars, each bar labeled
// "g<group>b<bar>" so a test can tell which one an accessor returned.
func barRefTestReport() *ProgressReport {
	report := makeReport(2, 0, 4)
	for g := range report.Groups {
		for b := range report.Groups[g].Bars {
			report.Groups[g].Bars[b].Label = fmt.Sprintf("g%db%d", g, b)
		}
	}
	return report
}

func TestBarRefString(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "{group 2, bar 5}", BarRef{Group: 2, Bar: 5}.String())
}

func TestReportTrackerBar_ReturnsReferencedBar(t *testing.T) {
	t.Parallel()

	tests := []struct {
		ref  BarRef
		want string
	}{
		{BarRef{Group: 0, Bar: 0}, "g0b0"},
		{BarRef{Group: 0, Bar: 1}, "g0b1"},
		{BarRef{Group: 2, Bar: 0}, "g2b0"},
		{BarRef{Group: 2, Bar: 3}, "g2b3"},
	}
	for _, tt := range tests {
		t.Run(tt.ref.String(), func(t *testing.T) {
			t.Parallel()
			report := barRefTestReport()
			tracker := &ReportTracker{report: report}

			bar := tracker.bar(tt.ref)
			assert.Equal(t, tt.want, bar.Label)
			assert.Same(t, &report.Groups[tt.ref.Group].Bars[tt.ref.Bar], bar,
				"bar must point into the report, not at a copy")
		})
	}
}

func TestReportTrackerBar_PanicsOutOfRange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ref  BarRef
		want string
	}{
		{"negative group", BarRef{Group: -1, Bar: 0},
			"progress: bar {group -1, bar 0} outside report (3 groups)"},
		{"group past end", BarRef{Group: 3, Bar: 0},
			"progress: bar {group 3, bar 0} outside report (3 groups)"},
		{"negative bar", BarRef{Group: 2, Bar: -1},
			"progress: bar {group 2, bar -1} outside report (3 groups; group 2 has 4 bars)"},
		{"bar past end", BarRef{Group: 2, Bar: 5},
			"progress: bar {group 2, bar 5} outside report (3 groups; group 2 has 4 bars)"},
		{"group without bars", BarRef{Group: 1, Bar: 0},
			"progress: bar {group 1, bar 0} outside report (3 groups; group 1 has 0 bars)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tracker := &ReportTracker{report: barRefTestReport()}

			assert.PanicsWithValue(t, tt.want, func() { tracker.bar(tt.ref) })
		})
	}
}

func TestReportTrackerGroup(t *testing.T) {
	t.Parallel()

	report := barRefTestReport()
	tracker := &ReportTracker{report: report}
	for idx := range report.Groups {
		assert.Same(t, &report.Groups[idx], tracker.group(idx))
	}

	for _, idx := range []int{-1, 3} {
		want := fmt.Sprintf("progress: group %d outside report (3 groups)", idx)
		assert.PanicsWithValue(t, want, func() { tracker.group(idx) })
	}
}

// TestReportTrackerMethods_PanicDescriptively checks that every public
// method reaches the report through bar or group rather than indexing it
// directly. ctx is nil: the reference is checked before anything (Save,
// workflow.Now) uses the context.
func TestReportTrackerMethods_PanicDescriptively(t *testing.T) {
	t.Parallel()

	const (
		badBar   = "progress: bar {group 0, bar 2} outside report (3 groups; group 0 has 2 bars)"
		badGroup = "progress: group 3 outside report (3 groups)"
	)
	var ctx workflow.Context
	bad := BarRef{Group: 0, Bar: 2}
	pastEnd := BarRange{Start: 1, Count: 2}
	tests := []struct {
		name string
		call func(*ReportTracker)
		want string
	}{
		{"IncrementBar", func(tr *ReportTracker) { tr.IncrementBar(ctx, bad) }, badBar},
		{"IncrementBarBy", func(tr *ReportTracker) { tr.IncrementBarBy(ctx, bad, 1) }, badBar},
		{"StartBar", func(tr *ReportTracker) { tr.StartBar(ctx, bad) }, badBar},
		{"CompleteBar", func(tr *ReportTracker) { tr.CompleteBar(ctx, bad) }, badBar},
		{"ResizeBar", func(tr *ReportTracker) { tr.ResizeBar(ctx, bad, 1) }, badBar},
		{"SetBarTotal", func(tr *ReportTracker) { tr.SetBarTotal(bad, 1) }, badBar},
		{"SetBarLabel", func(tr *ReportTracker) { tr.SetBarLabel(bad, "x") }, badBar},
		{"Bar", func(tr *ReportTracker) { tr.Bar(bad) }, badBar},
		{"IncrementBars bad bar", func(tr *ReportTracker) { tr.IncrementBars(ctx, 0, []int{0, 2}) }, badBar},
		{"IncrementBars bad group", func(tr *ReportTracker) { tr.IncrementBars(ctx, 3, nil) }, badGroup},
		{"StartBars bad bar", func(tr *ReportTracker) { tr.StartBars(ctx, 0, pastEnd) }, badBar},
		{"StartBars bad group", func(tr *ReportTracker) { tr.StartBars(ctx, 3, BarRange{}) }, badGroup},
		{"CompleteBars bad bar", func(tr *ReportTracker) { tr.CompleteBars(ctx, 0, pastEnd) }, badBar},
		{"CompleteBars bad group", func(tr *ReportTracker) { tr.CompleteBars(ctx, 3, BarRange{}) }, badGroup},
		{"StartGroup", func(tr *ReportTracker) { tr.StartGroup(ctx, 3) }, badGroup},
		{"CompleteGroup", func(tr *ReportTracker) { tr.CompleteGroup(ctx, 3, "") }, badGroup},
		{"GetElapsed", func(tr *ReportTracker) { tr.GetElapsed(ctx, 3) }, badGroup},
		{"GroupBarTotals", func(tr *ReportTracker) { tr.GroupBarTotals(3) }, badGroup},
		{"AddBar", func(tr *ReportTracker) { tr.AddBar(3, ProgressBar{}) }, badGroup},
		{"AddBarsForSeasons", func(tr *ReportTracker) { _, _ = tr.AddBarsForSeasons(ctx, 3, nil, nil, nil) }, badGroup},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tracker := &ReportTracker{report: barRefTestReport()}

			assert.PanicsWithValue(t, tt.want, func() { tt.call(tracker) })
		})
	}
}
