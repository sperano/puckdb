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

// Yahoo access check flags
const (
	// FlagYahooCheckSeason is the season (start year) whose Yahoo API access
	// `yahoo check-access` probes first, alongside the season before it; 0
	// picks the latest season in the Yahoo seasons config.
	FlagYahooCheckSeason = "yahoo-check-season"
)

// Notification email flags (SMTP submission)
const (
	FlagSMTPHost     = "smtp-host"
	FlagSMTPPort     = "smtp-port"
	FlagSMTPUsername = "smtp-username"
	FlagSMTPPassword = "smtp-password"
	FlagSMTPFrom     = "smtp-from"
	// FlagNotifyEmailTo is a comma-separated list of recipients. Empty
	// disables the email; the report is still printed.
	FlagNotifyEmailTo = "notify-email-to"
)

// Yahoo player download flags
const (
	FlagMaxYahooPlayerID             = "max-yahoo-player-id"
	FlagYahooPlayerBatchSize         = "yahoo-player-batch-size"
	FlagYahooPlayerActivityBatchSize = "yahoo-player-activity-batch-size"
	FlagYahooPlayersPerExecution     = "yahoo-players-per-execution"
	FlagYahooPlayerPoolMaxAge        = "yahoo-player-pool-max-age"
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
	FlagMauriceModel         = "maurice-model"
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

// YahooAccessCheckFlags defines the `yahoo check-access` flags.
var YahooAccessCheckFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagYahooCheckSeason, "", DefaultYahooCheckSeason, "Season (start year) to check first, with the season before it as a control; 0 = latest season in the Yahoo seasons config", false},
	},
}

// NotifyEmailFlags defines the SMTP submission flags for notification emails.
var NotifyEmailFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagSMTPHost, "", "", "SMTP submission host for notification emails", false},
		{FlagSMTPPort, "", DefaultSMTPPort, "SMTP submission port (STARTTLS is used when the server offers it)", false},
		{FlagSMTPUsername, "", "", "SMTP username (empty sends without authentication)", false},
		{FlagSMTPPassword, "", "", "SMTP password", true},
		{FlagSMTPFrom, "", "", "Sender address of notification emails", false},
		{FlagNotifyEmailTo, "", "", "Comma-separated recipients of notification emails (empty disables the email)", false},
	},
}

// YahooPlayerFlags defines Yahoo player download flags.
var YahooPlayerFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagMaxYahooPlayerID, "", DefaultMaxYahooPlayerID, "Maximum Yahoo player ID to scan when importing players", false},
		{FlagYahooPlayerBatchSize, "", DefaultYahooPlayerBatchSize, "Number of concurrent activities for downloading Yahoo players", false},
		{FlagYahooPlayerActivityBatchSize, "", DefaultYahooPlayerActivityBatchSize, "Number of players to process per activity", false},
		{FlagYahooPlayersPerExecution, "", DefaultYahooPlayersPerExecution, "Players to process per workflow execution before ContinueAsNew", false},
		{FlagYahooPlayerPoolMaxAge, "", DefaultYahooPlayerPoolMaxAge, "Hours before a league's Yahoo player pool (eligibility and status) is downloaded again", false},
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
		{FlagMauriceModel, "", DefaultMauriceModel, "Maurice LLM model ID (must be a registered Maurice model)", false},
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
		{FlagNewsForce, "", DefaultNewsForce, "refresh-news: fetch every news source now, ignoring refresh schedules (e.g. right before a draft)", false},
		{FlagNewsOnly, "", DefaultNewsOnly, "refresh-news: comma-separated news source IDs to refresh (default: every enabled source)", false},
		{FlagDraftLeagues, "", DefaultDraftLeagues, "refresh-draft-rankings: comma-separated Yahoo league IDs (default: the season's leagues in seasons.yaml)", false},
		{FlagDraftBenchPolicy, "", DefaultDraftBenchPolicy, "refresh-draft-rankings: bench seats in draft demand, included or excluded", false},
		{FlagDraftWorkloadCaps, "", DefaultDraftWorkloadCaps, "refresh-draft-rankings: interpret max_games_played/max_goalie_starts as per-player caps (see docs/draft-ranking.md)", false},
		{FlagDraftUncertaintyPenalty, "", DefaultDraftUncertaintyPenalty, "refresh-draft-rankings: nonnegative uncertainty penalty on adjusted value (e.g. 0.5)", false},
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

// Draft helper report flags
const (
	FlagDraftSeason        = "draft-season"
	FlagDraftLeagues       = "draft-leagues"
	FlagDraftOutput        = "draft-output"
	FlagDraftStaleAfter    = "draft-stale-after"
	FlagDraftSessionLeague = "league"
	FlagDraftWatch         = "watch"
	FlagDraftPollInterval  = "draft-poll-interval"
	FlagDraftMaxBackoff    = "draft-max-backoff"
	FlagDraftFinalTimeout  = "draft-final-timeout"
	FlagDraftRound         = "round"
	FlagDraftPick          = "pick"
	FlagDraftTeamKey       = "team-key"
	FlagDraftPlayerKey     = "player-key"
	FlagDraftCost          = "cost"
	FlagDraftResolution    = "resolution"
)

// DraftFlags defines the flags of the `draft` report commands.
var DraftFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagDraftSeason, "", DefaultDraftSeason, "Season start year of the leagues (e.g. 2026); required", false},
		{FlagDraftLeagues, "", DefaultDraftLeagues, "Comma-separated Yahoo league IDs; required", false},
		{FlagDraftOutput, "", DefaultDraftOutput, "Write the report to this file instead of standard output", false},
		{FlagDraftStaleAfter, "", DefaultDraftStaleAfter, "Hours after which league settings or a player pool are reported stale", false},
	},
}

// DraftSessionFlags identifies a full-key live draft session.
var DraftSessionFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagDraftSessionLeague, "", DefaultDraftSessionLeague, "Full Yahoo league key or numeric league ID; required", false},
		{FlagDraftSeason, "", DefaultDraftSeason, "Season start year when resolving a numeric league ID", false},
	},
}

// DraftWatchFlags controls live polling. Duration values are seconds so they
// can be supplied through viper/env files.
var DraftWatchFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagDraftWatch, "", false, "Keep polling until canceled or Yahoo reports the draft complete", false},
		{FlagDraftPollInterval, "", DefaultDraftPollIntervalSeconds, "Seconds between successful draft polls", false},
		{FlagDraftMaxBackoff, "", DefaultDraftMaxBackoffSeconds, "Maximum seconds between failed or rate-limited polls", false},
		{FlagDraftFinalTimeout, "", DefaultDraftFinalTimeoutSeconds, "Seconds allowed for the final reconciliation after cancellation", false},
	},
}

// DraftManualSlotFlags identifies a slot edited by a manual operation.
var DraftManualSlotFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagDraftRound, "", DefaultDraftRound, "Draft round for a manual operation", false},
		{FlagDraftPick, "", DefaultDraftPick, "Overall pick number for a manual operation", false},
	},
}

// DraftManualPickFlags describes a locally recorded selection.
var DraftManualPickFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagDraftTeamKey, "", DefaultDraftTeamKey, "Yahoo team key for a manual pick", false},
		{FlagDraftPlayerKey, "", DefaultDraftPlayerKey, "Yahoo player key for a manual pick", false},
		{FlagDraftCost, "", DefaultDraftCost, "Auction cost for a manual pick", false},
	},
}

// DraftResolutionFlags selects the explicit side of a manual/Yahoo conflict.
var DraftResolutionFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagDraftResolution, "", DefaultDraftResolution, "Conflict resolution: keep-manual or accept-upstream", false},
	},
}

// Draft ranking flags
const (
	FlagDraftLeague        = "draft-league"
	FlagDraftPositions     = "draft-positions"
	FlagDraftPlayers       = "draft-players"
	FlagDraftFormat        = "draft-format"
	FlagDraftScenario      = "draft-scenario"
	FlagDraftSearch        = "draft-search"
	FlagDraftSort          = "draft-sort"
	FlagDraftDirection     = "draft-direction"
	FlagDraftOffset        = "draft-offset"
	FlagDraftLimit         = "draft-limit"
	FlagDraftSnapshot      = "draft-snapshot"
	FlagDraftKeepSnapshots = "draft-keep-snapshots"
	// Refresh settings of the refresh-draft-rankings sync step.
	FlagDraftBenchPolicy        = "draft-bench-policy"
	FlagDraftWorkloadCaps       = "draft-workload-caps"
	FlagDraftUncertaintyPenalty = "draft-uncertainty-penalty"
)

// DraftRankingsFlags defines the flags of `draft rankings`.
var DraftRankingsFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagDraftLeague, "", DefaultDraftLeague, "League key (e.g. 465.l.1001) or numeric league ID (with --draft-season); required", false},
		{FlagDraftSeason, "", DefaultDraftSeason, "Season start year of a numeric league ID (default: the current season)", false},
		{FlagDraftPositions, "", DefaultDraftPositions, "Comma-separated positions (C,LW,RW,D,G); a player matching any is listed once", false},
		{FlagDraftPlayers, "", DefaultDraftPlayers, "Comma-separated Yahoo player keys to compare", false},
		{FlagDraftFormat, "", DefaultDraftFormat, "Output format: table, csv or json", false},
		{FlagDraftScenario, "", DefaultDraftScenario, "Scenario: baseline, conservative, base or optimistic (default: base when news scenarios exist)", false},
		{FlagDraftSearch, "", DefaultDraftSearch, "Words that must all appear in the player's name, team or key", false},
		{FlagDraftSort, "", DefaultDraftSort, "Sort: overall_rank, position_rank, name, team, score, value, adjusted_value, uncertainty, tier, baseline_rank, rank_change", false},
		{FlagDraftDirection, "", DefaultDraftDirection, "Sort direction: asc or desc (default: the field's natural direction)", false},
		{FlagDraftOffset, "", DefaultDraftOffset, "Rows to skip", false},
		{FlagDraftLimit, "", DefaultDraftLimit, "Rows to return (0: every matching player)", false},
		{FlagDraftSnapshot, "", DefaultDraftSnapshot, "Snapshot ID to read instead of the league's latest", false},
		{FlagDraftOutput, "", DefaultDraftOutput, "Write the rankings to this file instead of standard output", false},
		{FlagDraftStaleAfter, "", DefaultDraftStaleAfter, "Hours after which a snapshot or player pool is reported stale", false},
	},
}

// DraftAPIFlags defines the API's draft ranking settings.
var DraftAPIFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagDraftStaleAfter, "", DefaultDraftStaleAfter, "Hours after which a ranking snapshot or player pool is reported stale", false},
	},
}

// DraftWorkerFlags defines the worker's draft ranking settings.
var DraftWorkerFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagDraftKeepSnapshots, "", DefaultDraftKeepSnapshots, "Ranking snapshots kept per league (older ones are deleted after a successful refresh)", false},
	},
}

// Player news flags
const (
	FlagNewsSourcesFile         = "news-sources-file"
	FlagNewsProcessBatchSize    = "news-process-batch-size"
	FlagNewsIncidentWindowHours = "news-incident-window-hours"
	FlagNewsRetentionDays       = "news-retention-days"
	FlagNewsKeepVersions        = "news-keep-versions"
	FlagNewsFetchMaxAttempts    = "news-fetch-max-attempts"
	FlagNewsFetchRetryInitial   = "news-fetch-retry-initial"
	FlagNewsFetchRetryMax       = "news-fetch-retry-max"
	FlagNewsScheduleMinutes     = "news-schedule-minutes"
	FlagNewsForce               = "news-force"
	FlagNewsOnly                = "news-only"
	FlagNewsSeason              = "news-season"
	FlagNewsSinceDays           = "news-since-days"
	FlagNewsLimit               = "news-limit"
	FlagNewsPlayerNHLID         = "news-player-nhl-id"
	FlagNewsPlayerYahooID       = "news-player-yahoo-id"
	FlagNewsOutput              = "news-output"
)

// Player news event extraction flags
const (
	FlagNewsExtractEnabled         = "news-extract-enabled"
	FlagNewsExtractProvider        = "news-extract-provider"
	FlagNewsExtractModel           = "news-extract-model"
	FlagNewsExtractReasoningEffort = "news-extract-reasoning-effort"
	FlagNewsExtractMaxOutputTokens = "news-extract-max-output-tokens"
	FlagNewsExtractMaxInputChars   = "news-extract-max-input-chars"
	FlagNewsExtractBatchSize       = "news-extract-batch-size"
	FlagNewsExtractConcurrency     = "news-extract-concurrency"
	FlagNewsExtractMaxCalls        = "news-extract-max-calls"
	FlagNewsExtractMaxTokens       = "news-extract-max-tokens"
	FlagNewsExtractMaxAttempts     = "news-extract-max-attempts"
	FlagNewsExtractRetryMinutes    = "news-extract-retry-minutes"
	FlagNewsExtractTimeoutSeconds  = "news-extract-timeout-seconds"
	FlagNewsExtractLookbackDays    = "news-extract-lookback-days"
	FlagNewsEvalCorpus             = "news-eval-corpus"
)

// NewsSourcesFlags selects the news source set (worker and report).
var NewsSourcesFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagNewsSourcesFile, "", DefaultNewsSourcesFile, "YAML file of player news sources (default: the built-in set, see docs/draft-player-news.md)", false},
	},
}

// NewsWorkerFlags defines the worker's player news ingestion settings.
var NewsWorkerFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagNewsProcessBatchSize, "", DefaultNewsProcessBatchSize, "Article versions processed per news activity", false},
		{FlagNewsIncidentWindowHours, "", DefaultNewsIncidentWindowHours, "Hours within which same-category reports for a player join one incident", false},
		{FlagNewsRetentionDays, "", DefaultNewsRetentionDays, "Days news articles and incidents are kept after they were last seen or reported", false},
		{FlagNewsKeepVersions, "", DefaultNewsKeepVersions, "Versions kept per news article (versions behind an incident are always kept)", false},
		{FlagNewsFetchMaxAttempts, "", DefaultNewsFetchMaxAttempts, "Attempts per news source fetch before it is recorded as failed", false},
		{FlagNewsFetchRetryInitial, "", DefaultNewsFetchRetryInitial, "Seconds before the first news fetch retry (doubles each attempt)", false},
		{FlagNewsFetchRetryMax, "", DefaultNewsFetchRetryMax, "Maximum seconds between news fetch retries", false},
		{FlagNewsScheduleMinutes, "", DefaultNewsScheduleMinutes, "Start the news refresh every N minutes via a Temporal schedule (0 removes the schedule)", false},
	},
}

// NewsExtractModelFlags selects the model that extracts news events and
// bounds each call (worker and `news eval`).
var NewsExtractModelFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagNewsExtractProvider, "", DefaultNewsExtractProvider, "LLM provider of news event extraction: anthropic, openai or ollama", false},
		{FlagNewsExtractModel, "", DefaultNewsExtractModel, "LLM model of news event extraction", false},
		{FlagNewsExtractReasoningEffort, "", DefaultNewsExtractReasoningEffort, "Reasoning effort of news event extraction for openai and ollama: none, minimal, low, medium or high (none turns off a thinking model's reasoning; empty keeps the provider default; ignored by anthropic)", false},
		{FlagNewsExtractMaxOutputTokens, "", DefaultNewsExtractMaxOutputTokens, "Maximum output tokens per news extraction call", false},
		{FlagNewsExtractMaxInputChars, "", DefaultNewsExtractMaxInputChars, "Maximum article characters sent per news extraction call", false},
		{FlagNewsExtractTimeoutSeconds, "", DefaultNewsExtractTimeoutSeconds, "Seconds before a news extraction call times out", false},
	},
}

// NewsExtractWorkerFlags turns on news event extraction in the refresh and
// bounds its cost.
var NewsExtractWorkerFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagNewsExtractEnabled, "", DefaultNewsExtractEnabled, "Extract validated news events with the LLM after each news refresh", false},
		{FlagNewsExtractBatchSize, "", DefaultNewsExtractBatchSize, "Article versions per news extraction activity", false},
		{FlagNewsExtractConcurrency, "", DefaultNewsExtractConcurrency, "Concurrent LLM calls per news extraction activity", false},
		{FlagNewsExtractMaxCalls, "", DefaultNewsExtractMaxCalls, "Maximum LLM calls per news refresh", false},
		{FlagNewsExtractMaxTokens, "", DefaultNewsExtractMaxTokens, "Maximum LLM tokens (prompt plus completion) per news refresh", false},
		{FlagNewsExtractMaxAttempts, "", DefaultNewsExtractMaxAttempts, "Attempts per article version before a failing extraction is left for review", false},
		{FlagNewsExtractRetryMinutes, "", DefaultNewsExtractRetryMinutes, "Minutes before a failed news extraction is retried", false},
		{FlagNewsExtractLookbackDays, "", DefaultNewsExtractLookbackDays, "Extract only versions behind incidents reported in the last N days", false},
	},
}

// NewsEvalFlags defines the flags of the `news eval` command.
var NewsEvalFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagNewsEvalCorpus, "", DefaultNewsEvalCorpus, "Labeled evaluation corpus YAML (default: the built-in corpus)", false},
		{FlagNewsIncidentWindowHours, "", DefaultNewsIncidentWindowHours, "Hours within which reports of one event are reconciled together", false},
		{FlagNewsOutput, "", DefaultNewsOutput, "Write the report to this file instead of standard output", false},
	},
}

// NewsEventsReportFlags defines the flags of the `news events` command.
var NewsEventsReportFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagNewsSinceDays, "", DefaultNewsSinceDays, "Show events and extraction problems of the last N days", false},
		{FlagNewsLimit, "", DefaultNewsLimit, "Maximum events and extraction problems listed", false},
		{FlagNewsPlayerNHLID, "", DefaultNewsPlayerNHLID, "Also list every event of this NHL player ID", false},
		{FlagNewsPlayerYahooID, "", DefaultNewsPlayerYahooID, "Also list every event of this Yahoo player ID", false},
		{FlagNewsExtractMaxAttempts, "", DefaultNewsExtractMaxAttempts, "Attempts after which a failing extraction is listed for review", false},
		{FlagNewsOutput, "", DefaultNewsOutput, "Write the report to this file instead of standard output", false},
	},
}

// NewsReportFlags defines the flags of the `news report` command.
var NewsReportFlags = FlagGroup{
	Flags: []FlagDef{
		{FlagNewsSeason, "", DefaultNewsSeason, "Season start year whose Yahoo status coverage is shown (default: the current season)", false},
		{FlagNewsSinceDays, "", DefaultNewsSinceDays, "Show incidents reported in the last N days", false},
		{FlagNewsLimit, "", DefaultNewsLimit, "Maximum incidents and unattached subjects listed", false},
		{FlagNewsPlayerNHLID, "", DefaultNewsPlayerNHLID, "Also report on this NHL player ID", false},
		{FlagNewsPlayerYahooID, "", DefaultNewsPlayerYahooID, "Also report on this Yahoo player ID", false},
		{FlagNewsOutput, "", DefaultNewsOutput, "Write the report to this file instead of standard output", false},
	},
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
	&NotifyEmailFlags,
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
