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

// EffectiveEndDate returns the end date or yesterday, whichever is earlier.
// We stop at yesterday to ensure all games on the date are final before processing.
// Games in progress at fetch time would be skipped and permanently missed from the database.
// Uses workflow.Now(ctx) to ensure determinism on workflow replay.
func EffectiveEndDate(ctx workflow.Context, end time.Time) time.Time {
	yesterday := workflow.Now(ctx).AddDate(0, 0, -1)
	if end.After(yesterday) {
		return yesterday
	}
	return end
}

// CountDaysInSeason returns the number of days from season start to min(season end, today).
// Uses workflow.Now(ctx) to ensure determinism on workflow replay.
func CountDaysInSeason(ctx workflow.Context, season nhl.SeasonInfo) (int, error) {
	return core.CountDays(season.StandingsStart.Time, EffectiveEndDate(ctx, season.StandingsEnd.Time)), nil
}

// PlayoffProgressSteps is the number of extra progress steps for playoff processing.
const PlayoffProgressSteps = 1

// CountDaysWithPlayoffs returns CountDaysInSeason + 1 for the playoff processing step.
// Used by parent workflows (FetchSeasons, ImportSeasons) so the parent bar total
// accounts for both regular-season days and the playoff activity.
// Uses workflow.Now(ctx) to ensure determinism on workflow replay.
func CountDaysWithPlayoffs(ctx workflow.Context, season nhl.SeasonInfo) (int, error) {
	days, err := CountDaysInSeason(ctx, season)
	return days + PlayoffProgressSteps, err
}

// IsCurrentSeason returns true if the given start year represents the current NHL season.
// TODO: add IsCurrentSeasonInProgress = IsCurrentSeason && no Stanley Cup winner yet.
// That would let us skip cache invalidation once the season is truly over.
func IsCurrentSeason(startYear int) bool {
	return nhl.Current().StartYear() == startYear
}

// GetDayConcurrency returns the configured day concurrency for parallel processing.
func GetDayConcurrency() int {
	return ViperIntOrDefault(config.FlagDayConcurrency, config.DefaultDayConcurrency)
}
