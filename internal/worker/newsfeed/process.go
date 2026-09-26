package newsfeed

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sperano/puckdb/internal/news"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"go.temporal.io/sdk/activity"
)

// ProcessInput asks for one batch of unprocessed article versions.
type ProcessInput struct {
	Season              int `json:"season"`
	BatchSize           int `json:"batchSize"`
	IncidentWindowHours int `json:"incidentWindowHours"`
}

// ProcessResult is one batch's outcome.
type ProcessResult struct {
	Outcome news.ProcessOutcome `json:"outcome"`
	// Remaining is true when the batch was full, so more versions may wait.
	Remaining bool `json:"remaining"`
}

// newsProcessingLockKey is the PostgreSQL advisory lock that serializes
// version processing: without it, two refreshes processing reports of the
// same event at once could each create an incident for it.
const newsProcessingLockKey int64 = 0x6e657773 // "news"

// ProcessNewsVersions resolves players and attaches incidents for up to
// BatchSize unprocessed versions, oldest first, each in its own transaction.
func (a *Activities) ProcessNewsVersions(ctx context.Context, input ProcessInput) (ProcessResult, error) {
	rows, err := a.Queries.ListUnprocessedNewsVersions(ctx, int32(input.BatchSize))
	if err != nil {
		return ProcessResult{}, fmt.Errorf("list unprocessed news versions: %w", err)
	}
	result := ProcessResult{Remaining: len(rows) == input.BatchSize}
	if len(rows) == 0 {
		return result, nil
	}
	dir, err := news.LoadDirectory(ctx, a.Queries, input.Season)
	if err != nil {
		return result, err
	}
	window := time.Duration(input.IncidentWindowHours) * time.Hour
	for i, row := range rows {
		outcome, err := a.processVersion(ctx, dir, row, window)
		if err != nil {
			return result, fmt.Errorf("process news version %d: %w", row.ID, err)
		}
		result.Outcome.Add(outcome)
		activity.RecordHeartbeat(ctx, i+1)
	}
	return result, nil
}

// processVersion processes one version under the processing lock, unless
// another refresh processed (or pruned) it since the batch was listed.
func (a *Activities) processVersion(ctx context.Context, dir *news.Directory, row sqlcdb.ListUnprocessedNewsVersionsRow,
	window time.Duration) (news.ProcessOutcome, error) {
	tx, err := a.Pool.Begin(ctx)
	if err != nil {
		return news.ProcessOutcome{}, fmt.Errorf("begin transaction: %w", err)
	}
	// Rollback is a no-op once the transaction is committed.
	defer func() { _ = tx.Rollback(ctx) }()
	q := a.Queries.WithTx(tx)
	if err := q.LockNewsProcessing(ctx, newsProcessingLockKey); err != nil {
		return news.ProcessOutcome{}, fmt.Errorf("lock news processing: %w", err)
	}
	processedAt, err := q.GetNewsVersionProcessedAt(ctx, row.ID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && processedAt.Valid) {
		return news.ProcessOutcome{}, nil
	}
	if err != nil {
		return news.ProcessOutcome{}, err
	}
	outcome, err := news.ProcessVersion(ctx, q, dir, row, window, a.now())
	if err != nil {
		return outcome, err
	}
	return outcome, tx.Commit(ctx)
}

// PruneInput bounds how much news is kept.
type PruneInput struct {
	RetentionDays int `json:"retentionDays"`
	KeepVersions  int `json:"keepVersions"`
}

// PruneResult counts what was removed.
type PruneResult struct {
	Incidents int64 `json:"incidents"`
	Events    int64 `json:"events"`
	Articles  int64 `json:"articles"`
	Versions  int64 `json:"versions"`
}

const hoursPerDay = 24

// PruneNews removes incidents and events last reported and articles last
// seen before the retention cutoff (an article still backing an incident or
// event stays), and old versions beyond KeepVersions per article that back
// no incident or event.
func (a *Activities) PruneNews(ctx context.Context, input PruneInput) (PruneResult, error) {
	cutoff := news.Timestamptz(a.now().Add(-time.Duration(input.RetentionDays) * hoursPerDay * time.Hour))
	var result PruneResult
	var err error
	if result.Incidents, err = a.Queries.DeleteExpiredNewsIncidents(ctx, cutoff); err != nil {
		return result, fmt.Errorf("prune news incidents: %w", err)
	}
	if result.Events, err = a.Queries.DeleteExpiredNewsEvents(ctx, cutoff); err != nil {
		return result, fmt.Errorf("prune news events: %w", err)
	}
	if result.Articles, err = a.Queries.DeleteExpiredNewsArticles(ctx, cutoff); err != nil {
		return result, fmt.Errorf("prune news articles: %w", err)
	}
	if result.Versions, err = a.Queries.DeleteExcessNewsArticleVersions(ctx, int32(input.KeepVersions)); err != nil {
		return result, fmt.Errorf("prune news versions: %w", err)
	}
	return result, nil
}
