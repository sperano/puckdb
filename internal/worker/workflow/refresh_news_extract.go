package workflow

import (
	"errors"
	"fmt"
	"time"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/worker/newsfeed"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/spf13/viper"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	// newsExtractTimeout bounds one extraction batch: BatchSize model calls
	// of up to the call timeout each, a few at a time.
	newsExtractTimeout = 30 * time.Minute
	// newsExtractHeartbeat is the longest a batch may go without finishing
	// a version (one slow model call plus reconciling).
	newsExtractHeartbeat = 5 * time.Minute
	// maxNewsExtractBatches bounds the extraction loop of one refresh; the
	// call and token caps normally stop it first.
	maxNewsExtractBatches = 50
)

// newsExtractConfig is the event extraction part of the refresh's config
// snapshot. A snapshot recorded before extraction existed decodes with
// Enabled false, so replaying such a run schedules no extraction activity.
type newsExtractConfig struct {
	Enabled         bool   `json:"enabled"`
	Provider        string `json:"provider"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
	MaxOutputTokens int    `json:"maxOutputTokens"`
	MaxInputChars   int    `json:"maxInputChars"`
	TimeoutSeconds  int    `json:"timeoutSeconds"`
	BatchSize       int    `json:"batchSize"`
	Concurrency     int    `json:"concurrency"`
	MaxCalls        int    `json:"maxCalls"`
	MaxTokens       int    `json:"maxTokens"`
	MaxAttempts     int    `json:"maxAttempts"`
	RetryMinutes    int    `json:"retryMinutes"`
	LookbackDays    int    `json:"lookbackDays"`
}

func loadNewsExtractConfig() newsExtractConfig {
	return newsExtractConfig{
		Enabled:         viper.GetBool(config.FlagNewsExtractEnabled),
		Provider:        shared.ViperStringOrDefault(config.FlagNewsExtractProvider, config.DefaultNewsExtractProvider),
		Model:           shared.ViperStringOrDefault(config.FlagNewsExtractModel, config.DefaultNewsExtractModel),
		ReasoningEffort: viper.GetString(config.FlagNewsExtractReasoningEffort),
		MaxOutputTokens: shared.ViperIntOrDefault(config.FlagNewsExtractMaxOutputTokens, config.DefaultNewsExtractMaxOutputTokens),
		MaxInputChars:   shared.ViperIntOrDefault(config.FlagNewsExtractMaxInputChars, config.DefaultNewsExtractMaxInputChars),
		TimeoutSeconds:  shared.ViperIntOrDefault(config.FlagNewsExtractTimeoutSeconds, config.DefaultNewsExtractTimeoutSeconds),
		BatchSize:       shared.ViperIntOrDefault(config.FlagNewsExtractBatchSize, config.DefaultNewsExtractBatchSize),
		Concurrency:     shared.ViperIntOrDefault(config.FlagNewsExtractConcurrency, config.DefaultNewsExtractConcurrency),
		MaxCalls:        shared.ViperIntOrDefault(config.FlagNewsExtractMaxCalls, config.DefaultNewsExtractMaxCalls),
		MaxTokens:       shared.ViperIntOrDefault(config.FlagNewsExtractMaxTokens, config.DefaultNewsExtractMaxTokens),
		MaxAttempts:     shared.ViperIntOrDefault(config.FlagNewsExtractMaxAttempts, config.DefaultNewsExtractMaxAttempts),
		RetryMinutes:    shared.ViperIntOrDefault(config.FlagNewsExtractRetryMinutes, config.DefaultNewsExtractRetryMinutes),
		LookbackDays:    shared.ViperIntOrDefault(config.FlagNewsExtractLookbackDays, config.DefaultNewsExtractLookbackDays),
	}
}

// extractNewsEvents runs extraction batches until nothing is left, the
// refresh's call or token cap is spent, or the batch limit is reached. It
// returns the failure message of a batch that failed after its retries;
// the refresh goes on and its fetched news is kept.
func extractNewsEvents(ctx workflow.Context, cfg newsConfig, result *RefreshNewsResult) string {
	var act *newsfeed.Activities
	extractCtx := workflow.WithActivityOptions(ctx, newsExtractOptions())
	calls, tokens := cfg.Extract.MaxCalls, cfg.Extract.MaxTokens
	for range maxNewsExtractBatches {
		input := newsExtractInput(cfg, calls, tokens)
		var batch newsfeed.ExtractResult
		if err := workflow.ExecuteActivity(extractCtx, act.ExtractNewsEvents, input).Get(ctx, &batch); err != nil {
			workflow.GetLogger(ctx).Error("News event extraction failed; the next refresh continues", "error", err)
			return activityMessage(err)
		}
		result.Extracted.Add(batch)
		calls -= batch.Calls
		tokens -= batch.Tokens()
		if !batch.Remaining || calls <= 0 || tokens <= 0 {
			return ""
		}
	}
	workflow.GetLogger(ctx).Warn("News event extraction stopped at the batch limit", "batches", maxNewsExtractBatches)
	return ""
}

func newsExtractInput(cfg newsConfig, calls, tokens int) newsfeed.ExtractInput {
	x := cfg.Extract
	return newsfeed.ExtractInput{
		Provider: x.Provider, Model: x.Model, ReasoningEffort: x.ReasoningEffort,
		MaxOutputTokens: x.MaxOutputTokens, MaxInputChars: x.MaxInputChars,
		TimeoutSeconds: x.TimeoutSeconds, BatchSize: x.BatchSize, Concurrency: x.Concurrency,
		MaxAttempts: x.MaxAttempts, RetryMinutes: x.RetryMinutes, LookbackDays: x.LookbackDays,
		IncidentWindowHours: cfg.IncidentWindowHours, RemainingCalls: calls, RemainingTokens: tokens,
	}
}

func newsExtractOptions() workflow.ActivityOptions {
	opts := shared.DefaultActivityOptions()
	opts.StartToCloseTimeout = newsExtractTimeout
	opts.HeartbeatTimeout = newsExtractHeartbeat
	return opts
}

// activityMessage is an activity failure's own message, without Temporal's
// wrapping.
func activityMessage(err error) string {
	var appErr *temporal.ApplicationError
	if errors.As(err, &appErr) {
		return appErr.Message()
	}
	return fmt.Sprint(err)
}
