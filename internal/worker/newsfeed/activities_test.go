package newsfeed

import (
	"errors"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/news"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
)

const (
	planTestRefreshMinutes = 30
	planTestSeason         = 2026
	planTestOtherSeason    = 2025
)

var planTestNow = time.Date(2026, time.January, 15, 12, 0, 0, 0, time.UTC)

func rssSource(id string) news.Source {
	return news.Source{ID: id, Adapter: news.AdapterRSS, URL: "https://example.test/" + id, RefreshMinutes: planTestRefreshMinutes}
}

func yahooStatusSource(id string) news.Source {
	return news.Source{ID: id, Adapter: news.AdapterYahooStatus, RefreshMinutes: planTestRefreshMinutes}
}

func TestPlanRefresh_NeverAttemptedIsDue(t *testing.T) {
	src := rssSource("never")
	plan := planRefresh(PlanInput{Sources: []news.Source{src}, Season: planTestSeason}, nil, planTestNow)

	assert.Equal(t, []news.Source{src}, plan.Due)
	assert.Empty(t, plan.NotDue)
}

func TestPlanRefresh_AttemptedRecentlyIsNotDue(t *testing.T) {
	src := rssSource("recent")
	last := planTestNow.Add(-10 * time.Minute)
	states := []news.FetchState{{SourceID: src.ID, Scope: news.ScopeFeed, LastAttemptAt: last}}

	plan := planRefresh(PlanInput{Sources: []news.Source{src}, Season: planTestSeason}, states, planTestNow)

	assert.Empty(t, plan.Due)
	require.Len(t, plan.NotDue, 1)
	assert.Equal(t, src.ID, plan.NotDue[0].SourceID)
	assert.Equal(t, last.Add(src.RefreshInterval()), plan.NotDue[0].NextDue)
}

func TestPlanRefresh_AttemptedExactlyAtIntervalIsDue(t *testing.T) {
	src := rssSource("exact")
	last := planTestNow.Add(-planTestRefreshMinutes * time.Minute)
	states := []news.FetchState{{SourceID: src.ID, Scope: news.ScopeFeed, LastAttemptAt: last}}

	plan := planRefresh(PlanInput{Sources: []news.Source{src}, Season: planTestSeason}, states, planTestNow)

	assert.Equal(t, []news.Source{src}, plan.Due)
	assert.Empty(t, plan.NotDue)
}

func TestPlanRefresh_ForceMakesEverythingDueRegardlessOfRecency(t *testing.T) {
	src := rssSource("forced")
	states := []news.FetchState{{SourceID: src.ID, Scope: news.ScopeFeed, LastAttemptAt: planTestNow}}

	plan := planRefresh(PlanInput{Sources: []news.Source{src}, Season: planTestSeason, Force: true}, states, planTestNow)

	assert.Equal(t, []news.Source{src}, plan.Due)
	assert.Empty(t, plan.NotDue)
}

// A Yahoo-status source's fetch state is scoped by season ("season:<year>");
// a recent attempt recorded under a different season must not make the
// current season's status look up to date.
func TestPlanRefresh_YahooStatusScopedBySeasonNotByAnotherSeasonsState(t *testing.T) {
	src := yahooStatusSource("yahoo-status")
	states := []news.FetchState{{SourceID: src.ID, Scope: src.Scope(planTestOtherSeason), LastAttemptAt: planTestNow}}

	plan := planRefresh(PlanInput{Sources: []news.Source{src}, Season: planTestSeason}, states, planTestNow)

	assert.Equal(t, []news.Source{src}, plan.Due, "a different season's recent attempt must not satisfy this season's scope")
	assert.Empty(t, plan.NotDue)
}

// The matching season's own recorded attempt does make the source not due.
func TestPlanRefresh_YahooStatusMatchesItsOwnSeasonScope(t *testing.T) {
	src := yahooStatusSource("yahoo-status")
	last := planTestNow.Add(-10 * time.Minute)
	states := []news.FetchState{{SourceID: src.ID, Scope: src.Scope(planTestSeason), LastAttemptAt: last}}

	plan := planRefresh(PlanInput{Sources: []news.Source{src}, Season: planTestSeason}, states, planTestNow)

	assert.Empty(t, plan.Due)
	require.Len(t, plan.NotDue, 1)
	assert.Equal(t, src.ID, plan.NotDue[0].SourceID)
}

func TestFetchError_HTTPNotFoundIsNonRetryable(t *testing.T) {
	httpErr := &news.HTTPError{URL: "https://example.test/feed", StatusCode: 404}

	err := fetchError(httpErr)

	var appErr *temporal.ApplicationError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, ErrTypeHTTP, appErr.Type())
	assert.True(t, appErr.NonRetryable())
	assert.Zero(t, appErr.NextRetryDelay())
}

func TestFetchError_HTTPTooManyRequestsIsRetryableWithDelay(t *testing.T) {
	retryAfter := 5 * time.Second
	httpErr := &news.HTTPError{URL: "https://example.test/feed", StatusCode: 429, RetryAfter: retryAfter}

	err := fetchError(httpErr)

	var appErr *temporal.ApplicationError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, ErrTypeHTTP, appErr.Type())
	assert.False(t, appErr.NonRetryable())
	assert.Equal(t, retryAfter, appErr.NextRetryDelay())
}

func TestFetchError_NonHTTPErrorIsReturnedUnchanged(t *testing.T) {
	original := errors.New("connection reset")

	err := fetchError(original)

	assert.Same(t, original, err)
}

const nhlContentTestBody = `{"items":[{"_entityId":"story-1","slug":"player-x-day-to-day","headline":"Player X day-to-day",` +
	`"summary":"Team update on the injury.","contentDate":"2026-01-05T12:00:00Z"}]}`

const rssTestBody = `<?xml version="1.0"?><rss><channel><item><guid>item-1</guid><link>https://example.test/item-1</link>` +
	`<title>Trade rumor swirls</title><description>Some evidence text.</description></item></channel></rss>`

func TestParseFeed_DispatchesNHLContentAdapter(t *testing.T) {
	src := news.Source{ID: "nhl", Adapter: news.AdapterNHLContent, MaxItems: 10}

	items, err := parseFeed(src, []byte(nhlContentTestBody), time.Now())

	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "story-1", items[0].ExternalID)
	assert.Equal(t, "Player X day-to-day", items[0].Title)
}

func TestParseFeed_DispatchesRSSAdapter(t *testing.T) {
	src := news.Source{ID: "rss", Adapter: news.AdapterRSS, MaxItems: 10}

	items, err := parseFeed(src, []byte(rssTestBody), time.Now())

	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "item-1", items[0].ExternalID)
	assert.Equal(t, "Trade rumor swirls", items[0].Title)
}

// FetchNewsSource must reject an unknown adapter before touching the
// database; a nil Queries would panic if it were reached.
func TestFetchNewsSource_UnknownAdapterFailsWithoutTouchingDB(t *testing.T) {
	a := &Activities{}
	input := FetchInput{Source: news.Source{ID: "mystery", Adapter: "bogus"}, Season: planTestSeason}

	result, err := a.FetchNewsSource(t.Context(), input)

	assert.Equal(t, FetchResult{}, result)
	var appErr *temporal.ApplicationError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, ErrTypeParse, appErr.Type())
	assert.True(t, appErr.NonRetryable())
}
