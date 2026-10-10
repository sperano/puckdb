package newsevent

// PostgreSQL-backed equivalence tests of the two "events near a report"
// rules: the generated ListNewsEventsNear SQL behind LoadNear, which the
// worker reconciles against, and MemoryStore.Near, which the evaluation
// harness and the release gate reconcile against. They skip unless
// PUCKDB_TEST_PG_URL names a test database (see CLAUDE.md "Database-backed
// tests"). The fake database in fakedb_test.go does not run the SQL, so it
// cannot stand in for these.
//
// This file holds the fixture plumbing and the predicate-boundary test;
// store_pg_corpus_test.go replays the evaluation corpus through both stores.

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/database"
	"github.com/sperano/puckdb/internal/news"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	pgEnvTestPGURL     = "PUCKDB_TEST_PG_URL"
	pgTestDBNameMarker = "test"

	// Seed values of the article, version and extraction rows the evidence
	// rows reference; the predicates under test never read them.
	pgSeedSourceID     = "seed"
	pgSeedURL          = "https://example.test/news"
	pgSeedExtractorKey = "seed/labels/v1/v1"
	pgSeedStatus       = "succeeded"

	// pgListAllLimit comfortably exceeds the events any test stores.
	pgListAllLimit = 1000
)

var pgMigrateOnce struct {
	sync.Once
	err error
}

// pgNow is the clock Apply records lifecycle changes with.
var pgNow = time.Date(2027, time.January, 15, 9, 0, 0, 0, time.UTC)

// pgListAllSince predates every report time in the fixtures and the corpus.
var pgListAllSince = time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)

// openNewsEventPGTestDB migrates the test database once and empties the news
// tables. RESTART IDENTITY makes event IDs start at 1 like MemoryStore's, so
// the two stores can be compared event by event.
func openNewsEventPGTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv(pgEnvTestPGURL)
	if dbURL == "" {
		t.Skipf("set %s to run the PostgreSQL news event tests", pgEnvTestPGURL)
	}
	require.Contains(t, dbURL, pgTestDBNameMarker,
		"%s must name a dedicated test database (URL containing %q)", pgEnvTestPGURL, pgTestDBNameMarker)
	pgMigrateOnce.Do(func() { pgMigrateOnce.err = database.MigrateUp(dbURL) })
	require.NoError(t, pgMigrateOnce.err, "migrate test database")

	pool, err := pgxpool.New(context.Background(), dbURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	resetNewsEventTables(t, pool)
	return pool
}

func resetNewsEventTables(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `TRUNCATE news_event_transitions, news_event_evidence, news_events,
		news_extractions, news_incident_evidence, news_incidents, news_mentions, news_article_versions, news_articles
		RESTART IDENTITY CASCADE`)
	require.NoError(t, err)
}

// seedReportRows inserts the article, version and extraction rows a report's
// evidence references, under the report's own IDs. An article or version
// seeded by an earlier report is kept.
func seedReportRows(t *testing.T, pool *pgxpool.Pool, r Report) {
	t.Helper()
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO news_articles (id, publisher, external_id, source_id, kind, url, first_seen_at, last_seen_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7) ON CONFLICT (id) DO NOTHING`,
		r.ArticleID, r.Publisher, fmt.Sprintf("article-%d", r.ArticleID), pgSeedSourceID, string(r.Kind), pgSeedURL, r.ReportedAt)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO news_article_versions (id, article_id, version, content_hash, title_fingerprint,
		text_fingerprint, title, evidence_text, url, published_at, retrieved_at)
		VALUES ($1, $2, $3, $4, $4, $4, $5, $5, $6, $7, $7) ON CONFLICT (id) DO NOTHING`,
		r.VersionID, r.ArticleID, r.Version, fmt.Sprintf("hash-%d", r.VersionID), fmt.Sprintf("version %d", r.VersionID),
		pgSeedURL, r.ReportedAt)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO news_extractions (id, version_id, extractor_key, provider, model, prompt_version,
		schema_version, input_hash, status)
		VALUES ($1, $2, $3, $4, $4, $4, $4, $5, $6) ON CONFLICT (id) DO NOTHING`,
		r.ExtractionID, r.VersionID, pgSeedExtractorKey, pgSeedSourceID, fmt.Sprintf("input-%d", r.VersionID), pgSeedStatus)
	require.NoError(t, err)
}

// supportReport is the report a Support entry stands for, as seedReportRows
// and InsertNewsEventEvidence need it. The version's extraction shares its
// ID, and its version number is the version ID, which the fixtures keep
// unique per article.
func supportReport(s Support, at time.Time) Report {
	return Report{
		VersionID: s.VersionID, ArticleID: s.ArticleID, Version: int(s.VersionID), Publisher: s.Publisher, Kind: s.Kind,
		ReportedAt: at, ExtractionID: s.VersionID,
	}
}

// insertStoredEvent stores ev through the production write queries: created
// at its first report, widened to its last, moved to its lifecycle, and
// attached to each supporting version. It requires the sequence to hand out
// ev.ID, which the RESTART IDENTITY reset and insertion in ID order ensure.
func insertStoredEvent(t *testing.T, q *sqlcdb.Queries, pool *pgxpool.Pool, ev StoredEvent) {
	t.Helper()
	ctx := context.Background()
	e := ev.Event
	id, err := q.CreateNewsEvent(ctx, sqlcdb.CreateNewsEventParams{
		NhlPlayerID: pgInt8(e.Player.NHLPlayerID), YahooPlayerID: pgInt4(e.Player.YahooPlayerID), PlayerName: e.Player.Name,
		EventType: string(e.Type), ReportStatus: string(e.Status), DurationKind: string(e.Duration.Kind),
		DurationGames: pgInt4(e.Duration.Games), DurationDays: pgInt4(e.Duration.Days),
		Lifecycle: string(LifecycleActive), FirstReportedAt: news.Timestamptz(ev.FirstReportedAt),
	})
	require.NoError(t, err)
	require.Equal(t, ev.ID, id, "fixture events must be inserted in ID order")
	require.NoError(t, q.TouchNewsEvent(ctx, sqlcdb.TouchNewsEventParams{ID: id, ReportedAt: news.Timestamptz(ev.LastReportedAt)}))
	if ev.Lifecycle != LifecycleActive {
		require.NoError(t, q.SetNewsEventLifecycle(ctx, sqlcdb.SetNewsEventLifecycleParams{
			ID: id, Lifecycle: string(ev.Lifecycle), LifecycleChangedAt: news.Timestamptz(pgNow),
		}))
	}
	for _, s := range ev.Support {
		insertEvidence(t, q, pool, id, supportReport(s, ev.LastReportedAt), RelationSupports)
	}
}

// insertEvidence seeds the report's rows and attaches it to an event.
func insertEvidence(t *testing.T, q *sqlcdb.Queries, pool *pgxpool.Pool, eventID int64, r Report, relation Relation) {
	t.Helper()
	seedReportRows(t, pool, r)
	added, err := q.InsertNewsEventEvidence(context.Background(), sqlcdb.InsertNewsEventEvidenceParams{
		EventID: eventID, VersionID: r.VersionID, ExtractionID: r.ExtractionID, Relation: string(relation),
		ArticleID: r.ArticleID, Publisher: r.Publisher, Kind: string(r.Kind), ReportedAt: news.Timestamptz(r.ReportedAt),
		Quotes: []byte("[]"),
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, added)
}

// comparableEvents puts both stores' events in a form that can be compared.
// A news_events row does not carry the quotes (the event's, the length's and
// the move's) or the player's team, and the memory store keeps review flags
// apart in Reviews, so those are cleared; Reconcile reads none of them from
// events on record (it reads the player, type, status, facts, lifecycle, span
// and support). Support is sorted as
// ListNewsEventSupport orders it, and events by (first_reported_at, id) as
// ListNewsEventsNear orders them.
func comparableEvents(events []StoredEvent) []StoredEvent {
	out := make([]StoredEvent, len(events))
	for i, ev := range events {
		ev.Event.Evidence = nil
		ev.Event.Duration.Quote, ev.Event.Change.Quote = "", ""
		ev.Event.Player.Team = ""
		ev.Event.NeedsReview, ev.Event.ReviewReason = false, ""
		ev.Support = slices.Clone(ev.Support)
		slices.SortFunc(ev.Support, func(a, b Support) int {
			return cmp.Or(cmp.Compare(a.ArticleID, b.ArticleID), cmp.Compare(a.VersionID, b.VersionID))
		})
		if len(ev.Support) == 0 {
			ev.Support = nil
		}
		out[i] = ev
	}
	slices.SortFunc(out, func(a, b StoredEvent) int {
		return cmp.Or(a.FirstReportedAt.Compare(b.FirstReportedAt), cmp.Compare(a.ID, b.ID))
	})
	return out
}

func eventIDs(events []StoredEvent) []int64 {
	ids := make([]int64, len(events))
	for i, ev := range events {
		ids[i] = ev.ID
	}
	return ids
}

// assertSameNear asserts that the database and the memory store return the
// same events, with the same lifecycle, span and support, for one report.
func assertSameNear(t *testing.T, mem, pg []StoredEvent) {
	t.Helper()
	assert.Equal(t, eventIDs(comparableEvents(mem)), eventIDs(pg), "ListNewsEventsNear orders by first_reported_at, id")
	assert.Equal(t, comparableEvents(mem), comparableEvents(pg))
}

// pgSelectEvents lists the memory store's events as the database holds
// them, by ID, with their support.
func pgSelectEvents(t *testing.T, q *sqlcdb.Queries) []StoredEvent {
	t.Helper()
	ctx := context.Background()
	rows, err := q.ListRecentNewsEvents(ctx, sqlcdb.ListRecentNewsEventsParams{
		LastReportedAt: news.Timestamptz(pgListAllSince), Limit: pgListAllLimit,
	})
	require.NoError(t, err)
	events := make([]StoredEvent, len(rows))
	index := make(map[int64]int, len(rows))
	for i, row := range rows {
		events[i], index[row.ID] = eventFromRow(row), i
	}
	support, err := q.ListNewsEventSupport(ctx, eventIDs(events))
	require.NoError(t, err)
	for _, s := range support {
		ev := &events[index[s.EventID]]
		ev.Support = append(ev.Support, Support{ArticleID: s.ArticleID, VersionID: s.VersionID, Publisher: s.Publisher, Kind: news.Kind(s.Kind)})
	}
	slices.SortFunc(events, func(a, b StoredEvent) int { return cmp.Compare(a.ID, b.ID) })
	return events
}

// pgReviewState reads an event's review flag and reasons.
func pgReviewState(t *testing.T, pool *pgxpool.Pool, id int64) (bool, string) {
	t.Helper()
	var needsReview bool
	var reason string
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT needs_review, review_reason FROM news_events WHERE id = $1`, id).Scan(&needsReview, &reason))
	return needsReview, reason
}

// assertSameStore asserts that the database holds exactly the memory
// store's events: the same IDs, facts, lifecycle, span, support and review
// reasons.
func assertSameStore(t *testing.T, mem *MemoryStore, q *sqlcdb.Queries, pool *pgxpool.Pool) {
	t.Helper()
	pg := pgSelectEvents(t, q)
	require.Equal(t, eventIDs(mem.Events), eventIDs(pg))
	assert.Equal(t, comparableEvents(mem.Events), comparableEvents(pg))
	for _, ev := range mem.Events {
		needsReview, reason := pgReviewState(t, pool, ev.ID)
		reasons := mem.Reviews[ev.ID]
		assert.Equal(t, len(reasons) > 0, needsReview, "event %d needs_review", ev.ID)
		assert.Equal(t, strings.Join(reasons, reviewSeparator), reason, "event %d review_reason", ev.ID)
	}
}
