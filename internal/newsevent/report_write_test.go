package newsevent

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sperano/puckdb/internal/news"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// digestFixture builds a small, hand-wired set of events, evidence,
// transitions and extraction-review rows exercising every element
// WriteEventDigest renders: a quoted, needs-review event; an event
// superseded by another (the pointer and its history); and two extractions
// that need a person (invalid output, and a failure out of attempts).
type digestFixture struct {
	db                     *fakeDB
	eventA, eventB, eventC int64
	playerA                news.Identity
	now                    time.Time
}

func newDigestFixture(t *testing.T) digestFixture {
	t.Helper()
	db := newFakeDB()
	ctx := context.Background()
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	f := digestFixture{db: db, now: now, playerA: news.Identity{NHLPlayerID: 5001, Name: "Alex Rivers"}}

	f.eventA = f.createReviewedEvent(t, ctx)
	f.eventB, f.eventC = f.createSupersededPair(t, ctx)
	f.seedReviews(t)
	return f
}

// createReviewedEvent seeds event A: active, quoted evidence, flagged for
// review, with its creation history.
func (f *digestFixture) createReviewedEvent(t *testing.T, ctx context.Context) int64 {
	t.Helper()
	db := f.db
	id, err := db.CreateNewsEvent(ctx, sqlcdb.CreateNewsEventParams{
		NhlPlayerID: pgInt8(f.playerA.NHLPlayerID), PlayerName: f.playerA.Name, EventType: string(TypeInjury),
		ReportStatus: string(StatusConfirmed), DurationKind: string(DurationUnknown), Lifecycle: string(LifecycleActive),
		FirstReportedAt: news.Timestamptz(f.now.Add(-48 * time.Hour)), NeedsReview: true,
		ReviewReason: "conflicts with event 2",
	})
	require.NoError(t, err)

	db.addVersion(10, fakeVersionInfo{ArticleID: 900, Version: 1, Title: "Rivers hurt", URL: "http://x/1", Publisher: "NHL.com"})
	quotes, err := json.Marshal([]Quote{{Doc: "E1", Text: "placed on injured reserve"}})
	require.NoError(t, err)
	_, err = db.InsertNewsEventEvidence(ctx, sqlcdb.InsertNewsEventEvidenceParams{
		EventID: id, VersionID: 10, ExtractionID: 100, Relation: string(RelationSupports), ArticleID: 900,
		Publisher: "NHL.com", Kind: string(news.KindOfficial), ReportedAt: news.Timestamptz(f.now.Add(-48 * time.Hour)), Quotes: quotes,
	})
	require.NoError(t, err)
	require.NoError(t, db.InsertNewsEventTransition(ctx, sqlcdb.InsertNewsEventTransitionParams{
		EventID: id, ToLifecycle: string(LifecycleActive), Reason: "reported by NHL.com", At: news.Timestamptz(f.now.Add(-48 * time.Hour)),
	}))
	return id
}

// createSupersededPair seeds event B, active then superseded by event C.
func (f *digestFixture) createSupersededPair(t *testing.T, ctx context.Context) (int64, int64) {
	t.Helper()
	db := f.db
	b, err := db.CreateNewsEvent(ctx, sqlcdb.CreateNewsEventParams{
		NhlPlayerID: pgInt8(5002), PlayerName: "Owen Chase", EventType: string(TypeSuspension),
		ReportStatus: string(StatusReported), DurationKind: string(DurationGames), DurationGames: pgInt4(2),
		Lifecycle: string(LifecycleActive), FirstReportedAt: news.Timestamptz(f.now.Add(-6 * time.Hour)),
	})
	require.NoError(t, err)
	require.NoError(t, db.InsertNewsEventTransition(ctx, sqlcdb.InsertNewsEventTransitionParams{
		EventID: b, ToLifecycle: string(LifecycleActive), Reason: "reported by Local Beat", At: news.Timestamptz(f.now.Add(-6 * time.Hour)),
	}))

	c, err := db.CreateNewsEvent(ctx, sqlcdb.CreateNewsEventParams{
		NhlPlayerID: pgInt8(5002), PlayerName: "Owen Chase", EventType: string(TypeSuspension),
		ReportStatus: string(StatusReported), DurationKind: string(DurationGames), DurationGames: pgInt4(4),
		Lifecycle: string(LifecycleActive), FirstReportedAt: news.Timestamptz(f.now.Add(-1 * time.Hour)),
	})
	require.NoError(t, err)

	require.NoError(t, db.SetNewsEventLifecycle(ctx, sqlcdb.SetNewsEventLifecycleParams{
		ID: b, Lifecycle: string(LifecycleSuperseded), LifecycleReason: "updated by Local Beat version 9",
		LifecycleChangedAt: news.Timestamptz(f.now.Add(-1 * time.Hour)), SupersededBy: pgInt8(c),
	}))
	require.NoError(t, db.InsertNewsEventTransition(ctx, sqlcdb.InsertNewsEventTransitionParams{
		EventID: b, FromLifecycle: string(LifecycleActive), ToLifecycle: string(LifecycleSuperseded),
		Reason: "updated by Local Beat version 9", At: news.Timestamptz(f.now.Add(-1 * time.Hour)),
	}))
	return b, c
}

// seedReviews seeds two extraction rows ListNewsExtractionReviews must
// surface: an invalid reply with issues, and a failure out of attempts.
func (f *digestFixture) seedReviews(t *testing.T) {
	t.Helper()
	db := f.db
	db.addVersion(20, fakeVersionInfo{ArticleID: 901, Version: 1, Title: "Bad Reply Article", URL: "http://x/2", Publisher: "Local Beat"})
	issues, err := json.Marshal([]Issue{{Code: IssueQuoteNotFound, Event: 0, Message: `quote "xyz" is not in document E1`}})
	require.NoError(t, err)
	db.seedExtraction(sqlcdb.NewsExtraction{
		VersionID: 20, ExtractorKey: "prov/model-a", Status: string(ExtractionInvalid), Attempts: 1,
		LastAttemptAt: news.Timestamptz(f.now), Issues: issues,
	})

	db.addVersion(21, fakeVersionInfo{ArticleID: 902, Version: 1, Title: "Flaky Source Article", URL: "http://x/3", Publisher: "Wire Service"})
	db.seedExtraction(sqlcdb.NewsExtraction{
		VersionID: 21, ExtractorKey: "prov/model-a", Status: string(ExtractionFailed), Attempts: 3,
		LastAttemptAt: news.Timestamptz(f.now), LastError: "connection reset", Issues: []byte(`[]`),
	})
}

func TestLoadWriteEventDigest_Populated(t *testing.T) {
	f := newDigestFixture(t)
	req := DigestRequest{Since: f.now.Add(-72 * time.Hour), Limit: 50, MaxAttempts: 3, Player: f.playerA}

	d, err := LoadEventDigest(context.Background(), f.db, req, f.now)
	require.NoError(t, err)

	var buf bytes.Buffer
	require.NoError(t, WriteEventDigest(&buf, d))
	out := buf.String()

	assertReviewTable(t, out)
	assertReviewedEvent(t, out, f.eventA)
	assertSupersededPair(t, out, f.eventC)
	assert.Contains(t, out, "## Alex Rivers (NHL 5001): every event")
	assert.Contains(t, out, "## Events reported since ")
}

func assertReviewTable(t *testing.T, out string) {
	t.Helper()
	assert.Contains(t, out, "## Extractions to review")
	assert.Contains(t, out, "| Extraction | Status | Attempts | Article | Publisher | Problems |")
	assert.Contains(t, out, "[Bad Reply Article](http://x/2)")
	assert.Contains(t, out, `quote "xyz" is not in document E1`)
	assert.Contains(t, out, "[Flaky Source Article](http://x/3)")
	assert.Contains(t, out, "connection reset")
}

func assertReviewedEvent(t *testing.T, out string, eventA int64) {
	t.Helper()
	assert.Contains(t, out, "Rivers hurt")
	assert.Contains(t, out, "> placed on injured reserve")
	assert.Contains(t, out, "**Needs review:** conflicts with event 2")
	assert.Contains(t, out, "History 2026-02-27 12:00 UTC: new → active")
	assert.Equal(t, int64(1), eventA)
}

func assertSupersededPair(t *testing.T, out string, eventC int64) {
	t.Helper()
	assert.Contains(t, out, "superseded: updated by Local Beat version 9")
	assert.Contains(t, out, "(replaced by event 3)")
	assert.Contains(t, out, "active → superseded")
	assert.Equal(t, int64(3), eventC)
}

func TestLoadWriteEventDigest_EmptyShowsAbsenceCaveats(t *testing.T) {
	db := newFakeDB()
	req := DigestRequest{Since: time.Now().Add(-time.Hour), Limit: 10, MaxAttempts: 3}

	d, err := LoadEventDigest(context.Background(), db, req, time.Now())
	require.NoError(t, err)
	assert.Nil(t, d.Player)

	var buf bytes.Buffer
	require.NoError(t, WriteEventDigest(&buf, d))
	out := buf.String()

	assert.Contains(t, out, "## Extractions to review\n\nNone.\n\n")
	assert.Contains(t, out, "None on record. "+absenceCaveat)
	assert.NotContains(t, out, "every event")
}
