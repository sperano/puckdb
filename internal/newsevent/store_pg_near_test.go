package newsevent

// Predicate-boundary test of ListNewsEventsNear against MemoryStore.Near on
// one hand-built set of events: every clause of the SQL (player match by
// either ID, active lifecycle, the since bound, support by the report's
// article) is exercised on both sides of its boundary. The fixture plumbing
// is in store_pg_test.go.

import (
	"context"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/news"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	pgNearWindow = 14 * 24 * time.Hour
	// pgNearShortWindow is a second window, so the since bound moves.
	pgNearShortWindow = time.Hour

	// Players: A is named by NHL ID, B by Yahoo ID; C and D are not named.
	pgPlayerANHLID   int64 = 8470001
	pgPlayerAYahooID       = 6001
	pgPlayerBYahooID       = 6002
	pgPlayerCNHLID   int64 = 8470003
	pgPlayerDYahooID       = 6004

	pgPublisher = "Seed Press"

	// The reconciled report's article and another one.
	pgReportArticleID int64 = 100
	pgOtherArticleID  int64 = 200
	// Versions: an earlier version of the report's article, one carrying a
	// contradiction, the report itself, and the other article's.
	pgEarlierVersionID       int64 = 101
	pgContradictingVersionID int64 = 102
	pgReportVersionID        int64 = 103
	pgOtherVersionID         int64 = 201

	// Fixture event IDs, in insertion order.
	pgEvActiveOld           int64 = 1
	pgEvLastAtSince         int64 = 2
	pgEvLastBeforeSince     int64 = 3
	pgEvReportedLater       int64 = 4
	pgEvUnnamedActive       int64 = 5
	pgEvSupportedByArticle  int64 = 6
	pgEvContradictedOnly    int64 = 7
	pgEvSupportedByOther    int64 = 8
	pgEvYahooOnlyNamed      int64 = 9
	pgEvYahooOnlyUnnamed    int64 = 10
	pgEvNamedByNHLWithYahoo int64 = 11
	pgEvNamedByYahooOnly    int64 = 12
)

var pgReportedAt = time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)

var (
	pgPlayerA = news.Identity{NHLPlayerID: pgPlayerANHLID, Name: "Player A"}
	pgPlayerB = news.Identity{YahooPlayerID: pgPlayerBYahooID, Name: "Player B"}
	pgPlayerC = news.Identity{NHLPlayerID: pgPlayerCNHLID, Name: "Player C"}
	pgPlayerD = news.Identity{YahooPlayerID: pgPlayerDYahooID, Name: "Player D"}
	// pgUnresolved has no ID at all: it must match nothing, NULL columns
	// included.
	pgUnresolved = news.Identity{Name: "Unresolved"}
)

func pgStored(id int64, p news.Identity, lc Lifecycle, first, last time.Time, support ...Support) StoredEvent {
	return StoredEvent{
		ID:        id,
		Event:     Event{Player: p, Type: TypeInjury, Status: StatusReported, Duration: Duration{Kind: DurationUnknown}},
		Lifecycle: lc, FirstReportedAt: first, LastReportedAt: last, Support: support,
	}
}

// pgNearFixture is the set of events on record. Times are whole
// microseconds: timestamptz keeps no finer precision, so a finer boundary
// would test rounding, not the predicate.
func pgNearFixture() []StoredEvent {
	since := pgReportedAt.Add(-pgNearWindow)
	old := since.Add(-7 * 24 * time.Hour)
	aWithYahoo, cWithBYahoo := pgPlayerA, pgPlayerC
	aWithYahoo.YahooPlayerID = pgPlayerAYahooID
	cWithBYahoo.YahooPlayerID = pgPlayerBYahooID
	fromReportArticle := Support{ArticleID: pgReportArticleID, VersionID: pgEarlierVersionID, Publisher: pgPublisher, Kind: news.KindReporting}
	fromOtherArticle := Support{ArticleID: pgOtherArticleID, VersionID: pgOtherVersionID, Publisher: pgPublisher, Kind: news.KindReporting}
	return []StoredEvent{
		pgStored(pgEvActiveOld, pgPlayerA, LifecycleActive, old, old),
		pgStored(pgEvLastAtSince, pgPlayerA, LifecycleSuperseded, old.Add(time.Hour), since),
		pgStored(pgEvLastBeforeSince, pgPlayerA, LifecycleSuperseded, old, since.Add(-time.Microsecond)),
		pgStored(pgEvReportedLater, pgPlayerA, LifecycleResolved, pgReportedAt.Add(time.Hour), pgReportedAt.Add(time.Hour)),
		pgStored(pgEvUnnamedActive, pgPlayerC, LifecycleActive, pgReportedAt.Add(-time.Hour), pgReportedAt.Add(-time.Hour)),
		pgStored(pgEvSupportedByArticle, pgPlayerC, LifecycleRetracted, old.Add(-24*time.Hour), old, fromReportArticle),
		pgStored(pgEvContradictedOnly, pgPlayerC, LifecycleActive, old, old),
		pgStored(pgEvSupportedByOther, pgPlayerC, LifecycleActive, old, old, fromOtherArticle),
		pgStored(pgEvYahooOnlyNamed, pgPlayerB, LifecycleSuperseded, pgReportedAt.Add(-time.Hour), pgReportedAt.Add(-time.Hour)),
		pgStored(pgEvYahooOnlyUnnamed, pgPlayerD, LifecycleActive, old, old),
		pgStored(pgEvNamedByNHLWithYahoo, aWithYahoo, LifecycleSuperseded, old, pgReportedAt.Add(-2*time.Hour)),
		pgStored(pgEvNamedByYahooOnly, cWithBYahoo, LifecycleSuperseded, pgReportedAt.Add(-2*time.Hour), pgReportedAt.Add(-time.Hour)),
	}
}

func TestPGListNewsEventsNearMatchesMemoryStoreAtBoundaries(t *testing.T) {
	pool := openNewsEventPGTestDB(t)
	q := sqlcdb.New(pool)
	fixture := pgNearFixture()
	for _, ev := range fixture {
		insertStoredEvent(t, q, pool, ev)
	}
	// A contradiction from the report's article is evidence the SQL must not
	// count as support; the memory store never records one.
	contradiction := supportReport(Support{
		ArticleID: pgReportArticleID, VersionID: pgContradictingVersionID, Publisher: pgPublisher, Kind: news.KindReporting,
	}, pgReportedAt.Add(-time.Hour))
	insertEvidence(t, q, pool, pgEvContradictedOnly, contradiction, RelationContradicts)
	mem := &MemoryStore{Events: fixture}

	cases := []struct {
		name    string
		players []news.Identity
		article int64
		window  time.Duration
		// want lists the IDs in the order ListNewsEventsNear returns them
		// (first_reported_at, then id).
		want []int64
	}{
		{
			name: "named players", players: []news.Identity{pgPlayerA, pgPlayerB, pgUnresolved}, article: pgReportArticleID, window: pgNearWindow,
			want: []int64{pgEvSupportedByArticle, pgEvActiveOld, pgEvNamedByNHLWithYahoo, pgEvLastAtSince, pgEvNamedByYahooOnly, pgEvYahooOnlyNamed, pgEvReportedLater},
		},
		{
			name: "no players", players: nil, article: pgReportArticleID, window: pgNearWindow,
			want: []int64{pgEvSupportedByArticle},
		},
		{
			name: "other article and player", players: []news.Identity{pgPlayerC}, article: pgOtherArticleID, window: pgNearWindow,
			want: []int64{pgEvContradictedOnly, pgEvSupportedByOther, pgEvNamedByYahooOnly, pgEvUnnamedActive},
		},
		{
			name: "short window", players: []news.Identity{pgPlayerA, pgPlayerB}, article: pgReportArticleID, window: pgNearShortWindow,
			want: []int64{pgEvSupportedByArticle, pgEvActiveOld, pgEvNamedByYahooOnly, pgEvYahooOnlyNamed, pgEvReportedLater},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := Report{
				VersionID: pgReportVersionID, ArticleID: tc.article, Version: int(pgReportVersionID),
				Publisher: pgPublisher, Kind: news.KindReporting, ReportedAt: pgReportedAt,
			}
			pg, err := LoadNear(context.Background(), q, tc.players, r, tc.window)
			require.NoError(t, err)
			assert.Equal(t, tc.want, eventIDs(pg))
			assertSameNear(t, mem.Near(tc.players, r, tc.window), pg)
		})
	}
}
