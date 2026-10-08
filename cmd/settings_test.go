package cmd

import (
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/database"
	"github.com/sperano/puckdb/internal/graph"
	"github.com/sperano/puckdb/internal/httpx"
	"github.com/sperano/puckdb/internal/temporal"
	flag "github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// boundViper registers groups on a fresh flag set and binds them to a
// fresh viper, as the commands do with the global one. args are parsed as
// command-line flags.
func boundViper(t *testing.T, args []string, groups ...*config.FlagGroup) *viper.Viper {
	t.Helper()
	flags := flag.NewFlagSet(t.Name(), flag.ContinueOnError)
	config.InitFlags(flags, groups...)
	require.NoError(t, flags.Parse(args))
	v := viper.New()
	for _, group := range groups {
		for _, f := range group.Flags {
			require.NoError(t, v.BindPFlag(f.Name, flags.Lookup(f.Name)))
		}
	}
	return v
}

func TestDatabaseConfigFrom(t *testing.T) {
	t.Parallel()
	t.Run("flag defaults keep the current pool sizing", func(t *testing.T) {
		t.Parallel()
		got := databaseConfigFrom(boundViper(t, nil, &config.PostgresFlags))
		assert.Equal(t, database.DefaultPoolOptions(), got.Pool)
		assert.Equal(t, database.PoolOptions{MaxConns: 5, MinConns: 2, MaxConnLifetime: 5 * time.Minute}, got.Pool)
		assert.Equal(t, database.ConnConfig{
			Host: "localhost", Port: 5432, User: "puckdb", Database: "puckdb",
			SSLMode: "disable", TimeZone: "America/Los_Angeles",
		}, got.Conn)
	})
	t.Run("flags override", func(t *testing.T) {
		t.Parallel()
		v := boundViper(t, []string{
			"--postgres-host=vhost", "--postgres-port=6543", "--postgres-user=vuser",
			"--postgres-password=v%pass", "--postgres-database=vdb", "--postgres-ssl-mode=verify-full",
			"--postgres-time-zone=America/Toronto", "--postgres-max-open-conns=9", "--postgres-max-idle-conns=4",
		}, &config.PostgresFlags)
		assert.Equal(t, database.Config{
			Conn: database.ConnConfig{
				Host: "vhost", Port: 6543, User: "vuser", Password: "v%pass", Database: "vdb",
				SSLMode: "verify-full", TimeZone: "America/Toronto",
			},
			Pool: database.PoolOptions{MaxConns: 9, MinConns: 4, MaxConnLifetime: config.DefaultDBConnMaxLifetime},
		}, databaseConfigFrom(v))
	})
}

func TestTemporalOptionsFrom(t *testing.T) {
	t.Parallel()
	t.Run("flag defaults keep the current settings", func(t *testing.T) {
		t.Parallel()
		got := temporalOptionsFrom(boundViper(t, nil, &config.TemporalFlags))
		assert.Equal(t, temporal.DefaultOptions(), got)
		assert.Equal(t, temporal.Options{HostPort: "localhost:7233", Namespace: "puckdb"}, got)
	})
	t.Run("flags override", func(t *testing.T) {
		t.Parallel()
		v := boundViper(t, []string{"--temporal-hostport=temporal:7233", "--temporal-namespace=staging"}, &config.TemporalFlags)
		assert.Equal(t, temporal.Options{HostPort: "temporal:7233", Namespace: "staging"}, temporalOptionsFrom(v))
	})
}

func TestRedisOptionsFrom(t *testing.T) {
	t.Parallel()
	t.Run("flag defaults keep the current settings", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, cache.DefaultOptions(), redisOptionsFrom(boundViper(t, nil, &config.RedisFlags)))
	})
	t.Run("flags override", func(t *testing.T) {
		t.Parallel()
		v := boundViper(t, []string{"--redis-url=redis:6379", "--redis-password=pw", "--redis-db=4"}, &config.RedisFlags)
		assert.Equal(t, cache.Options{Addr: "redis:6379", Password: "pw", DB: 4}, redisOptionsFrom(v))
	})
}

func TestDataPathFrom(t *testing.T) {
	t.Parallel()
	assert.Empty(t, dataPathFrom(boundViper(t, nil, &config.DataPathFlags)))
	assert.Equal(t, "/data", dataPathFrom(boundViper(t, []string{"--data-path=/data"}, &config.DataPathFlags)))
}

func TestYahooAuthFrom(t *testing.T) {
	t.Parallel()
	v := boundViper(t, []string{
		"--yahoo-oauth2-client-id=id", "--yahoo-oauth2-client-secret=secret", "--public-url=https://puck.example",
	}, &config.YahooOAuth2Flags)
	assert.Equal(t, httpx.YahooAuth{ClientID: "id", ClientSecret: "secret", PublicURL: "https://puck.example"}, yahooAuthFrom(v))
}

func TestAdminAuthFrom(t *testing.T) {
	t.Parallel()
	assert.Equal(t, graph.AdminAuth{Group: "puckdb-admins"}, adminAuthFrom(boundViper(t, nil, &config.AdminAuthFlags)))
	v := boundViper(t, []string{"--admin-group=ops", "--admin-token=s3cr3t"}, &config.AdminAuthFlags)
	assert.Equal(t, graph.AdminAuth{Group: "ops", Token: "s3cr3t"}, adminAuthFrom(v))
}
