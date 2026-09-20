package database

import (
	"errors"
	"testing"
	"testing/fstest"

	"github.com/golang-migrate/migrate/v4"
	migratedb "github.com/golang-migrate/migrate/v4/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	callVersion = "Version"
	callUp      = "Up"
	callDown    = "Down"
	callForce   = "Force"

	firstVersion  = 1
	secondVersion = 2
	// missingVersion leaves a gap after secondVersion: versions need not be
	// contiguous, which is one reason "dirty version minus one" was wrong.
	missingVersion = 3
	gappedVersion  = 5
)

var errBoom = errors.New("boom")

// fakeMigrator records every operation attempted so tests can prove that a
// dirty database triggers neither Force nor any migration SQL.
type fakeMigrator struct {
	version    int
	dirty      bool
	versionErr error
	upErr      error
	downErr    error
	// dirtyAfterRun marks the state dirty once Up or Down has run, like a
	// migration that fails partway.
	dirtyAfterRun bool

	calls  []string
	forced []int
}

func (f *fakeMigrator) Version() (int, bool, error) {
	f.calls = append(f.calls, callVersion)
	return f.version, f.dirty, f.versionErr
}

func (f *fakeMigrator) Up() error {
	f.calls = append(f.calls, callUp)
	f.dirty = f.dirty || f.dirtyAfterRun
	return f.upErr
}

func (f *fakeMigrator) Down() error {
	f.calls = append(f.calls, callDown)
	f.dirty = f.dirty || f.dirtyAfterRun
	return f.downErr
}

func (f *fakeMigrator) Force(version int) error {
	f.calls = append(f.calls, callForce)
	f.forced = append(f.forced, version)
	return nil
}

func (f *fakeMigrator) Close() (error, error) { return nil, nil }

var runners = map[string]func(migrator) error{
	callUp:   runUp,
	callDown: runDown,
}

func testMigrationsFS() fstest.MapFS {
	return fstest.MapFS{
		"migrations/000001_first.up.sql":    {Data: []byte("SELECT 1;")},
		"migrations/000001_first.down.sql":  {Data: []byte("SELECT 1;")},
		"migrations/000002_second.up.sql":   {Data: []byte("SELECT 1;")},
		"migrations/000002_second.down.sql": {Data: []byte("SELECT 1;")},
		"migrations/000005_gapped.up.sql":   {Data: []byte("SELECT 1;")},
		"migrations/000005_gapped.down.sql": {Data: []byte("SELECT 1;")},
	}
}

func TestRun_DirtyDatabaseRunsNothing(t *testing.T) {
	t.Parallel()
	for name, run := range runners {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			m := &fakeMigrator{version: secondVersion, dirty: true}

			err := run(m)

			var dirtyErr *DirtyMigrationError
			require.ErrorAs(t, err, &dirtyErr)
			assert.Equal(t, secondVersion, dirtyErr.Version)
			assert.Contains(t, err.Error(), "force-version")
			assert.Contains(t, err.Error(), recoveryDoc)
			assert.Equal(t, []string{callVersion}, m.calls, "only the version may be read")
		})
	}
}

func TestRun_NeverMigratedDatabaseProceeds(t *testing.T) {
	t.Parallel()
	for name, run := range runners {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			m := &fakeMigrator{version: migratedb.NilVersion}

			require.NoError(t, run(m))
			assert.Contains(t, m.calls, name)
		})
	}
}

// When the first down migration fails the driver records "no migration,
// dirty". Migrate.Version hides that as ErrNilVersion; the raw version does
// not, and the message must not render -1 as an unsigned number.
func TestRun_DirtyWithNoVersionRunsNothing(t *testing.T) {
	t.Parallel()
	for name, run := range runners {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			m := &fakeMigrator{version: migratedb.NilVersion, dirty: true}

			err := run(m)

			var dirtyErr *DirtyMigrationError
			require.ErrorAs(t, err, &dirtyErr)
			assert.Equal(t, migratedb.NilVersion, dirtyErr.Version)
			assert.Contains(t, err.Error(), "no migration recorded (version -1)")
			assert.Equal(t, []string{callVersion}, m.calls)
		})
	}
}

func TestRun_VersionReadErrorIsNotMistakenForFreshDatabase(t *testing.T) {
	t.Parallel()
	for name, run := range runners {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			m := &fakeMigrator{versionErr: errBoom}

			err := run(m)

			require.ErrorIs(t, err, errBoom)
			assert.Equal(t, []string{callVersion}, m.calls)
		})
	}
}

func TestRun_NoChangeIsSuccess(t *testing.T) {
	t.Parallel()
	m := &fakeMigrator{version: secondVersion, upErr: migrate.ErrNoChange, downErr: migrate.ErrNoChange}
	require.NoError(t, runUp(m))
	require.NoError(t, runDown(m))
}

func TestRun_FailureThatLeavesDatabaseDirtySaysSo(t *testing.T) {
	t.Parallel()
	for name, run := range runners {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			m := &fakeMigrator{version: secondVersion, upErr: errBoom, downErr: errBoom, dirtyAfterRun: true}

			err := run(m)

			require.ErrorIs(t, err, errBoom)
			var dirtyErr *DirtyMigrationError
			require.ErrorAs(t, err, &dirtyErr)
			assert.NotContains(t, m.calls, callForce)
		})
	}
}

func TestRun_FailureThatLeavesDatabaseCleanIsPlain(t *testing.T) {
	t.Parallel()
	m := &fakeMigrator{version: secondVersion, upErr: errBoom}

	err := runUp(m)

	require.ErrorIs(t, err, errBoom)
	var dirtyErr *DirtyMigrationError
	assert.False(t, errors.As(err, &dirtyErr))
}

// A concurrent runner can dirty the database between our check and Up;
// golang-migrate then refuses with its own ErrDirty.
func TestRun_LibraryDirtyRefusalBecomesActionable(t *testing.T) {
	t.Parallel()
	m := &fakeMigrator{version: firstVersion, upErr: migrate.ErrDirty{Version: secondVersion}}

	err := runUp(m)

	var dirtyErr *DirtyMigrationError
	require.ErrorAs(t, err, &dirtyErr)
	assert.Equal(t, secondVersion, dirtyErr.Version)
}

func TestForceVersion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		migrator   *fakeMigrator
		version    int
		wantErr    error
		wantForced []int
	}{
		{name: "dirty to existing version", migrator: &fakeMigrator{version: secondVersion, dirty: true},
			version: firstVersion, wantForced: []int{firstVersion}},
		{name: "dirty to the dirty version itself", migrator: &fakeMigrator{version: secondVersion, dirty: true},
			version: secondVersion, wantForced: []int{secondVersion}},
		{name: "dirty across a version gap", migrator: &fakeMigrator{version: gappedVersion, dirty: true},
			version: secondVersion, wantForced: []int{secondVersion}},
		{name: "dirty to no migration", migrator: &fakeMigrator{version: firstVersion, dirty: true},
			version: NoMigrationVersion, wantForced: []int{migratedb.NilVersion}},
		{name: "clean database is refused", migrator: &fakeMigrator{version: secondVersion},
			version: firstVersion, wantErr: ErrMigrationNotDirty},
		{name: "never migrated database is refused", migrator: &fakeMigrator{version: migratedb.NilVersion},
			version: firstVersion, wantErr: ErrMigrationNotDirty},
		{name: "dirty with no version to existing version", migrator: &fakeMigrator{version: migratedb.NilVersion, dirty: true},
			version: firstVersion, wantForced: []int{firstVersion}},
		{name: "dirty with no version to no migration", migrator: &fakeMigrator{version: migratedb.NilVersion, dirty: true},
			version: NoMigrationVersion, wantForced: []int{migratedb.NilVersion}},
		{name: "version in a gap is refused", migrator: &fakeMigrator{version: gappedVersion, dirty: true},
			version: missingVersion, wantErr: ErrUnknownMigrationVersion},
		{name: "negative version is refused", migrator: &fakeMigrator{version: firstVersion, dirty: true},
			version: migratedb.NilVersion, wantErr: ErrUnknownMigrationVersion},
		{name: "version read error is propagated", migrator: &fakeMigrator{versionErr: errBoom},
			version: firstVersion, wantErr: errBoom},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := forceVersion(tt.migrator, testMigrationsFS(), tt.version)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.wantForced, tt.migrator.forced)
			assert.NotContains(t, tt.migrator.calls, callUp)
			assert.NotContains(t, tt.migrator.calls, callDown)
		})
	}
}

func TestCheckVersionExists_EmbeddedMigrations(t *testing.T) {
	t.Parallel()
	require.NoError(t, checkVersionExists(migrationsFS, firstVersion))

	const farBeyondAnyMigration = 999999
	require.ErrorIs(t, checkVersionExists(migrationsFS, farBeyondAnyMigration), ErrUnknownMigrationVersion)
}
