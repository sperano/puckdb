package workflow

import (
	"fmt"
	"time"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/sperano/puckdb/internal/news"
	"github.com/sperano/puckdb/internal/worker/newsfeed"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/spf13/viper"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	// newsFetchTimeout bounds one source fetch, including storing its items
	// (the Yahoo status source stores one item per pool player).
	newsFetchTimeout = 5 * time.Minute
	// maxNewsProcessBatches bounds the processing loop of one refresh; what
	// is left is processed by the next refresh.
	maxNewsProcessBatches = 100
	// seasonRolloverMonth is the first month of a new NHL season's year
	// (matches nhl.Current, which cannot be used in workflow code).
	seasonRolloverMonth = time.July
)

// newsConfig is what RefreshNewsWorkflow snapshots at start: the source set
// and the limits that shape which activities run.
type newsConfig struct {
	Sources                  []news.Source     `json:"sources"`
	Err                      string            `json:"err,omitempty"`
	BatchSize                int               `json:"batchSize"`
	IncidentWindowHours      int               `json:"incidentWindowHours"`
	RetentionDays            int               `json:"retentionDays"`
	KeepVersions             int               `json:"keepVersions"`
	FetchMaxAttempts         int               `json:"fetchMaxAttempts"`
	FetchRetryInitialSeconds int               `json:"fetchRetryInitialSeconds"`
	FetchRetryMaxSeconds     int               `json:"fetchRetryMaxSeconds"`
	Extract                  newsExtractConfig `json:"extract"`
}

// loadNewsSources reads the source set; a variable so tests can swap it.
var loadNewsSources = news.LoadSources

func loadNewsConfig() newsConfig {
	cfg := newsConfig{
		BatchSize:                shared.ViperIntOrDefault(config.FlagNewsProcessBatchSize, config.DefaultNewsProcessBatchSize),
		IncidentWindowHours:      shared.ViperIntOrDefault(config.FlagNewsIncidentWindowHours, config.DefaultNewsIncidentWindowHours),
		RetentionDays:            shared.ViperIntOrDefault(config.FlagNewsRetentionDays, config.DefaultNewsRetentionDays),
		KeepVersions:             shared.ViperIntOrDefault(config.FlagNewsKeepVersions, config.DefaultNewsKeepVersions),
		FetchMaxAttempts:         shared.ViperIntOrDefault(config.FlagNewsFetchMaxAttempts, config.DefaultNewsFetchMaxAttempts),
		FetchRetryInitialSeconds: shared.ViperIntOrDefault(config.FlagNewsFetchRetryInitial, config.DefaultNewsFetchRetryInitial),
		FetchRetryMaxSeconds:     shared.ViperIntOrDefault(config.FlagNewsFetchRetryMax, config.DefaultNewsFetchRetryMax),
		Extract:                  loadNewsExtractConfig(),
	}
	sources, err := loadNewsSources(viper.GetString(config.FlagNewsSourcesFile))
	if err != nil {
		cfg.Err = err.Error()
	}
	cfg.Sources = sources
	return cfg
}

// FailedNewsSource is a source whose fetch failed after its retries.
type FailedNewsSource struct {
	SourceID string `json:"sourceId"`
	Error    string `json:"error"`
}

// RefreshNewsResult summarizes one news refresh.
type RefreshNewsResult struct {
	Season      int                 `json:"season"`
	Fetched     []string            `json:"fetched"`
	NotModified []string            `json:"notModified"`
	Failed      []FailedNewsSource  `json:"failed"`
	NotDue      []newsfeed.NotDue   `json:"notDue"`
	Stored      news.IngestResult   `json:"stored"`
	Bodies      news.BodyResult     `json:"bodies"`
	Processed   news.ProcessOutcome `json:"processed"`
	// Extracted is what event extraction did; zero when it is disabled.
	Extracted newsfeed.ExtractResult `json:"extracted"`
	// ExtractError is the failure that stopped extraction early, if any.
	ExtractError string               `json:"extractError,omitempty"`
	Pruned       newsfeed.PruneResult `json:"pruned"`
}

// RefreshNewsWorkflow fetches the due news sources, stores new and changed
// stories, resolves their players into incident candidates, extracts
// validated events with an LLM when enabled, and prunes old news. A source
// that keeps failing is recorded as failed and the others still run; its
// last good data stays in place and the report shows it aging. A failed
// extraction batch is reported in the result and the refresh goes on. The
// workflow fails on invalid configuration or input, or when planning,
// processing or pruning fails; stored news is kept either way.
func RefreshNewsWorkflow(ctx workflow.Context, input *model.RefreshNewsInput) (RefreshNewsResult, error) {
	cfg, err := shared.SnapshotConfig(ctx, loadNewsConfig)
	if err != nil {
		return RefreshNewsResult{}, err
	}
	if cfg.Err != "" {
		return RefreshNewsResult{}, fmt.Errorf("load news sources: %s", cfg.Err)
	}
	if input == nil {
		input = &model.RefreshNewsInput{}
	}
	sources, err := news.EnabledSources(cfg.Sources, input.Sources)
	if err != nil {
		return RefreshNewsResult{}, temporal.NewNonRetryableApplicationError(err.Error(), "InvalidInput", err)
	}
	result := RefreshNewsResult{Season: newsSeason(ctx, input)}
	tracker, err := shared.InitTracker(ctx, &shared.ProgressReport{
		Total:  len(sources),
		Groups: []shared.ProgressGroup{{Header: "Refreshing player news...", Bars: []shared.ProgressBar{{Total: len(sources)}}}},
	})
	if err != nil {
		return result, err
	}
	tracker.StartGroup(ctx, 0)
	ctx = workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())

	var act *newsfeed.Activities
	var plan newsfeed.Plan
	planInput := newsfeed.PlanInput{Sources: sources, Season: result.Season, Force: input.Force != nil && *input.Force}
	if err := workflow.ExecuteActivity(ctx, act.PlanNewsRefresh, planInput).Get(ctx, &plan); err != nil {
		return result, fmt.Errorf("plan news refresh: %w", err)
	}
	result.NotDue = plan.NotDue
	tracker.IncrementBarBy(ctx, 0, 0, len(plan.NotDue))
	fetchNewsSources(ctx, cfg, plan.Due, &result, tracker)
	if err := processNewsVersions(ctx, cfg, &result); err != nil {
		return result, err
	}
	if cfg.Extract.Enabled {
		result.ExtractError = extractNewsEvents(ctx, cfg, &result)
	}
	pruneInput := newsfeed.PruneInput{RetentionDays: cfg.RetentionDays, KeepVersions: cfg.KeepVersions}
	if err := workflow.ExecuteActivity(ctx, act.PruneNews, pruneInput).Get(ctx, &result.Pruned); err != nil {
		return result, fmt.Errorf("prune news: %w", err)
	}
	tracker.CompleteGroup(ctx, 0, newsSummary(result, tracker.GetElapsed(ctx, 0)))
	return result, nil
}

// newsSeason is the requested season, or the one current at the workflow's
// start.
func newsSeason(ctx workflow.Context, input *model.RefreshNewsInput) int {
	if input.Season != nil && *input.Season > 0 {
		return *input.Season
	}
	now := workflow.Now(ctx)
	if now.Month() < seasonRolloverMonth {
		return now.Year() - 1
	}
	return now.Year()
}

// fetchNewsSources fetches the due sources in parallel. A fetch that fails
// after its retries is recorded as a failure of that source only.
func fetchNewsSources(ctx workflow.Context, cfg newsConfig, due []news.Source, result *RefreshNewsResult, tracker *shared.ReportTracker) {
	var act *newsfeed.Activities
	fetchCtx := workflow.WithActivityOptions(ctx, newsFetchOptions(cfg))
	futures := make([]workflow.Future, len(due))
	for i, src := range due {
		futures[i] = workflow.ExecuteActivity(fetchCtx, act.FetchNewsSource, newsfeed.FetchInput{Source: src, Season: result.Season})
	}
	for i, src := range due {
		var fetched newsfeed.FetchResult
		err := futures[i].Get(ctx, &fetched)
		tracker.IncrementBar(ctx, 0, 0)
		if err != nil {
			recordNewsFailure(ctx, src, result, err)
			continue
		}
		if fetched.NotModified {
			result.NotModified = append(result.NotModified, src.ID)
		} else {
			result.Fetched = append(result.Fetched, src.ID)
		}
		result.Stored.Items += fetched.Stored.Items
		result.Stored.NewVersions += fetched.Stored.NewVersions
		result.Stored.Unchanged += fetched.Stored.Unchanged
		result.Stored.Skipped += fetched.Stored.Skipped
		result.Bodies.Add(fetched.Bodies)
	}
}

func recordNewsFailure(ctx workflow.Context, src news.Source, result *RefreshNewsResult, fetchErr error) {
	var act *newsfeed.Activities
	message := activityMessage(fetchErr)
	result.Failed = append(result.Failed, FailedNewsSource{SourceID: src.ID, Error: message})
	failure := newsfeed.FailureInput{Source: src, Season: result.Season, Error: message}
	if err := workflow.ExecuteActivity(ctx, act.RecordNewsFetchFailure, failure).Get(ctx, nil); err != nil {
		workflow.GetLogger(ctx).Error("Could not record news fetch failure", "source", src.ID, "error", err)
	}
}

func newsFetchOptions(cfg newsConfig) workflow.ActivityOptions {
	return workflow.ActivityOptions{
		StartToCloseTimeout: newsFetchTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Duration(cfg.FetchRetryInitialSeconds) * time.Second,
			MaximumInterval:    time.Duration(cfg.FetchRetryMaxSeconds) * time.Second,
			MaximumAttempts:    int32(cfg.FetchMaxAttempts),
			BackoffCoefficient: config.DefaultBackoffCoefficient,
		},
	}
}

// processNewsVersions processes new versions batch by batch.
func processNewsVersions(ctx workflow.Context, cfg newsConfig, result *RefreshNewsResult) error {
	var act *newsfeed.Activities
	input := newsfeed.ProcessInput{Season: result.Season, BatchSize: cfg.BatchSize, IncidentWindowHours: cfg.IncidentWindowHours}
	for range maxNewsProcessBatches {
		var batch newsfeed.ProcessResult
		if err := workflow.ExecuteActivity(ctx, act.ProcessNewsVersions, input).Get(ctx, &batch); err != nil {
			return fmt.Errorf("process news: %w", err)
		}
		result.Processed.Add(batch.Outcome)
		if !batch.Remaining {
			return nil
		}
	}
	workflow.GetLogger(ctx).Warn("News processing stopped at the batch limit; the next refresh continues", "batches", maxNewsProcessBatches)
	return nil
}

func newsSummary(r RefreshNewsResult, elapsed string) string {
	return fmt.Sprintf("Fetched %d, unchanged %d, failed %d, not due %d sources; %d new versions, %d incidents, %d repeats, %d events in %s.",
		len(r.Fetched), len(r.NotModified), len(r.Failed), len(r.NotDue),
		r.Stored.NewVersions, r.Processed.IncidentsCreated, r.Processed.Repeats, r.Extracted.Events.Created, elapsed)
}
