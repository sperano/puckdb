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

// Build version constants
const (
	BuildNumberNotAvailable = "n/a"
)

// Yahoo OAuth2 scopes
var YahooOAuthScopes = []string{"openid", "fspt-r"}

var ErrNotImplementedYet = errors.New("not implemented yet")
