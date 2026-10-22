// Package shared holds what every worker workflow/activity package needs
// in common: progress tracking (ReportTracker, BarRef, ProgressReport) so
// a parent can query a running child's progress bars, SnapshotConfig/
// SnapshotConfigInt for reading viper config once through a deterministic
// SideEffect, RunWorkerPool for bounded-concurrency activity fan-out,
// shared input/output types (DateSeasonInput, PartialPlayer), task queue
// and workflow ID constants, and small workflow helpers (rate limiting,
// heartbeats, batching).
package shared

import "time"

// DateSeasonInput is the common base for activity inputs that operate on a single date within a season.
// Season is the start year (e.g., 2023 for the 2023-2024 season).
type DateSeasonInput struct {
	Date   time.Time `json:"date"`
	Season int       `json:"season"`
}

// PartialPlayer holds player data from games/boxscores before enrichment.
// It tracks which data sources have contributed information to allow
// proper merging strategies.
type PartialPlayer struct {
	ID            int64
	FirstName     string
	LastName      string
	Position      string
	SweaterNumber int
	TeamID        int64

	// Yahoo-specific fields
	YahooHomeURL string
	YahooImage   string

	// Track data sources for merge priority
	HasYahooData    bool
	HasBoxscoreData bool
}

// PlayerEnrichmentError tracks failures to enrich specific players.
type PlayerEnrichmentError struct {
	PlayerID int64
	Error    string
}

// BatchResult is returned by batch extraction activities instead of full player maps.
// This keeps Temporal workflow history small by storing actual data in Redis.
type BatchResult struct {
	RedisKey    string
	PlayerCount int
}

// BatchInput contains parameters for batch extraction activities.
type BatchInput struct {
	Days       []time.Time
	Season     int
	BatchIndex int
}
