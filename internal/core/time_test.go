package core

import (
	"testing"
	"time"
)

func TestFormatDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   time.Duration
		want string
	}{
		{name: "negative", in: -500 * time.Millisecond, want: "-500ms"},
		{name: "zero", in: 0, want: "0ms"},
		{name: "last millisecond", in: 999 * time.Millisecond, want: "999ms"},
		{name: "one second", in: time.Second, want: "1.0s"},
		{name: "fractional seconds", in: 45*time.Second + 300*time.Millisecond, want: "45.3s"},
		{name: "last second", in: 59*time.Second + 900*time.Millisecond, want: "59.9s"},
		{name: "one minute", in: time.Minute, want: "1m0s"},
		{name: "last minute", in: 59*time.Minute + 59*time.Second, want: "59m59s"},
		{name: "one hour", in: time.Hour, want: "1h0m"},
		{name: "hours and minutes", in: time.Hour + time.Minute, want: "1h1m"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := FormatDuration(tt.in); got != tt.want {
				t.Errorf("FormatDuration(%s) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

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
