package player

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/fixtures/metricsfixtures"
	"github.com/sperano/puckdb/internal/httpx"
	"github.com/sperano/puckdb/internal/matching"
	"github.com/sperano/puckdb/internal/metrics"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// missingTestNow is the fixed clock of the 404 marker tests.
var missingTestNow = time.Date(2026, time.November, 20, 12, 0, 0, 0, time.UTC)

func fixedNow() time.Time { return missingTestNow }

// notFoundServer answers 404 for every page, as Yahoo Sports does for a
// retired player, recording each requested URL.
type notFoundServer struct{ requested []string }

func (s *notFoundServer) download(_ context.Context, url string) ([]byte, error) {
	s.requested = append(s.requested, url)
	return nil, &httpx.HTTPError{StatusCode: http.StatusNotFound, Status: "404 Not Found", URL: url}
}

func markerPath(id store.YahooPlayerID) string {
	return resource.MissingYahooPlayer{PlayerID: id}.Path()
}

func writeMarker(t *testing.T, s store.Storage, id store.YahooPlayerID, recordedAt time.Time) {
	t.Helper()
	marker := resource.MissingYahooPlayer{PlayerID: id}
	require.NoError(t, resource.WriteParsed(context.Background(), s, marker, resource.MissingYahooPlayerContent{RecordedAt: recordedAt}))
}

func readMarker(t *testing.T, s store.Storage, id store.YahooPlayerID) resource.MissingYahooPlayerContent {
	t.Helper()
	content, err := resource.ReadParsed(context.Background(), s, resource.MissingYahooPlayer{PlayerID: id})
	require.NoError(t, err)
	return content
}

// A 404 records a marker dated by the clock, and the next batch (a later
// run) does not ask for the page again: it counts it as missing, not
// failed.
func TestAddYahooBirthDates_RecordsA404AndDoesNotAskAgain(t *testing.T) {
	t.Parallel()
	mem := store.NewMemStorage()
	server := &notFoundServer{}
	a := &Activities{Storage: mem, YahooPlayerPages: server.download, Now: fixedNow}

	_, first := matchElias(t, a, eliasPool())
	_, second := matchElias(t, a, eliasPool())

	assert.Equal(t, ProcessPlayerBatchResult{BirthDatePagesFailed: 2}, first)
	assert.Equal(t, ProcessPlayerBatchResult{BirthDatePagesMissing: 2}, second)
	assert.Equal(t, []string{pageURL(eliasForwardID), pageURL(eliasDefenceID)}, server.requested)
	assert.Equal(t, resource.MissingYahooPlayerContent{RecordedAt: missingTestNow}, readMarker(t, mem, eliasForwardID))
	assert.Equal(t, resource.MissingYahooPlayerContent{RecordedAt: missingTestNow}, readMarker(t, mem, eliasDefenceID))
}

// A download error other than 404 records nothing, so the next run asks
// for the page again.
func TestAddYahooBirthDates_OtherDownloadErrorsRecordNoMarker(t *testing.T) {
	t.Parallel()
	mem := store.NewMemStorage()
	server := &pageServer{err: &httpx.HTTPError{StatusCode: http.StatusServiceUnavailable, Status: "503 Service Unavailable"}}
	a := &Activities{Storage: mem, YahooPlayerPages: server.download, Now: fixedNow}

	_, result := matchElias(t, a, eliasPool())

	assert.Equal(t, ProcessPlayerBatchResult{BirthDatePagesFailed: 2}, result)
	assert.False(t, mem.Exists(context.Background(), markerPath(eliasForwardID)))
	assert.False(t, mem.Exists(context.Background(), markerPath(eliasDefenceID)))
}

// A marker as old as missingYahooPlayerPageTTL, or unreadable, no longer
// holds the page back; a younger one does. A stored page is read whatever
// the marker says.
func TestYahooBirthDate_MarkerAge(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		marker        func(t *testing.T, s store.Storage)
		storedPage    bool
		wantErr       error
		wantRequested int
	}{
		{name: "younger than the TTL",
			marker: func(t *testing.T, s store.Storage) {
				writeMarker(t, s, eliasForwardID, missingTestNow.Add(-missingYahooPlayerPageTTL+time.Second))
			},
			wantErr: errYahooPlayerPageMissing},
		{name: "as old as the TTL",
			marker: func(t *testing.T, s store.Storage) {
				writeMarker(t, s, eliasForwardID, missingTestNow.Add(-missingYahooPlayerPageTTL))
			},
			wantRequested: 1},
		{name: "dated after now",
			marker: func(t *testing.T, s store.Storage) {
				writeMarker(t, s, eliasForwardID, missingTestNow.Add(time.Second))
			},
			wantRequested: 1},
		{name: "does not parse",
			marker: func(t *testing.T, s store.Storage) {
				require.NoError(t, s.Write(context.Background(), markerPath(eliasForwardID), []byte("{")))
			},
			wantRequested: 1},
		{name: "stored page wins",
			marker: func(t *testing.T, s store.Storage) {
				writeMarker(t, s, eliasForwardID, missingTestNow)
			},
			storedPage: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mem := store.NewMemStorage()
			tc.marker(t, mem)
			if tc.storedPage {
				require.NoError(t, mem.Write(context.Background(), pagePath(eliasForwardID), yahooPlayerPageHTML(eliasForwardBirth)))
			}
			server := &pageServer{pages: map[store.YahooPlayerID][]byte{eliasForwardID: yahooPlayerPageHTML(eliasForwardBirth)}}
			a := &Activities{Storage: mem, YahooPlayerPages: server.download, Now: fixedNow}

			birth, downloaded, err := a.yahooBirthDate(context.Background(), eliasForwardID)

			assert.Len(t, server.requested, tc.wantRequested)
			assert.Equal(t, tc.wantRequested == 1, downloaded)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				assert.True(t, birth.IsZero())
				return
			}
			require.NoError(t, err)
			assert.Equal(t, eliasForwardBirth, birth)
		})
	}
}

// A marker that cannot be written is logged: the page counts as failed and
// is asked for again next time.
func TestAddYahooBirthDates_MarkerWriteFailureKeepsAskingForThePage(t *testing.T) {
	t.Parallel()
	storage := faultyStorage{Storage: store.NewMemStorage(), writeErr: errors.New("juicefs read-only")}
	server := &notFoundServer{}
	a := &Activities{Storage: storage, YahooPlayerPages: server.download, Now: fixedNow}

	_, first := matchElias(t, a, eliasPool())
	_, second := matchElias(t, a, eliasPool())

	assert.Equal(t, ProcessPlayerBatchResult{BirthDatePagesFailed: 2}, first)
	assert.Equal(t, ProcessPlayerBatchResult{BirthDatePagesFailed: 2}, second)
	assert.Len(t, server.requested, 4)
}

// A known 404 leaves the tied candidates without a birth date, as a failure
// does: the match falls through to the later steps.
func TestAddYahooBirthDates_KnownMissingPageLeavesTheMatchWithoutBirthDates(t *testing.T) {
	t.Parallel()
	mem := store.NewMemStorage()
	writeMarker(t, mem, eliasForwardID, missingTestNow)
	writeMarker(t, mem, eliasDefenceID, missingTestNow)
	a := &Activities{Storage: mem, YahooPlayerPages: (&notFoundServer{}).download, Now: fixedNow}
	pool := eliasPool()

	match, result := matchElias(t, a, pool)

	assert.Equal(t, matching.YahooIDMatchResult{Reason: matching.MatchReasonAmbiguous}, match)
	assert.Equal(t, ProcessPlayerBatchResult{BirthDatePagesMissing: 2}, result)
	assert.True(t, pool[eliasForwardID].BirthDate.IsZero())
}

// A page skipped for its recorded 404 counts as a missing download. Not
// parallel: the download counter is process-global.
func TestYahooBirthDate_KnownMissingPageCountsAsMissingDownload(t *testing.T) {
	mem := store.NewMemStorage()
	writeMarker(t, mem, eliasForwardID, missingTestNow)
	a := &Activities{Storage: mem, Now: fixedNow}
	before := metricsfixtures.DownloadCount(t, core.YahooPlayer, metrics.ResultMissing)
	errorsBefore := metricsfixtures.DownloadCount(t, core.YahooPlayer, metrics.ResultError)

	_, _, err := a.yahooBirthDate(context.Background(), eliasForwardID)

	require.ErrorIs(t, err, errYahooPlayerPageMissing)
	assert.Equal(t, before+1, metricsfixtures.DownloadCount(t, core.YahooPlayer, metrics.ResultMissing))
	assert.Equal(t, errorsBefore, metricsfixtures.DownloadCount(t, core.YahooPlayer, metrics.ResultError))
}
