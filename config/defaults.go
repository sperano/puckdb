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

var ErrNotImplementedYet = errors.New("not implemented yet")
