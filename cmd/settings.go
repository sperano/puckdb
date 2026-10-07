package cmd

import (
	"context"

	"github.com/go-redis/redis/v8"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/database"
	"github.com/sperano/puckdb/internal/graph"
	"github.com/sperano/puckdb/internal/httpx"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/temporal"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/spf13/viper"
	"go.temporal.io/sdk/client"
)

// The readers below are the only place where library client settings come
// out of viper: commands pass viper.GetViper(), tests a viper.New() of
// their own. The library packages take the returned values and never read
// viper themselves.

// postgresConnFrom reads the --postgres-* connection flags.
func postgresConnFrom(v *viper.Viper) database.ConnConfig {
	return database.ConnConfig{
		Host:     v.GetString(config.FlagPostgresHost),
		Port:     v.GetInt(config.FlagPostgresPort),
		User:     v.GetString(config.FlagPostgresUser),
		Password: v.GetString(config.FlagPostgresPassword),
		Database: v.GetString(config.FlagPostgresDatabase),
		SSLMode:  v.GetString(config.FlagPostgresSSLMode),
		TimeZone: v.GetString(config.FlagPostgresTimeZone),
	}
}

// poolOptionsFrom reads the --postgres-max-* pool sizing flags. The
// connection lifetime has no flag.
func poolOptionsFrom(v *viper.Viper) database.PoolOptions {
	return database.PoolOptions{
		MaxConns:        v.GetInt(config.FlagPostgresMaxOpenConns),
		MinConns:        v.GetInt(config.FlagPostgresMaxIdleConns),
		MaxConnLifetime: config.DefaultDBConnMaxLifetime,
	}
}

// databaseConfigFrom reads every --postgres-* flag.
func databaseConfigFrom(v *viper.Viper) database.Config {
	return database.Config{Conn: postgresConnFrom(v), Pool: poolOptionsFrom(v)}
}

// temporalOptionsFrom reads the --temporal-* connection flags.
func temporalOptionsFrom(v *viper.Viper) temporal.Options {
	return temporal.Options{
		HostPort:  v.GetString(config.FlagTemporalHostPort),
		Namespace: v.GetString(config.FlagTemporalNamespace),
	}
}

// redisOptionsFrom reads the --redis-* connection flags.
func redisOptionsFrom(v *viper.Viper) cache.Options {
	return cache.Options{
		Addr:     v.GetString(config.FlagRedisURL),
		Password: v.GetString(config.FlagRedisPassword),
		DB:       v.GetInt(config.FlagRedisDB),
	}
}

// yahooAuthFrom reads the Yahoo OAuth2 application flags and --public-url.
func yahooAuthFrom(v *viper.Viper) httpx.YahooAuth {
	return httpx.YahooAuth{
		ClientID:     v.GetString(config.FlagYahooOAuth2ClientID),
		ClientSecret: v.GetString(config.FlagYahooOAuth2ClientSecret),
		PublicURL:    v.GetString(config.FlagPublicURL),
	}
}

// adminAuthFrom reads --admin-group and --admin-token.
func adminAuthFrom(v *viper.Viper) graph.AdminAuth {
	return graph.AdminAuth{
		Group: v.GetString(config.FlagAdminGroup),
		Token: v.GetString(config.FlagAdminToken),
	}
}

// dataPathFrom reads --data-path, the root of the file cache.
func dataPathFrom(v *viper.Viper) string {
	return v.GetString(config.FlagDataPath)
}

// openPGXPool opens the PostgreSQL pool with the configured settings.
func openPGXPool(ctx context.Context) (*pgxpool.Pool, error) {
	return database.OpenPGXPool(ctx, databaseConfigFrom(viper.GetViper()))
}

// newTemporalClient dials Temporal with the configured settings.
func newTemporalClient() (client.Client, error) {
	return temporal.NewClient(temporalOptionsFrom(viper.GetViper()))
}

// newRedisClient opens a Redis client with the configured settings.
func newRedisClient() *redis.Client {
	return cache.NewClient(redisOptionsFrom(viper.GetViper()))
}

// newYahooDownloader downloads Yahoo resources with the configured OAuth2
// application and the token stored in redisClient.
func newYahooDownloader(redisClient *redis.Client) shared.Downloader {
	return shared.NewYahooDownloader(redisClient, yahooAuthFrom(viper.GetViper()))
}

// newDefaultStorage opens the file cache under the configured data path.
func newDefaultStorage() store.Storage {
	return store.NewDefaultStorage(dataPathFrom(viper.GetViper()))
}
