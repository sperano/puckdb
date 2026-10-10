package newsfeed

// PostgreSQL-backed tests of processVersion's transaction: the advisory
// lock it waits for, the processed-at check under that lock, and commit or
// rollback of the whole version. See activities_pg_helpers_test.go for
// fixtures.

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/news"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// pgLockWaitTimeout bounds how long processVersion may wait for a lock
	// in these tests: ample for a free lock, short enough to keep a wait on
	// a held lock cheap.
	pgLockWaitTimeout = 2 * time.Second
	// pgBogusCategory fails the news_incidents category check, so the
	// version fails after its mentions were written.
	pgBogusCategory = "bogus"
	// pgEarlierProcessing is how long before pgFixedNow another refresh
	// processed a version in the short-circuit test.
	pgEarlierProcessing = time.Hour
)

// pgUnprocessedVersion is the official McNabb suspension story, stored but
// not yet processed, with what processVersion needs to process it.
type pgUnprocessedVersion struct {
	acts *Activities
	dir  *news.Directory
	row  sqlcdb.ListUnprocessedNewsVersionsRow
}

func seedUnprocessedMcNabb(t *testing.T, pool *pgxpool.Pool) pgUnprocessedVersion {
	t.Helper()
	seedNewsDirectory(t, pool)
	srv := staticServer(t, http.StatusOK, readNewsTestdata(t, "nhl-player-safety.json"))
	acts, env := newNewsActivities(pool, http.DefaultClient, pgFixedNow)
	fetchSource(t, env, acts, defaultNewsSource(t, "nhl-player-safety", srv.URL))

	ctx := context.Background()
	rows, err := acts.Queries.ListUnprocessedNewsVersions(ctx, pgLargeBatch)
	require.NoError(t, err)
	dir, err := news.LoadDirectory(ctx, acts.Queries, pgYahooSeason)
	require.NoError(t, err)
	for _, row := range rows {
		if strings.Contains(row.Title, "McNabb") {
			return pgUnprocessedVersion{acts: acts, dir: dir, row: row}
		}
	}
	t.Fatal("no unprocessed McNabb version")
	return pgUnprocessedVersion{}
}

func (v pgUnprocessedVersion) process(ctx context.Context, row sqlcdb.ListUnprocessedNewsVersionsRow) (news.ProcessOutcome, error) {
	return v.acts.processVersion(ctx, v.dir, row, pgIncidentWindowHours*time.Hour)
}

// holdAdvisoryLock takes the transaction-level advisory lock key on its own
// connection, as a concurrent refresh would, until the returned release
// (also run at cleanup).
func holdAdvisoryLock(t *testing.T, pool *pgxpool.Pool, key int64) (release func()) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	release = func() { _ = tx.Rollback(ctx) }
	t.Cleanup(release)
	_, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", key)
	require.NoError(t, err)
	return release
}

func versionProcessedAt(t *testing.T, pool *pgxpool.Pool, id int64) pgtype.Timestamptz {
	t.Helper()
	var at pgtype.Timestamptz
	require.NoError(t, pool.QueryRow(context.Background(),
		"SELECT processed_at FROM news_article_versions WHERE id = $1", id).Scan(&at))
	return at
}

func mentionCount(t *testing.T, pool *pgxpool.Pool, versionID int64) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(),
		"SELECT count(*) FROM news_mentions WHERE version_id = $1", versionID).Scan(&n))
	return n
}

// assertUntouched asserts nothing of processing versionID was committed.
func assertUntouched(t *testing.T, pool *pgxpool.Pool, versionID int64) {
	t.Helper()
	assert.False(t, versionProcessedAt(t, pool, versionID).Valid, "the version is still unprocessed")
	assert.Zero(t, mentionCount(t, pool, versionID), "no mention was committed")
	assert.Zero(t, countRows(t, pool, "news_incidents"), "no incident was committed")
}

// assertCommitted asserts the version, its mentions and its incident were
// committed at the activities' clock.
func assertCommitted(t *testing.T, pool *pgxpool.Pool, versionID int64, outcome news.ProcessOutcome) {
	t.Helper()
	assert.Equal(t, 1, outcome.Versions)
	assert.Equal(t, 1, outcome.IncidentsCreated)
	processedAt := versionProcessedAt(t, pool, versionID)
	require.True(t, processedAt.Valid, "the version is processed")
	assert.True(t, pgFixedNow.Equal(processedAt.Time), "processed at the injected clock, got %s", processedAt.Time)
	assert.Positive(t, mentionCount(t, pool, versionID))
	assert.Equal(t, 1, countRows(t, pool, "news_incidents"))
}

func TestPGProcessVersionWaitsForTheProcessingLock(t *testing.T) {
	pool := openNewsPGTestDB(t)
	v := seedUnprocessedMcNabb(t, pool)
	release := holdAdvisoryLock(t, pool, newsProcessingLockKey)

	ctx, cancel := context.WithTimeout(context.Background(), pgLockWaitTimeout)
	defer cancel()
	_, err := v.process(ctx, v.row)
	require.ErrorIs(t, err, context.DeadlineExceeded, "processing waits while another refresh holds the processing lock")
	assertUntouched(t, pool, v.row.ID)

	release()
	outcome, err := v.process(context.Background(), v.row)
	require.NoError(t, err)
	assertCommitted(t, pool, v.row.ID, outcome)
}

func TestPGProcessVersionDoesNotWaitForTheEventLock(t *testing.T) {
	pool := openNewsPGTestDB(t)
	v := seedUnprocessedMcNabb(t, pool)
	holdAdvisoryLock(t, pool, newsEventLockKey)

	ctx, cancel := context.WithTimeout(context.Background(), pgLockWaitTimeout)
	defer cancel()
	outcome, err := v.process(ctx, v.row)
	require.NoError(t, err, "event reconciliation does not block version processing")
	assertCommitted(t, pool, v.row.ID, outcome)
}

func TestPGProcessVersionSkipsAVersionProcessedSinceListing(t *testing.T) {
	pool := openNewsPGTestDB(t)
	v := seedUnprocessedMcNabb(t, pool)
	earlier := pgFixedNow.Add(-pgEarlierProcessing)
	_, err := pool.Exec(context.Background(), "UPDATE news_article_versions SET processed_at = $2 WHERE id = $1",
		v.row.ID, earlier)
	require.NoError(t, err)

	outcome, err := v.process(context.Background(), v.row)

	require.NoError(t, err)
	assert.Zero(t, outcome)
	processedAt := versionProcessedAt(t, pool, v.row.ID)
	assert.True(t, earlier.Equal(processedAt.Time), "the other refresh's processing time stays, got %s", processedAt.Time)
	assert.Zero(t, mentionCount(t, pool, v.row.ID))
	assert.Zero(t, countRows(t, pool, "news_incidents"))
}

func TestPGProcessVersionSkipsAVersionPrunedSinceListing(t *testing.T) {
	pool := openNewsPGTestDB(t)
	v := seedUnprocessedMcNabb(t, pool)
	_, err := pool.Exec(context.Background(), "DELETE FROM news_article_versions WHERE id = $1", v.row.ID)
	require.NoError(t, err)

	outcome, err := v.process(context.Background(), v.row)

	require.NoError(t, err)
	assert.Zero(t, outcome)
	assert.Zero(t, countRows(t, pool, "news_incidents"))
}

func TestPGProcessVersionRollsBackAFailedVersion(t *testing.T) {
	pool := openNewsPGTestDB(t)
	v := seedUnprocessedMcNabb(t, pool)
	bad := v.row
	bad.CategoryHint = pgBogusCategory

	_, err := v.process(context.Background(), bad)
	require.Error(t, err, "the incident insert violates the category check")
	assertUntouched(t, pool, v.row.ID)

	ctx, cancel := context.WithTimeout(context.Background(), pgLockWaitTimeout)
	defer cancel()
	outcome, err := v.process(ctx, v.row)
	require.NoError(t, err, "the failed transaction released the processing lock")
	assertCommitted(t, pool, v.row.ID, outcome)
}
