package database

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// appUserInstancesVersion makes users per instance.
	appUserInstancesVersion = 28
	// mainInstance owns the users created before instances existed.
	mainInstance = "puckdb"
)

// Up gives existing users the main instance and lets one identity be a user
// on each instance, with the instance in app_user_usage; down refuses while
// an identity has users on two instances, then restores the global key.
func TestIntegration_AppUserInstancesMigration(t *testing.T) {
	conn, dbURL := freshDatabase(t)
	migrateTo(t, dbURL, appUserInstancesVersion-1)
	const legacy = "66666666-6666-6666-6666-666666666666"
	execSQL(t, conn, `INSERT INTO app_users (id, auth_provider, auth_subject, username) VALUES ($1, 'authentik', 'sub', 'u')`, legacy)

	migrateTo(t, dbURL, appUserInstancesVersion)
	exists, nullable := columnNullable(t, conn, "app_users", "instance")
	assert.True(t, exists)
	assert.False(t, nullable, "app_users.instance must be NOT NULL")
	assert.Equal(t, 1, countRows(t, conn, `SELECT count(*) FROM app_users WHERE id = $1 AND instance = $2`, legacy, mainInstance))
	assert.Equal(t, 1, countRows(t, conn, `SELECT count(*) FROM app_user_usage WHERE user_id = $1 AND instance = $2`, legacy, mainInstance))

	_, err := conn.Exec(t.Context(), `INSERT INTO app_users (auth_provider, auth_subject, username) VALUES ('authentik', 'new', 'n')`)
	require.Error(t, err, "a user must name its instance")
	_, err = conn.Exec(t.Context(), `INSERT INTO app_users (instance, auth_provider, auth_subject, username) VALUES ('', 'authentik', 'new', 'n')`)
	require.Error(t, err, "the instance must not be empty")
	_, err = conn.Exec(t.Context(), `INSERT INTO app_users (instance, auth_provider, auth_subject, username) VALUES ($1, 'authentik', 'sub', 'u')`, mainInstance)
	require.Error(t, err, "one user per identity and instance")
	execSQL(t, conn, `INSERT INTO app_users (instance, auth_provider, auth_subject, username) VALUES ('maurice-crapettes', 'authentik', 'sub', 'u')`)

	m, err := openMigrator(migrationsFS, dbURL)
	require.NoError(t, err)
	err = m.Migrate.Migrate(appUserInstancesVersion - 1)
	_, _ = m.Close()
	require.ErrorContains(t, err, "an identity has users on several instances")

	// The failed down left the schema at the new version, recorded as a dirty
	// older one; an operator removes the other instance's user, then forces
	// the version back (db force-version) before rolling back again.
	assert.Equal(t, 1, countRows(t, conn, `SELECT count(*) FROM schema_migrations WHERE version = $1 AND dirty`, appUserInstancesVersion-1))
	exists, _ = columnNullable(t, conn, "app_users", "instance")
	assert.True(t, exists, "the refused rollback changed nothing")
	execSQL(t, conn, `DELETE FROM app_users WHERE instance <> $1`, mainInstance)
	execSQL(t, conn, `UPDATE schema_migrations SET version = $1, dirty = false`, appUserInstancesVersion)
	migrateTo(t, dbURL, appUserInstancesVersion-1)
	exists, _ = columnNullable(t, conn, "app_users", "instance")
	assert.False(t, exists)
	assert.Equal(t, 1, countRows(t, conn, `SELECT count(*) FROM app_user_usage WHERE user_id = $1`, legacy))
	_, err = conn.Exec(t.Context(), `INSERT INTO app_users (auth_provider, auth_subject, username) VALUES ('authentik', 'sub', 'u')`)
	require.Error(t, err, "the global identity key is back")

	migrateTo(t, dbURL, appUserInstancesVersion)
}
