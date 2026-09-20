package database

import (
	"context"
	"net/url"
	"testing"

	"github.com/sperano/puckdb/internal/config"
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

func TestOpenPGXPool_InvalidConfig(t *testing.T) {
	// pgconn rejects ports outside 1-65535 during ParseConfig, before any
	// network call. This exercises the parse-error branch in OpenPGXPool
	// without needing a real database.
	const secretPassword = "s3cr3t-p@ss"
	withPostgresViperSettings(t, "localhost", "u", secretPassword, "d", 99999, "disable", "UTC")
	pool, err := OpenPGXPool(context.Background())
	require.Error(t, err)
	assert.Nil(t, pool)
	assert.NotContains(t, err.Error(), secretPassword)
	assert.NotContains(t, err.Error(), url.QueryEscape(secretPassword))
}
