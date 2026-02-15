package cmd

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestFormatElapsed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		duration time.Duration
		expected string
	}{
		{
			name:     "under one second",
			duration: 500 * time.Millisecond,
			expected: "0.5s",
		},
		{
			name:     "seconds",
			duration: 45*time.Second + 300*time.Millisecond,
			expected: "45.3s",
		},
		{
			name:     "one minute",
			duration: 60 * time.Second,
			expected: "1m0s",
		},
		{
			name:     "minutes and seconds",
			duration: 2*time.Minute + 10*time.Second,
			expected: "2m10s",
		},
		{
			name:     "one hour",
			duration: 60 * time.Minute,
			expected: "1h0m",
		},
		{
			name:     "hours and minutes",
			duration: 1*time.Hour + 30*time.Minute,
			expected: "1h30m",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatElapsed(tt.duration)
			assert.Equal(t, tt.expected, result)
		})
	}
}
