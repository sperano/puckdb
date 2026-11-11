// Package pgtest gives PostgreSQL-backed tests the database PUCKDB_TEST_PG_URL
// names: it skips a test when the variable is unset, refuses a URL whose
// database name does not mark it as a disposable test database, migrates that
// database once per test binary, and opens a pool closed at cleanup.
//
// Every package's tests share that one database and truncate the tables they
// name, with CASCADE, so every table referencing them is emptied too; packages
// must therefore run one at a time (`go test -p 1 ./...`). Tests that delete
// rows other packages rely on, or drop the schema, use a private database
// instead (simfixtures.NewDatabase); the one exception is the simulation
// integration TestMain, which migrates the named database down (see
// CLAUDE.md, "Database-backed tests"). Only tests import this package.
package pgtest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
	"github.com/sperano/puckdb/internal/database"
	"github.com/stretchr/testify/require"
)

const (
	// EnvURL names the throwaway database (see CLAUDE.md, "Database-backed
	// tests"); tests skip without it.
	EnvURL = "PUCKDB_TEST_PG_URL"
	// DatabaseMarker must appear in the database name the URL connects to.
	DatabaseMarker = "test"
)

// urlSchemes are the prefixes database.MigrateUp accepts: golang-migrate
// picks its driver from the scheme, so a keyword/value string cannot be
// migrated.
var urlSchemes = []string{"postgres://", "postgresql://"}

// errNoDatabase reports a URL that names no database, so the server would
// pick one (the role's name) the check never saw.
var errNoDatabase = errors.New("names no database")

// errDriversDisagree reports a URL that pgx and lib/pq (golang-migrate's
// driver) resolve to different servers or databases, as when the path and a
// dbname query parameter both name a database, or the authority and a host,
// port or user parameter both name one of those: pgx lets the parameter win,
// lib/pq keeps the lexically greater value, so the migrator would touch a
// database the check never saw.
var errDriversDisagree = errors.New("names a different server or database for pgx and lib/pq")

// errNotURL reports a keyword/value string or another scheme, which
// golang-migrate cannot open.
var errNotURL = errors.New("is not a postgres:// or postgresql:// URL")

// CheckURL returns an error unless rawURL is a postgres:// or postgresql://
// URL that connects to a database whose name contains DatabaseMarker, on the
// same server, as the same user, through pgx (the pools) and lib/pq (the
// migrator). The marker
// elsewhere in the URL (user, host, password, a query parameter) does not
// count: only the database name tells which data a test would destroy.
func CheckURL(rawURL string) error {
	if !hasURLScheme(rawURL) {
		return fmt.Errorf("%s %w", EnvURL, errNotURL)
	}
	cfg, err := pgconn.ParseConfig(rawURL)
	if err != nil {
		return fmt.Errorf("%s does not parse: %w", EnvURL, err)
	}
	migratorCfg, err := libpqConfig(rawURL)
	if err != nil {
		return fmt.Errorf("%s does not parse for lib/pq: %w", EnvURL, err)
	}
	if pools, migrator := targetOf(cfg), targetOf(migratorCfg); pools != migrator {
		return fmt.Errorf("%s %w (%+v, %+v); name each of them once, in the URL itself",
			EnvURL, errDriversDisagree, pools, migrator)
	}
	if cfg.Database == "" {
		return fmt.Errorf("%s %w; name a dedicated test database", EnvURL, errNoDatabase)
	}
	if !strings.Contains(cfg.Database, DatabaseMarker) {
		return fmt.Errorf("%s names database %q, whose name lacks %q; name a dedicated test database",
			EnvURL, cfg.Database, DatabaseMarker)
	}
	return nil
}

// target is where a connection lands: the server, the role and the database.
type target struct {
	Host     string
	Port     uint16
	User     string
	Database string
}

func targetOf(cfg *pgconn.Config) target {
	return target{Host: cfg.Host, Port: cfg.Port, User: cfg.User, Database: cfg.Database}
}

// libpqConfig returns the connection settings lib/pq uses for rawURL.
// pq.ParseURL keeps every setting it finds, the URL's own and the query's,
// sorted; like lib/pq, pgconn lets the last keyword/value setting win, so it
// reads that DSN the way lib/pq does.
func libpqConfig(rawURL string) (*pgconn.Config, error) {
	dsn, err := pq.ParseURL(rawURL)
	if err != nil {
		return nil, err
	}
	return pgconn.ParseConfig(dsn)
}

func hasURLScheme(rawURL string) bool {
	for _, scheme := range urlSchemes {
		if strings.HasPrefix(rawURL, scheme) {
			return true
		}
	}
	return false
}

// URL returns the checked PUCKDB_TEST_PG_URL without migrating it. It skips
// the test when the variable is unset and fails it when CheckURL refuses it.
func URL(t testing.TB) string {
	t.Helper()
	dbURL := os.Getenv(EnvURL)
	if dbURL == "" {
		t.Skipf("set %s to run PostgreSQL-backed tests", EnvURL)
	}
	require.NoError(t, CheckURL(dbURL))
	return dbURL
}

// MigratedURL returns URL after migrating that database up. The migration
// runs once per URL in the test binary; later calls reuse its result.
func MigratedURL(t testing.TB) string {
	t.Helper()
	dbURL := URL(t)
	require.NoError(t, migrations.once(dbURL, database.MigrateUp), "migrate test database")
	return dbURL
}

// Open returns a pool on the migrated test database, closed at cleanup, after
// truncating the given tables (see Truncate).
func Open(t testing.TB, truncate ...string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), MigratedURL(t))
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	if len(truncate) > 0 {
		Truncate(t, pool, truncate...)
	}
	return pool
}

// Truncate empties tables, and every table referencing them (CASCADE).
func Truncate(t testing.TB, pool *pgxpool.Pool, tables ...string) {
	t.Helper()
	execTruncate(t, pool, truncateSQL(tables, false))
}

// TruncateRestartIdentity is Truncate that also restarts the tables' identity
// sequences, so generated IDs start at 1 again.
func TruncateRestartIdentity(t testing.TB, pool *pgxpool.Pool, tables ...string) {
	t.Helper()
	execTruncate(t, pool, truncateSQL(tables, true))
}

func execTruncate(t testing.TB, pool *pgxpool.Pool, sql string) {
	t.Helper()
	_, err := pool.Exec(context.Background(), sql)
	require.NoError(t, err, "truncate test tables")
}

// truncateSQL quotes each table name, so a name is never read as SQL.
func truncateSQL(tables []string, restartIdentity bool) string {
	quoted := make([]string, len(tables))
	for i, table := range tables {
		quoted[i] = pgx.Identifier{table}.Sanitize()
	}
	sql := "TRUNCATE " + strings.Join(quoted, ", ")
	if restartIdentity {
		sql += " RESTART IDENTITY"
	}
	return sql + " CASCADE"
}

// migrations is the binary's migrate-once registry.
var migrations migrateRegistry

// migrateRegistry runs one migration per URL and remembers its result.
type migrateRegistry struct {
	mu    sync.Mutex
	byURL map[string]*migrateResult
}

type migrateResult struct {
	once sync.Once
	err  error
}

func (r *migrateRegistry) once(dbURL string, migrate func(string) error) error {
	r.mu.Lock()
	if r.byURL == nil {
		r.byURL = make(map[string]*migrateResult)
	}
	result, ok := r.byURL[dbURL]
	if !ok {
		result = &migrateResult{}
		r.byURL[dbURL] = result
	}
	r.mu.Unlock()
	result.once.Do(func() { result.err = migrate(dbURL) })
	return result.err
}
