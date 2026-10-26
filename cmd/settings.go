package cmd

import (
	"context"
	"io"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
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
	"golang.org/x/time/rate"
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

// yahooRateLimitFrom reads the --yahoo-rate-* per-process limiter flags.
func yahooRateLimitFrom(v *viper.Viper) shared.YahooRateLimit {
	return shared.YahooRateLimit{
		PerSecond: v.GetFloat64(config.FlagYahooRateLimit),
		Burst:     v.GetInt(config.FlagYahooRateBurst),
	}
}

// boxscorePlayerCacheTTLFrom reads the boxscore-player Redis lifetime.
func boxscorePlayerCacheTTLFrom(v *viper.Viper) time.Duration {
	return time.Duration(v.GetInt(config.FlagBoxscorePlayerCacheTTL)) * time.Minute
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

// newTemporalClient dials Temporal with the configured settings. metricsHandler
// is optional: a nil handler leaves Temporal SDK metrics unreported.
func newTemporalClient(metricsHandler client.MetricsHandler) (client.Client, error) {
	opts := temporalOptionsFrom(viper.GetViper())
	opts.MetricsHandler = metricsHandler
	return temporal.NewClient(opts)
}

// newRedisClient opens a Redis client with the configured settings.
func newRedisClient() *redis.Client {
	return cache.NewClient(redisOptionsFrom(viper.GetViper()))
}

// closeOrWarn closes c and logs a failure as a warning naming what. Commands
// defer it for clients whose close error cannot change their outcome (Redis,
// the Temporal metrics reporter), so every such close is reported the same way.
func closeOrWarn(c io.Closer, what string) {
	if err := c.Close(); err != nil {
		log.Warn().Err(err).Msgf("failed to close %s", what)
	}
}

// newYahooRateLimiter builds the process's Yahoo request limiter from the
// --yahoo-rate-* flags. Call it once per process and share the result by
// every Yahoo downloader, or the process exceeds its configured rate.
func newYahooRateLimiter() (*rate.Limiter, error) {
	return shared.NewYahooRateLimiter(yahooRateLimitFrom(viper.GetViper()))
}

// newYahooDownloader downloads Yahoo resources with the configured OAuth2
// application and the token stored in redisClient, at the rate limiter allows.
func newYahooDownloader(redisClient *redis.Client, limiter *rate.Limiter) shared.Downloader {
	return shared.NewYahooDownloader(redisClient, yahooAuthFrom(viper.GetViper()), limiter)
}

// newDefaultStorage opens the file cache under the configured data path.
func newDefaultStorage() store.Storage {
	return store.NewDefaultStorage(dataPathFrom(viper.GetViper()))
}
