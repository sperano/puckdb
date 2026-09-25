package projection

import (
	"fmt"
	"math"
)

const (
	ModelVersion = "nhl-baseline-v1"

	DefaultLookbackSeasons       = 3
	DefaultSeasonDecay           = 0.40
	DefaultSkaterPriorTOISeconds = 9_000
	DefaultGoaliePriorShots      = 500
	DefaultGoalieShutoutMinTOI   = 59 * 60
	DefaultMaxGames              = 82
	DefaultIntervalZ             = 1.28
	DefaultMinimumUncertainty    = 0.10
	DefaultMaximumUncertainty    = 1.00
	DefaultMinimumHistoryGames   = 10
)

// Config contains every parameter that can affect a baseline snapshot.
// Persisting it with the snapshot makes model output reproducible.
type Config struct {
	ModelVersion          string
	LookbackSeasons       int
	SeasonDecay           float64
	SkaterPriorTOISeconds float64
	GoaliePriorShots      float64
	GoalieShutoutMinTOI   int
	MaxGames              float64
	IntervalZ             float64
	MinimumUncertainty    float64
	MaximumUncertainty    float64
	MinimumHistoryGames   int
}

func DefaultConfig() Config {
	return Config{
		ModelVersion:          ModelVersion,
		LookbackSeasons:       DefaultLookbackSeasons,
		SeasonDecay:           DefaultSeasonDecay,
		SkaterPriorTOISeconds: DefaultSkaterPriorTOISeconds,
		GoaliePriorShots:      DefaultGoaliePriorShots,
		GoalieShutoutMinTOI:   DefaultGoalieShutoutMinTOI,
		MaxGames:              DefaultMaxGames,
		IntervalZ:             DefaultIntervalZ,
		MinimumUncertainty:    DefaultMinimumUncertainty,
		MaximumUncertainty:    DefaultMaximumUncertainty,
		MinimumHistoryGames:   DefaultMinimumHistoryGames,
	}
}

func (c Config) Validate() error {
	switch {
	case c.ModelVersion == "":
		return fmt.Errorf("model version is required")
	case c.LookbackSeasons <= 0:
		return fmt.Errorf("lookback seasons must be positive")
	case !finite(c.SeasonDecay) || !finite(c.SkaterPriorTOISeconds) ||
		!finite(c.GoaliePriorShots) || !finite(c.MaxGames) ||
		!finite(c.IntervalZ) || !finite(c.MinimumUncertainty) ||
		!finite(c.MaximumUncertainty):
		return fmt.Errorf("projection config values must be finite")
	case c.SeasonDecay <= 0 || c.SeasonDecay > 1:
		return fmt.Errorf("season decay must be in (0, 1]")
	case c.SkaterPriorTOISeconds < 0:
		return fmt.Errorf("skater prior TOI must not be negative")
	case c.GoaliePriorShots < 0:
		return fmt.Errorf("goalie prior shots must not be negative")
	case c.GoalieShutoutMinTOI <= 0:
		return fmt.Errorf("goalie shutout minimum TOI must be positive")
	case c.MaxGames <= 0:
		return fmt.Errorf("max games must be positive")
	case c.IntervalZ <= 0:
		return fmt.Errorf("interval z-score must be positive")
	case c.MinimumUncertainty < 0:
		return fmt.Errorf("minimum uncertainty must not be negative")
	case c.MaximumUncertainty < c.MinimumUncertainty:
		return fmt.Errorf("maximum uncertainty must be at least the minimum")
	case c.MinimumHistoryGames < 0:
		return fmt.Errorf("minimum history games must not be negative")
	}
	return nil
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
