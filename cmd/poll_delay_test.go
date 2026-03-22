package cmd

import (
	"testing"
	"time"

	"github.com/sperano/puckdb/config"
)

func TestPollDelay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                string
		consecutiveFailures int
		want                time.Duration
	}{
		{
			name:                "no failures returns base interval",
			consecutiveFailures: 0,
			want:                config.DefaultWorkflowPollInterval,
		},
		{
			name:                "first failure doubles interval",
			consecutiveFailures: 1,
			want:                config.DefaultWorkflowPollInterval * 2,
		},
		{
			name:                "second failure quadruples interval",
			consecutiveFailures: 2,
			want:                config.DefaultWorkflowPollInterval * 4,
		},
		{
			name:                "third failure 8x interval",
			consecutiveFailures: 3,
			want:                config.DefaultWorkflowPollInterval * 8,
		},
		{
			name:                "high failure count caps at max backoff",
			consecutiveFailures: 10,
			want:                config.MaxWorkflowPollBackoff,
		},
		{
			name:                "very high failure count still caps at max",
			consecutiveFailures: 20,
			want:                config.MaxWorkflowPollBackoff,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := pollDelay(tt.consecutiveFailures)
			if got != tt.want {
				t.Errorf("pollDelay(%d) = %v, want %v", tt.consecutiveFailures, got, tt.want)
			}
		})
	}
}
