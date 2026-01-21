package config

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	flag "github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// TODO some flags should just go in cmd and not be exported maybe?
const (
	FlagAPIPort                      = "api-port"
	FlagAPITLSEnabled                = "api-tls-enabled"
	FlagDataPath                     = "data-path"
	FlagLogLevel                     = "log-level"
	FlagMetricsPort                  = "metrics-port"
	FlagMetricsRefreshInterval       = "metrics-refresh-interval"
	FlagMetricsTLSEnabled            = "metrics-tls-enabled"
	FlagPostgresHost                 = "postgres-host"
	FlagPostgresUser                 = "postgres-user"
	FlagPostgresPassword             = "postgres-password"
	FlagPostgresDatabase             = "postgres-database"
	FlagPostgresPort                 = "postgres-port"
	FlagPostgresSSLMode              = "postgres-ssl-mode"
	FlagPostgresTimeZone             = "postgres-time-zone"
	FlagRedisURL                     = "redis-url"
	FlagRedisPassword                = "redis-password"
	FlagRedisDB                      = "redis-db"
	FlagSeasons                      = "seasons" // this is for the league config file
	FlagSkipPreseason                = "skip-preseason"
	FlagTemporalHostPort             = "temporal-hostport"
	FlagTemporalNamespace            = "temporal-namespace"
	FlagTLSCertificate               = "tls-certificate"
	FlagTLSKey                       = "tls-key"
	FlagWorkerPort                   = "worker-port"
	FlagWorkerTLSEnabled             = "worker-tls-enabled"
	FlagYahooOAuth2ClientID          = "yahoo-oauth2-client-id"
	FlagYahooOAuth2ClientSecret      = "yahoo-oauth2-client-secret"
	FlagYahooHostname                = "yahoo-hostname"
	FlagYahooLogToken                = "yahoo-log-token"
	FlagMaxOpenDBConns               = "max-open-db-conns"
	FlagMaxIdleDBConns               = "max-idle-db-conns"
	FlagTemporalRetryInitialInterval = "temporal-retry-initial-interval"
	FlagTemporalRetryMaxAttempts     = "temporal-retry-max-attempts"
	FlagMaxSeasonConcurrency         = "max-season-concurrency"
	FlagMaxYahooPlayerID             = "max-yahoo-player-id"
	FlagYahooPlayerBatchSize         = "yahoo-player-batch-size"
	FlagYahooPlayersPerExecution     = "yahoo-players-per-execution"
)

// const FlagInteractive = "interactive"

func InitLogLevelFlag(flags *flag.FlagSet, defaultLevel string) {
	flags.StringP(FlagLogLevel, "L", defaultLevel, fmt.Sprintf("Log Level: %s", getLogLevelsStr()))
}

func InitSeasonsFlag(cmd *cobra.Command, flags *flag.FlagSet, persistent bool) {
	flags.StringP(FlagSeasons, "S", "seasons.yaml", "Seasons config file")
	var err error
	if persistent {
		err = cmd.MarkPersistentFlagRequired(FlagSeasons)
	} else {
		err = cmd.MarkFlagRequired(FlagSeasons)
	}
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to mark flag as required")
	}
}

func InitYahooOAuth2Flags(flags *flag.FlagSet) {
	flags.String(FlagYahooOAuth2ClientID, "", "Yahoo! OAuth2 Client ID")
	flags.String(FlagYahooOAuth2ClientSecret, "", "Yahoo! OAuth2 Client Secret")
	flags.String(FlagYahooHostname, "", "Hostname used to create login and authentication URLs")
	flags.Bool(FlagYahooLogToken, false, "Log the token after succesful authentication (for debugging)")
}

func BindYahooOAuth2Flags(flags *flag.FlagSet) error {
	if err := viper.BindPFlag(FlagYahooOAuth2ClientID, flags.Lookup(FlagYahooOAuth2ClientID)); err != nil {
		return err
	}
	if err := viper.BindPFlag(FlagYahooOAuth2ClientSecret, flags.Lookup(FlagYahooOAuth2ClientSecret)); err != nil {
		return err
	}
	if err := viper.BindPFlag(FlagYahooHostname, flags.Lookup(FlagYahooHostname)); err != nil {
		return err
	}
	return viper.BindPFlag(FlagYahooLogToken, flags.Lookup(FlagYahooLogToken))
}

func InitDataPathFlag(flags *flag.FlagSet) {
	flags.String(FlagDataPath, "", "Path to local files data")
}

func InitTemporalFlags(flags *flag.FlagSet) {
	flags.String(FlagTemporalHostPort, "localhost:7233", "Temporal host/port")
	flags.String(FlagTemporalNamespace, DefaultTemporalNamespace, "Temporal namespace")
}

func BindTemporalFlags(flags *flag.FlagSet) error {
	if err := viper.BindPFlag(FlagTemporalHostPort, flags.Lookup(FlagTemporalHostPort)); err != nil {
		return err
	}
	if err := viper.BindPFlag(FlagTemporalNamespace, flags.Lookup(FlagTemporalNamespace)); err != nil {
		return err
	}
	return nil
}

func InitTemporalRetryFlags(flags *flag.FlagSet) {
	flags.Int(FlagTemporalRetryInitialInterval, 5, "Initial interval in seconds between activity retries")
	flags.Int(FlagTemporalRetryMaxAttempts, 10, "Maximum number of activity retry attempts")
}

func BindTemporalRetryFlags(flags *flag.FlagSet) error {
	if err := viper.BindPFlag(FlagTemporalRetryInitialInterval, flags.Lookup(FlagTemporalRetryInitialInterval)); err != nil {
		return err
	}
	return viper.BindPFlag(FlagTemporalRetryMaxAttempts, flags.Lookup(FlagTemporalRetryMaxAttempts))
}

func InitMaxSeasonConcurrencyFlag(flags *flag.FlagSet) {
	flags.Int(FlagMaxSeasonConcurrency, 10, "Maximum number of seasons to download concurrently")
}

func BindMaxSeasonConcurrencyFlag(flags *flag.FlagSet) error {
	return viper.BindPFlag(FlagMaxSeasonConcurrency, flags.Lookup(FlagMaxSeasonConcurrency))
}

func InitMaxYahooPlayerIDFlag(flags *flag.FlagSet) {
	flags.Int(FlagMaxYahooPlayerID, 35000, "Maximum Yahoo player ID to scan when importing players")
}

func BindMaxYahooPlayerIDFlag(flags *flag.FlagSet) error {
	return viper.BindPFlag(FlagMaxYahooPlayerID, flags.Lookup(FlagMaxYahooPlayerID))
}

func InitYahooPlayerBatchSizeFlag(flags *flag.FlagSet) {
	flags.Int(FlagYahooPlayerBatchSize, 50, "Batch size for downloading Yahoo players")
}

func BindYahooPlayerBatchSizeFlag(flags *flag.FlagSet) error {
	return viper.BindPFlag(FlagYahooPlayerBatchSize, flags.Lookup(FlagYahooPlayerBatchSize))
}

func InitYahooPlayersPerExecutionFlag(flags *flag.FlagSet) {
	flags.Int(FlagYahooPlayersPerExecution, 5000, "Players to process per workflow execution before ContinueAsNew")
}

func BindYahooPlayersPerExecutionFlag(flags *flag.FlagSet) error {
	return viper.BindPFlag(FlagYahooPlayersPerExecution, flags.Lookup(FlagYahooPlayersPerExecution))
}

func InitRedisFlags(flags *flag.FlagSet) {
	flags.String(FlagRedisURL, "localhost:6379", "Redis url")
	flags.String(FlagRedisPassword, "", "Redis password")
	flags.Int(FlagRedisDB, 0, "Redis db") // TODO 1 should be managed by pulumi_home
}

func BindRedisFlags(flags *flag.FlagSet) error {
	if err := viper.BindPFlag(FlagRedisURL, flags.Lookup(FlagRedisURL)); err != nil {
		return err
	}
	if err := viper.BindPFlag(FlagRedisPassword, flags.Lookup(FlagRedisPassword)); err != nil {
		return err
	}
	return viper.BindPFlag(FlagRedisDB, flags.Lookup(FlagRedisDB))
}

func InitPostgresFlags(flags *flag.FlagSet) {
	flags.String(FlagPostgresHost, "localhost", "Postgres host")
	flags.String(FlagPostgresUser, "puckdb", "Postgres user")
	flags.String(FlagPostgresPassword, "", "Postgres password")
	flags.String(FlagPostgresDatabase, "puckdb", "Postgres database")
	flags.Int(FlagPostgresPort, 5432, "Postgres port")
	flags.String(FlagPostgresSSLMode, "disable", "Postgres SSL mode")
	flags.String(FlagPostgresTimeZone, "America/Los_Angeles", "Postgres time zone")
	flags.Int(FlagMaxIdleDBConns, 100, "Maximum idle database connections")
	flags.Int(FlagMaxOpenDBConns, 100, "Maximum open database connections")
}

func BindPostgresFlags(flags *flag.FlagSet) error {
	if err := viper.BindPFlag(FlagPostgresHost, flags.Lookup(FlagPostgresHost)); err != nil {
		return err
	}
	if err := viper.BindPFlag(FlagPostgresUser, flags.Lookup(FlagPostgresUser)); err != nil {
		return err
	}
	if err := viper.BindPFlag(FlagPostgresPassword, flags.Lookup(FlagPostgresPassword)); err != nil {
		return err
	}
	if err := viper.BindPFlag(FlagPostgresDatabase, flags.Lookup(FlagPostgresDatabase)); err != nil {
		return err
	}
	if err := viper.BindPFlag(FlagPostgresPort, flags.Lookup(FlagPostgresPort)); err != nil {
		return err
	}
	if err := viper.BindPFlag(FlagPostgresSSLMode, flags.Lookup(FlagPostgresSSLMode)); err != nil {
		return err
	}
	if err := viper.BindPFlag(FlagMaxIdleDBConns, flags.Lookup(FlagMaxIdleDBConns)); err != nil {
		return err
	}
	if err := viper.BindPFlag(FlagMaxOpenDBConns, flags.Lookup(FlagMaxOpenDBConns)); err != nil {
		return err
	}
	return viper.BindPFlag(FlagPostgresTimeZone, flags.Lookup(FlagPostgresTimeZone))
}

func InitWorkerPortFlag(flags *flag.FlagSet) {
	flags.IntP(FlagWorkerPort, "", 8788, "Default port")
}

func InitWorkerTLSEnabledFlag(flags *flag.FlagSet) {
	flags.Bool(FlagWorkerTLSEnabled, false, "Enable TLS Mode")
}

func InitSkipPreseasonFlag(flags *flag.FlagSet) {
	flags.Bool(FlagSkipPreseason, false, "Skip downloading and importing preseason games")
}

func BindSkipPreseasonFlag(flags *flag.FlagSet) error {
	return viper.BindPFlag(FlagSkipPreseason, flags.Lookup(FlagSkipPreseason))
}

func InitAPIPortFlag(flags *flag.FlagSet) {
	flags.IntP(FlagAPIPort, "p", 8787, "Default port")
}

func InitAPITLSEnabledFlag(flags *flag.FlagSet) {
	flags.Bool(FlagAPITLSEnabled, false, "Enable TLS Mode")
}

func InitMetricsPortFlag(flags *flag.FlagSet) {
	flags.IntP(FlagMetricsPort, "", 8789, "Default port")
}

func InitMetricsTLSEnabledFlag(flags *flag.FlagSet) {
	flags.Bool(FlagMetricsTLSEnabled, false, "Enable TLS Mode")
}

func InitMetricsRefreshIntervalFlag(flags *flag.FlagSet) {
	flags.Int(FlagMetricsRefreshInterval, 5, "refresh every seconds")
}

func InitTLSCertificate(flags *flag.FlagSet) {
	flags.String(FlagTLSCertificate, "", "TLS Certificates")
}

func InitTLSKey(flags *flag.FlagSet) {
	flags.String(FlagTLSKey, "", "TLS Key")
}

// Season range flags for filtering by season year
const (
	FlagSeasonYear     = "season"
	FlagFromSeasonYear = "from-season"
	FlagToSeasonYear   = "to-season"
)

func InitSeasonRangeFlags(flags *flag.FlagSet) {
	flags.Int(FlagSeasonYear, 0, "Season year (e.g., 2024). Sets both from and to season.")
	flags.Int(FlagFromSeasonYear, 0, "Start season year for range (e.g., 2020)")
	flags.Int(FlagToSeasonYear, 0, "End season year for range (e.g., 2024)")
}

func BindSeasonRangeFlags(flags *flag.FlagSet) error {
	if err := viper.BindPFlag(FlagSeasonYear, flags.Lookup(FlagSeasonYear)); err != nil {
		return err
	}
	if err := viper.BindPFlag(FlagFromSeasonYear, flags.Lookup(FlagFromSeasonYear)); err != nil {
		return err
	}
	return viper.BindPFlag(FlagToSeasonYear, flags.Lookup(FlagToSeasonYear))
}

// GetSeasonRange returns start and end season years from flags.
// If --season is set, it returns that value for both.
// Otherwise returns --from-season and --to-season (0 means not set).
func GetSeasonRange() (start, end int) {
	season := viper.GetInt(FlagSeasonYear)
	if season > 0 {
		return season, season
	}
	return viper.GetInt(FlagFromSeasonYear), viper.GetInt(FlagToSeasonYear)
}

func getTeamIDs(teamIDs string) []uint {
	tokens := strings.Split(teamIDs, ",")
	ids := make([]uint, len(tokens))
	for idx, tok := range tokens {
		id, err := strconv.Atoi(tok)
		if err != nil {
			log.Fatal().Err(err)
		}
		ids[idx] = uint(id)
	}
	return ids
}

func SetupViper() {
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	viper.SetEnvPrefix("puckdb")
	viper.AutomaticEnv()
	viper.AddConfigPath(".")
	viper.SetConfigName(".env")
	viper.SetConfigType("env")
	if err := viper.ReadInConfig(); err != nil {
		var configFileNotFoundError viper.ConfigFileNotFoundError
		if !errors.As(err, &configFileNotFoundError) {
			log.Warn().Msgf("error reading env file: %s", err.Error())
		}
	}
}

// sensitiveFlags contains flag names that should not be logged
var sensitiveFlags = map[string]bool{
	FlagPostgresPassword:        true,
	FlagRedisPassword:           true,
	FlagYahooOAuth2ClientSecret: true,
	FlagTLSKey:                  true,
}

// LogFlagValues logs all viper settings at debug level, redacting sensitive values
func LogFlagValues() {
	settings := viper.AllSettings()
	if len(settings) == 0 {
		return
	}

	// Collect and sort keys for consistent output
	keys := make([]string, 0, len(settings))
	for k := range settings {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		// Convert underscore back to hyphen for flag name lookup
		flagName := strings.ReplaceAll(key, "_", "-")
		value := settings[key]
		if sensitiveFlags[flagName] {
			log.Debug().Str("flag", flagName).Str("value", "[REDACTED]").Msg("config")
		} else {
			log.Debug().Str("flag", flagName).Interface("value", value).Msg("config")
		}
	}
}
