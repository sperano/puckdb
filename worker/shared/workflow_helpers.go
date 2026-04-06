package shared

import (
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/core"
	"github.com/spf13/viper"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// WithChildOptions returns a workflow context with child workflow options set.
func WithChildOptions(ctx workflow.Context, id string) workflow.Context {
	return workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowExecutionTimeout: time.Duration(ViperIntOrDefault(config.FlagWorkflowExecutionTimeout, config.DefaultWorkflowExecutionTimeout)) * time.Minute,
		WorkflowTaskTimeout:      config.DefaultWorkflowTaskTimeout,
		WorkflowID:               id,
		// Allow terminating orphaned child workflows from previous failed parent runs
		WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_TERMINATE_IF_RUNNING,
	})
}

// DefaultActivityOptions returns the default activity options configured via viper.
func DefaultActivityOptions() workflow.ActivityOptions {
	return workflow.ActivityOptions{
		StartToCloseTimeout: time.Duration(ViperIntOrDefault(config.FlagActivityStartToCloseTimeout, config.DefaultActivityStartToCloseTimeout)) * time.Minute,
		HeartbeatTimeout:    time.Duration(ViperIntOrDefault(config.FlagActivityHeartbeatTimeout, config.DefaultActivityHeartbeatTimeout)) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Duration(ViperIntOrDefault(config.FlagTemporalRetryInitialInterval, config.DefaultTemporalRetryInitialInterval)) * time.Second,
			MaximumInterval:    time.Duration(ViperIntOrDefault(config.FlagTemporalRetryMaxInterval, config.DefaultTemporalRetryMaxInterval)) * time.Second,
			MaximumAttempts:    int32(ViperIntOrDefault(config.FlagTemporalRetryMaxAttempts, config.DefaultTemporalRetryMaxAttempts)),
			BackoffCoefficient: config.DefaultBackoffCoefficient,
		},
	}
}

// ViperIntOrDefault returns the viper value for key, or fallback if viper returns 0.
func ViperIntOrDefault(key string, fallback int) int {
	if v := viper.GetInt(key); v > 0 {
		return v
	}
	return fallback
}

// FetchDayActivityOptions returns activity options with longer timeout for FetchDayActivity.
// This activity downloads multiple files with rate limiting, requiring more time.
func FetchDayActivityOptions() workflow.ActivityOptions {
	opts := DefaultActivityOptions()
	opts.StartToCloseTimeout = time.Duration(ViperIntOrDefault(config.FlagFetchDayActivityTimeout, config.DefaultFetchDayActivityTimeout)) * time.Minute
	return opts
}

// EffectiveEndDate returns the end date or today, whichever is earlier.
// Used to avoid processing future dates.
func EffectiveEndDate(end time.Time) time.Time {
	if end.After(time.Now()) {
		return time.Now()
	}
	return end
}

// CountDaysInSeason returns the number of days from season start to min(season end, today).
func CountDaysInSeason(season nhl.SeasonInfo) (int, error) {
	return core.CountDays(season.StandingsStart.Time, EffectiveEndDate(season.StandingsEnd.Time)), nil
}

// GetDayConcurrency returns the configured day concurrency for parallel processing.
func GetDayConcurrency() int {
	concurrency := viper.GetInt(config.FlagDayConcurrency)
	if concurrency <= 0 {
		return config.DefaultDayConcurrency
	}
	return concurrency
}
