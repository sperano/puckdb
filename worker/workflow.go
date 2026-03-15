package worker

import (
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/config"
	"github.com/spf13/viper"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	TaskQueueName             = "puckdb-tasks"
	WorkflowIDImportSeasons   = "import-seasons"
	WorkflowIDFetchSeasons    = "fetch-seasons"
	WorkflowIDFetchPlayerLogs = "fetch-player-logs"
)

func withChildOptions(ctx workflow.Context, id string) workflow.Context {
	return workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowExecutionTimeout: time.Duration(viperIntOrDefault(config.FlagWorkflowExecutionTimeout, config.DefaultWorkflowExecutionTimeout)) * time.Minute,
		WorkflowTaskTimeout:      config.DefaultWorkflowTaskTimeout,
		WorkflowID:               id,
		// Allow terminating orphaned child workflows from previous failed parent runs
		WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_TERMINATE_IF_RUNNING,
	})
}

func defaultActivityOptions() workflow.ActivityOptions {
	return workflow.ActivityOptions{
		StartToCloseTimeout: time.Duration(viperIntOrDefault(config.FlagActivityStartToCloseTimeout, config.DefaultActivityStartToCloseTimeout)) * time.Minute,
		HeartbeatTimeout:    time.Duration(viperIntOrDefault(config.FlagActivityHeartbeatTimeout, config.DefaultActivityHeartbeatTimeout)) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Duration(viperIntOrDefault(config.FlagTemporalRetryInitialInterval, config.DefaultTemporalRetryInitialInterval)) * time.Second,
			MaximumInterval:    time.Duration(viperIntOrDefault(config.FlagTemporalRetryMaxInterval, config.DefaultTemporalRetryMaxInterval)) * time.Second,
			MaximumAttempts:    int32(viperIntOrDefault(config.FlagTemporalRetryMaxAttempts, config.DefaultTemporalRetryMaxAttempts)),
			BackoffCoefficient: config.DefaultBackoffCoefficient,
		},
	}
}

// viperIntOrDefault returns the viper value for key, or fallback if viper returns 0.
func viperIntOrDefault(key string, fallback int) int {
	if v := viper.GetInt(key); v > 0 {
		return v
	}
	return fallback
}

// fetchDayActivityOptions returns activity options with longer timeout for FetchDayActivity.
// This activity downloads multiple files with rate limiting, requiring more time.
func fetchDayActivityOptions() workflow.ActivityOptions {
	opts := defaultActivityOptions()
	opts.StartToCloseTimeout = time.Duration(viperIntOrDefault(config.FlagFetchDayActivityTimeout, config.DefaultFetchDayActivityTimeout)) * time.Minute
	return opts
}

// effectiveEndDate returns the end date or today, whichever is earlier.
// Used to avoid processing future dates.
func effectiveEndDate(end time.Time) time.Time {
	if end.After(time.Now()) {
		return time.Now()
	}
	return end
}

// countDays returns the number of days between start and end (inclusive).
func countDays(start, end time.Time) int {
	days := int(end.Sub(start).Hours()/config.HoursPerDay) + 1
	if days < 0 {
		return 0
	}
	return days
}

// countDaysInSeason returns the number of days from season start to min(season end, today).
func countDaysInSeason(season nhl.SeasonInfo) (int, error) {
	return countDays(season.StandingsStart.Time, effectiveEndDate(season.StandingsEnd.Time)), nil
}

// getDayConcurrency returns the configured day concurrency for parallel processing.
func getDayConcurrency() int {
	concurrency := viper.GetInt(config.FlagDayConcurrency)
	if concurrency <= 0 {
		return config.DefaultDayConcurrency
	}
	return concurrency
}
