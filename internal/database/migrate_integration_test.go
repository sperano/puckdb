package database

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	migratedb "github.com/golang-migrate/migrate/v4/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// envTestPGURL points at a throwaway PostgreSQL (see CLAUDE.md, "Database-
// backed tests"). These tests skip without it. They never touch the public
// schema: each one works in its own schema, selected through search_path,
// which is also where golang-migrate keeps its schema_migrations table.
const (
	envTestPGURL         = "PUCKDB_TEST_PG_URL"
	testSchemaPrefix     = "migrate_dirty_test"
	queryParamSearchPath = "search_path"
	integrationDBTimeout = 30 * time.Second

	tableWidgets = "widgets"
	tableGadgets = "gadgets"

	sqlCreateWidgets = "CREATE TABLE widgets (id int PRIMARY KEY);"
	sqlDropWidgets   = "DROP TABLE widgets;"
	sqlCreateGadgets = "CREATE TABLE gadgets (id int PRIMARY KEY);"
	sqlDropGadgets   = "DROP TABLE gadgets;"
	sqlFail          = "SELECT 1/0;"

	upFirst    = "migrations/000001_widgets.up.sql"
	downFirst  = "migrations/000001_widgets.down.sql"
	upSecond   = "migrations/000002_gadgets.up.sql"
	downSecond = "migrations/000002_gadgets.down.sql"
)

func healthyMigrations() fstest.MapFS {
	return fstest.MapFS{
		upFirst:    {Data: []byte(sqlCreateWidgets)},
		downFirst:  {Data: []byte(sqlDropWidgets)},
		upSecond:   {Data: []byte(sqlCreateGadgets)},
		downSecond: {Data: []byte(sqlDropGadgets)},
	}
}

// isolatedSchema creates a schema for one test and returns a connection to
// it plus a URL whose search_path confines golang-migrate to it.
func isolatedSchema(t *testing.T) (*pgx.Conn, string) {
	t.Helper()
	baseURL := os.Getenv(envTestPGURL)
	if baseURL == "" {
		t.Skipf("%s not set", envTestPGURL)
	}
	ctx, cancel := context.WithTimeout(context.Background(), integrationDBTimeout)
	t.Cleanup(cancel)

	schema := uniqueIdentifier(testSchemaPrefix)
	parsed, err := url.Parse(baseURL)
	require.NoError(t, err)
	query := parsed.Query()
	query.Set(queryParamSearchPath, schema)
	parsed.RawQuery = query.Encode()

	admin, err := pgx.Connect(ctx, baseURL)
	require.NoError(t, err)
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize())
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), integrationDBTimeout)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		_ = admin.Close(cleanupCtx)
	})

	conn, err := pgx.Connect(ctx, parsed.String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn, parsed.String()
}

// uniqueIdentifier returns prefix plus a random suffix, so parallel tests
// (and concurrent runs against the same server) never share a schema or
// database name, as a clock-based suffix can on coarse timers. It stays
// within PostgreSQL's 63-byte identifier limit for the prefixes used here.
func uniqueIdentifier(prefix string) string {
	return prefix + "_" + strings.ReplaceAll(uuid.NewString(), "-", "")
}

func tableExists(t *testing.T, conn *pgx.Conn, table string) bool {
	t.Helper()
	var exists bool
	err := conn.QueryRow(context.Background(),
		"SELECT to_regclass(current_schema() || '.' || $1) IS NOT NULL", table).Scan(&exists)
	require.NoError(t, err)
	return exists
}

func recordedVersion(t *testing.T, conn *pgx.Conn) (version int, dirty bool) {
	t.Helper()
	err := conn.QueryRow(context.Background(), "SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty)
	require.NoError(t, err)
	return version, dirty
}

func requireDirtyAt(t *testing.T, err error, version int) {
	t.Helper()
	var dirtyErr *DirtyMigrationError
	require.ErrorAs(t, err, &dirtyErr)
	assert.Equal(t, version, dirtyErr.Version)
}

// A failed up migration can be partially applied. The old recovery forced
// the version back to 1 and re-ran migration 2, which then failed again on
// the already-created table. Now nothing runs until an operator decides.
func TestIntegration_DirtyUp(t *testing.T) {
	t.Parallel()
	conn, dbURL := isolatedSchema(t)

	broken := healthyMigrations()
	// COMMIT ends the implicit transaction, so gadgets survives the failure.
	broken[upSecond] = &fstest.MapFile{Data: []byte(sqlCreateGadgets + " COMMIT; " + sqlFail)}

	requireDirtyAt(t, withMigrator(broken, dbURL, runUp), secondVersion)
	require.True(t, tableExists(t, conn, tableGadgets), "migration 2 must be partially applied")

	for name, run := range runners {
		requireDirtyAt(t, withMigrator(healthyMigrations(), dbURL, run), secondVersion)
		version, dirty := recordedVersion(t, conn)
		assert.Equal(t, secondVersion, version, name)
		assert.True(t, dirty, name)
		assert.True(t, tableExists(t, conn, tableWidgets), name)
		assert.True(t, tableExists(t, conn, tableGadgets), name)
	}

	// Operator inspects the schema, sees gadgets exists, and records that.
	require.NoError(t, withMigrator(healthyMigrations(), dbURL, func(m migrator) error {
		return forceVersion(m, healthyMigrations(), secondVersion)
	}))
	require.NoError(t, withMigrator(healthyMigrations(), dbURL, runUp))
	version, dirty := recordedVersion(t, conn)
	assert.Equal(t, secondVersion, version)
	assert.False(t, dirty)
}

// A failed down migration records the version it was heading to, so the
// dirty version is 1 while the schema still matches 2. "Dirty version minus
// one" would have recorded version 0, which no migration defines.
func TestIntegration_DirtyDown(t *testing.T) {
	t.Parallel()
	conn, dbURL := isolatedSchema(t)

	broken := healthyMigrations()
	broken[downSecond] = &fstest.MapFile{Data: []byte(sqlFail)}

	require.NoError(t, withMigrator(broken, dbURL, runUp))
	requireDirtyAt(t, withMigrator(broken, dbURL, runDown), firstVersion)

	for name, run := range runners {
		requireDirtyAt(t, withMigrator(healthyMigrations(), dbURL, run), firstVersion)
		version, dirty := recordedVersion(t, conn)
		assert.Equal(t, firstVersion, version, name)
		assert.True(t, dirty, name)
		assert.True(t, tableExists(t, conn, tableGadgets), name+": schema still matches version 2")
	}

	require.NoError(t, withMigrator(healthyMigrations(), dbURL, func(m migrator) error {
		return forceVersion(m, healthyMigrations(), secondVersion)
	}))
	require.NoError(t, withMigrator(healthyMigrations(), dbURL, runDown))
	assert.False(t, tableExists(t, conn, tableWidgets))
	assert.False(t, tableExists(t, conn, tableGadgets))
}

// When the first migration's down fails there is no lower version to record,
// so the driver stores "-1, dirty". Migrate.Version reports that as a fresh
// database; reading the driver's raw version keeps it refusable and
// repairable.
func TestIntegration_DirtyDownToNoVersion(t *testing.T) {
	t.Parallel()
	conn, dbURL := isolatedSchema(t)

	broken := healthyMigrations()
	broken[downFirst] = &fstest.MapFile{Data: []byte(sqlFail)}

	require.NoError(t, withMigrator(broken, dbURL, runUp))
	requireDirtyAt(t, withMigrator(broken, dbURL, runDown), migratedb.NilVersion)

	for name, run := range runners {
		requireDirtyAt(t, withMigrator(healthyMigrations(), dbURL, run), migratedb.NilVersion)
		version, dirty := recordedVersion(t, conn)
		assert.Equal(t, migratedb.NilVersion, version, name)
		assert.True(t, dirty, name)
		assert.True(t, tableExists(t, conn, tableWidgets), name+": schema still matches version 1")
	}

	// Migration 2 came down cleanly, migration 1 did not: the schema is at 1.
	require.NoError(t, withMigrator(healthyMigrations(), dbURL, func(m migrator) error {
		return forceVersion(m, healthyMigrations(), firstVersion)
	}))
	require.NoError(t, withMigrator(healthyMigrations(), dbURL, runDown))
	assert.False(t, tableExists(t, conn, tableWidgets))
}
