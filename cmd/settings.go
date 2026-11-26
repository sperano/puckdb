package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/appuser"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/database"
	"github.com/sperano/puckdb/internal/graph"
	"github.com/sperano/puckdb/internal/httpx"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/temporal"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/sperano/puckdb/internal/yahoolink"
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

// maxYahooRateMaxWaitSeconds is the largest --yahoo-rate-max-wait a
// time.Duration holds.
const maxYahooRateMaxWaitSeconds = math.MaxInt64 / int64(time.Second)

// yahooRateLimitFrom reads the --yahoo-rate-limit, --yahoo-rate-burst and
// --yahoo-rate-max-wait limiter flags, refusing a maximum wait a
// time.Duration cannot hold.
func yahooRateLimitFrom(v *viper.Viper) (shared.YahooRateLimit, error) {
	maxWait := v.GetInt64(config.FlagYahooRateMaxWait)
	if maxWait > maxYahooRateMaxWaitSeconds || maxWait < -maxYahooRateMaxWaitSeconds {
		return shared.YahooRateLimit{}, fmt.Errorf("%w: --%s must be at most %d seconds, got %d",
			shared.ErrInvalidYahooRateLimit, config.FlagYahooRateMaxWait, maxYahooRateMaxWaitSeconds, maxWait)
	}
	return shared.YahooRateLimit{
		PerSecond: v.GetFloat64(config.FlagYahooRateLimit),
		Burst:     v.GetInt(config.FlagYahooRateBurst),
		MaxWait:   time.Duration(maxWait) * time.Second,
	}, nil
}

// errInvalidYahooRateLimitScope reports a --yahoo-rate-limit-scope value
// other than deployment or process.
var errInvalidYahooRateLimitScope = errors.New("invalid Yahoo rate limit scope")

// yahooRateLimitScopeFrom reads --yahoo-rate-limit-scope.
func yahooRateLimitScopeFrom(v *viper.Viper) (string, error) {
	scope := v.GetString(config.FlagYahooRateLimitScope)
	switch scope {
	case config.YahooRateLimitScopeDeployment, config.YahooRateLimitScopeProcess:
		return scope, nil
	}
	return "", fmt.Errorf("%w: --%s must be %q or %q, got %q", errInvalidYahooRateLimitScope,
		config.FlagYahooRateLimitScope, config.YahooRateLimitScopeDeployment, config.YahooRateLimitScopeProcess, scope)
}

// yahooRateLimiterFrom builds the Yahoo request limiter the --yahoo-rate-*
// flags of v configure: the token bucket at key of redisClient, shared by
// the whole deployment, or the process's own.
func yahooRateLimiterFrom(v *viper.Viper, redisClient *redis.Client, key string) (shared.RateLimiter, error) {
	scope, err := yahooRateLimitScopeFrom(v)
	if err != nil {
		return nil, err
	}
	limit, err := yahooRateLimitFrom(v)
	if err != nil {
		return nil, err
	}
	if scope == config.YahooRateLimitScopeProcess {
		return shared.NewYahooRateLimiter(limit)
	}
	return shared.NewDeploymentRateLimiter(redisClient, key, limit)
}

// boxscorePlayerCacheTTLFrom reads the boxscore-player Redis lifetime.
func boxscorePlayerCacheTTLFrom(v *viper.Viper) time.Duration {
	return time.Duration(v.GetInt(config.FlagBoxscorePlayerCacheTTL)) * time.Minute
}

// playerLandingMaxAgeFrom reads the age past which an active player's
// cached landing page is downloaded again (zero: never), refusing hours
// outside [0, config.MaxPlayerLandingMaxAge].
func playerLandingMaxAgeFrom(v *viper.Viper) (time.Duration, error) {
	hours := v.GetInt(config.FlagPlayerLandingMaxAge)
	if hours < 0 || hours > config.MaxPlayerLandingMaxAge {
		return 0, fmt.Errorf("--%s: %d hours is outside [0, %d]",
			config.FlagPlayerLandingMaxAge, hours, config.MaxPlayerLandingMaxAge)
	}
	return time.Duration(hours) * time.Hour, nil
}

// adminAuthFrom reads --admin-group and --admin-token.
func adminAuthFrom(v *viper.Viper) graph.AdminAuth {
	return graph.AdminAuth{
		Group: v.GetString(config.FlagAdminGroup),
		Token: v.GetString(config.FlagAdminToken),
	}
}

// mauriceReportingFrom reads --maurice-reporting-enabled, with the login
// provider and the admin group it is checked against.
func mauriceReportingFrom(v *viper.Viper) graph.ReportingAuth {
	return graph.ReportingAuth{
		Enabled:  v.GetBool(config.FlagMauriceReportingEnabled),
		Provider: appAuthFrom(v).Provider,
		Group:    v.GetString(config.FlagAdminGroup),
	}
}

// appAuthFrom reads --auth-provider and the Cloudflare Access settings.
func appAuthFrom(v *viper.Viper) appuser.AuthConfig {
	return appuser.AuthConfig{
		Provider: strings.TrimSpace(v.GetString(config.FlagAuthProvider)),
		Cloudflare: appuser.CloudflareAccessConfig{
			TeamDomain: strings.TrimSpace(v.GetString(config.FlagCloudflareAccessTeamDomain)),
			Audience:   strings.TrimSpace(v.GetString(config.FlagCloudflareAccessAUD)),
		},
	}
}

// mauriceMembersFrom reads the members file --maurice-members names (no
// entries when it names none).
func mauriceMembersFrom(v *viper.Viper) (appuser.Members, error) {
	path := strings.TrimSpace(v.GetString(config.FlagMauriceMembers))
	if path == "" {
		return appuser.Members{}, nil
	}
	members, err := appuser.LoadMembers(path)
	if err != nil {
		return appuser.Members{}, fmt.Errorf("--%s: %w", config.FlagMauriceMembers, err)
	}
	return members, nil
}

// mauriceYahooLeaguesFrom reads --maurice-yahoo-leagues, the Yahoo league
// allowlist of Maurice's prompt and team picker (empty: every league).
func mauriceYahooLeaguesFrom(v *viper.Viper) ([]int32, error) {
	ids, err := yahoolink.ParseLeagueIDs(v.GetString(config.FlagMauriceYahooLeagues))
	if err != nil {
		return nil, fmt.Errorf("--%s: %w", config.FlagMauriceYahooLeagues, err)
	}
	return ids, nil
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

// newYahooRateLimiter builds the Yahoo request limiter from the
// --yahoo-rate-* flags, over the bucket at key of redisClient for the
// deployment scope: shared.YahooRateLimitKey for the worker's downloads,
// shared.YahooDraftRateLimitKey for live draft polls, so they never queue
// behind a season sync. Call it once per process and share the result by
// every Yahoo downloader, or the process exceeds its configured rate (its
// fallback's, for the deployment scope).
func newYahooRateLimiter(redisClient *redis.Client, key string) (shared.RateLimiter, error) {
	return yahooRateLimiterFrom(viper.GetViper(), redisClient, key)
}

// newYahooDraftRateLimiter is newYahooRateLimiter for live draft polls (the
// api's draft watcher, sync draft): their own bucket, at the same settings.
func newYahooDraftRateLimiter(redisClient *redis.Client) (shared.RateLimiter, error) {
	return newYahooRateLimiter(redisClient, shared.YahooDraftRateLimitKey)
}

// newYahooDownloader downloads Yahoo resources with the configured OAuth2
// application and the token stored in redisClient, at the rate limiter allows.
func newYahooDownloader(redisClient *redis.Client, limiter shared.RateLimiter) shared.Downloader {
	return shared.NewYahooDownloader(redisClient, yahooAuthFrom(viper.GetViper()), limiter)
}

// newDefaultStorage opens the file cache under the configured data path.
func newDefaultStorage() store.Storage {
	return store.NewDefaultStorage(dataPathFrom(viper.GetViper()))
}
