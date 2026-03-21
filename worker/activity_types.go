package worker

import "time"

// DateSeasonInput is the common base for activity inputs that operate on a single date within a season.
// Season is the start year (e.g., 2023 for the 2023-2024 season).
type DateSeasonInput struct {
	Date   time.Time `json:"date"`
	Season int       `json:"season"`
}
