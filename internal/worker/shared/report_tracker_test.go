package shared

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeReport builds a ProgressReport with the specified bar layout for tests.
// groups is a slice where each element specifies how many bars that group should have.
func makeReport(groupBarCounts ...int) *ProgressReport {
	groups := make([]ProgressGroup, len(groupBarCounts))
	for i, n := range groupBarCounts {
		bars := make([]ProgressBar, n)
		for j := range bars {
			bars[j] = ProgressBar{Total: 10}
		}
		groups[i] = ProgressGroup{Bars: bars}
	}
	return &ProgressReport{Groups: groups}
}

// --- completedFromBars ---

func TestCompletedFromBars_AllZero(t *testing.T) {
	t.Parallel()

	report := makeReport(3, 2) // two groups, bars all at Current=0
	tracker := &ReportTracker{report: report}

	assert.Equal(t, 0, tracker.completedFromBars())
}

func TestCompletedFromBars_SumsAcrossGroups(t *testing.T) {
	t.Parallel()

	report := &ProgressReport{
		Groups: []ProgressGroup{
			{Bars: []ProgressBar{{Current: 3, Total: 10}, {Current: 7, Total: 10}}},
			{Bars: []ProgressBar{{Current: 5, Total: 10}}},
		},
	}
	tracker := &ReportTracker{report: report}

	assert.Equal(t, 15, tracker.completedFromBars())
}

func TestCompletedFromBars_EmptyGroups(t *testing.T) {
	t.Parallel()

	tracker := &ReportTracker{report: &ProgressReport{}}

	assert.Equal(t, 0, tracker.completedFromBars())
}

// --- totalFromBars ---

func TestTotalFromBars_SumsAllTotals(t *testing.T) {
	t.Parallel()

	report := &ProgressReport{
		Groups: []ProgressGroup{
			{Bars: []ProgressBar{{Total: 10}, {Total: 20}}},
			{Bars: []ProgressBar{{Total: 30}}},
		},
	}
	tracker := &ReportTracker{report: report}

	assert.Equal(t, 60, tracker.totalFromBars())
}

func TestTotalFromBars_Empty(t *testing.T) {
	t.Parallel()

	tracker := &ReportTracker{report: &ProgressReport{}}

	assert.Equal(t, 0, tracker.totalFromBars())
}

// --- SetBarTotal and SetBarLabel (already partially tested, extend for multi-group) ---

func TestSetBarTotal_MultiGroup(t *testing.T) {
	t.Parallel()

	report := makeReport(2, 3)
	tracker := &ReportTracker{report: report}

	tracker.SetBarTotal(0, 1, 99)
	tracker.SetBarTotal(1, 2, 77)

	assert.Equal(t, 99, report.Groups[0].Bars[1].Total)
	assert.Equal(t, 77, report.Groups[1].Bars[2].Total)
}

// --- CompleteGroup (pure state mutation, no workflow.Context needed for invariant tests) ---

// Since CompleteGroup calls workflow.Now(ctx) and Save(ctx), we test the pure
// sub-parts: completedFromBars recalculation after bar states are set.

func TestCompletedFromBars_AfterManualCompletion(t *testing.T) {
	t.Parallel()

	report := &ProgressReport{
		Groups: []ProgressGroup{
			{Bars: []ProgressBar{{Current: 0, Total: 5}, {Current: 0, Total: 3}}},
		},
	}
	tracker := &ReportTracker{report: report}

	// Manually simulate what CompleteGroup does to the bars.
	for i := range report.Groups[0].Bars {
		report.Groups[0].Bars[i].Current = report.Groups[0].Bars[i].Total
	}

	assert.Equal(t, 8, tracker.completedFromBars())
}

// --- ReportTracker holds a live reference ---

func TestReportTracker_LiveReference(t *testing.T) {
	t.Parallel()

	report := &ProgressReport{
		Groups: []ProgressGroup{
			{Bars: []ProgressBar{{Label: "a", Total: 5}}},
		},
	}

	tracker := &ReportTracker{report: report}
	require.NotNil(t, tracker)

	// Mutations through SetBarTotal are reflected in the original pointer.
	tracker.SetBarTotal(0, 0, 99)
	assert.Equal(t, 99, report.Groups[0].Bars[0].Total)
}
