package config

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/rs/zerolog/log"
	flag "github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/subosito/gotenv"
)

// FlagDef defines a single CLI flag.
type FlagDef struct {
	Name      string
	Short     string // optional single-char shorthand
	Default   any    // string, int, or bool
	Usage     string
	Sensitive bool // if true, value is redacted in logs
}

// FlagGroup is a collection of related flags.
type FlagGroup struct {
	Flags []FlagDef
}

// Init registers all flags in the group with the given FlagSet.
func (g *FlagGroup) Init(flags *flag.FlagSet) {
	for _, f := range g.Flags {
		switch v := f.Default.(type) {
		case string:
			if f.Short != "" {
				flags.StringP(f.Name, f.Short, v, f.Usage)
			} else {
				flags.String(f.Name, v, f.Usage)
			}
		case int:
			if f.Short != "" {
				flags.IntP(f.Name, f.Short, v, f.Usage)
			} else {
				flags.Int(f.Name, v, f.Usage)
			}
		case bool:
			if f.Short != "" {
				flags.BoolP(f.Name, f.Short, v, f.Usage)
			} else {
				flags.Bool(f.Name, v, f.Usage)
			}
		}
	}
}

// Bind binds all flags in the group to viper.
func (g *FlagGroup) Bind(flags *flag.FlagSet) error {
	for _, f := range g.Flags {
		if err := viper.BindPFlag(f.Name, flags.Lookup(f.Name)); err != nil {
			return err
		}
	}
	return nil
}

// sensitiveFlags returns a set of flag names that should be redacted.
func (g *FlagGroup) sensitiveFlags() map[string]bool {
	result := make(map[string]bool)
	for _, f := range g.Flags {
		if f.Sensitive {
			result[f.Name] = true
		}
	}
	return result
}

// BindFlags binds multiple flag groups to viper, returning first error.
func BindFlags(flags *flag.FlagSet, groups ...*FlagGroup) error {
	for _, g := range groups {
		if err := g.Bind(flags); err != nil {
			return err
		}
	}
	return nil
}

// InitFlags registers multiple flag groups with the FlagSet.
func InitFlags(flags *flag.FlagSet, groups ...*FlagGroup) {
	for _, g := range groups {
		g.Init(flags)
	}
}

// Flag name constants - used by viper.Get* calls throughout the codebase

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
	FlagWorkflowExecutionTimeout     = "workflow-execution-timeout"
	FlagActivityStartToCloseTimeout  = "activity-start-to-close-timeout"
	FlagFetchDayActivityTimeout      = "fetch-day-activity-timeout"
	FlagActivityHeartbeatTimeout     = "activity-heartbeat-timeout"
)

// Worker concurrency flags
const (
	FlagWorkerQueue                = "worker-queue"
	FlagWorkerMaxWorkflowPollers   = "worker-max-workflow-pollers"
	FlagWorkerMaxActivityPollers   = "worker-max-activity-pollers"
	FlagWorkerMaxWorkflowExecution = "worker-max-workflow-execution"
	FlagWorkerMaxActivityExecution = "worker-max-activity-execution"
)

// Yahoo OAuth2 flags
const (
	FlagYahooOAuth2ClientID     = "yahoo-oauth2-client-id"
	FlagYahooOAuth2ClientSecret = "yahoo-oauth2-client-secret"
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
	FlagMaxPlayerLandingConcurrency = "max-player-landing-concurrency"
	FlagPlayerLandingBatchSize      = "player-landing-batch-size"
	FlagPlayerLandingPlayersPerExec = "player-landing-players-per-exec"
)

// Asset download flags
const (
	// FlagAssetClassConcurrency controls how many asset batches run concurrently
	// within a single asset class child workflow (analogous to FlagPlayerLandingConcurrency).
	FlagAssetClassConcurrency = "asset-class-concurrency"

	// FlagMaxAssetClassConcurrency controls how many asset class child workflows
	// the parent FetchAssetsWorkflow runs concurrently (Phase 4, analogous to
	// FlagMaxPlayerLandingConcurrency).
	FlagMaxAssetClassConcurrency = "max-asset-class-concurrency"

	// FlagAssetBatchSize controls how many assets are processed per
	// FetchAssetBatch activity invocation.
	FlagAssetBatchSize = "asset-batch-size"
)

// Process players workflow flags
const (
	FlagProcessPlayersConcurrency = "process-players-concurrency"
	FlagProcessPlayersBatchSize   = "process-players-batch-size"
)

// Player logs workflow flags
const (
	FlagPlayerLogsBatchSize        = "player-logs-batch-size"
	FlagPlayerLogsBatchConcurrency = "player-logs-batch-concurrency"
)

// Download workflow flags
const (
	FlagGameDownloadConcurrency  = "game-download-concurrency"
	FlagMaxSeasonConcurrency     = "max-season-concurrency"
	FlagDayConcurrency           = "day-concurrency"
	FlagSkipPreseason            = "skip-preseason"
	FlagRefreshCurrentPlayerLogs = "refresh-current-player-logs"
	FlagRefreshCurrentEdge       = "refresh-current-edge"
	FlagSeasonConcurrency        = "season-concurrency"
	FlagMonitor                  = "monitor"
	FlagSeasonYear               = "season"
	FlagFromSeasonYear           = "from-season"
	FlagToSeasonYear             = "to-season"
)

// Cache flags
const (
	FlagGobCacheTTL            = "gob-cache-ttl"
	FlagGobCacheConfig         = "gob-cache-config"
	FlagBoxscorePlayerCacheTTL = "boxscore-player-cache-ttl"
)

// Metrics collection flags
const (
	FlagMetricsRefreshInterval = "metrics-refresh-interval"
	FlagCacheIntervalSeconds   = "cache-interval-seconds"
	FlagRedisIntervalSeconds   = "redis-interval-seconds"
	FlagDBIntervalSeconds      = "db-interval-seconds"
)

// MCP server flags
const (
	FlagMCPPort  = "mcp-port"
	FlagMCPStdio = "mcp-stdio"
)

// CLI display flags
const (
	FlagVerbose    = "verbose"
	FlagIncomplete = "incomplete"
	FlagTheme      = "theme"
)

// Maurice AI chat flags
const (
	FlagMauriceConfig        = "maurice-config"
	FlagMauriceMaxTokens     = "maurice-max-tokens"
	FlagMauriceMaxHistory    = "maurice-max-history"
	FlagMauriceMaxToolRounds = "maurice-max-tool-rounds"
	FlagOllamaBaseURL        = "ollama-base-url"
	FlagAnthropicAPIKey      = "anthropic-api-key"
	FlagOpenAIAPIKey         = "openai-api-key"
)

// Provisioner flags
const (
	FlagProvisionerHost     = "provisioner-host"
	FlagProvisionerUser     = "provisioner-user"
	FlagProvisionerPassword = "provisioner-password"
)

// Admin authorization flags
const (
	// FlagAdminGroup is the Authentik group (from the X-authentik-groups
	// header) whose members are granted access to @admin mutations.
	FlagAdminGroup = "admin-group"

	// FlagAdminToken is a shared secret accepted via the X-Admin-Token
	// header as an alternative to group-based admin authorization. Empty
	// disables the token path entirely.
	FlagAdminToken = "admin-token"
)

// API client basic-auth flags. CLI commands that call the GraphQL API
// through the authentik-gated public host authenticate with an authentik
// app password over HTTP Basic (the proxy outpost intercepts the
// Authorization header). Not needed for in-cluster or port-forwarded access.
const (
	// FlagAPIUser is the HTTP Basic auth username (authentik username).
	FlagAPIUser = "api-user"

	// FlagAPIPassword is the HTTP Basic auth password (an authentik app
	// password). Empty disables basic auth entirely.
	FlagAPIPassword = "api-password"
)

// Flag groups - related flags grouped together

// PostgresFlags defines all PostgreSQL connection flags.
var PostgresFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagPostgresHost, "", DefaultPostgresHost, "Postgres host", false},
		{FlagPostgresUser, "", DefaultPostgresUser, "Postgres user", false},
		{FlagPostgresPassword, "", "", "Postgres password", true},
		{FlagPostgresDatabase, "", DefaultPostgresDatabase, "Postgres database", false},
		{FlagPostgresPort, "", DefaultPostgresPort, "Postgres port", false},
		{FlagPostgresSSLMode, "", DefaultPostgresSSLMode, "Postgres SSL mode", false},
		{FlagPostgresTimeZone, "", DefaultPostgresTimeZone, "Postgres time zone", false},
		{FlagPostgresMaxIdleConns, "", DefaultPostgresMaxIdleConns, "Maximum idle database connections", false},
		{FlagPostgresMaxOpenConns, "", DefaultPostgresMaxOpenConns, "Maximum open database connections", false},
	},
}

// RedisFlags defines all Redis connection flags.
var RedisFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagRedisURL, "", DefaultRedisURL, "Redis url", false},
		{FlagRedisPassword, "", "", "Redis password", true},
		{FlagRedisDB, "", DefaultRedisDB, "Redis db", false},
	},
}

// TemporalFlags defines Temporal connection flags.
var TemporalFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagTemporalHostPort, "", DefaultTemporalHostPort, "Temporal host/port", false},
		{FlagTemporalNamespace, "", DefaultTemporalNamespace, "Temporal namespace", false},
	},
}

// TemporalRetryFlags defines Temporal retry policy and timeout flags.
var TemporalRetryFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagTemporalRetryInitialInterval, "", DefaultTemporalRetryInitialInterval, "Initial interval in seconds between activity retries", false},
		{FlagTemporalRetryMaxInterval, "", DefaultTemporalRetryMaxInterval, "Maximum interval in seconds between activity retries (for rate limit recovery)", false},
		{FlagTemporalRetryMaxAttempts, "", DefaultTemporalRetryMaxAttempts, "Maximum number of activity retry attempts (0 for unlimited)", false},
		{FlagWorkflowExecutionTimeout, "", DefaultWorkflowExecutionTimeout, "Workflow execution timeout in minutes", false},
		{FlagActivityStartToCloseTimeout, "", DefaultActivityStartToCloseTimeout, "Activity start-to-close timeout in minutes", false},
		{FlagFetchDayActivityTimeout, "", DefaultFetchDayActivityTimeout, "Fetch day activity timeout in minutes", false},
		{FlagActivityHeartbeatTimeout, "", DefaultActivityHeartbeatTimeout, "Activity heartbeat timeout in seconds", false},
	},
}

// YahooOAuth2Flags defines Yahoo OAuth2 configuration flags.
var YahooOAuth2Flags = FlagGroup{
	Flags: []FlagDef{
		{FlagYahooOAuth2ClientID, "", "", "Yahoo! OAuth2 Client ID", false},
		{FlagYahooOAuth2ClientSecret, "", "", "Yahoo! OAuth2 Client Secret", true},
		{FlagPublicURL, "", "", "Public URL for OAuth callbacks (e.g., https://localhost:8787 or http://api.example.com)", false},
	},
}

// YahooSeasonsFlags defines the Yahoo seasons configuration file flag.
var YahooSeasonsFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagYahooSeasons, "S", DefaultYahooSeasonsFile, "Yahoo seasons config file", false},
	},
}

// YahooPlayerFlags defines Yahoo player download flags.
var YahooPlayerFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagMaxYahooPlayerID, "", DefaultMaxYahooPlayerID, "Maximum Yahoo player ID to scan when importing players", false},
		{FlagYahooPlayerBatchSize, "", DefaultYahooPlayerBatchSize, "Number of concurrent activities for downloading Yahoo players", false},
		{FlagYahooPlayerActivityBatchSize, "", DefaultYahooPlayerActivityBatchSize, "Number of players to process per activity", false},
		{FlagYahooPlayersPerExecution, "", DefaultYahooPlayersPerExecution, "Players to process per workflow execution before ContinueAsNew", false},
	},
}

// YahooDownloadSleepFlags defines Yahoo download rate limiting flags.
var YahooDownloadSleepFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagYahooDownloadSleepMin, "", DefaultYahooDownloadSleepMin, "Minimum seconds to sleep after each Yahoo API download", false},
		{FlagYahooDownloadSleepMax, "", DefaultYahooDownloadSleepMax, "Maximum seconds to sleep after each Yahoo API download", false},
	},
}

// PlayerLandingFlags defines NHL player landing download flags.
var PlayerLandingFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagPlayerLandingConcurrency, "", DefaultPlayerLandingConcurrency, "Number of concurrent activities for downloading NHL player landings", false},
		{FlagMaxPlayerLandingConcurrency, "", DefaultMaxPlayerLandingConcurrency, "Maximum concurrent activities for player landing downloads", false},
		{FlagPlayerLandingBatchSize, "", DefaultPlayerLandingBatchSize, "Number of players to download per activity", false},
		{FlagPlayerLandingPlayersPerExec, "", DefaultPlayerLandingPlayersPerExec, "Players to process per workflow execution before ContinueAsNew", false},
	},
}

// AssetFlags defines asset download workflow flags.
var AssetFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagAssetClassConcurrency, "", DefaultAssetClassConcurrency, "Number of concurrent asset batch activities within each class child workflow", false},
		{FlagMaxAssetClassConcurrency, "", DefaultMaxAssetClassConcurrency, "Maximum concurrent asset class child workflows in the parent fetch-assets workflow", false},
		{FlagAssetBatchSize, "", DefaultAssetBatchSize, "Number of assets processed per FetchAssetBatch activity", false},
	},
}

// ProcessPlayersFlags defines process players workflow flags.
var ProcessPlayersFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagProcessPlayersConcurrency, "", DefaultProcessPlayersConcurrency, "Number of concurrent batch activities for processing players", false},
		{FlagProcessPlayersBatchSize, "", DefaultProcessPlayersBatchSize, "Number of players per batch activity", false},
	},
}

// PlayerLogsFlags defines player logs workflow flags.
var PlayerLogsFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagPlayerLogsBatchSize, "", DefaultPlayerLogsBatchSize, "Number of players per batch when downloading game logs", false},
		{FlagPlayerLogsBatchConcurrency, "", DefaultPlayerLogsBatchConcurrency, "Number of concurrent batches for player logs download", false},
	},
}

// WorkerConcurrencyFlags defines Temporal worker concurrency flags.
var WorkerConcurrencyFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagWorkerQueue, "", DefaultWorkerQueue, "Task queue to poll: tasks, admin", false},
		{FlagWorkerMaxWorkflowPollers, "", DefaultWorkerMaxWorkflowPollers, "Max concurrent workflow task pollers", false},
		{FlagWorkerMaxActivityPollers, "", DefaultWorkerMaxActivityPollers, "Max concurrent activity task pollers", false},
		{FlagWorkerMaxWorkflowExecution, "", DefaultWorkerMaxWorkflowExecution, "Max concurrent workflow task executions", false},
		{FlagWorkerMaxActivityExecution, "", DefaultWorkerMaxActivityExecution, "Max concurrent activity executions", false},
	},
}

// MauriceFlags defines Maurice AI chat flags.
var MauriceFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagMauriceConfig, "", DefaultMauriceConfig, "Path to Maurice config file (MCP servers, etc.)", false},
		{FlagMauriceMaxTokens, "", DefaultMauriceMaxTokens, "Maurice max tokens per completion", false},
		{FlagMauriceMaxHistory, "", DefaultMauriceMaxHistory, "Maurice max conversation history messages", false},
		{FlagMauriceMaxToolRounds, "", DefaultMauriceMaxToolRounds, "Maurice max tool call rounds per message", false},
		{FlagOllamaBaseURL, "", DefaultOllamaBaseURL, "Ollama base URL", false},
		{FlagAnthropicAPIKey, "", "", "Anthropic API key", true},
		{FlagOpenAIAPIKey, "", "", "OpenAI API key", true},
	},
}

// ProvisionerFlags defines database provisioner flags.
var ProvisionerFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagProvisionerHost, "", "", "PostgreSQL host for provisioner connection", false},
		{FlagProvisionerUser, "", "", "PostgreSQL user with CREATE DATABASE privileges", false},
		{FlagProvisionerPassword, "", "", "PostgreSQL provisioner password", true},
	},
}

// AdminAuthFlags defines admin authorization flags for the @admin GraphQL directive.
var AdminAuthFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagAdminGroup, "", DefaultAdminGroup, "Authentik group granting access to admin GraphQL mutations", false},
		{FlagAdminToken, "", "", "Shared secret token granting access to admin GraphQL mutations (empty disables the token path)", true},
	},
}

// APIBasicAuthFlags defines HTTP Basic auth flags for CLI commands calling
// the GraphQL API through the authentik-gated public host.
var APIBasicAuthFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagAPIUser, "", "", "HTTP Basic auth username for the GraphQL API (authentik app-password auth)", false},
		{FlagAPIPassword, "", "", "HTTP Basic auth password for the GraphQL API (empty disables basic auth)", true},
	},
}

// SeasonRangeFlags defines season selection flags.
var SeasonRangeFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagSeasonYear, "", 0, "Season year (e.g., 2024). Sets both from and to season.", false},
		{FlagFromSeasonYear, "", 0, "Start season year for range (e.g., 2020)", false},
		{FlagToSeasonYear, "", 0, "End season year for range (e.g., 2024)", false},
	},
}

// SyncBehaviorFlags defines non-step behavioral flags for sync.
var SyncBehaviorFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagRefreshCurrentPlayerLogs, "", false, "Re-download player game logs for the latest season, or for every season in an explicit --start-season/--end-season range (overwrites cached files)", false},
		{FlagRefreshCurrentEdge, "", false, "Re-download Edge stats for the latest season, or for every season in an explicit --start-season/--end-season range (overwrites cached files)", false},
	},
}

// MetricsIntervalFlags defines metrics collection interval flags.
var MetricsIntervalFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagCacheIntervalSeconds, "", DefaultCacheIntervalSeconds, "Interval in seconds for cache metrics collection", false},
		{FlagRedisIntervalSeconds, "", DefaultRedisIntervalSeconds, "Interval in seconds for Redis metrics collection", false},
		{FlagDBIntervalSeconds, "", DefaultDBIntervalSeconds, "Interval in seconds for database metrics collection", false},
	},
}

// MCPServerFlags defines the MCP server transport flags: the HTTP listen
// port and the switch to serve over stdio instead.
var MCPServerFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagMCPPort, "", DefaultMCPPort, "MCP server HTTP port", false},
		{FlagMCPStdio, "", false, "Serve over stdio instead of HTTP", false},
	},
}

// DisplayFlags defines CLI display option flags.
var DisplayFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagVerbose, "v", false, "Show detailed output per season", false},
		{FlagIncomplete, "i", false, "Only show seasons with less than 100% completion", false},
	},
}

// SpinnerFlags defines spinner display option flags.
var SpinnerFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagTheme, "", "", "Color theme for progress bars (blue, red, green, teal, purple, random, random-each)", false},
	},
}

// TLSFlags defines TLS certificate flags.
var TLSFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagTLSCertificate, "", "", "TLS Certificate", false},
		{FlagTLSKey, "", "", "TLS Key", true},
	},
}

// Single-flag groups for individual flags

// LoggingFlags defines logging configuration flags.
// Note: Use InitLoggingFlags for custom defaults per command.
var LoggingFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagLogLevel, "L", DefaultLogLevel, fmt.Sprintf("Log Level: %s", getLogLevelsStr()), false},
		{FlagLogFile, "", DefaultLogFile, fmt.Sprintf("Log file path (empty for stdout, 'default' for %s)", GetDefaultLogPath()), false},
	},
}

// APIServerAddrFlags defines API server address flag.
var APIServerAddrFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagAPIServerAddr, "A", DefaultAPIServerAddr, fmt.Sprintf("API server address (e.g., %s)", DefaultAPIServerAddr), false},
	},
}

// APIPortFlags defines API server port flags.
var APIPortFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagAPIPort, "p", DefaultAPIPort, "API server port", false},
		{FlagAPITLSEnabled, "", false, "Enable TLS Mode", false},
	},
}

// WorkerPortFlags defines worker server port flags.
var WorkerPortFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagWorkerPort, "", DefaultWorkerPort, "Worker metrics port", false},
		{FlagWorkerTLSEnabled, "", false, "Enable TLS Mode", false},
	},
}

// MetricsPortFlags defines metrics server port flags.
var MetricsPortFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagMetricsPort, "", DefaultMetricsPort, "Metrics server port", false},
		{FlagMetricsTLSEnabled, "", false, "Enable TLS Mode", false},
		{FlagMetricsRefreshInterval, "", DefaultMetricsRefreshInterval, "Metrics refresh interval in seconds", false},
	},
}

// DataPathFlags defines data path flag.
var DataPathFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagDataPath, "", "", "Path to local files data", false},
	},
}

// DownloadConcurrencyFlags defines download concurrency flags.
var DownloadConcurrencyFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagMaxSeasonConcurrency, "", DefaultMaxSeasonConcurrency, "Maximum number of seasons to download concurrently", false},
		{FlagDayConcurrency, "", DefaultDayConcurrency, "Number of days to download concurrently within each season", false},
		{FlagGameDownloadConcurrency, "", DefaultGameDownloadConcurrency, "Maximum concurrent game file downloads per daily schedule", false},
		{FlagSkipPreseason, "", false, "Skip downloading and importing preseason games", false},
	},
}

// GobCacheFlags defines gob cache TTL flags.
var GobCacheFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagGobCacheTTL, "", DefaultGobCacheTTL, "TTL in minutes for gob-encoded object cache in Redis (fallback when no config file)", false},
		{FlagGobCacheConfig, "", DefaultGobCacheConfig, "Path to gob cache config YAML (per-type TTL and skip_redis settings)", false},
		{FlagBoxscorePlayerCacheTTL, "", DefaultBoxscorePlayerCacheTTL, "TTL in minutes for boxscore player cache in Redis", false},
	},
}

// SeasonConcurrencyFlags defines season concurrency flag.
var SeasonConcurrencyFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagSeasonConcurrency, "", 0, "Number of seasons to process concurrently (0 uses server default)", false},
	},
}

// MonitorFlags defines workflow monitor flag.
var MonitorFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagMonitor, "", false, "Skip triggering workflow, only monitor existing workflow", false},
	},
}

// Special functions for flags that need custom initialization

// InitLoggingFlags initializes logging flags with custom defaults.
func InitLoggingFlags(flags *flag.FlagSet, defaultLevel, defaultFile string) {
	flags.StringP(FlagLogLevel, "L", defaultLevel, fmt.Sprintf("Log Level: %s", getLogLevelsStr()))
	flags.String(FlagLogFile, defaultFile, fmt.Sprintf("Log file path (empty for stdout, 'default' for %s)", GetDefaultLogPath()))
}

// BindLoggingFlags binds logging flags to viper.
func BindLoggingFlags(flags *flag.FlagSet) error {
	if err := viper.BindPFlag(FlagLogLevel, flags.Lookup(FlagLogLevel)); err != nil {
		return err
	}
	return viper.BindPFlag(FlagLogFile, flags.Lookup(FlagLogFile))
}

// GetSeasonRange returns start and end season years from flags.
// If --season is set, it returns that value for both.
// Otherwise returns --from-season and --to-season (0 means not set).
func GetSeasonRange() (start, end int) {
	season := viper.GetInt(FlagSeasonYear)
	if season != 0 {
		return season, season
	}
	return viper.GetInt(FlagFromSeasonYear), viper.GetInt(FlagToSeasonYear)
}

// EnvFileName is the dotenv file loaded from the current directory.
const EnvFileName = ".env"

// EnvVarPrefix is the prefix viper expects on real environment variables,
// matching SetEnvPrefix below.
const EnvVarPrefix = "PUCKDB_"

// SetupViper configures viper for environment variable and .env file support.
//
// The .env file must be loaded into the process environment rather than as a
// viper config file: viper applies the env prefix and the -/_ key replacer
// only to real environment variables, so a config-file key like ADMIN_TOKEN
// can never satisfy a lookup of the admin-token flag.
func SetupViper() {
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	viper.SetEnvPrefix("puckdb")
	viper.AutomaticEnv()
	loadEnvFile()
}

// loadEnvFile loads EnvFileName into the process environment. Keys are
// upper-cased and prefixed with EnvVarPrefix unless already prefixed.
// Variables already present in the environment are not overwritten, so real
// environment variables take precedence over .env values (and CLI flags
// override both via viper).
func loadEnvFile() {
	f, err := os.Open(EnvFileName)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Warn().Msgf("error reading env file: %s", err.Error())
		}
		return
	}
	defer f.Close()
	env, err := gotenv.StrictParse(f)
	if err != nil {
		log.Warn().Msgf("error parsing env file: %s", err.Error())
		return
	}
	for key, value := range env {
		name := strings.ToUpper(key)
		if !strings.HasPrefix(name, EnvVarPrefix) {
			name = EnvVarPrefix + name
		}
		if _, exists := os.LookupEnv(name); exists {
			continue
		}
		if err := os.Setenv(name, value); err != nil {
			log.Warn().Msgf("error setting %s from env file: %s", name, err.Error())
		}
	}
}

// allFlagGroups contains all flag groups for sensitive flag detection.
var allFlagGroups = []*FlagGroup{
	&PostgresFlags,
	&RedisFlags,
	&ProvisionerFlags,
	&YahooOAuth2Flags,
	&TLSFlags,
	&MauriceFlags,
	&AdminAuthFlags,
	&APIBasicAuthFlags,
}

// LogFlagValues logs all viper settings at debug level, redacting sensitive values.
func LogFlagValues() {
	settings := viper.AllSettings()
	if len(settings) == 0 {
		return
	}

	// Build set of sensitive flags from all groups
	sensitiveFlags := make(map[string]bool)
	for _, group := range allFlagGroups {
		for k, v := range group.sensitiveFlags() {
			sensitiveFlags[k] = v
		}
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
