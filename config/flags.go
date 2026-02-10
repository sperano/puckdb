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

// Server flags
const (
	FlagAPIPort           = "api-port"
	FlagAPITLSEnabled     = "api-tls-enabled"
	FlagAPIServerAddr     = "api-server-addr"
	FlagWorkerPort        = "worker-port"
	FlagWorkerTLSEnabled  = "worker-tls-enabled"
	FlagMetricsPort       = "metrics-port"
	FlagMetricsTLSEnabled = "metrics-tls-enabled"
	FlagTLSCertificate    = "tls-certificate"
	FlagTLSKey            = "tls-key"
)

// Data and logging flags
const (
	FlagDataPath = "data-path"
	FlagLogLevel = "log-level"
	FlagLogFile  = "log-file"
)

// PostgreSQL flags
const (
	FlagPostgresHost         = "postgres-host"
	FlagPostgresUser         = "postgres-user"
	FlagPostgresPassword     = "postgres-password"
	FlagPostgresDatabase     = "postgres-database"
	FlagPostgresPort         = "postgres-port"
	FlagPostgresSSLMode      = "postgres-ssl-mode"
	FlagPostgresTimeZone     = "postgres-time-zone"
	FlagPostgresMaxOpenConns = "postgres-max-open-conns"
	FlagPostgresMaxIdleConns = "postgres-max-idle-conns"
)

// Redis flags
const (
	FlagRedisURL      = "redis-url"
	FlagRedisPassword = "redis-password"
	FlagRedisDB       = "redis-db"
)

// Temporal flags
const (
	FlagTemporalHostPort             = "temporal-hostport"
	FlagTemporalNamespace            = "temporal-namespace"
	FlagTemporalRetryInitialInterval = "temporal-retry-initial-interval"
	FlagTemporalRetryMaxInterval     = "temporal-retry-max-interval"
	FlagTemporalRetryMaxAttempts     = "temporal-retry-max-attempts"
)

// Worker concurrency flags
const (
	FlagWorkerMaxWorkflowPollers   = "worker-max-workflow-pollers"
	FlagWorkerMaxActivityPollers   = "worker-max-activity-pollers"
	FlagWorkerMaxWorkflowExecution = "worker-max-workflow-execution"
	FlagWorkerMaxActivityExecution = "worker-max-activity-execution"
)

// Yahoo OAuth2 flags
const (
	FlagYahooOAuth2ClientID     = "yahoo-oauth2-client-id"
	FlagYahooOAuth2ClientSecret = "yahoo-oauth2-client-secret"
	FlagYahooLogToken           = "yahoo-log-token"
	FlagYahooSeasons            = "yahoo-seasons"
	FlagPublicURL               = "public-url"
)

// Yahoo player download flags
const (
	FlagMaxYahooPlayerID             = "max-yahoo-player-id"
	FlagYahooPlayerBatchSize         = "yahoo-player-batch-size"
	FlagYahooPlayerActivityBatchSize = "yahoo-player-activity-batch-size"
	FlagYahooPlayersPerExecution     = "yahoo-players-per-execution"
	FlagYahooDownloadSleepMin        = "yahoo-download-sleep-min"
	FlagYahooDownloadSleepMax        = "yahoo-download-sleep-max"
)

// NHL player landing download flags
const (
	FlagPlayerLandingConcurrency    = "player-landing-concurrency"
	FlagPlayerLandingBatchSize      = "player-landing-batch-size"
	FlagPlayerLandingPlayersPerExec = "player-landing-players-per-exec"
)

// Download workflow flags
const (
	FlagMaxSeasonConcurrency = "max-season-concurrency"
	FlagDayConcurrency       = "day-concurrency"
	FlagSkipPreseason        = "skip-preseason"
	FlagSkipDownloadPlayers  = "skip-players"
	FlagSkipImportPlayers    = "skip-import-players"
	FlagSkipYahooPlayers     = "skip-yahoo-players"
	FlagSkipSeasons          = "skip-seasons"
	FlagSkipInitializing     = "skip-initializing"
	FlagSeasonConcurrency    = "season-concurrency"
	FlagMonitor              = "monitor"
	FlagSeasonYear           = "season"
	FlagFromSeasonYear       = "from-season"
	FlagToSeasonYear         = "to-season"
)

// Cache flags
const (
	FlagGameIDCacheTTL = "game-id-cache-ttl"
)

// Metrics collection flags
const (
	FlagMetricsRefreshInterval = "metrics-refresh-interval"
	FlagCacheIntervalSeconds   = "cache-interval-seconds"
	FlagRedisIntervalSeconds   = "redis-interval-seconds"
	FlagDBIntervalSeconds      = "db-interval-seconds"
)

// CLI display flags
const (
	FlagVerbose    = "verbose"
	FlagIncomplete = "incomplete"
)

// Provisioner flags
const (
	FlagProvisionerHost     = "provisioner-host"
	FlagProvisionerUser     = "provisioner-user"
	FlagProvisionerPassword = "provisioner-password"
)

// const FlagInteractive = "interactive"

func InitLoggingFlags(flags *flag.FlagSet, defaultLevel, defaultFile string) {
	flags.StringP(FlagLogLevel, "L", defaultLevel, fmt.Sprintf("Log Level: %s", getLogLevelsStr()))
	flags.String(FlagLogFile, defaultFile, fmt.Sprintf("Log file path (empty for stdout, 'default' for %s)", GetDefaultLogPath()))
}

func BindLoggingFlags(flags *flag.FlagSet) error {
	if err := viper.BindPFlag(FlagLogLevel, flags.Lookup(FlagLogLevel)); err != nil {
		return err
	}
	return viper.BindPFlag(FlagLogFile, flags.Lookup(FlagLogFile))
}

func InitSeasonsFlag(cmd *cobra.Command, flags *flag.FlagSet, persistent bool) {
	flags.StringP(FlagYahooSeasons, "S", DefaultYahooSeasonsFile, "Yahoo seasons config file")
	var err error
	if persistent {
		err = cmd.MarkPersistentFlagRequired(FlagYahooSeasons)
	} else {
		err = cmd.MarkFlagRequired(FlagYahooSeasons)
	}
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to mark flag as required")
	}
}

func InitYahooOAuth2Flags(flags *flag.FlagSet) {
	flags.String(FlagYahooOAuth2ClientID, "", "Yahoo! OAuth2 Client ID")
	flags.String(FlagYahooOAuth2ClientSecret, "", "Yahoo! OAuth2 Client Secret")
	flags.String(FlagPublicURL, "", "Public URL for OAuth callbacks (e.g., https://localhost:8787 or http://api.example.com)")
	flags.Bool(FlagYahooLogToken, false, "Log the token after succesful authentication (for debugging)")
}

func BindYahooOAuth2Flags(flags *flag.FlagSet) error {
	if err := viper.BindPFlag(FlagYahooOAuth2ClientID, flags.Lookup(FlagYahooOAuth2ClientID)); err != nil {
		return err
	}
	if err := viper.BindPFlag(FlagYahooOAuth2ClientSecret, flags.Lookup(FlagYahooOAuth2ClientSecret)); err != nil {
		return err
	}
	if err := viper.BindPFlag(FlagPublicURL, flags.Lookup(FlagPublicURL)); err != nil {
		return err
	}
	return viper.BindPFlag(FlagYahooLogToken, flags.Lookup(FlagYahooLogToken))
}

func InitDataPathFlag(flags *flag.FlagSet) {
	flags.String(FlagDataPath, "", "Path to local files data")
}

func InitTemporalFlags(flags *flag.FlagSet) {
	flags.String(FlagTemporalHostPort, DefaultTemporalHostPort, "Temporal host/port")
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
	flags.Int(FlagTemporalRetryInitialInterval, DefaultTemporalRetryInitialInterval, "Initial interval in seconds between activity retries")
	flags.Int(FlagTemporalRetryMaxInterval, DefaultTemporalRetryMaxInterval, "Maximum interval in seconds between activity retries (for rate limit recovery)")
	flags.Int(FlagTemporalRetryMaxAttempts, DefaultTemporalRetryMaxAttempts, "Maximum number of activity retry attempts (0 for unlimited)")
}

func BindTemporalRetryFlags(flags *flag.FlagSet) error {
	if err := viper.BindPFlag(FlagTemporalRetryInitialInterval, flags.Lookup(FlagTemporalRetryInitialInterval)); err != nil {
		return err
	}
	if err := viper.BindPFlag(FlagTemporalRetryMaxInterval, flags.Lookup(FlagTemporalRetryMaxInterval)); err != nil {
		return err
	}
	return viper.BindPFlag(FlagTemporalRetryMaxAttempts, flags.Lookup(FlagTemporalRetryMaxAttempts))
}

func InitMaxSeasonConcurrencyFlag(flags *flag.FlagSet) {
	flags.Int(FlagMaxSeasonConcurrency, DefaultMaxSeasonConcurrency, "Maximum number of seasons to download concurrently")
}

func BindMaxSeasonConcurrencyFlag(flags *flag.FlagSet) error {
	return viper.BindPFlag(FlagMaxSeasonConcurrency, flags.Lookup(FlagMaxSeasonConcurrency))
}

func InitDayConcurrencyFlag(flags *flag.FlagSet) {
	flags.Int(FlagDayConcurrency, DefaultDayConcurrency, "Number of days to download concurrently within each season")
}

func BindDayConcurrencyFlag(flags *flag.FlagSet) error {
	return viper.BindPFlag(FlagDayConcurrency, flags.Lookup(FlagDayConcurrency))
}

func InitMaxYahooPlayerIDFlag(flags *flag.FlagSet) {
	flags.Int(FlagMaxYahooPlayerID, DefaultMaxYahooPlayerID, "Maximum Yahoo player ID to scan when importing players")
}

func BindMaxYahooPlayerIDFlag(flags *flag.FlagSet) error {
	return viper.BindPFlag(FlagMaxYahooPlayerID, flags.Lookup(FlagMaxYahooPlayerID))
}

func InitYahooPlayerBatchSizeFlag(flags *flag.FlagSet) {
	flags.Int(FlagYahooPlayerBatchSize, DefaultYahooPlayerBatchSize, "Number of concurrent activities for downloading Yahoo players")
}

func BindYahooPlayerBatchSizeFlag(flags *flag.FlagSet) error {
	return viper.BindPFlag(FlagYahooPlayerBatchSize, flags.Lookup(FlagYahooPlayerBatchSize))
}

func InitYahooPlayerActivityBatchSizeFlag(flags *flag.FlagSet) {
	flags.Int(FlagYahooPlayerActivityBatchSize, DefaultYahooPlayerActivityBatchSize, "Number of players to process per activity")
}

func BindYahooPlayerActivityBatchSizeFlag(flags *flag.FlagSet) error {
	return viper.BindPFlag(FlagYahooPlayerActivityBatchSize, flags.Lookup(FlagYahooPlayerActivityBatchSize))
}

func InitYahooPlayersPerExecutionFlag(flags *flag.FlagSet) {
	flags.Int(FlagYahooPlayersPerExecution, DefaultYahooPlayersPerExecution, "Players to process per workflow execution before ContinueAsNew")
}

func BindYahooPlayersPerExecutionFlag(flags *flag.FlagSet) error {
	return viper.BindPFlag(FlagYahooPlayersPerExecution, flags.Lookup(FlagYahooPlayersPerExecution))
}

func InitGameIDCacheTTLFlag(flags *flag.FlagSet) {
	flags.Int(FlagGameIDCacheTTL, DefaultGameIDCacheTTL, "TTL in seconds for game ID cache in Redis")
}

func BindGameIDCacheTTLFlag(flags *flag.FlagSet) error {
	return viper.BindPFlag(FlagGameIDCacheTTL, flags.Lookup(FlagGameIDCacheTTL))
}

func InitYahooDownloadSleepFlags(flags *flag.FlagSet) {
	flags.Int(FlagYahooDownloadSleepMin, DefaultYahooDownloadSleepMin, "Minimum seconds to sleep after each Yahoo API download")
	flags.Int(FlagYahooDownloadSleepMax, DefaultYahooDownloadSleepMax, "Maximum seconds to sleep after each Yahoo API download")
}

func BindYahooDownloadSleepFlags(flags *flag.FlagSet) error {
	if err := viper.BindPFlag(FlagYahooDownloadSleepMin, flags.Lookup(FlagYahooDownloadSleepMin)); err != nil {
		return err
	}
	return viper.BindPFlag(FlagYahooDownloadSleepMax, flags.Lookup(FlagYahooDownloadSleepMax))
}

func InitPlayerLandingFlags(flags *flag.FlagSet) {
	flags.Int(FlagPlayerLandingConcurrency, DefaultPlayerLandingConcurrency, "Number of concurrent activities for downloading NHL player landings")
	flags.Int(FlagPlayerLandingBatchSize, DefaultPlayerLandingBatchSize, "Number of players to download per activity")
	flags.Int(FlagPlayerLandingPlayersPerExec, DefaultPlayerLandingPlayersPerExec, "Players to process per workflow execution before ContinueAsNew")
}

func BindPlayerLandingFlags(flags *flag.FlagSet) error {
	if err := viper.BindPFlag(FlagPlayerLandingConcurrency, flags.Lookup(FlagPlayerLandingConcurrency)); err != nil {
		return err
	}
	if err := viper.BindPFlag(FlagPlayerLandingBatchSize, flags.Lookup(FlagPlayerLandingBatchSize)); err != nil {
		return err
	}
	return viper.BindPFlag(FlagPlayerLandingPlayersPerExec, flags.Lookup(FlagPlayerLandingPlayersPerExec))
}

func InitProvisionerFlags(flags *flag.FlagSet) {
	flags.String(FlagProvisionerHost, "", "PostgreSQL host for provisioner connection")
	flags.String(FlagProvisionerUser, "", "PostgreSQL user with CREATE DATABASE privileges")
	flags.String(FlagProvisionerPassword, "", "PostgreSQL provisioner password")
}

func BindProvisionerFlags(flags *flag.FlagSet) error {
	if err := viper.BindPFlag(FlagProvisionerHost, flags.Lookup(FlagProvisionerHost)); err != nil {
		return err
	}
	if err := viper.BindPFlag(FlagProvisionerUser, flags.Lookup(FlagProvisionerUser)); err != nil {
		return err
	}
	return viper.BindPFlag(FlagProvisionerPassword, flags.Lookup(FlagProvisionerPassword))
}

func InitRedisFlags(flags *flag.FlagSet) {
	flags.String(FlagRedisURL, DefaultRedisURL, "Redis url")
	flags.String(FlagRedisPassword, "", "Redis password")
	flags.Int(FlagRedisDB, DefaultRedisDB, "Redis db")
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
	flags.String(FlagPostgresHost, DefaultPostgresHost, "Postgres host")
	flags.String(FlagPostgresUser, DefaultPostgresUser, "Postgres user")
	flags.String(FlagPostgresPassword, "", "Postgres password")
	flags.String(FlagPostgresDatabase, DefaultPostgresDatabase, "Postgres database")
	flags.Int(FlagPostgresPort, DefaultPostgresPort, "Postgres port")
	flags.String(FlagPostgresSSLMode, DefaultPostgresSSLMode, "Postgres SSL mode")
	flags.String(FlagPostgresTimeZone, DefaultPostgresTimeZone, "Postgres time zone")
	flags.Int(FlagPostgresMaxIdleConns, DefaultPostgresMaxIdleConns, "Maximum idle database connections")
	flags.Int(FlagPostgresMaxOpenConns, DefaultPostgresMaxOpenConns, "Maximum open database connections")
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
	if err := viper.BindPFlag(FlagPostgresMaxIdleConns, flags.Lookup(FlagPostgresMaxIdleConns)); err != nil {
		return err
	}
	if err := viper.BindPFlag(FlagPostgresMaxOpenConns, flags.Lookup(FlagPostgresMaxOpenConns)); err != nil {
		return err
	}
	return viper.BindPFlag(FlagPostgresTimeZone, flags.Lookup(FlagPostgresTimeZone))
}

func InitWorkerPortFlag(flags *flag.FlagSet) {
	flags.IntP(FlagWorkerPort, "", DefaultWorkerPort, "Worker metrics port")
}

func InitWorkerTLSEnabledFlag(flags *flag.FlagSet) {
	flags.Bool(FlagWorkerTLSEnabled, false, "Enable TLS Mode")
}

func InitWorkerConcurrencyFlags(flags *flag.FlagSet) {
	flags.Int(FlagWorkerMaxWorkflowPollers, DefaultWorkerMaxWorkflowPollers, "Max concurrent workflow task pollers")
	flags.Int(FlagWorkerMaxActivityPollers, DefaultWorkerMaxActivityPollers, "Max concurrent activity task pollers")
	flags.Int(FlagWorkerMaxWorkflowExecution, DefaultWorkerMaxWorkflowExecution, "Max concurrent workflow task executions")
	flags.Int(FlagWorkerMaxActivityExecution, DefaultWorkerMaxActivityExecution, "Max concurrent activity executions")
}

func BindWorkerConcurrencyFlags(flags *flag.FlagSet) error {
	if err := viper.BindPFlag(FlagWorkerMaxWorkflowPollers, flags.Lookup(FlagWorkerMaxWorkflowPollers)); err != nil {
		return err
	}
	if err := viper.BindPFlag(FlagWorkerMaxActivityPollers, flags.Lookup(FlagWorkerMaxActivityPollers)); err != nil {
		return err
	}
	if err := viper.BindPFlag(FlagWorkerMaxWorkflowExecution, flags.Lookup(FlagWorkerMaxWorkflowExecution)); err != nil {
		return err
	}
	return viper.BindPFlag(FlagWorkerMaxActivityExecution, flags.Lookup(FlagWorkerMaxActivityExecution))
}

func InitSkipPreseasonFlag(flags *flag.FlagSet) {
	flags.Bool(FlagSkipPreseason, false, "Skip downloading and importing preseason games")
}

func BindSkipPreseasonFlag(flags *flag.FlagSet) error {
	return viper.BindPFlag(FlagSkipPreseason, flags.Lookup(FlagSkipPreseason))
}

func InitAPIPortFlag(flags *flag.FlagSet) {
	flags.IntP(FlagAPIPort, "p", DefaultAPIPort, "API server port")
}

func InitAPITLSEnabledFlag(flags *flag.FlagSet) {
	flags.Bool(FlagAPITLSEnabled, false, "Enable TLS Mode")
}

func InitMetricsPortFlag(flags *flag.FlagSet) {
	flags.IntP(FlagMetricsPort, "", DefaultMetricsPort, "Metrics server port")
}

func InitMetricsTLSEnabledFlag(flags *flag.FlagSet) {
	flags.Bool(FlagMetricsTLSEnabled, false, "Enable TLS Mode")
}

func InitMetricsRefreshIntervalFlag(flags *flag.FlagSet) {
	flags.Int(FlagMetricsRefreshInterval, DefaultMetricsRefreshInterval, "Metrics refresh interval in seconds")
}

func InitTLSCertificate(flags *flag.FlagSet) {
	flags.String(FlagTLSCertificate, "", "TLS Certificates")
}

func InitTLSKey(flags *flag.FlagSet) {
	flags.String(FlagTLSKey, "", "TLS Key")
}

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

// CLI client flags
func InitAPIServerAddrFlag(flags *flag.FlagSet) {
	flags.StringP(FlagAPIServerAddr, "A", DefaultAPIServerAddr, "API server address (e.g., http://localhost:8080)")
}

func BindAPIServerAddrFlag(flags *flag.FlagSet) error {
	return viper.BindPFlag(FlagAPIServerAddr, flags.Lookup(FlagAPIServerAddr))
}

func InitMonitorFlag(flags *flag.FlagSet) {
	flags.Bool(FlagMonitor, false, "Skip triggering workflow, only monitor existing workflow")
}

func BindMonitorFlag(flags *flag.FlagSet) error {
	return viper.BindPFlag(FlagMonitor, flags.Lookup(FlagMonitor))
}

func InitSkipPlayersFlag(flags *flag.FlagSet) {
	flags.Bool(FlagSkipDownloadPlayers, false, "Skip downloading players")
}

func BindSkipPlayersFlag(flags *flag.FlagSet) error {
	return viper.BindPFlag(FlagSkipDownloadPlayers, flags.Lookup(FlagSkipDownloadPlayers))
}

func InitSkipImportPlayersFlag(flags *flag.FlagSet) {
	flags.Bool(FlagSkipImportPlayers, false, "Skip importing players to database")
}

func BindSkipImportPlayersFlag(flags *flag.FlagSet) error {
	return viper.BindPFlag(FlagSkipImportPlayers, flags.Lookup(FlagSkipImportPlayers))
}

func InitSkipYahooPlayersFlag(flags *flag.FlagSet) {
	flags.Bool(FlagSkipYahooPlayers, false, "Skip downloading Yahoo! players")
}

func BindSkipYahooPlayersFlag(flags *flag.FlagSet) error {
	return viper.BindPFlag(FlagSkipYahooPlayers, flags.Lookup(FlagSkipYahooPlayers))
}

func InitSkipSeasonsFlag(flags *flag.FlagSet) {
	flags.Bool(FlagSkipSeasons, false, "Skip downloading season data (NHL schedules, boxscores, Yahoo! fantasy)")
}

func BindSkipSeasonsFlag(flags *flag.FlagSet) error {
	return viper.BindPFlag(FlagSkipSeasons, flags.Lookup(FlagSkipSeasons))
}

func InitSkipInitializingFlag(flags *flag.FlagSet) {
	flags.Bool(FlagSkipInitializing, false, "Skip the initialization workflow (franchises, seasons, league structure)")
}

func BindSkipInitializingFlag(flags *flag.FlagSet) error {
	return viper.BindPFlag(FlagSkipInitializing, flags.Lookup(FlagSkipInitializing))
}

func InitSeasonConcurrencyFlag(flags *flag.FlagSet) {
	flags.Int(FlagSeasonConcurrency, 0, "Number of seasons to process concurrently (0 uses server default)")
}

func BindSeasonConcurrencyFlag(flags *flag.FlagSet) error {
	return viper.BindPFlag(FlagSeasonConcurrency, flags.Lookup(FlagSeasonConcurrency))
}

// Metrics collection interval flags
func InitCacheIntervalSecondsFlag(flags *flag.FlagSet) {
	flags.Int(FlagCacheIntervalSeconds, DefaultCacheIntervalSeconds, "Interval in seconds for cache metrics collection")
}

func BindCacheIntervalSecondsFlag(flags *flag.FlagSet) error {
	return viper.BindPFlag(FlagCacheIntervalSeconds, flags.Lookup(FlagCacheIntervalSeconds))
}

func InitRedisIntervalSecondsFlag(flags *flag.FlagSet) {
	flags.Int(FlagRedisIntervalSeconds, DefaultRedisIntervalSeconds, "Interval in seconds for Redis metrics collection")
}

func BindRedisIntervalSecondsFlag(flags *flag.FlagSet) error {
	return viper.BindPFlag(FlagRedisIntervalSeconds, flags.Lookup(FlagRedisIntervalSeconds))
}

func InitDBIntervalSecondsFlag(flags *flag.FlagSet) {
	flags.Int(FlagDBIntervalSeconds, DefaultDBIntervalSeconds, "Interval in seconds for database metrics collection")
}

func BindDBIntervalSecondsFlag(flags *flag.FlagSet) error {
	return viper.BindPFlag(FlagDBIntervalSeconds, flags.Lookup(FlagDBIntervalSeconds))
}

// CLI display flags
func InitVerboseFlag(flags *flag.FlagSet) {
	flags.BoolP(FlagVerbose, "v", false, "Show detailed output per season")
}

func BindVerboseFlag(flags *flag.FlagSet) error {
	return viper.BindPFlag(FlagVerbose, flags.Lookup(FlagVerbose))
}

func InitIncompleteFlag(flags *flag.FlagSet) {
	flags.BoolP(FlagIncomplete, "i", false, "Only show seasons with less than 100% completion")
}

func BindIncompleteFlag(flags *flag.FlagSet) error {
	return viper.BindPFlag(FlagIncomplete, flags.Lookup(FlagIncomplete))
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
	FlagProvisionerPassword:     true,
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
