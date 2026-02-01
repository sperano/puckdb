package config

import (
	"errors"
	"time"
)

// Application defaults
const (
	DefaultTemporalNamespace = "puckdb"
	DefaultUser              = "eric"
	DefaultLogLevel          = "info"
	DefaultYahooSeasonsFile  = "yahoo-seasons.yaml"
)

// Server port defaults
const (
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
	DefaultWorkerMaxWorkflowPollers   = 2
	DefaultWorkerMaxActivityPollers   = 2
	DefaultWorkerMaxWorkflowExecution = 50
	DefaultWorkerMaxActivityExecution = 50
)

// Download concurrency defaults
const (
	DefaultMaxSeasonConcurrency = 10
	DefaultDayConcurrency       = 20
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
	DefaultPlayerLandingConcurrency      = 20   // Concurrent activities
	DefaultPlayerLandingBatchSize        = 50   // Players per activity
	DefaultPlayerLandingPlayersPerExec   = 2000 // Players before ContinueAsNew
)

// Cache defaults
const (
	DefaultGameIDCacheTTL = 3600
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
const (
	DefaultAPIServerAddr = "http://localhost:8080"
)

// Workflow timeout defaults
const (
	DefaultWorkflowPollInterval   = 2 * time.Second
	DefaultWorkflowPollTimeout    = 30 * time.Minute
	DefaultYahooPlayersTimeout    = 4 * time.Hour
	DefaultDownloadPlayersTimeout = 2 * time.Hour
)

// UI defaults
const (
	DefaultSpinnerInterval = 125 * time.Millisecond
	DefaultProgressBarWidth = 40
)

// Database provisioning defaults
const (
	DefaultDBProvisionLockTTL = 60 * time.Second
)

// HTTP/GraphQL server defaults
const (
	DefaultHTTPClientTimeout      = 60 * time.Second
	DefaultWebsocketKeepAlive     = 10 * time.Second
	DefaultCancelTimeout          = 10 * time.Second
	DefaultCORSMaxAge             = 300
	DefaultGraphQLQueryCacheSize  = 1000
	DefaultGraphQLAPQCacheSize    = 100
	DefaultViteDevServerOrigin    = "http://localhost:5173"
)

// GraphQL resolver timeout defaults
const (
	DefaultQueryTimeout         = 30 * time.Second
	DefaultChildWorkflowTimeout = 1 * time.Second
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
	DefaultWorkflowExecutionTimeout    = 30 * time.Minute
	DefaultActivityStartToCloseTimeout = 3 * time.Minute
	DefaultBackoffCoefficient          = 2.0
	DefaultSeasonConcurrency           = 5
)

// Time constants
const (
	HoursPerDay = 24
	DateFormat  = "2006-01-02"
)

var ErrNotImplementedYet = errors.New("not implemented yet")
