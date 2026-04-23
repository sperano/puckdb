package shared

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestFormatDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		d        time.Duration
		expected string
	}{
		{
			name:     "zero duration shows milliseconds",
			d:        0,
			expected: "0ms",
		},
		{
			name:     "500 ms shows milliseconds",
			d:        500 * time.Millisecond,
			expected: "500ms",
		},
		{
			name:     "999 ms stays in milliseconds (below 1 second)",
			d:        999 * time.Millisecond,
			expected: "999ms",
		},
		{
			name:     "exactly 1 second shows seconds",
			d:        time.Second,
			expected: "1.0s",
		},
		{
			name:     "3.5 seconds shows one decimal",
			d:        3500 * time.Millisecond,
			expected: "3.5s",
		},
		{
			name:     "59 seconds stays in seconds (below 1 minute)",
			d:        59 * time.Second,
			expected: "59.0s",
		},
		{
			name:     "exactly 1 minute shows Xm Ys format",
			d:        time.Minute,
			expected: "1m0s",
		},
		{
			name:     "1 minute 30 seconds shows Xm Ys format",
			d:        90 * time.Second,
			expected: "1m30s",
		},
		{
			name:     "59 minutes 59 seconds stays in minutes format",
			d:        59*time.Minute + 59*time.Second,
			expected: "59m59s",
		},
		{
			name:     "exactly 1 hour shows XhYm format",
			d:        time.Hour,
			expected: "1h0m",
		},
		{
			name:     "1 hour 30 minutes shows XhYm format",
			d:        90 * time.Minute,
			expected: "1h30m",
		},
		{
			name:     "2 hours 5 minutes shows XhYm format",
			d:        2*time.Hour + 5*time.Minute,
			expected: "2h5m",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, FormatDuration(tt.d))
		})
	}
}

func TestFormatElapsedMilli(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		ms       int64
		expected string
	}{
		{
			name:     "zero milliseconds",
			ms:       0,
			expected: "0ms",
		},
		{
			name:     "750 milliseconds",
			ms:       750,
			expected: "750ms",
		},
		{
			name:     "5000 ms is 5 seconds",
			ms:       5000,
			expected: "5.0s",
		},
		{
			name:     "90000 ms is 1 minute 30 seconds",
			ms:       90_000,
			expected: "1m30s",
		},
		{
			name:     "3600000 ms is 1 hour",
			ms:       3_600_000,
			expected: "1h0m",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, FormatElapsedMilli(tt.ms))
		})
	}
}

func TestSetBarTotal(t *testing.T) {
	t.Parallel()

	report := &ProgressReport{
		Groups: []ProgressGroup{
			{
				Bars: []ProgressBar{
					{Label: "season 1", Total: 0},
					{Label: "season 2", Total: 0},
				},
			},
		},
	}
	tracker := &ReportTracker{report: report}

	tracker.SetBarTotal(0, 0, 42)
	tracker.SetBarTotal(0, 1, 7)

	assert.Equal(t, 42, report.Groups[0].Bars[0].Total)
	assert.Equal(t, 7, report.Groups[0].Bars[1].Total)
}

func TestSetBarLabel(t *testing.T) {
	t.Parallel()

	report := &ProgressReport{
		Groups: []ProgressGroup{
			{
				Bars: []ProgressBar{
					{Label: "original", Total: 10},
				},
			},
		},
	}
	tracker := &ReportTracker{report: report}

	tracker.SetBarLabel(0, 0, "updated label")

	assert.Equal(t, "updated label", report.Groups[0].Bars[0].Label)
}

