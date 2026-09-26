package newsfeed

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/llm"
	"github.com/sperano/puckdb/internal/news"
	"github.com/sperano/puckdb/internal/newsevent"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"golang.org/x/sync/errgroup"
)

// ErrTypeExtractConfig marks an extraction that cannot start: unknown
// provider, or no client for it.
const ErrTypeExtractConfig = "NewsExtractConfigError"

// newsEventLockKey is the PostgreSQL advisory lock that serializes event
// reconciliation: two refreshes reconciling reports of one event at once
// could each create it.
const newsEventLockKey int64 = 0x6e657674 // "nevt"

// ClientFactory builds the LLM client of a provider and model with a
// per-call timeout. The worker builds it from its provider keys, so no key
// ever travels through workflow history.
type ClientFactory func(provider llm.Provider, model string, timeout time.Duration) llm.Client

// ExtractInput is one extraction batch, with the refresh's remaining budget.
type ExtractInput struct {
	Provider            string `json:"provider"`
	Model               string `json:"model"`
	MaxOutputTokens     int    `json:"maxOutputTokens"`
	MaxInputChars       int    `json:"maxInputChars"`
	TimeoutSeconds      int    `json:"timeoutSeconds"`
	BatchSize           int    `json:"batchSize"`
	Concurrency         int    `json:"concurrency"`
	MaxAttempts         int    `json:"maxAttempts"`
	RetryMinutes        int    `json:"retryMinutes"`
	LookbackDays        int    `json:"lookbackDays"`
	IncidentWindowHours int    `json:"incidentWindowHours"`
	RemainingCalls      int    `json:"remainingCalls"`
	RemainingTokens     int    `json:"remainingTokens"`
}

// ExtractResult is what one batch did.
type ExtractResult struct {
	Versions         int               `json:"versions"`
	Succeeded        int               `json:"succeeded"`
	Invalid          int               `json:"invalid"`
	Failed           int               `json:"failed"`
	Deferred         int               `json:"deferred"`
	Cached           int               `json:"cached"`
	Calls            int               `json:"calls"`
	PromptTokens     int               `json:"promptTokens"`
	CompletionTokens int               `json:"completionTokens"`
	Claims           int               `json:"claims"`
	Unsupported      int               `json:"unsupported"`
	Issues           int               `json:"issues"`
	Events           newsevent.Outcome `json:"events"`
	// Remaining is true when the batch was full and nothing was deferred,
	// so more versions may wait.
	Remaining bool `json:"remaining"`
}

// Tokens is the batch's prompt plus completion tokens.
func (r ExtractResult) Tokens() int {
	return r.PromptTokens + r.CompletionTokens
}

// Add accumulates another batch.
func (r *ExtractResult) Add(o ExtractResult) {
	r.Versions += o.Versions
	r.Succeeded += o.Succeeded
	r.Invalid += o.Invalid
	r.Failed += o.Failed
	r.Deferred += o.Deferred
	r.Cached += o.Cached
	r.Calls += o.Calls
	r.PromptTokens += o.PromptTokens
	r.CompletionTokens += o.CompletionTokens
	r.Claims += o.Claims
	r.Unsupported += o.Unsupported
	r.Issues += o.Issues
	r.Events.Add(o.Events)
}

func (r *ExtractResult) count(v newsevent.VersionResult) {
	r.Versions++
	switch v.Status {
	case newsevent.ExtractionSucceeded:
		r.Succeeded++
	case newsevent.ExtractionInvalid:
		r.Invalid++
	case newsevent.ExtractionFailed:
		r.Failed++
	case newsevent.ExtractionDeferred:
		r.Deferred++
	}
	if v.Cached {
		r.Cached++
	}
	if v.Called {
		r.Calls++
	}
	r.PromptTokens += v.Usage.PromptTokens
	r.CompletionTokens += v.Usage.CompletionTokens
	r.Claims += v.Claims
	r.Unsupported += v.Unsupported
	r.Issues += v.Issues
	r.Events.Add(v.Events)
}

// ExtractNewsEvents extracts events from up to BatchSize article versions,
// oldest report first, with at most Concurrency model calls at once and
// within the refresh's remaining calls and tokens. A failed or invalid
// reply is recorded on its extraction and leaves earlier events untouched;
// only database failures fail the activity.
func (a *Activities) ExtractNewsEvents(ctx context.Context, input ExtractInput) (ExtractResult, error) {
	runner, err := a.extractRunner(input)
	if err != nil {
		return ExtractResult{}, err
	}
	now := a.now()
	rows, err := a.Queries.ListNewsVersionsToExtract(ctx, sqlcdb.ListNewsVersionsToExtractParams{
		Since:        news.Timestamptz(now.Add(-time.Duration(input.LookbackDays) * hoursPerDay * time.Hour)),
		ExtractorKey: runner.Extractor.Key(), MaxAttempts: int32(input.MaxAttempts),
		RetryBefore: news.Timestamptz(now.Add(-time.Duration(input.RetryMinutes) * time.Minute)),
		MaxVersions: int32(input.BatchSize),
	})
	if err != nil {
		return ExtractResult{}, fmt.Errorf("list news versions to extract: %w", err)
	}
	budget := &runBudget{calls: input.RemainingCalls, tokens: input.RemainingTokens}
	result, err := runExtractions(ctx, runner, rows, budget, input.Concurrency)
	result.Remaining = len(rows) == input.BatchSize && result.Deferred == 0
	return result, err
}

// runExtractions extracts the versions with bounded concurrency.
func runExtractions(ctx context.Context, runner *newsevent.Runner, rows []sqlcdb.ListNewsVersionsToExtractRow,
	budget newsevent.Budget, concurrency int) (ExtractResult, error) {
	var result ExtractResult
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(max(concurrency, 1))
	for _, row := range rows {
		g.Go(func() error {
			v, err := runner.Run(gctx, row, budget)
			if err != nil {
				return fmt.Errorf("extract news version %d: %w", row.ID, err)
			}
			mu.Lock()
			defer mu.Unlock()
			result.count(v)
			activity.RecordHeartbeat(ctx, result.Versions)
			return nil
		})
	}
	err := g.Wait()
	return result, err
}

func (a *Activities) extractRunner(input ExtractInput) (*newsevent.Runner, error) {
	provider, err := llm.ParseProvider(input.Provider)
	if err != nil {
		return nil, temporal.NewNonRetryableApplicationError(err.Error(), ErrTypeExtractConfig, err)
	}
	if a.LLM == nil {
		err := fmt.Errorf("the worker has no LLM client factory")
		return nil, temporal.NewNonRetryableApplicationError(err.Error(), ErrTypeExtractConfig, err)
	}
	client := a.LLM(provider, input.Model, time.Duration(input.TimeoutSeconds)*time.Second)
	return &newsevent.Runner{
		Extractor: newsevent.Extractor{
			Client: client, Provider: input.Provider, Model: input.Model, MaxOutputTokens: input.MaxOutputTokens,
		},
		Queries:       a.Queries,
		InTx:          a.inEventTx,
		Window:        time.Duration(input.IncidentWindowHours) * time.Hour,
		MaxInputRunes: input.MaxInputChars,
		RetryAfter:    time.Duration(input.RetryMinutes) * time.Minute,
		Now:           a.now,
	}, nil
}

// inEventTx runs fn in a transaction holding the event reconciliation lock.
func (a *Activities) inEventTx(ctx context.Context, fn func(q newsevent.TxQueries) error) error {
	return inLockedTx(ctx, a.Pool, a.Queries, newsEventLockKey, func(q *sqlcdb.Queries) error { return fn(q) })
}

func inLockedTx(ctx context.Context, pool *pgxpool.Pool, queries *sqlcdb.Queries, key int64, fn func(q *sqlcdb.Queries) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	// Rollback is a no-op once the transaction is committed.
	defer func() { _ = tx.Rollback(ctx) }()
	q := queries.WithTx(tx)
	if err := q.LockNewsProcessing(ctx, key); err != nil {
		return fmt.Errorf("lock news events: %w", err)
	}
	if err := fn(q); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// runBudget is a refresh's remaining model calls and tokens. A call is
// allowed while both remain; tokens are counted after each call, so
// concurrent calls may overshoot the token cap by up to the concurrency
// times one call's use.
type runBudget struct {
	mu     sync.Mutex
	calls  int
	tokens int
}

func (b *runBudget) Reserve() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.calls <= 0 || b.tokens <= 0 {
		return false
	}
	b.calls--
	return true
}

func (b *runBudget) Release() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls++
}

func (b *runBudget) Spend(usage llm.Usage) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tokens -= usage.PromptTokens + usage.CompletionTokens
}
