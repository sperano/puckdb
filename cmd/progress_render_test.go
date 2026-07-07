package cmd

import (
	"strings"
	"testing"

	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/graph/model"
)

func TestRenderProgressBar_EdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		pct   float64
		width int
		check func(t *testing.T, bar string)
	}{
		{
			name:  "0 percent all empty",
			pct:   0,
			width: 80,
			check: func(t *testing.T, bar string) {
				if !strings.HasPrefix(bar, "[") || !strings.HasSuffix(bar, "]") {
					t.Errorf("bar should be wrapped in brackets: %q", bar)
				}
				// Strip sentinels and brackets to check fill characters
				stripped := stripProgressSentinels(bar)
				if strings.Contains(stripped, "█") {
					t.Errorf("0%% bar should have no filled blocks: %q", stripped)
				}
			},
		},
		{
			name:  "100 percent all filled",
			pct:   100,
			width: 80,
			check: func(t *testing.T, bar string) {
				stripped := stripProgressSentinels(bar)
				if strings.Contains(stripped, "░") {
					t.Errorf("100%% bar should have no empty blocks: %q", stripped)
				}
			},
		},
		{
			name:  "50 percent half filled",
			pct:   50,
			width: 80,
			check: func(t *testing.T, bar string) {
				stripped := stripProgressSentinels(bar)
				filled := strings.Count(stripped, "█")
				empty := strings.Count(stripped, "░")
				if filled != 40 {
					t.Errorf("50%% bar should have 40 filled blocks, got %d", filled)
				}
				if empty != 40 {
					t.Errorf("50%% bar should have 40 empty blocks, got %d", empty)
				}
			},
		},
		{
			name:  "negative percent clamps to zero",
			pct:   -10,
			width: 80,
			check: func(t *testing.T, bar string) {
				stripped := stripProgressSentinels(bar)
				if strings.Contains(stripped, "█") {
					t.Errorf("negative %% bar should have no filled blocks: %q", stripped)
				}
			},
		},
		{
			name:  "over 100 percent clamps to full",
			pct:   150,
			width: 80,
			check: func(t *testing.T, bar string) {
				stripped := stripProgressSentinels(bar)
				if strings.Contains(stripped, "░") {
					t.Errorf("150%% bar should have no empty blocks: %q", stripped)
				}
			},
		},
		{
			name:  "contains gradient shade sentinels",
			pct:   75,
			width: 80,
			check: func(t *testing.T, bar string) {
				for _, shade := range progressShades {
					if !strings.Contains(bar, shade) {
						t.Errorf("bar should contain shade sentinel %q", shade)
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			bar := renderProgressBar(tt.pct, tt.width)
			tt.check(t, bar)
		})
	}
}

// stripProgressSentinels removes shade control characters from a progress bar string.
func stripProgressSentinels(s string) string {
	for _, shade := range progressShades {
		s = strings.ReplaceAll(s, shade, "")
	}
	s = strings.ReplaceAll(s, ProgressShadeEnd, "")
	return s
}

func TestFormatLabelArea(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		label   string
		current int
		total   int
		check   func(t *testing.T, result string)
	}{
		{
			name:    "no label right-aligns progress",
			label:   "",
			current: 5,
			total:   10,
			check: func(t *testing.T, result string) {
				if len(result) != config.ProgressLabelAreaWidth {
					t.Errorf("expected width %d, got %d: %q", config.ProgressLabelAreaWidth, len(result), result)
				}
				if !strings.HasSuffix(result, "5/10") {
					t.Errorf("should end with 5/10: %q", result)
				}
			},
		},
		{
			name:    "with label left-aligns label and right-aligns progress",
			label:   "2024-25",
			current: 42,
			total:   100,
			check: func(t *testing.T, result string) {
				if !strings.HasPrefix(result, "2024-25") {
					t.Errorf("should start with label: %q", result)
				}
				if !strings.HasSuffix(result, "42/100") {
					t.Errorf("should end with 42/100: %q", result)
				}
			},
		},
		{
			name:    "long label still has at least one space padding",
			label:   "Very Long Label!",
			current: 1,
			total:   2,
			check: func(t *testing.T, result string) {
				if !strings.Contains(result, " 1/2") {
					t.Errorf("should have at least one space before progress: %q", result)
				}
			},
		},
		{
			name:    "Total label",
			label:   "Total",
			current: 13441,
			total:   17406,
			check: func(t *testing.T, result string) {
				if !strings.HasPrefix(result, "Total") {
					t.Errorf("should start with Total: %q", result)
				}
				if !strings.HasSuffix(result, "13441/17406") {
					t.Errorf("should end with 13441/17406: %q", result)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := formatLabelArea(tt.label, tt.current, tt.total)
			tt.check(t, result)
		})
	}
}

func TestFormatProgressReport(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		report *model.ProgressReport
		check  func(t *testing.T, result string)
	}{
		{
			name: "completed group shows checkmark",
			report: &model.ProgressReport{
				Groups: []*model.ProgressGroup{
					{
						Header:       "Fetching data",
						CompletedMsg: "Fetched data in 5s",
						CompletedAt:  1234567890,
						Bars:         []*model.ProgressBar{{Label: ptr("test"), Current: 10, Total: 10, Started: true}},
					},
				},
			},
			check: func(t *testing.T, result string) {
				if !strings.Contains(result, "✓ Fetched data in 5s") {
					t.Errorf("completed group should show checkmark message: %q", result)
				}
			},
		},
		{
			name: "single bar in-progress shows header and progress",
			report: &model.ProgressReport{
				Groups: []*model.ProgressGroup{
					{
						Header: "Importing players",
						Bars:   []*model.ProgressBar{{Current: 50, Total: 200, Started: true}},
					},
				},
			},
			check: func(t *testing.T, result string) {
				if !strings.Contains(result, "▶ Importing players") {
					t.Errorf("should contain header: %q", result)
				}
				if !strings.Contains(result, "50/200") {
					t.Errorf("should contain progress: %q", result)
				}
			},
		},
		{
			name: "single bar with total 1 shows spinner only",
			report: &model.ProgressReport{
				Groups: []*model.ProgressGroup{
					{
						Header: "Initializing",
						Bars:   []*model.ProgressBar{{Current: 0, Total: 1, Started: true}},
					},
				},
			},
			check: func(t *testing.T, result string) {
				if !strings.Contains(result, SpinnerPlaceholder) {
					t.Errorf("should contain spinner placeholder: %q", result)
				}
				if !strings.Contains(result, "Initializing") {
					t.Errorf("should contain header: %q", result)
				}
				// Should NOT contain a progress bar
				if strings.Contains(result, "█") || strings.Contains(result, "░") {
					t.Errorf("total=1 should not show progress bar: %q", result)
				}
			},
		},
		{
			name: "multi-bar shows active bars and total",
			report: &model.ProgressReport{
				Groups: []*model.ProgressGroup{
					{
						Header: "Downloading seasons",
						Bars: []*model.ProgressBar{
							{Label: ptr("2023-24"), Current: 100, Total: 100, Started: true},
							{Label: ptr("2024-25"), Current: 50, Total: 200, Started: true},
							{Label: ptr("2025-26"), Current: 0, Total: 150, Started: false},
						},
					},
				},
			},
			check: func(t *testing.T, result string) {
				if !strings.Contains(result, "▶ Downloading seasons") {
					t.Errorf("should contain header: %q", result)
				}
				// Only the in-progress bar should be shown (completed + not-started are excluded)
				if !strings.Contains(result, "2024-25") {
					t.Errorf("should contain active bar label: %q", result)
				}
				if strings.Contains(result, "2023-24") {
					t.Errorf("should not contain completed bar: %q", result)
				}
				if strings.Contains(result, "2025-26") {
					t.Errorf("should not contain not-started bar: %q", result)
				}
				if !strings.Contains(result, "Total") {
					t.Errorf("should contain total line: %q", result)
				}
			},
		},
		{
			name: "multiple groups rendered in order",
			report: &model.ProgressReport{
				Groups: []*model.ProgressGroup{
					{
						Header:       "Step 1",
						CompletedMsg: "Step 1 done",
						CompletedAt:  1,
						Bars:         []*model.ProgressBar{{Current: 10, Total: 10, Started: true}},
					},
					{
						Header: "Step 2",
						Bars:   []*model.ProgressBar{{Current: 5, Total: 20, Started: true}},
					},
				},
			},
			check: func(t *testing.T, result string) {
				step1Pos := strings.Index(result, "Step 1 done")
				step2Pos := strings.Index(result, "Step 2")
				if step1Pos == -1 || step2Pos == -1 {
					t.Fatalf("missing groups in output: %q", result)
				}
				if step1Pos >= step2Pos {
					t.Errorf("step 1 should appear before step 2: %q", result)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := formatProgressReport(tt.report)
			tt.check(t, result)
		})
	}
}
