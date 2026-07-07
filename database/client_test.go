package database

import (
	"context"
	"testing"

	"github.com/sperano/puckdb/config"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withPostgresViperSettings sets the seven Postgres viper keys to the given
// values for the duration of the test, restoring originals on cleanup.
// viper is global mutable state, so tests that touch these keys must not
// run in parallel with each other.
func withPostgresViperSettings(t *testing.T, host, user, pass, db string, port int, sslMode, timeZone string) {
	t.Helper()
	keys := []struct {
		name string
		val  any
	}{
		{config.FlagPostgresHost, host},
		{config.FlagPostgresUser, user},
		{config.FlagPostgresPassword, pass},
		{config.FlagPostgresDatabase, db},
		{config.FlagPostgresPort, port},
		{config.FlagPostgresSSLMode, sslMode},
		{config.FlagPostgresTimeZone, timeZone},
	}
	originals := make(map[string]any, len(keys))
	for _, k := range keys {
		originals[k.name] = viper.Get(k.name)
		viper.Set(k.name, k.val)
	}
	t.Cleanup(func() {
		for k, v := range originals {
			viper.Set(k, v)
		}
	})
}

func TestGetDSN(t *testing.T) {
	s := getDSN("a", "b", "c", "d", 44, "e", "f")
	assert.Equal(t, "host=a user=b password=c dbname=d port=44 sslmode=e timezone=f", s)
}

func TestGetDSN_FromViper(t *testing.T) {
	withPostgresViperSettings(t, "vhost", "vuser", "vpass", "vdb", 6543, "verify-full", "America/Toronto")
	expected := "host=vhost user=vuser password=vpass dbname=vdb port=6543 sslmode=verify-full timezone=America/Toronto"
	assert.Equal(t, expected, GetDSN())
}

func TestGetDatabaseURL(t *testing.T) {
	withPostgresViperSettings(t, "testhost", "testuser", "testpass", "testdb", 5433, "require", "UTC")
	expected := "postgres://testuser:testpass@testhost:5433/testdb?sslmode=require"
	assert.Equal(t, expected, GetDatabaseURL())
}

func TestOpenPGXPool_InvalidConfig(t *testing.T) {
	// pgconn rejects ports outside 1-65535 during ParseConfig, before any
	// network call. This exercises the parse-error branch in OpenPGXPool
	// without needing a real database.
	withPostgresViperSettings(t, "localhost", "u", "p", "d", 99999, "disable", "UTC")
	pool, err := OpenPGXPool(context.Background())
	require.Error(t, err)
	assert.Nil(t, pool)
}
