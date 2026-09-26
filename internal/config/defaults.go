package config

import (
	"errors"
	"fmt"
	"time"
)

// Application defaults
const (
	DefaultTemporalNamespace = "puckdb"
	DefaultUser              = "eric"
	DefaultLogLevel          = LogLevelInfo
	DefaultImportLogLevel    = LogLevelInfo
	DefaultYahooSeasonsFile  = "yahoo-seasons.yaml"
)

// Log file defaults
const (
	DefaultLogFile       = ""        // empty = stdout/stderr
	DefaultImportLogFile = "default" // special value = use platform default path
	DefaultLogMaxSize    = 10        // MB before rotation
	DefaultLogMaxBackups = 3         // old files to keep
	DefaultLogMaxAge     = 30        // days to keep old files
	DefaultLogCompress   = false     // don't gzip old files
)

// Server port defaults
const (
	DefaultAPIHost     = "localhost"
	DefaultAPIPort     = 8787
	DefaultWorkerPort  = 8788
	DefaultMetricsPort = 8789
	DefaultMCPPort     = 8790
)

// Temporal defaults
const (
	DefaultTemporalHostPort             = "localhost:7233"
	DefaultTemporalRetryInitialInterval = 30
	DefaultTemporalRetryMaxInterval     = 300
	DefaultTemporalRetryMaxAttempts     = 20
)

// Worker concurrency defaults
const (
	DefaultWorkerQueue                = "tasks"
	DefaultWorkerMaxWorkflowPollers   = 2
	DefaultWorkerMaxActivityPollers   = 2
	DefaultWorkerMaxWorkflowExecution = 50
	DefaultWorkerMaxActivityExecution = 50
)

// Download concurrency defaults
const (
	DefaultMaxSeasonConcurrency    = 10
	DefaultDayConcurrency          = 20
	DefaultGameDownloadConcurrency = 8
)

// Yahoo player download defaults
const (
	DefaultMaxYahooPlayerID             = 35000
	DefaultYahooPlayerBatchSize         = 50
	DefaultYahooPlayerActivityBatchSize = 100
	DefaultYahooPlayersPerExecution     = 5000
	DefaultYahooPlayerPoolMaxAge        = 12 // hours
	DefaultYahooDownloadSleepMin        = 5
	DefaultYahooDownloadSleepMax        = 15
)

// NHL player landing download defaults
const (
	DefaultPlayerLandingConcurrency    = 20   // Concurrent activities
	DefaultMaxPlayerLandingConcurrency = 20   // Cap on concurrent activities
	DefaultPlayerLandingBatchSize      = 50   // Players per activity
	DefaultPlayerLandingPlayersPerExec = 2000 // Players before ContinueAsNew
)

// Asset download defaults
const (
	// DefaultAssetClassConcurrency is the number of FetchAssetBatch activities
	// that run concurrently within one asset class child workflow.
	DefaultAssetClassConcurrency = 16

	// DefaultMaxAssetClassConcurrency is the number of asset class child
	// workflows the parent FetchAssetsWorkflow runs concurrently (Phase 4).
	DefaultMaxAssetClassConcurrency = 3

	// DefaultAssetBatchSize is the number of assets processed per FetchAssetBatch
	// activity. Kept in sync with worker/asset.DefaultAssetBatchSize; defined here
	// to avoid importing worker/asset from the config package.
	DefaultAssetBatchSize = 100
)

// Process players workflow defaults
const (
	DefaultProcessPlayersConcurrency = 10 // Concurrent batch activities
	DefaultProcessPlayersBatchSize   = 50 // Players per batch activity
)

// Player game logs download defaults
const (
	DefaultPlayerLogsBatchSize        = 10 // Players per batch (smaller = more frequent progress)
	DefaultPlayerLogsBatchConcurrency = 10 // Concurrent batches
)

// Cache defaults
const (
	DefaultGobCacheTTL             = 60
	DefaultGobCacheConfig          = "" // empty = no per-type config, use flat TTL
	DefaultBoxscorePlayerCacheTTL  = 60
	DefaultSeasonsManifestStaleTTL = 24 * time.Hour // Filesystem staleness
)

// Redis defaults
const (
	DefaultRedisURL = "localhost:6379"
	DefaultRedisDB  = 0
)

// PostgreSQL defaults
const (
	DefaultPostgresHost         = "localhost"
	DefaultPostgresUser         = "puckdb"
	DefaultPostgresDatabase     = "puckdb"
	DefaultPostgresPort         = 5432
	DefaultPostgresSSLMode      = "disable"
	DefaultPostgresTimeZone     = "America/Los_Angeles"
	DefaultPostgresMaxIdleConns = 2
	DefaultPostgresMaxOpenConns = 5
)

// Metrics collection defaults
const (
	DefaultMetricsRefreshInterval = 5
	DefaultCacheIntervalSeconds   = 300
	DefaultRedisIntervalSeconds   = 60
	DefaultDBIntervalSeconds      = 300
)

// CLI client defaults
var DefaultAPIServerAddr = fmt.Sprintf("http://%s:%d", DefaultAPIHost, DefaultAPIPort)

// Workflow polling defaults
const (
	DefaultWorkflowStartupDelay = 500 * time.Millisecond
	DefaultWorkflowPollInterval = 2 * time.Second
	MaxWorkflowPollBackoff      = 30 * time.Second
	MaxConsecutiveQueryFailures = 10
)

// UI defaults
const (
	DefaultSpinnerInterval  = 125 * time.Millisecond
	DefaultProgressBarWidth = 80 // Fallback inner width when terminal size is unavailable
	MinProgressBarWidth     = 24 // Minimum inner width (3 gradient segments)
	ProgressLabelAreaWidth  = 17 // Fixed width for label + x/y area (x/y right-aligned within)
	ProgressLineOverhead    = 27 // Non-bar chars per line: spinner/indent(2) + labelArea(17) + brackets(2) + spaces(2) + pct(4)
)

// Database operations defaults
const (
	DefaultDBInitLockTTL      = 60 * time.Second
	DefaultDBProvisionLockTTL = 60 * time.Second
)

// HTTP/GraphQL server defaults
const (
	DefaultHTTPClientTimeout     = 60 * time.Second
	DefaultCancelTimeout         = 10 * time.Second
	DefaultGraphQLQueryCacheSize = 1000
	DefaultGraphQLAPQCacheSize   = 100
)

// GraphQL resolver timeout defaults
const (
	DefaultQueryTimeout         = 30 * time.Second
	DefaultChildWorkflowTimeout = 5 * time.Second
	DefaultWorkflowTaskTimeout  = 60 * time.Second
)

// Database connection defaults
const (
	DefaultDBConnMaxLifetime  = 5 * time.Minute
	DefaultSlowQueryThreshold = 2 * time.Second
)

// Worker processing defaults
const (
	EnrichmentLogInterval = 10
)

// Temporal workflow defaults
const (
	DefaultWorkflowExecutionTimeout    = 180 // minutes
	DefaultActivityStartToCloseTimeout = 10  // minutes
	DefaultFetchDayActivityTimeout     = 10  // minutes
	DefaultActivityHeartbeatTimeout    = 60  // seconds
	DefaultBackoffCoefficient          = 2.0
	DefaultSeasonConcurrency           = 5
)

// Time constants
const (
	DateFormat = "2006-01-02"
)

// File system constants
const (
	DirPermOwnerRWX = 0700 // Owner read/write/execute only
)

// Maurice AI chat defaults
const (
	DefaultOllamaBaseURL        = "http://localhost:11434/v1"
	DefaultMauriceModel         = "llama3.1:8b"
	DefaultMauriceConfig        = "" // empty = ~/.puckdb/maurice.yaml
	DefaultMauriceMaxTokens     = 4096
	DefaultMauriceMaxHistory    = 50
	DefaultMauriceMaxToolRounds = 10
)

// Admin authorization defaults
const (
	// DefaultAdminGroup is the Authentik group whose members are granted
	// access to the @admin-guarded GraphQL mutations.
	DefaultAdminGroup = "puckdb-admins"
)

// Draft helper report defaults
const (
	DefaultDraftSeason     = 0 // no default: the season must be named
	DefaultDraftLeagues    = ""
	DefaultDraftOutput     = ""
	DefaultDraftStaleAfter = 24 // hours
)

// Player news defaults (draft helper)
const (
	DefaultNewsSourcesFile = "" // built-in source set
	// DefaultNewsProcessBatchSize is how many article versions one
	// processing activity handles.
	DefaultNewsProcessBatchSize = 200
	// DefaultNewsIncidentWindowHours: reports of the same category for a
	// player within this many hours of an incident's span join it (14 days,
	// so weekly injury updates stay one incident).
	DefaultNewsIncidentWindowHours = 14 * 24
	// DefaultNewsRetentionDays keeps articles and incidents this long after
	// they were last seen or reported (an offseason injury must still be
	// visible at draft time).
	DefaultNewsRetentionDays = 400
	// DefaultNewsKeepVersions is the number of versions kept per article
	// (versions backing an incident are always kept).
	DefaultNewsKeepVersions      = 20
	DefaultNewsFetchMaxAttempts  = 4
	DefaultNewsFetchRetryInitial = 30  // seconds
	DefaultNewsFetchRetryMax     = 900 // seconds
	// DefaultNewsScheduleMinutes of 0 leaves the periodic refresh off.
	DefaultNewsScheduleMinutes = 0
	DefaultNewsForce           = false
	DefaultNewsOnly            = ""
	DefaultNewsSeason          = 0 // the current season
	DefaultNewsSinceDays       = 14
	DefaultNewsLimit           = 50
	DefaultNewsPlayerNHLID     = 0
	DefaultNewsPlayerYahooID   = 0
	DefaultNewsOutput          = ""
)

// Player news event extraction defaults (draft helper). Extraction calls a
// paid or local LLM, so it is off until enabled.
const (
	DefaultNewsExtractEnabled  = false
	DefaultNewsExtractProvider = "anthropic"
	DefaultNewsExtractModel    = "claude-haiku-4-5-20251001"
	// DefaultNewsExtractMaxOutputTokens bounds one reply (a few events).
	DefaultNewsExtractMaxOutputTokens = 2048
	// DefaultNewsExtractMaxInputChars bounds the article text sent per
	// call; the longest NHL.com stories seen are about 26,000 characters.
	DefaultNewsExtractMaxInputChars = 24_000
	// DefaultNewsExtractBatchSize is how many versions one activity handles.
	DefaultNewsExtractBatchSize = 10
	// DefaultNewsExtractConcurrency is how many model calls one activity
	// makes at once.
	DefaultNewsExtractConcurrency = 2
	// DefaultNewsExtractMaxCalls and DefaultNewsExtractMaxTokens cap one
	// refresh's model calls and tokens (prompt plus completion); versions
	// left over wait for the next refresh.
	DefaultNewsExtractMaxCalls  = 100
	DefaultNewsExtractMaxTokens = 500_000
	// DefaultNewsExtractMaxAttempts is how often a failing call is tried
	// per version and extractor before it is left for review.
	DefaultNewsExtractMaxAttempts = 3
	// DefaultNewsExtractRetryMinutes is the wait before a failed call is
	// tried again.
	DefaultNewsExtractRetryMinutes = 30
	// DefaultNewsExtractTimeoutSeconds bounds one model call.
	DefaultNewsExtractTimeoutSeconds = 120
	// DefaultNewsExtractLookbackDays: only versions behind incidents
	// reported this recently are extracted (a model change re-extracts no
	// older news).
	DefaultNewsExtractLookbackDays = 180
	// DefaultNewsEvalCorpus of "" is the built-in labeled corpus.
	DefaultNewsEvalCorpus = ""
)

// Build version constants
const (
	BuildNumberNotAvailable = "n/a"
)

// Yahoo OAuth2 scopes
var YahooOAuthScopes = []string{"openid", "fspt-r"}

var ErrNotImplementedYet = errors.New("not implemented yet")
