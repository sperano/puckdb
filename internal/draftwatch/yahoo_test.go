package draftwatch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/httpx"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testDraftIdentity = Identity{LeagueKey: "500.l.5621", Season: 2026, LeagueID: 5621, GameKey: 500}

func TestBuildSnapshotClassifiesPartialAndMalformedRows(t *testing.T) {
	content := &store.FantasyContent{League: store.League{DraftResults: store.DraftResultList{
		HasCount: true, Count: 3,
		Slice: []store.DraftResult{
			{Round: 1, Pick: 1, TeamKey: "500.l.5621.t.2", PlayerKey: "500.p.101"},
			{Round: 1, Pick: 2, TeamKey: "wrong", PlayerKey: "500.p.102"},
		},
	}}}

	snapshot := buildSnapshot(testDraftIdentity, content, "drafting")

	assert.True(t, snapshot.Authoritative, "positive partial snapshots may merge valid rows")
	assert.Equal(t, 3, snapshot.ExpectedCount)
	assert.Equal(t, 2, snapshot.RawCount)
	require.Len(t, snapshot.Picks, 2)
	assert.Empty(t, snapshot.Picks[0].Issue)
	assert.NotEmpty(t, snapshot.Picks[1].Issue)
}

func TestBuildSnapshotRequiresExplicitPredraftEvidenceForEmptyReset(t *testing.T) {
	content := &store.FantasyContent{League: store.League{DraftResults: store.DraftResultList{HasCount: true}}}

	assert.False(t, buildSnapshot(testDraftIdentity, content, "drafting").Authoritative)
	assert.False(t, buildSnapshot(testDraftIdentity, content, "postdraft").Authoritative)
	assert.True(t, buildSnapshot(testDraftIdentity, content, "predraft").Authoritative)
	content.League.DraftResults.HasCount = false
	assert.False(t, buildSnapshot(testDraftIdentity, content, "predraft").Authoritative)
}

func TestClassifyErrorDistinguishesAuthenticationAndRateLimit(t *testing.T) {
	assert.Equal(t, ErrorClassNone, ClassifyError(nil))
	assert.Equal(t, ErrorClassAuthentication, ClassifyError(&cache.OAuth2TokenMissingError{}))
	assert.Equal(t, ErrorClassAuthentication, ClassifyError(fmt.Errorf("wrapped: %w", &httpx.HTTPError{StatusCode: http.StatusUnauthorized})))
	assert.Equal(t, ErrorClassRateLimited, ClassifyError(&httpx.HTTPError{StatusCode: http.StatusTooManyRequests}))
	assert.Equal(t, ErrorClassUpstream, ClassifyError(&httpx.HTTPError{StatusCode: http.StatusServiceUnavailable}))
	assert.Equal(t, ErrorClassHTTP, ClassifyError(&httpx.HTTPError{StatusCode: http.StatusNotFound}))
	assert.Equal(t, ErrorClassCanceled, ClassifyError(fmt.Errorf("poll: %w", context.DeadlineExceeded)))
	assert.Equal(t, ErrorClassTransport, ClassifyError(errors.New("connection reset")))
}

// The stored class strings are read back by the capability report and must
// not change with the Go constant names.
func TestErrorClassStoredValuesAreStable(t *testing.T) {
	stored := map[ErrorClass]string{
		ErrorClassNone: "", ErrorClassAuthentication: "authentication", ErrorClassRateLimited: "rate_limited",
		ErrorClassUpstream: "upstream", ErrorClassHTTP: "http", ErrorClassCanceled: "canceled",
		ErrorClassTransport: "transport",
	}
	for class, want := range stored {
		assert.Equal(t, want, string(class))
	}
}
