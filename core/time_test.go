package core

import (
	"testing"
	"time"
)

func TestCountDays(t *testing.T) {
	tests := []struct {
		name  string
		start time.Time
		end   time.Time
		want  int
	}{
		{
			name:  "same day",
			start: time.Date(2024, 10, 15, 0, 0, 0, 0, time.UTC),
			end:   time.Date(2024, 10, 15, 23, 59, 59, 0, time.UTC),
			want:  1,
		},
		{
			name:  "two consecutive days",
			start: time.Date(2024, 10, 15, 0, 0, 0, 0, time.UTC),
			end:   time.Date(2024, 10, 16, 0, 0, 0, 0, time.UTC),
			want:  2,
		},
		{
			name:  "full week",
			start: time.Date(2024, 10, 1, 0, 0, 0, 0, time.UTC),
			end:   time.Date(2024, 10, 7, 0, 0, 0, 0, time.UTC),
			want:  7,
		},
		{
			name:  "month span",
			start: time.Date(2024, 10, 1, 0, 0, 0, 0, time.UTC),
			end:   time.Date(2024, 10, 31, 0, 0, 0, 0, time.UTC),
			want:  31,
		},
		{
			name:  "cross year boundary",
			start: time.Date(2024, 12, 30, 0, 0, 0, 0, time.UTC),
			end:   time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
			want:  4,
		},
		{
			name:  "end before start",
			start: time.Date(2024, 10, 15, 0, 0, 0, 0, time.UTC),
			end:   time.Date(2024, 10, 10, 0, 0, 0, 0, time.UTC),
			want:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CountDays(tt.start, tt.end)
			if got != tt.want {
				t.Errorf("CountDays() = %d, want %d", got, tt.want)
			}
		})
	}
}
