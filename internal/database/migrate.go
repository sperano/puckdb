package database

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/golang-migrate/migrate/v4"
	migratedb "github.com/golang-migrate/migrate/v4/database"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/rs/zerolog/log"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

const (
	migrationsDir        = "migrations"
	migrationsSourceName = "iofs"
	databaseDriverName   = "postgres"

	// NoMigrationVersion is the force-version argument meaning "no migration
	// has been applied". Real versions start at 1.
	NoMigrationVersion = 0

	recoveryDoc = "docs/migration-recovery.md"
)

// ErrMigrationNotDirty is returned when a version is forced on a database
// whose migration state is clean: there is nothing to repair.
var ErrMigrationNotDirty = errors.New("migration state is not dirty; refusing to force a version")

// ErrUnknownMigrationVersion is returned when the version to force does not
// exist in the embedded migrations.
var ErrUnknownMigrationVersion = errors.New("unknown migration version")

// DirtyMigrationError reports that a previous migration failed partway.
// The recorded version says which migration was running, not which
// direction it ran in nor how much of it was applied, so the state cannot
// be repaired without a person inspecting the schema.
//
// Version is the raw recorded version. It is migratedb.NilVersion (-1) when
// the first down migration failed: the driver then records "no migration,
// dirty".
type DirtyMigrationError struct {
	Version int
}

func (e *DirtyMigrationError) Error() string {
	recorded := fmt.Sprintf("at migration version %d", e.Version)
	if e.Version == migratedb.NilVersion {
		recorded = fmt.Sprintf("with no migration recorded (version %d)", e.Version)
	}
	return fmt.Sprintf("database is dirty %s: a previous migration failed partway. "+
		"Inspect the schema, then run `puckdb db force-version <version>` with the version the schema actually matches (see %s)",
		recorded, recoveryDoc)
}

// migrator is what the runner needs from golang-migrate, so tests can
// substitute a double and assert which operations were attempted.
//
// Version is the database driver's raw version, not Migrate.Version: the
// latter reports NilVersion as ErrNilVersion and drops the dirty flag, which
// hides the "no migration, dirty" state a failed first down migration leaves.
type migrator interface {
	Version() (version int, dirty bool, err error)
	Up() error
	Down() error
	Force(version int) error
	Close() (source error, database error)
}

// MigrateUp runs the embedded SQL migrations against the given
// dbURL. Exposed for integration tests, which get a URL from
// PUCKDB_TEST_PG_URL rather than a ConnConfig.
//
// Functionally identical to RunSQLMigrations, just with the URL
// passed in.
func MigrateUp(dbURL string) error {
	return withMigrator(migrationsFS, dbURL, runUp)
}

// MigrateDown is the integration-test counterpart to MigrateUp —
// runs every down migration against dbURL. Tests call this in
// t.Cleanup so a fresh schema lands on every test run.
func MigrateDown(dbURL string) error {
	return withMigrator(migrationsFS, dbURL, runDown)
}

// RunSQLMigrations runs the embedded SQL migrations against conn.
func RunSQLMigrations(conn ConnConfig) error {
	dbURL, err := conn.URL()
	if err != nil {
		return err
	}
	return MigrateUp(dbURL)
}

// RunSQLMigrationsDown runs all down migrations against conn.
func RunSQLMigrationsDown(conn ConnConfig) error {
	dbURL, err := conn.URL()
	if err != nil {
		return err
	}
	return MigrateDown(dbURL)
}

// ForceMigrationVersion marks a dirty database clean at version without
// running any SQL. It is the explicit operator repair step: call it only
// after confirming which version the schema really matches.
// NoMigrationVersion records that no migration is applied.
func ForceMigrationVersion(conn ConnConfig, version int) error {
	dbURL, err := conn.URL()
	if err != nil {
		return err
	}
	return withMigrator(migrationsFS, dbURL, func(m migrator) error {
		return forceVersion(m, migrationsFS, version)
	})
}

// DoMigration runs the embedded SQL migrations against conn.
func DoMigration(conn ConnConfig) error {
	log.Info().Msg("Starting database migration")
	// Run SQL migrations (handles franchises, seasons, season_teams, players, games, stats)
	if err := RunSQLMigrations(conn); err != nil {
		return err
	}
	log.Info().Msg("Database migration completed")
	return nil
}

func openSource(fsys fs.FS) (source.Driver, error) {
	src, err := iofs.New(fsys, migrationsDir)
	if err != nil {
		return nil, fmt.Errorf("create iofs source: %w", err)
	}
	return src, nil
}

// driverMigrator is *migrate.Migrate with Version answered by the database
// driver it was built on. Migrate.Close closes that driver too.
type driverMigrator struct {
	*migrate.Migrate
	driver migratedb.Driver
}

func (d *driverMigrator) Version() (int, bool, error) {
	return d.driver.Version()
}

func openMigrator(fsys fs.FS, dbURL string) (*driverMigrator, error) {
	src, err := openSource(fsys)
	if err != nil {
		return nil, err
	}
	driver, err := migratedb.Open(dbURL)
	if err != nil {
		closeQuietly("migration source", src.Close)
		return nil, fmt.Errorf("open migration database: %w", err)
	}
	m, err := migrate.NewWithInstance(migrationsSourceName, src, databaseDriverName, driver)
	if err != nil {
		closeQuietly("migration source", src.Close)
		closeQuietly("migration database", driver.Close)
		return nil, fmt.Errorf("create migrate instance: %w", err)
	}
	return &driverMigrator{Migrate: m, driver: driver}, nil
}

// withMigrator opens a migrator over fsys and dbURL, runs fn, and closes it.
func withMigrator(fsys fs.FS, dbURL string, fn func(migrator) error) error {
	m, err := openMigrator(fsys, dbURL)
	if err != nil {
		return err
	}
	defer func() {
		sourceErr, databaseErr := m.Close()
		if err := errors.Join(sourceErr, databaseErr); err != nil {
			log.Warn().Err(err).Msg("Failed to close migrator")
		}
	}()
	return fn(m)
}

func closeQuietly(what string, closeFn func() error) {
	if err := closeFn(); err != nil {
		log.Warn().Err(err).Str("resource", what).Msg("Failed to close")
	}
}

// readCleanState returns the recorded version (migratedb.NilVersion on a
// database that has never been migrated), reports a real read failure as
// such, and refuses a dirty state.
func readCleanState(m migrator) (int, error) {
	version, dirty, err := m.Version()
	if err != nil {
		return migratedb.NilVersion, fmt.Errorf("read migration version: %w", err)
	}
	if dirty {
		return version, &DirtyMigrationError{Version: version}
	}
	return version, nil
}

// explainFailure turns golang-migrate's own dirty refusal into the
// actionable error, and tells the operator when the failure that just
// happened is what left the database dirty.
func explainFailure(m migrator, action string, err error) error {
	var dirtyErr migrate.ErrDirty
	if errors.As(err, &dirtyErr) {
		return &DirtyMigrationError{Version: dirtyErr.Version}
	}
	wrapped := fmt.Errorf("%s: %w", action, err)
	if version, dirty, versionErr := m.Version(); versionErr == nil && dirty {
		return errors.Join(wrapped, &DirtyMigrationError{Version: version})
	}
	return wrapped
}

func runUp(m migrator) error {
	log.Info().Msg("Running SQL migrations")
	before, err := readCleanState(m)
	if err != nil {
		return err
	}

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return explainFailure(m, "run migrations", err)
	}

	after, err := readCleanState(m)
	if err != nil {
		return fmt.Errorf("verify migrations: %w", err)
	}
	log.Info().Int("from_version", before).Int("version", after).
		Msg("SQL migrations completed")
	return nil
}

func runDown(m migrator) error {
	log.Info().Msg("Running SQL down migrations")
	if _, err := readCleanState(m); err != nil {
		return err
	}

	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return explainFailure(m, "run down migrations", err)
	}

	log.Info().Msg("SQL down migrations completed")
	return nil
}

// forceVersion repairs a dirty database. It refuses a clean one, and
// refuses a version the migrations in fsys do not define.
func forceVersion(m migrator, fsys fs.FS, version int) error {
	_, err := readCleanState(m)
	if err == nil {
		return ErrMigrationNotDirty
	}
	var dirtyErr *DirtyMigrationError
	if !errors.As(err, &dirtyErr) {
		return err
	}

	target := migratedb.NilVersion
	if version != NoMigrationVersion {
		if err := checkVersionExists(fsys, version); err != nil {
			return err
		}
		target = version
	}

	log.Warn().Int("dirty_version", dirtyErr.Version).Int("forced_version", version).
		Msg("Forcing migration version on operator request")
	if err := m.Force(target); err != nil {
		return fmt.Errorf("force migration version %d: %w", version, err)
	}
	return nil
}

// checkVersionExists reports ErrUnknownMigrationVersion unless fsys defines
// an up migration for version.
func checkVersionExists(fsys fs.FS, version int) error {
	if version < NoMigrationVersion {
		return fmt.Errorf("%w: %d", ErrUnknownMigrationVersion, version)
	}
	src, err := openSource(fsys)
	if err != nil {
		return err
	}
	defer closeQuietly("migration source", src.Close)

	reader, _, err := src.ReadUp(uint(version))
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: %d", ErrUnknownMigrationVersion, version)
	}
	if err != nil {
		return fmt.Errorf("read migration %d: %w", version, err)
	}
	return reader.Close()
}
