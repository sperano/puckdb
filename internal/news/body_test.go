package news

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testPageURL = "https://forge.example.com/stories/mcnabb"
	byfieldID   = 8482124
)

// bodyTestNow is an hour after the McNabb fixture story's last update, well
// inside MaxBodyDeferral.
var bodyTestNow = time.Date(2026, time.September, 22, 18, 5, 0, 0, time.UTC)

func bodyTestClock() time.Time { return bodyTestNow }

// pageServer is a PageFetcher answering from a map and counting requests.
type pageServer struct {
	pages    map[string][]byte
	errs     map[string]error
	requests int
}

func (p *pageServer) fetch(_ context.Context, url string) ([]byte, error) {
	p.requests++
	if err := p.errs[url]; err != nil {
		return nil, err
	}
	page, ok := p.pages[url]
	if !ok {
		return nil, &HTTPError{URL: url, StatusCode: http.StatusNotFound}
	}
	return page, nil
}

func newPageServer(t *testing.T) *pageServer {
	return &pageServer{pages: map[string][]byte{testPageURL: readFixture(t, "nhl-story-page.json")}, errs: map[string]error{}}
}

// bodyItem is the McNabb suspension story as its tag feed lists it, with a
// story page to read.
func bodyItem(t *testing.T) Item {
	t.Helper()
	item := nhlFixtureItems(t)[0]
	item.BodyURL = testPageURL
	return item
}

func attachBodies(t *testing.T, store *fakeStore, pages *pageServer, items ...Item) ([]Item, BodyResult) {
	t.Helper()
	out, result, err := AttachBodies(context.Background(), store, testSource(t, "nhl-player-safety"), items,
		BodyOptions{Fetch: pages.fetch, Now: bodyTestClock})
	require.NoError(t, err)
	return out, result
}

func TestAttachBodiesReadsNewStoryPage(t *testing.T) {
	pages := newPageServer(t)
	items, result := attachBodies(t, newFakeStore(), pages, bodyItem(t))

	assert.Equal(t, BodyResult{Fetched: 1}, result)
	require.Len(t, items, 1)
	body := items[0].Body
	assert.Contains(t, body, "Vegas Golden Knights defenseman Brayden McNabb has been suspended")
	assert.Contains(t, body, "the Golden Knights' preseason game", "inline entity links keep their text")
	assert.Contains(t, body, "Players' Emergency Assistance Fund", "every markdown part is kept")
	assert.Contains(t, body, "announced today.\n\nThe incident", "paragraphs are kept")
	assert.NotContains(t, body, "forge-entity")
	assert.NotContains(t, body, "Explanation video", "non-text parts are left out")
	assert.Equal(t, []Subject{
		{NHLPlayerID: mcnabbID, Name: "Brayden McNabb"},
		{NHLPlayerID: byfieldID, Name: "Quinton Byfield", InBody: true},
	}, items[0].Subjects, "the tagged player stays tagged; a player only the body links is marked as such")
}

func TestAttachBodiesReusesStoredBodyUntilTheStoryIsUpdated(t *testing.T) {
	store, pages := newFakeStore(), newPageServer(t)
	src := testSource(t, "nhl-player-safety")
	first, _ := attachBodies(t, store, pages, bodyItem(t))
	assert.Equal(t, IngestResult{Items: 1, NewVersions: 1}, ingest(t, store, src, first))

	second, result := attachBodies(t, store, pages, bodyItem(t))
	assert.Equal(t, BodyResult{Reused: 1}, result)
	assert.Equal(t, 1, pages.requests, "an unchanged story's page is not downloaded again")
	assert.Equal(t, IngestResult{Items: 1, Unchanged: 1}, ingest(t, store, src, second),
		"the reused body and body subjects make the same version")

	restamped := bodyItem(t)
	restamped.UpdatedAt = restamped.UpdatedAt.Add(time.Hour)
	third, result := attachBodies(t, store, pages, restamped)
	assert.Equal(t, BodyResult{Fetched: 1}, result, "a moved update time downloads the page again")
	assert.Equal(t, IngestResult{Items: 1, Unchanged: 1}, ingest(t, store, src, third),
		"a re-stamped story whose text did not change is not a new version")

	_, result = attachBodies(t, store, pages, restamped)
	assert.Equal(t, BodyResult{Reused: 1}, result,
		"the new update time was recorded even without a new version, so the page is not downloaded every refresh")
	assert.Equal(t, 2, pages.requests)
}

func TestAttachBodiesCorrectionInBodyIsANewVersion(t *testing.T) {
	store, pages := newFakeStore(), newPageServer(t)
	src := testSource(t, "nhl-player-safety")
	first, _ := attachBodies(t, store, pages, bodyItem(t))
	ingest(t, store, src, first)

	pages.pages[testPageURL] = []byte(`{"parts":[{"type":"markdown","content":"McNabb will forfeit $50,000."}]}`)
	updated := bodyItem(t)
	updated.UpdatedAt = updated.UpdatedAt.Add(time.Hour)
	second, _ := attachBodies(t, store, pages, updated)
	assert.Equal(t, IngestResult{Items: 1, NewVersions: 1}, ingest(t, store, src, second),
		"a correction inside the article, with the same summary, is reprocessed")
}

func TestAttachBodiesStoresSummaryOnlyWhenThePageIsGone(t *testing.T) {
	pages := newPageServer(t)
	pages.pages = map[string][]byte{}
	items, result := attachBodies(t, newFakeStore(), pages, bodyItem(t))

	assert.Equal(t, BodyResult{Unavailable: 1}, result)
	require.Len(t, items, 1)
	assert.Empty(t, items[0].Body)
	assert.Equal(t, nhlFixtureItems(t)[0].Subjects, items[0].Subjects)
}

func TestAttachBodiesUnparseablePageIsUnavailable(t *testing.T) {
	pages := newPageServer(t)
	pages.pages[testPageURL] = []byte("<html>not json</html>")
	items, result := attachBodies(t, newFakeStore(), pages, bodyItem(t))
	assert.Equal(t, BodyResult{Unavailable: 1}, result)
	assert.Len(t, items, 1)
}

func TestAttachBodiesDefersTransientFailures(t *testing.T) {
	tests := map[string]error{
		"server error": &HTTPError{URL: testPageURL, StatusCode: http.StatusServiceUnavailable},
		"rate limited": &HTTPError{URL: testPageURL, StatusCode: http.StatusTooManyRequests},
		"network":      errors.New("connection reset"),
	}
	for name, fetchErr := range tests {
		t.Run(name, func(t *testing.T) {
			pages := newPageServer(t)
			pages.errs[testPageURL] = fetchErr
			items, result := attachBodies(t, newFakeStore(), pages, bodyItem(t))
			assert.Equal(t, BodyResult{Deferred: 1}, result)
			assert.Empty(t, items, "a story whose page may come back is not stored without it")
		})
	}
}

func TestAttachBodiesStopsDeferringAStaleStory(t *testing.T) {
	pages := newPageServer(t)
	pages.errs[testPageURL] = &HTTPError{URL: testPageURL, StatusCode: http.StatusServiceUnavailable}
	item := bodyItem(t)
	stale := func() time.Time { return item.UpdatedAt.Add(MaxBodyDeferral) }
	items, result, err := AttachBodies(context.Background(), newFakeStore(), testSource(t, "nhl-player-safety"),
		[]Item{item}, BodyOptions{Fetch: pages.fetch, Now: stale})
	require.NoError(t, err)
	assert.Equal(t, BodyResult{Unavailable: 1}, result)
	require.Len(t, items, 1, "a flaky page must not keep an older story out of the database")
	assert.Empty(t, items[0].Body)
}

func TestAttachBodiesRefetchesWhenStoredSubjectsAreUnreadable(t *testing.T) {
	store, pages := newFakeStore(), newPageServer(t)
	items, _ := attachBodies(t, store, pages, bodyItem(t))
	ingest(t, store, testSource(t, "nhl-player-safety"), items)
	store.versions[0].Subjects = []byte("not json")

	_, result := attachBodies(t, store, pages, bodyItem(t))
	assert.Equal(t, BodyResult{Fetched: 1}, result)
}

func TestAttachBodiesDefersPastTheDeadline(t *testing.T) {
	pages := newPageServer(t)
	now := time.Date(2026, time.September, 25, 12, 0, 0, 0, time.UTC)
	items, result, err := AttachBodies(context.Background(), newFakeStore(), testSource(t, "nhl-player-safety"),
		[]Item{bodyItem(t)}, BodyOptions{Fetch: pages.fetch, Deadline: now, Now: func() time.Time { return now }})
	require.NoError(t, err)
	assert.Equal(t, BodyResult{Deferred: 1}, result)
	assert.Empty(t, items)
	assert.Zero(t, pages.requests)
}

func TestAttachBodiesPassesThroughItemsWithoutAPage(t *testing.T) {
	pages := newPageServer(t)
	item := nhlFixtureItems(t)[0]
	items, result := attachBodies(t, newFakeStore(), pages, item)
	assert.Equal(t, BodyResult{}, result)
	assert.Equal(t, []Item{item}, items)
	assert.Zero(t, pages.requests)
}

func TestAttachBodiesCancelledContextErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pages := newPageServer(t)
	_, _, err := AttachBodies(ctx, newFakeStore(), testSource(t, "nhl-player-safety"), []Item{bodyItem(t)},
		BodyOptions{Fetch: pages.fetch})
	require.ErrorIs(t, err, context.Canceled)
}

func TestProcessVersionResolvesBodyLinkedPlayersByID(t *testing.T) {
	store, pages := newFakeStore(), newPageServer(t)
	items, _ := attachBodies(t, store, pages, bodyItem(t))
	ingest(t, store, testSource(t, "nhl-player-safety"), items)
	processAll(t, store)

	mentions := store.mentions[store.versions[0].ID]
	var byfield *Mention
	for _, m := range mentions {
		if m.NhlPlayerID.Int64 == byfieldID {
			byfield = &Mention{Text: m.Mention, Role: Role(m.Role), Resolution: Resolution(m.Resolution), Method: Method(m.Method)}
		}
	}
	require.NotNil(t, byfield, "the player only the body links is recorded: %+v", mentions)
	assert.Equal(t, Mention{Text: "Quinton Byfield", Role: RoleMentioned, Resolution: ResolutionResolved, Method: MethodBodyNHLID}, *byfield,
		"a player the body links but the title does not name is mentioned, not a subject")
	assert.Len(t, store.incidentsOf(byfieldID), 0)
	assert.Len(t, store.incidentsOf(mcnabbID), 1)
}
