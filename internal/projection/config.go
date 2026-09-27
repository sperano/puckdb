package projection

import (
	"fmt"
	"math"
)

const (
	LegacyModelVersion       = "nhl-baseline-v1"
	FaceoffModelVersion      = "nhl-baseline-v2"
	LinemateModelVersion     = "nhl-baseline-v3"
	ModelVersion             = "nhl-baseline-v4"
	AgingCurveVersion        = "delta-v1"
	AgingTrainingFloorSeason = 20052006
	AgingAgeReference        = "jan-1-season-ending-year"

	DefaultLookbackSeasons            = 3
	DefaultSeasonDecay                = 0.40
	DefaultSkaterPriorTOISeconds      = 9_000
	DefaultLinemateRegressionStrength = 0.25
	DefaultGoaliePriorShots           = 500
	DefaultGoalieShutoutMinTOI        = 59 * 60
	DefaultMaxGames                   = 82
	DefaultIntervalZ                  = 1.28
	DefaultMinimumUncertainty         = 0.10
	DefaultMaximumUncertainty         = 1.00
	DefaultMinimumHistoryGames        = 10
)

// Config contains every parameter that can affect a baseline snapshot.
// Persisting it with the snapshot makes model output reproducible.
type Config struct {
	ModelVersion               string
	LookbackSeasons            int
	SeasonDecay                float64
	SkaterPriorTOISeconds      float64
	LinemateRegressionStrength float64
	GoaliePriorShots           float64
	GoalieShutoutMinTOI        int
	MaxGames                   float64
	IntervalZ                  float64
	MinimumUncertainty         float64
	MaximumUncertainty         float64
	MinimumHistoryGames        int
	AgingCurve                 *AgingCurve `json:",omitempty"`
}

func DefaultConfig() Config {
	return Config{
		ModelVersion:               ModelVersion,
		LookbackSeasons:            DefaultLookbackSeasons,
		SeasonDecay:                DefaultSeasonDecay,
		SkaterPriorTOISeconds:      DefaultSkaterPriorTOISeconds,
		LinemateRegressionStrength: DefaultLinemateRegressionStrength,
		GoaliePriorShots:           DefaultGoaliePriorShots,
		GoalieShutoutMinTOI:        DefaultGoalieShutoutMinTOI,
		MaxGames:                   DefaultMaxGames,
		IntervalZ:                  DefaultIntervalZ,
		MinimumUncertainty:         DefaultMinimumUncertainty,
		MaximumUncertainty:         DefaultMaximumUncertainty,
		MinimumHistoryGames:        DefaultMinimumHistoryGames,
	}
}

// HistoryFloorSeason returns the oldest NHL season ID (inclusive) whose
// games count toward a player's history when projecting targetSeason: the
// model looks back LookbackSeasons seasons from the one immediately before
// targetSeason and never reads targetSeason itself (see seasonAge in
// model.go, and historyFloorSeason, which this delegates to so the two
// cannot drift). Exported so a caller that builds a candidate pool for
// projection — such as draft.LoadStandInPool — can tell ahead of time
// whether a player will have any history, without duplicating the model's
// window logic.
func (c Config) HistoryFloorSeason(targetSeason int) int {
	return historyFloorSeason(targetSeason, c.LookbackSeasons)
}

func (c Config) Validate() error {
	switch {
	case c.ModelVersion == "":
		return fmt.Errorf("model version is required")
	case c.LookbackSeasons <= 0:
		return fmt.Errorf("lookback seasons must be positive")
	case !finite(c.SeasonDecay) || !finite(c.SkaterPriorTOISeconds) ||
		!finite(c.LinemateRegressionStrength) ||
		!finite(c.GoaliePriorShots) || !finite(c.MaxGames) ||
		!finite(c.IntervalZ) || !finite(c.MinimumUncertainty) ||
		!finite(c.MaximumUncertainty):
		return fmt.Errorf("projection config values must be finite")
	case c.SeasonDecay <= 0 || c.SeasonDecay > 1:
		return fmt.Errorf("season decay must be in (0, 1]")
	case c.SkaterPriorTOISeconds < 0:
		return fmt.Errorf("skater prior TOI must not be negative")
	case c.LinemateRegressionStrength < 0 || c.LinemateRegressionStrength > 1:
		return fmt.Errorf("linemate regression strength must be in [0, 1]")
	case c.ModelVersion == LegacyModelVersion && c.LinemateRegressionStrength != 0:
		return fmt.Errorf("legacy model does not support linemate regression")
	case c.ModelVersion == FaceoffModelVersion && c.LinemateRegressionStrength != 0:
		return fmt.Errorf("model %q does not support linemate regression", c.ModelVersion)
	case c.ModelVersion != ModelVersion && c.AgingCurve != nil:
		return fmt.Errorf("model %q does not support aging curves", c.ModelVersion)
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
	if c.AgingCurve != nil {
		return validateAgingCurve(c.AgingCurve)
	}
	return nil
}

func supportsFaceoffs(modelVersion string) bool {
	return modelVersion != LegacyModelVersion
}

func supportsLinemateContext(modelVersion string) bool {
	return modelVersion == LinemateModelVersion || modelVersion == ModelVersion
}

func supportsAgingCurve(modelVersion string) bool {
	return modelVersion == ModelVersion
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
