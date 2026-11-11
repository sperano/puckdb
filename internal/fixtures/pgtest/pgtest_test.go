package pgtest

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// concurrentCallers is how many goroutines race for one migration.
const concurrentCallers = 8

func TestCheckURLAcceptsATestDatabaseName(t *testing.T) {
	for _, dbURL := range []string{
		"postgres://puckdb:foo@localhost:5432/puckdb_test?sslmode=disable",
		"postgresql://localhost/test",
		"postgres://localhost/?dbname=puckdb_test",
	} {
		assert.NoError(t, CheckURL(dbURL), dbURL)
	}
}

// The old copies accepted any URL containing "test"; each of these connects
// to a database whose name lacks it.
func TestCheckURLRefusesTheMarkerOutsideTheDatabaseName(t *testing.T) {
	for _, dbURL := range []string{
		"postgres://tester:foo@localhost:5432/puckdb?sslmode=disable",
		"postgres://puckdb:test@localhost:5432/puckdb",
		"postgres://puckdb:foo@test-db.internal:5432/puckdb",
		"postgres://puckdb:foo@localhost:5432/puckdb?application_name=test",
		"postgres://puckdb:foo@localhost/puckdb?options=-c%20search_path%3Dtest",
	} {
		err := CheckURL(dbURL)
		require.Error(t, err, dbURL)
		assert.Contains(t, err.Error(), `database "puckdb"`, dbURL)
	}
}

// pgx lets a query parameter win over the URL's own path, host, port or user,
// while lib/pq, behind golang-migrate, keeps the lexically greater value. The
// first and third URLs would pass a pgx-only check yet migrate (or, in the
// simulation integration TestMain, migrate down) another database; the second
// names test for lib/pq and puckdb for pgx; the others name the same database
// on another server, port or role.
func TestCheckURLRefusesATargetTheDriversDisagreeOn(t *testing.T) {
	for _, dbURL := range []string{
		"postgres://puckdb:foo@localhost:5432/zeta?dbname=alpha_test",
		"postgres://puckdb:foo@localhost:5432/test?dbname=puckdb",
		"postgres://puckdb:foo@localhost:5432/puckdb_test?dbname=other_test",
		"postgres://puckdb:foo@prod.internal:5432/puckdb_test?host=localhost",
		"postgres://puckdb:foo@localhost:5432/puckdb_test?port=5431",
		"postgres://puckdb:foo@localhost:5432/puckdb_test?user=admin",
	} {
		require.ErrorIs(t, CheckURL(dbURL), errDriversDisagree, dbURL)
	}
}

// pgx tries a host list in order, so a refused first server would send the
// test to the next one; each URL names a test database on two servers.
func TestCheckURLRefusesAHostList(t *testing.T) {
	for _, dbURL := range []string{
		"postgres://puckdb:foo@localhost,prod.internal/puckdb_test",
		"postgres://puckdb:foo@localhost:5432,localhost:5433/puckdb_test",
		"postgres://puckdb:foo@localhost/puckdb_test?host=localhost,prod.internal",
	} {
		require.ErrorIs(t, CheckURL(dbURL), errSeveralServers, dbURL)
	}
}

// A query parameter that repeats a URL setting with the same value leaves
// both drivers on one target.
func TestCheckURLAcceptsAParameterRepeatingTheURL(t *testing.T) {
	dbURL := "postgres://puckdb:foo@localhost:5432/puckdb_test?host=localhost&port=5432&user=puckdb&dbname=puckdb_test"
	assert.NoError(t, CheckURL(dbURL))
}

// worker/shared used to lower-case the URL first; the marker is now
// case-sensitive, like the other copies.
func TestCheckURLIsCaseSensitive(t *testing.T) {
	err := CheckURL("postgres://puckdb:foo@localhost:5432/PUCKDB_TEST")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `database "PUCKDB_TEST"`)
}

// golang-migrate picks its driver from the URL scheme, so MigratedURL could
// not migrate a keyword/value string even when its dbname is a test database.
func TestCheckURLRefusesANonURLForm(t *testing.T) {
	for _, dbURL := range []string{
		"host=localhost user=puckdb dbname=puckdb_maurice_test sslmode=disable",
		"mysql://localhost/puckdb_test",
	} {
		require.ErrorIs(t, CheckURL(dbURL), errNotURL, dbURL)
	}
}

func TestCheckURLRefusesAURLWithoutDatabase(t *testing.T) {
	t.Setenv("PGDATABASE", "")
	err := CheckURL("postgres://tester:test@test-db.internal:5432")
	require.ErrorIs(t, err, errNoDatabase)
}

func TestCheckURLRefusesAnUnparsableURL(t *testing.T) {
	err := CheckURL("postgres://localhost:notaport/puckdb_test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not parse")
}

func TestURLSkipsWithoutTheVariable(t *testing.T) {
	t.Setenv(EnvURL, "")
	var skipped bool
	t.Run("unset", func(t *testing.T) {
		defer func() { skipped = t.Skipped() }()
		URL(t)
	})
	assert.True(t, skipped, "an unset %s must skip, not fail", EnvURL)
}

// failureRecorder is a testing.TB whose FailNow stops the helper under test
// with a panic the test recovers, so a refused URL can be observed.
type failureRecorder struct {
	testing.TB
	failed bool
}

// errFailNow is the panic value of failureRecorder.FailNow.
var errFailNow = errors.New("FailNow")

func (r *failureRecorder) Errorf(string, ...any) { r.failed = true }
func (r *failureRecorder) FailNow()              { panic(errFailNow) }

func TestURLFailsBeforeReturningAnUnsafeURL(t *testing.T) {
	t.Setenv(EnvURL, "postgres://tester:test@localhost:5432/puckdb?application_name=test")
	recorder := &failureRecorder{TB: t}
	returned := false
	func() {
		defer func() { assert.Equal(t, errFailNow, recover()) }()
		URL(recorder)
		returned = true
	}()
	assert.True(t, recorder.failed, "an unsafe URL must fail the test")
	assert.False(t, returned, "an unsafe URL must never reach the caller")
}

func TestMigrateRegistryRunsOncePerURL(t *testing.T) {
	var registry migrateRegistry
	var mu sync.Mutex
	calls := map[string]int{}
	migrate := func(dbURL string) error {
		mu.Lock()
		defer mu.Unlock()
		calls[dbURL]++
		return nil
	}

	var wg sync.WaitGroup
	for range concurrentCallers {
		wg.Go(func() {
			assert.NoError(t, registry.once("postgres://localhost/a_test", migrate))
			assert.NoError(t, registry.once("postgres://localhost/b_test", migrate))
		})
	}
	wg.Wait()
	assert.Equal(t, map[string]int{"postgres://localhost/a_test": 1, "postgres://localhost/b_test": 1}, calls)
}

func TestMigrateRegistryKeepsTheFailure(t *testing.T) {
	var registry migrateRegistry
	failure := errors.New("migration failed")
	calls := 0
	migrate := func(string) error {
		calls++
		return failure
	}
	const dbURL = "postgres://localhost/a_test"
	require.ErrorIs(t, registry.once(dbURL, migrate), failure)
	require.ErrorIs(t, registry.once(dbURL, migrate), failure, "a later caller sees the first failure")
	assert.Equal(t, 1, calls, "a failed migration is not retried")
}

func TestTruncateSQLQuotesEveryTable(t *testing.T) {
	assert.Equal(t, `TRUNCATE "a", "b; DROP TABLE c" CASCADE`, truncateSQL([]string{"a", "b; DROP TABLE c"}, false))
	assert.Equal(t, `TRUNCATE "a" RESTART IDENTITY CASCADE`, truncateSQL([]string{"a"}, true))
}

// The tests below need PUCKDB_TEST_PG_URL. Each works on tables of its own,
// so they never touch rows other packages seed.

func TestOpenMigratesTheDatabase(t *testing.T) {
	pool := Open(t)
	var exists bool
	require.NoError(t, pool.QueryRow(context.Background(),
		"SELECT to_regclass('public.schema_migrations') IS NOT NULL").Scan(&exists))
	assert.True(t, exists, "Open must migrate the database")
}

func TestTruncateEmptiesOnlyTheNamedTablesAndTheirReferences(t *testing.T) {
	pool := Open(t)
	parent, child, other := scratchTable(t, pool, "parent"), scratchTable(t, pool, "child"), scratchTable(t, pool, "other")
	exec(t, pool, "ALTER TABLE "+child+" ADD COLUMN parent_id bigint REFERENCES "+parent+" (id)")
	exec(t, pool, "INSERT INTO "+parent+" DEFAULT VALUES")
	exec(t, pool, "INSERT INTO "+child+" (parent_id) SELECT id FROM "+parent)
	exec(t, pool, "INSERT INTO "+other+" DEFAULT VALUES")

	Truncate(t, pool, unquoted(parent))

	assert.Zero(t, rowCount(t, pool, parent))
	assert.Zero(t, rowCount(t, pool, child), "CASCADE empties a referencing table")
	assert.Equal(t, 1, rowCount(t, pool, other), "a table not named keeps its rows")
}

func TestOpenTruncatesTheGivenTables(t *testing.T) {
	setup := Open(t)
	table := scratchTable(t, setup, "open")
	exec(t, setup, "INSERT INTO "+table+" DEFAULT VALUES")

	pool := Open(t, unquoted(table))
	assert.Zero(t, rowCount(t, pool, table))
}

func TestTruncateRestartIdentityRestartsIDs(t *testing.T) {
	pool := Open(t)
	table := scratchTable(t, pool, "identity")
	exec(t, pool, "INSERT INTO "+table+" DEFAULT VALUES")
	exec(t, pool, "INSERT INTO "+table+" DEFAULT VALUES")

	Truncate(t, pool, unquoted(table))
	exec(t, pool, "INSERT INTO "+table+" DEFAULT VALUES")
	assert.Equal(t, int64(3), maxID(t, pool, table), "plain Truncate keeps the sequence")

	TruncateRestartIdentity(t, pool, unquoted(table))
	exec(t, pool, "INSERT INTO "+table+" DEFAULT VALUES")
	assert.Equal(t, int64(1), maxID(t, pool, table))
}

// scratchTable creates a uniquely named table with an identity column,
// dropped at cleanup, and returns its quoted name.
func scratchTable(t *testing.T, pool *pgxpool.Pool, role string) string {
	t.Helper()
	name := pgx.Identifier{"pgtest_" + role + "_" + strings.ReplaceAll(uuid.NewString(), "-", "")}.Sanitize()
	exec(t, pool, "CREATE TABLE "+name+" (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY)")
	t.Cleanup(func() {
		_, err := pool.Exec(context.Background(), "DROP TABLE IF EXISTS "+name+" CASCADE")
		assert.NoError(t, err)
	})
	return name
}

func unquoted(name string) string { return strings.Trim(name, `"`) }

func exec(t *testing.T, pool *pgxpool.Pool, sql string) {
	t.Helper()
	_, err := pool.Exec(context.Background(), sql)
	require.NoError(t, err)
}

func rowCount(t *testing.T, pool *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n))
	return n
}

func maxID(t *testing.T, pool *pgxpool.Pool, table string) int64 {
	t.Helper()
	var id int64
	require.NoError(t, pool.QueryRow(context.Background(), "SELECT max(id) FROM "+table).Scan(&id))
	return id
}
