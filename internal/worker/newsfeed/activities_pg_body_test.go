package newsfeed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/sperano/puckdb/internal/news"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	pgStoryListPath = "/stories"
	pgStoryPagePath = "/stories/mcnabb"
	pgByfieldID     = 8482124
	// pgMcNabbUpdated is the McNabb story's lastUpdatedDate in
	// testdata/nhl-player-safety.json.
	pgMcNabbUpdated = `"lastUpdatedDate": "2026-09-22T17:05:00Z"`
)

// nhlBodyFakeServer serves the NHL content fixture as a tag feed whose
// McNabb story links a story page, and that page, which a test can make
// fail. Requests are sequential, as in rssFakeServer.
type nhlBodyFakeServer struct {
	mu           sync.Mutex
	list         []byte
	page         []byte
	pageStatus   int
	pageRequests int
	srv          *httptest.Server
}

func newNHLBodyFakeServer(t *testing.T) *nhlBodyFakeServer {
	t.Helper()
	f := &nhlBodyFakeServer{page: readNewsTestdata(t, "nhl-story-page.json"), pageStatus: http.StatusOK}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	f.list = f.listWith(t)
	return f
}

// listWith is the fixture feed with the McNabb story's page URL added and
// the given replacements applied.
func (f *nhlBodyFakeServer) listWith(t *testing.T, replacements ...[2]string) []byte {
	t.Helper()
	selfURL := `"_entityId": "` + pgMcNabbOfficialEntityID + `",` + "\n" +
		`      "selfUrl": "` + f.srv.URL + pgStoryPagePath + `",`
	all := append([][2]string{{`"_entityId": "` + pgMcNabbOfficialEntityID + `",`, selfURL}}, replacements...)
	return mutateNHLFixture(t, all...)
}

func (f *nhlBodyFakeServer) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.URL.Path {
	case pgStoryListPath:
		_, _ = w.Write(f.list)
	case pgStoryPagePath:
		f.pageRequests++
		if f.pageStatus != http.StatusOK {
			w.WriteHeader(f.pageStatus)
			return
		}
		_, _ = w.Write(f.page)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *nhlBodyFakeServer) set(list []byte, pageStatus int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.list, f.pageStatus = list, pageStatus
}

func (f *nhlBodyFakeServer) requests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pageRequests
}

// latestBody reads the body and subjects of an article's latest version.
func latestBody(t *testing.T, acts *Activities, externalID string) (string, []news.Subject) {
	t.Helper()
	row, err := acts.Queries.GetLatestNewsArticleBody(context.Background(), sqlcdb.GetLatestNewsArticleBodyParams{
		Publisher: pgNHLContentPublisher, ExternalID: externalID,
	})
	require.NoError(t, err)
	var subjects []news.Subject
	require.NoError(t, json.Unmarshal(row.Subjects, &subjects))
	return row.Body, subjects
}

func TestPGFetchNewsSourceNHLStoryBodies(t *testing.T) {
	pool := openNewsPGTestDB(t)
	fake := newNHLBodyFakeServer(t)
	acts, env := newNewsActivities(pool, fake.srv.Client(), pgFixedNow)
	src := defaultNewsSource(t, "nhl-player-safety", fake.srv.URL+pgStoryListPath)
	require.True(t, src.FetchBody)

	first := fetchSource(t, env, acts, src)
	assert.Equal(t, news.BodyResult{Fetched: 1}, first.Bodies, "only the story with a page URL is downloaded")
	assert.Equal(t, news.IngestResult{Items: 3, NewVersions: 3}, first.Stored)
	body, subjects := latestBody(t, acts, pgMcNabbOfficialEntityID)
	assert.Contains(t, body, "Players' Emergency Assistance Fund", "the full text is stored")
	assert.Contains(t, subjects, news.Subject{NHLPlayerID: pgByfieldID, Name: "Quinton Byfield", InBody: true})

	// Restamped an hour before pgFixedNow, so a failing page is deferred
	// rather than given up on (news.MaxBodyDeferral).
	restamped := fake.listWith(t, [2]string{pgMcNabbUpdated, `"lastUpdatedDate": "2026-09-25T11:00:00Z"`})
	fake.set(restamped, http.StatusServiceUnavailable)
	second := fetchSource(t, env, acts, src)
	assert.Equal(t, news.BodyResult{Deferred: 1}, second.Bodies)
	assert.Equal(t, news.IngestResult{Items: 2, Unchanged: 2}, second.Stored, "the deferred story is not stored")
	assert.Empty(t, readFetchState(t, pool, src.ID).bodyHash, "the feed's validators are dropped so the next refresh reads it again")

	fake.set(restamped, http.StatusOK)
	third := fetchSource(t, env, acts, src)
	assert.False(t, third.NotModified, "the same feed body is read again after a deferral")
	assert.Equal(t, news.BodyResult{Fetched: 1}, third.Bodies)
	assert.Equal(t, news.IngestResult{Items: 3, Unchanged: 3}, third.Stored, "a re-stamped story with the same text is not a new version")
	assert.Equal(t, 1, countVersionsFor(t, pool, pgNHLContentPublisher, pgMcNabbOfficialEntityID))

	fake.set(fake.listWith(t, [2]string{pgMcNabbUpdated, `"lastUpdatedDate": "2026-09-25T11:00:00Z"`},
		[2]string{"announced today.", "announced on Monday."}), http.StatusOK)
	fourth := fetchSource(t, env, acts, src)
	assert.Equal(t, news.BodyResult{Reused: 1}, fourth.Bodies, "the story's update time did not move since the last download")
	assert.Equal(t, news.IngestResult{Items: 3, NewVersions: 1, Unchanged: 2}, fourth.Stored,
		"a changed summary is a new version that keeps the stored body")
	assert.Equal(t, 3, fake.requests())
}
