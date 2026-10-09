// Package projectionfixtures provides projection model configs for tests
// that build projection snapshots by hand instead of running the model.
// Only tests import this package.
package projectionfixtures

import "github.com/sperano/puckdb/internal/projection"

// AgingCurveThroughSeason is the last season the fixture aging curve covers.
// It precedes every fixture target season, as the model requires.
const AgingCurveThroughSeason = 20252026

// Config returns the default projection config with an aging curve. The
// model fits a curve for every aging model version (nhl-baseline-v4 and
// later, which includes the default), the projection_snapshots_aging_curve_check
// constraint refuses to store a snapshot of those versions without one, and
// loading such a snapshot fails. The curve has no steps, as the model fits
// it when no player has two consecutive qualifying seasons, so it ages no
// value.
func Config() projection.Config {
	cfg := projection.DefaultConfig()
	cfg.AgingCurve = &projection.AgingCurve{
		Version:             projection.AgingCurveVersion,
		TrainingFloorSeason: projection.AgingTrainingFloorSeason,
		ThroughSeason:       AgingCurveThroughSeason,
		AgeReference:        projection.AgingAgeReference,
	}
	return cfg
}
