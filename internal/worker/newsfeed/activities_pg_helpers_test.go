package newsfeed

// PostgreSQL-backed tests of the news refresh activities: the real queries,
// the real migration, and the news package's report loaders reading back
// what the activities wrote. They skip unless PUCKDB_TEST_PG_URL names a
// test database (see CLAUDE.md "Database-backed tests").
//
// This file holds the fixture and helper plumbing shared by
// activities_pg_test.go and activities_pg_process_test.go.

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/database"
	"github.com/sperano/puckdb/internal/news"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

const (
	pgEnvTestPGURL     = "PUCKDB_TEST_PG_URL"
	pgTestDBNameMarker = "test"

	// Fixture identities shared by the PG tests. The McNabb NHL player ID
	// matches the player tag in testdata/nhl-player-safety.json, so the NHL
	// content fetch resolves him without extra plumbing.
	pgMcNabbID      int64 = 8475188
	pgMcNabbYahooID       = 7002
	pgPetterssonAID int64 = 8480012
	pgPetterssonBID int64 = 8483678
	pgVanTeamID     int64 = 23
	pgVgkTeamID     int64 = 54
	pgNHLSeasonID         = 20262027

	pgYahooSeason      = 2026
	pgOtherYahooSeason = 2025
	pgLeagueKeyA       = "465.l.11111"
	pgLeagueKeyB       = "465.l.22222"
	pgLeagueIDA        = 11111
	pgLeagueIDB        = 22222
	pgGameKey          = 465

	// entity IDs of the stories in testdata/nhl-player-safety.json.
	pgMcNabbOfficialEntityID   = "a1f0c7e2-0001-4b9a-9d40-000000000001"
	pgMcNabbSyndicatedEntityID = "a1f0c7e2-0002-4b9a-9d40-000000000002"
	pgCristallEntityID         = "a1f0c7e2-0003-4b9a-9d40-000000000003"
	pgNHLContentPublisher      = "NHL.com"

	pgIncidentWindowHours = 14 * 24 // matches the news package's own acceptance tests
	pgTotalSeedVersions   = 8       // 3 NHL-content stories + 5 RSS items
	pgLargeBatch          = 100
	pgKeepAllDays         = 3650
	pgFarFutureDays       = 400
)

var pgFixedNow = time.Date(2026, time.September, 25, 12, 0, 0, 0, time.UTC)

// pgLoadSince comfortably predates every fixture story's reported time, so
// report-loader queries filtered by a "since" bound see everything seeded.
var pgLoadSince = pgFixedNow.Add(-30 * 24 * time.Hour)

var pgMigrateOnce struct {
	sync.Once
	err error
}

// openNewsPGTestDB migrates the test database once and empties the tables
// the news activities read and write.
func openNewsPGTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv(pgEnvTestPGURL)
	if dbURL == "" {
		t.Skipf("set %s to run the PostgreSQL news activities tests", pgEnvTestPGURL)
	}
	require.Contains(t, dbURL, pgTestDBNameMarker,
		"%s must name a dedicated test database (URL containing %q)", pgEnvTestPGURL, pgTestDBNameMarker)
	pgMigrateOnce.Do(func() { pgMigrateOnce.err = database.MigrateUp(dbURL) })
	require.NoError(t, pgMigrateOnce.err, "migrate test database")

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	truncateNewsTables(t, pool)
	return pool
}

func truncateNewsTables(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `TRUNCATE news_extraction_evaluations, news_event_transitions,
		news_event_evidence, news_events, news_extractions, news_incident_evidence, news_incidents, news_mentions,
		news_article_versions, news_articles, news_fetch_state, yahoo_league_players, season_teams, franchises,
		seasons, players CASCADE`)
	require.NoError(t, err)
}

// newNewsActivities builds Activities over pool with a fixed clock and
// registers every activity the PG tests exercise.
func newNewsActivities(pool *pgxpool.Pool, client *http.Client, now time.Time) (*Activities, *testsuite.TestActivityEnvironment) {
	acts := &Activities{Pool: pool, Queries: sqlcdb.New(pool), HTTPClient: client, Now: func() time.Time { return now }}
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(acts.PlanNewsRefresh)
	env.RegisterActivity(acts.FetchNewsSource)
	env.RegisterActivity(acts.RecordNewsFetchFailure)
	env.RegisterActivity(acts.ProcessNewsVersions)
	env.RegisterActivity(acts.PruneNews)
	env.RegisterActivity(acts.ExtractNewsEvents)
	return acts, env
}

// seedNewsDirectory inserts the season, its two teams and three players the
// PG tests resolve stories against: two Elias Petterssons on VAN (so the
// name alone is ambiguous) and Brayden McNabb on VGK with a mapped Yahoo ID.
func seedNewsDirectory(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO seasons (id, standings_start, standings_end) VALUES ($1, '2026-10-01', '2027-04-15')`,
		pgNHLSeasonID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO season_teams (season, team_id, full_name, abbrev, division_name, division_abbrev)
		VALUES ($1, $2, 'Vancouver Canucks', 'VAN', 'Pacific', 'P'), ($1, $3, 'Vegas Golden Knights', 'VGK', 'Pacific', 'P')`,
		pgNHLSeasonID, pgVanTeamID, pgVgkTeamID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO players (id, first_name, last_name, team_id, is_active) VALUES
		($1, 'Elias', 'Pettersson', $3, true), ($2, 'Elias', 'Pettersson', $3, true)`,
		pgPetterssonAID, pgPetterssonBID, pgVanTeamID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO players (id, yahoo_id, first_name, last_name, team_id, is_active)
		VALUES ($1, $2, 'Brayden', 'McNabb', $3, true)`,
		pgMcNabbID, pgMcNabbYahooID, pgVgkTeamID)
	require.NoError(t, err)
}

// seedYahooPool lists McNabb in two leagues' player pools with different
// fetch times, the newer one listing a DTD status.
func seedYahooPool(t *testing.T, pool *pgxpool.Pool, older, newer time.Time) {
	t.Helper()
	ctx := context.Background()
	const insert = `INSERT INTO yahoo_league_players (league_key, season, league_id, game_key, player_id,
		player_key, full_name, editorial_team_abbr, eligible_positions, status, status_full, injury_note, fetched_at)
		VALUES ($1, $2, $3, $4, $5, '465.p.7002', 'Brayden McNabb', 'VGK', $6, $7, $8, $9, $10)`
	_, err := pool.Exec(ctx, insert, pgLeagueKeyA, pgYahooSeason, pgLeagueIDA, pgGameKey, pgMcNabbYahooID,
		[]string{"D"}, "", "", "", older)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, insert, pgLeagueKeyB, pgYahooSeason, pgLeagueIDB, pgGameKey, pgMcNabbYahooID,
		[]string{"D"}, "DTD", "Day-To-Day", "Lower body", newer)
	require.NoError(t, err)
}

// readNewsTestdata reads a fixture shared with the internal/news package
// tests (RSS/Atom/NHL-content bodies whose players match seedNewsDirectory).
func readNewsTestdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("../../news/testdata/" + name)
	require.NoError(t, err)
	return data
}

// mutateNHLFixture reads the NHL content fixture fresh and applies
// old-for-new byte replacements (each applied once), so a test can build a
// sequence of related bodies without one mutation compounding another.
func mutateNHLFixture(t *testing.T, replacements ...[2]string) []byte {
	t.Helper()
	body := readNewsTestdata(t, "nhl-player-safety.json")
	for _, r := range replacements {
		body = bytes.Replace(body, []byte(r[0]), []byte(r[1]), 1)
	}
	return body
}

// defaultNewsSource returns a copy of a built-in source definition by ID, so
// tests exercise the real Publisher/Kind/Adapter of the production sources,
// with the URL swapped for a local test server.
func defaultNewsSource(t *testing.T, id, url string) news.Source {
	t.Helper()
	sources, err := news.DefaultSources()
	require.NoError(t, err)
	for _, s := range sources {
		if s.ID == id {
			s.URL = url
			return s
		}
	}
	t.Fatalf("no default news source %s", id)
	return news.Source{}
}

// staticServer answers every request with status and body.
func staticServer(t *testing.T, status int, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// rssFakeServer serves a mutable body so a test can walk a source through
// NotModified-by-hash, an advertised ETag, a 304 response and a changed body
// across sequential fetches. Requests are only ever sequential in these
// tests (one fetch completes, and its result is asserted, before the next
// mutates the fixture), so a plain mutex is enough to keep -race quiet.
type rssFakeServer struct {
	mu              sync.Mutex
	body            []byte
	etag            string
	notModified     bool
	lastIfNoneMatch string
	srv             *httptest.Server
}

func newRSSFakeServer(t *testing.T, body []byte) *rssFakeServer {
	t.Helper()
	f := &rssFakeServer{body: body}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *rssFakeServer) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastIfNoneMatch = r.Header.Get("If-None-Match")
	if f.notModified {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if f.etag != "" {
		w.Header().Set("ETag", f.etag)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(f.body)
}

func (f *rssFakeServer) setBody(body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.body = body
}

func (f *rssFakeServer) setETag(etag string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.etag = etag
}

func (f *rssFakeServer) setNotModified(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notModified = v
}

func (f *rssFakeServer) requestedIfNoneMatch() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastIfNoneMatch
}

func (f *rssFakeServer) url() string {
	return f.srv.URL
}

// fetchSource runs FetchNewsSource as a Temporal activity and decodes its result.
func fetchSource(t *testing.T, env *testsuite.TestActivityEnvironment, acts *Activities, src news.Source) FetchResult {
	t.Helper()
	val, err := env.ExecuteActivity(acts.FetchNewsSource, FetchInput{Source: src, Season: pgYahooSeason})
	require.NoError(t, err)
	var result FetchResult
	require.NoError(t, val.Get(&result))
	return result
}

// processBatch runs ProcessNewsVersions as a Temporal activity and decodes its result.
func processBatch(t *testing.T, env *testsuite.TestActivityEnvironment, acts *Activities, batchSize int) ProcessResult {
	t.Helper()
	val, err := env.ExecuteActivity(acts.ProcessNewsVersions, ProcessInput{
		Season: pgYahooSeason, BatchSize: batchSize, IncidentWindowHours: pgIncidentWindowHours,
	})
	require.NoError(t, err)
	var result ProcessResult
	require.NoError(t, val.Get(&result))
	return result
}

// seedProcessedNews seeds the directory, fetches both the NHL content and
// RSS fixtures and processes every version, leaving one suspension incident
// (McNabb) and one ambiguous mention (Pettersson) on record.
func seedProcessedNews(t *testing.T, pool *pgxpool.Pool) (*Activities, *testsuite.TestActivityEnvironment) {
	t.Helper()
	seedNewsDirectory(t, pool)
	nhlSrv := staticServer(t, http.StatusOK, readNewsTestdata(t, "nhl-player-safety.json"))
	rssSrv := staticServer(t, http.StatusOK, readNewsTestdata(t, "rotowire.xml"))
	acts, env := newNewsActivities(pool, http.DefaultClient, pgFixedNow)
	fetchSource(t, env, acts, defaultNewsSource(t, "nhl-player-safety", nhlSrv.URL))
	fetchSource(t, env, acts, defaultNewsSource(t, "rotowire-nhl", rssSrv.URL))
	processBatch(t, env, acts, pgTotalSeedVersions)
	return acts, env
}

// assertApplicationErrorType asserts err is a Temporal application error of
// the given type and non-retryable flag.
func assertApplicationErrorType(t *testing.T, err error, wantType string, wantNonRetryable bool) {
	t.Helper()
	var appErr *temporal.ApplicationError
	require.ErrorAs(t, err, &appErr, "expected a Temporal application error, got: %v", err)
	assert.Equal(t, wantType, appErr.Type())
	assert.Equal(t, wantNonRetryable, appErr.NonRetryable())
}

type newsFetchStateRow struct {
	etag, bodyHash, lastError string
	lastSuccessAt             time.Time
	consecutiveFailures       int32
}

func readFetchState(t *testing.T, pool *pgxpool.Pool, sourceID string) newsFetchStateRow {
	t.Helper()
	var row newsFetchStateRow
	var lastSuccess pgtype.Timestamptz
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT etag, body_hash, last_error, last_success_at, consecutive_failures
		 FROM news_fetch_state WHERE source_id = $1 AND scope = $2`, sourceID, news.ScopeFeed).
		Scan(&row.etag, &row.bodyHash, &row.lastError, &lastSuccess, &row.consecutiveFailures))
	if lastSuccess.Valid {
		row.lastSuccessAt = lastSuccess.Time
	}
	return row
}

func countRows(t *testing.T, pool *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n))
	return n
}

func countVersionsFor(t *testing.T, pool *pgxpool.Pool, publisher, externalID string) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT count(*) FROM news_article_versions v JOIN news_articles a ON a.id = v.article_id
		 WHERE a.publisher = $1 AND a.external_id = $2`, publisher, externalID).Scan(&n))
	return n
}
