// Package newsfeed holds the Temporal activities of the player news refresh:
// planning which sources are due, fetching and storing a source, recording
// failures, processing new article versions, extracting validated events
// with an LLM, and pruning old news.
package newsfeed

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/news"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

// Application error types of the news activities.
const (
	ErrTypeHTTP       = "NewsHTTPError"
	ErrTypeParse      = "NewsParseError"
	ErrTypeNoCoverage = "NewsNoCoverage"
)

// Activities are the news refresh activities.
type Activities struct {
	Pool       *pgxpool.Pool
	Queries    *sqlcdb.Queries
	HTTPClient *http.Client
	// LLM builds the model client of news event extraction; nil disables
	// extraction on this worker.
	LLM ClientFactory
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

func (a *Activities) now() time.Time {
	if a.Now != nil {
		return a.Now().UTC()
	}
	return time.Now().UTC()
}

// PlanInput asks which sources are due.
type PlanInput struct {
	Sources []news.Source `json:"sources"`
	Season  int           `json:"season"`
	Force   bool          `json:"force"`
}

// NotDue is a source skipped because it was attempted recently.
type NotDue struct {
	SourceID string    `json:"sourceId"`
	NextDue  time.Time `json:"nextDue"`
}

// Plan lists the sources to fetch now and those that are not due yet.
type Plan struct {
	Due    []news.Source `json:"due"`
	NotDue []NotDue      `json:"notDue"`
}

// PlanNewsRefresh returns the sources whose refresh interval has passed
// since their last attempt (every source when forced).
func (a *Activities) PlanNewsRefresh(ctx context.Context, input PlanInput) (Plan, error) {
	rows, err := a.Queries.ListNewsFetchStates(ctx)
	if err != nil {
		return Plan{}, fmt.Errorf("list news fetch states: %w", err)
	}
	states := make([]news.FetchState, 0, len(rows))
	for _, r := range rows {
		states = append(states, news.FetchStateFromRow(r))
	}
	return planRefresh(input, states, a.now()), nil
}

func planRefresh(input PlanInput, states []news.FetchState, now time.Time) Plan {
	lastAttempt := make(map[string]time.Time, len(states))
	for _, s := range states {
		lastAttempt[s.SourceID+"\x00"+s.Scope] = s.LastAttemptAt
	}
	plan := Plan{Due: []news.Source{}, NotDue: []NotDue{}}
	for _, src := range input.Sources {
		last := lastAttempt[src.ID+"\x00"+src.Scope(input.Season)]
		next := last.Add(src.RefreshInterval())
		if input.Force || last.IsZero() || !now.Before(next) {
			plan.Due = append(plan.Due, src)
			continue
		}
		plan.NotDue = append(plan.NotDue, NotDue{SourceID: src.ID, NextDue: next})
	}
	return plan
}

// FetchInput names the source to fetch.
type FetchInput struct {
	Source news.Source `json:"source"`
	Season int         `json:"season"`
}

// FetchResult is what one fetch stored.
type FetchResult struct {
	NotModified bool              `json:"notModified"`
	Stored      news.IngestResult `json:"stored"`
	// Bodies counts the story pages of a fetch_body source.
	Bodies news.BodyResult `json:"bodies"`
}

const (
	// bodyFetchBudget bounds the time one fetch spends downloading story
	// pages, well inside the fetch activity's five-minute timeout; the
	// stories left are deferred to the next refresh.
	bodyFetchBudget = 2 * time.Minute
	// bodyPageTimeout bounds the download of one story page.
	bodyPageTimeout = 15 * time.Second
)

// FetchNewsSource fetches one source, stores its new or changed items and
// records the fetch as successful. Errors worth retrying (network, 5xx,
// 429) are returned as retryable; the others are not.
func (a *Activities) FetchNewsSource(ctx context.Context, input FetchInput) (FetchResult, error) {
	switch input.Source.Adapter {
	case news.AdapterYahooStatus:
		return a.fetchYahooStatus(ctx, input)
	case news.AdapterNHLContent, news.AdapterRSS:
		return a.fetchFeed(ctx, input)
	default:
		return FetchResult{}, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("source %s has unknown adapter %q", input.Source.ID, input.Source.Adapter), ErrTypeParse, nil)
	}
}

func (a *Activities) fetchYahooStatus(ctx context.Context, input FetchInput) (FetchResult, error) {
	statuses, asOf, err := news.LoadYahooStatuses(ctx, a.Queries, input.Season)
	if errors.Is(err, news.ErrNoYahooPools) {
		return FetchResult{}, temporal.NewNonRetryableApplicationError(err.Error(), ErrTypeNoCoverage, err)
	}
	if err != nil {
		return FetchResult{}, err
	}
	stored, err := news.StoreItems(ctx, a.Queries, input.Source, news.YahooStatusItems(statuses))
	if err != nil {
		return FetchResult{}, err
	}
	err = a.recordSuccess(ctx, input, news.Validators{}, asOf, stored)
	return FetchResult{Stored: stored}, err
}

func (a *Activities) fetchFeed(ctx context.Context, input FetchInput) (FetchResult, error) {
	src := input.Source
	last, err := a.validators(ctx, src, input.Season)
	if err != nil {
		return FetchResult{}, err
	}
	resp, err := news.Fetch(ctx, a.httpClient(), src.URL, last)
	if err != nil {
		return FetchResult{}, fetchError(err)
	}
	if resp.NotModified {
		err := a.recordSuccess(ctx, input, resp.Validators, time.Time{}, news.IngestResult{})
		return FetchResult{NotModified: true}, err
	}
	items, err := parseFeed(src, resp.Body, a.now())
	if err != nil {
		return FetchResult{}, temporal.NewNonRetryableApplicationError(err.Error(), ErrTypeParse, err)
	}
	validators := resp.Validators
	var bodies news.BodyResult
	if src.FetchBody {
		items, bodies, err = a.attachBodies(ctx, src, items)
		if err != nil {
			return FetchResult{}, err
		}
		if bodies.Deferred > 0 {
			// Forget the feed's validators so the next refresh reads the
			// feed again and picks up the deferred stories.
			validators = news.Validators{}
		}
	}
	stored, err := news.StoreItems(ctx, a.Queries, src, items)
	if err != nil {
		return FetchResult{}, err
	}
	err = a.recordSuccess(ctx, input, validators, time.Time{}, stored)
	return FetchResult{Stored: stored, Bodies: bodies}, err
}

// attachBodies downloads the full text of the source's new and updated
// stories within bodyFetchBudget.
func (a *Activities) attachBodies(ctx context.Context, src news.Source, items []news.Item) ([]news.Item, news.BodyResult, error) {
	items, bodies, err := news.AttachBodies(ctx, a.Queries, src, items, news.BodyOptions{
		Fetch: a.fetchPage, Deadline: a.now().Add(bodyFetchBudget), Now: a.now,
	})
	if err != nil {
		return nil, bodies, fmt.Errorf("fetch story bodies of %s: %w", src.ID, err)
	}
	if bodies.Deferred > 0 || bodies.Unavailable > 0 {
		activity.GetLogger(ctx).Warn("Some news story bodies could not be downloaded", "source", src.ID,
			"deferred", bodies.Deferred, "unavailable", bodies.Unavailable)
	}
	return items, bodies, nil
}

// fetchPage downloads one story page.
func (a *Activities) fetchPage(ctx context.Context, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, bodyPageTimeout)
	defer cancel()
	resp, err := news.Fetch(ctx, a.httpClient(), url, news.Validators{})
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func parseFeed(src news.Source, body []byte, retrievedAt time.Time) ([]news.Item, error) {
	if src.Adapter == news.AdapterNHLContent {
		return news.ParseNHLContent(body, retrievedAt, src.ItemLimit())
	}
	return news.ParseFeed(body, retrievedAt, src.ItemLimit())
}

// fetchError turns a fetch failure into a Temporal error: client errors
// other than 408/429 and oversized responses are not retried, and a
// Retry-After delay is honored.
func fetchError(err error) error {
	if errors.Is(err, news.ErrFeedTooLarge) {
		return temporal.NewNonRetryableApplicationError(err.Error(), ErrTypeParse, err)
	}
	var httpErr *news.HTTPError
	if !errors.As(err, &httpErr) {
		return err
	}
	return temporal.NewApplicationErrorWithOptions(err.Error(), ErrTypeHTTP, temporal.ApplicationErrorOptions{
		NonRetryable: !httpErr.Retryable(), Cause: err, NextRetryDelay: httpErr.RetryAfter,
	})
}

func (a *Activities) httpClient() *http.Client {
	if a.HTTPClient != nil {
		return a.HTTPClient
	}
	return http.DefaultClient
}

func (a *Activities) validators(ctx context.Context, src news.Source, season int) (news.Validators, error) {
	state, err := a.Queries.GetNewsFetchState(ctx, sqlcdb.GetNewsFetchStateParams{SourceID: src.ID, Scope: src.Scope(season)})
	if errors.Is(err, pgx.ErrNoRows) {
		return news.Validators{}, nil
	}
	if err != nil {
		return news.Validators{}, fmt.Errorf("load fetch state of %s: %w", src.ID, err)
	}
	return news.Validators{ETag: state.Etag, LastModified: state.LastModified, BodyHash: state.BodyHash}, nil
}

func (a *Activities) recordSuccess(ctx context.Context, input FetchInput, v news.Validators, dataAsOf time.Time, stored news.IngestResult) error {
	src := input.Source
	err := a.Queries.RecordNewsFetchSuccess(ctx, sqlcdb.RecordNewsFetchSuccessParams{
		SourceID: src.ID, Scope: src.Scope(input.Season), Publisher: src.Publisher,
		Etag: v.ETag, LastModified: v.LastModified, BodyHash: v.BodyHash,
		LastAttemptAt: news.Timestamptz(a.now()), DataAsOf: news.Timestamptz(dataAsOf),
		LastItems: int32(stored.Items), LastNewVersions: int32(stored.NewVersions),
	})
	if err != nil {
		return fmt.Errorf("record fetch of %s: %w", src.ID, err)
	}
	return nil
}

// FailureInput records a source fetch that failed after its retries.
type FailureInput struct {
	Source news.Source `json:"source"`
	Season int         `json:"season"`
	Error  string      `json:"error"`
}

// maxRecordedErrorRunes bounds the error text kept in the fetch state.
const maxRecordedErrorRunes = 500

// RecordNewsFetchFailure marks a source's fetch as failed. The source's
// stored news and its last success time stay untouched, so the report shows
// the last good data as aging instead of hiding it.
func (a *Activities) RecordNewsFetchFailure(ctx context.Context, input FailureInput) error {
	src := input.Source
	err := a.Queries.RecordNewsFetchFailure(ctx, sqlcdb.RecordNewsFetchFailureParams{
		SourceID: src.ID, Scope: src.Scope(input.Season), Publisher: src.Publisher,
		LastAttemptAt: news.Timestamptz(a.now()), LastError: news.CleanText(input.Error, maxRecordedErrorRunes),
	})
	if err != nil {
		return fmt.Errorf("record failed fetch of %s: %w", src.ID, err)
	}
	activity.GetLogger(ctx).Warn("News source fetch failed", "source", src.ID, "error", input.Error)
	return nil
}
