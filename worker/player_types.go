package worker

import "time"

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
	YahooHomeURL     string
	YahooImageSmall  string
	YahooImageMedium string
	YahooImageLarge  string

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
