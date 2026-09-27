package projection

import (
	"fmt"
	"math"
	"slices"
	"time"
)

const (
	PreviousSeasonComparison   = "previous-season-total"
	LinemateDisabledComparison = "linemate-adjustment-disabled"
)

type Metric struct {
	Stat           Stat
	SampleSize     int
	ModelMAE       float64
	ModelRMSE      float64
	ComparisonMAE  float64
	ComparisonRMSE float64
}

type Evaluation struct {
	ModelVersion    string
	ConfigHash      string
	SourceDataHash  string
	TargetSeason    int
	AsOf            time.Time
	ObservedAt      time.Time
	PlayerKind      PlayerKind
	ComparisonModel string
	Metrics         []Metric
}

// Evaluate performs a held-out season evaluation. Generate excludes rows from
// targetSeason and later, while the target rows are used only as outcomes.
// Both models are scored on the same players that have a previous-season row.
func Evaluate(cfg Config, input Input) ([]Evaluation, error) {
	if err := validateHeldOutInput(input); err != nil {
		return nil, err
	}
	snapshot, err := Generate(cfg, input)
	if err != nil {
		return nil, err
	}
	previousSeason := historyFloorSeason(input.TargetSeason, 1)
	result := make([]Evaluation, 0, 2)
	if evaluation := evaluateSkaters(snapshot, input, previousSeason); len(evaluation.Metrics) > 0 {
		result = append(result, evaluation)
	}
	if evaluation := evaluateGoalies(snapshot, input, previousSeason); len(evaluation.Metrics) > 0 {
		result = append(result, evaluation)
	}
	return result, nil
}

// EvaluateLinemateAdjustment scores the configured skater model against the
// same model with only its linemate correction disabled. The target-season
// rows remain outcomes, so callers can report the per-stat error change caused
// by historical context without changing any other projection parameter.
func EvaluateLinemateAdjustment(cfg Config, input Input) (Evaluation, error) {
	if err := validateHeldOutInput(input); err != nil {
		return Evaluation{}, err
	}
	adjusted, err := Generate(cfg, input)
	if err != nil {
		return Evaluation{}, err
	}
	comparisonConfig := cfg
	comparisonConfig.LinemateRegressionStrength = 0
	comparison, err := Generate(comparisonConfig, input)
	if err != nil {
		return Evaluation{}, err
	}
	actual := aggregateSkaterValues(input.Skaters, input.TargetSeason)
	comparisonValues := projectedValues(comparison, PlayerKindSkater)
	stats := supportedStats(PlayerKindSkater)
	return evaluateKind(
		adjusted, input.ObservedAt, evaluationDataHash(cfg, input),
		PlayerKindSkater, actual, comparisonValues, stats,
		LinemateDisabledComparison,
	), nil
}

func validateHeldOutInput(input Input) error {
	if len(input.Overrides) > 0 {
		return fmt.Errorf("held-out evaluation does not accept overrides")
	}
	if input.ObservedAt.IsZero() || input.ObservedAt.Before(input.AsOf) {
		return fmt.Errorf("held-out evaluation requires an observation time at or after its as-of time")
	}
	return nil
}

func evaluateSkaters(snapshot Snapshot, input Input, previousSeason int) Evaluation {
	actual := aggregateSkaterValues(input.Skaters, input.TargetSeason)
	comparison := aggregateSkaterValues(input.Skaters, previousSeason)
	stats := []Stat{
		StatGamesPlayed, StatTOISeconds, StatGoals, StatAssists, StatPoints,
		StatPlusMinus, StatPenaltyMinutes, StatPowerPlayPoints, StatShotsOnGoal,
		StatHits, StatBlockedShots, StatFaceoffsWon, StatFaceoffsLost,
	}
	return evaluateKind(
		snapshot, input.ObservedAt, evaluationDataHash(snapshot.Config, input),
		PlayerKindSkater, actual, comparison, stats, PreviousSeasonComparison,
	)
}

func evaluateGoalies(snapshot Snapshot, input Input, previousSeason int) Evaluation {
	actual := aggregateGoalieValues(input.Goalies, input.TargetSeason)
	comparison := aggregateGoalieValues(input.Goalies, previousSeason)
	stats := []Stat{
		StatGamesPlayed, StatGamesStarted, StatTOISeconds, StatWins, StatShutouts,
		StatShotsAgainst, StatSaves, StatGoalsAgainst, StatSavePercentage,
		StatGoalsAgainstAvg,
	}
	return evaluateKind(
		snapshot, input.ObservedAt, evaluationDataHash(snapshot.Config, input),
		PlayerKindGoalie, actual, comparison, stats, PreviousSeasonComparison,
	)
}

func evaluateKind(
	snapshot Snapshot,
	observedAt time.Time,
	sourceDataHash string,
	kind PlayerKind,
	actual map[string]map[Stat]float64,
	comparison map[string]map[Stat]float64,
	stats []Stat,
	comparisonModel string,
) Evaluation {
	projections := make(map[string]PlayerProjection)
	for _, player := range snapshot.Players {
		if player.Kind == kind {
			projections[player.PlayerKey] = player
		}
	}
	metrics := make([]Metric, 0, len(stats))
	for _, stat := range stats {
		metric := scoreStat(stat, projections, actual, comparison)
		if metric.SampleSize > 0 {
			metrics = append(metrics, metric)
		}
	}
	return Evaluation{
		ModelVersion: snapshot.Config.ModelVersion, ConfigHash: configHash(snapshot.Config),
		SourceDataHash: sourceDataHash, TargetSeason: snapshot.TargetSeason,
		AsOf: snapshot.AsOf, ObservedAt: observedAt.UTC(), PlayerKind: kind,
		ComparisonModel: comparisonModel, Metrics: metrics,
	}
}

func projectedValues(snapshot Snapshot, kind PlayerKind) map[string]map[Stat]float64 {
	values := make(map[string]map[Stat]float64)
	for _, player := range snapshot.Players {
		if player.Kind != kind {
			continue
		}
		stats := make(map[Stat]float64, len(player.Values))
		for stat, estimate := range player.Values {
			stats[stat] = estimate.Mean
		}
		values[player.PlayerKey] = stats
	}
	return values
}

func scoreStat(
	stat Stat,
	projections map[string]PlayerProjection,
	actual map[string]map[Stat]float64,
	comparison map[string]map[Stat]float64,
) Metric {
	var modelAbsolute, modelSquared, comparisonAbsolute, comparisonSquared float64
	var samples int
	for key, actualValues := range actual {
		projection, projected := projections[key]
		comparisonValues, compared := comparison[key]
		actualValue, actualKnown := actualValues[stat]
		projectedValue, projectedKnown := projection.Values[stat]
		comparisonValue, comparisonKnown := comparisonValues[stat]
		if !projected || !compared || !actualKnown || !projectedKnown || !comparisonKnown {
			continue
		}
		modelError := projectedValue.Mean - actualValue
		comparisonError := comparisonValue - actualValue
		modelAbsolute += math.Abs(modelError)
		modelSquared += modelError * modelError
		comparisonAbsolute += math.Abs(comparisonError)
		comparisonSquared += comparisonError * comparisonError
		samples++
	}
	if samples == 0 {
		return Metric{Stat: stat}
	}
	count := float64(samples)
	return Metric{
		Stat: stat, SampleSize: samples,
		ModelMAE: modelAbsolute / count, ModelRMSE: math.Sqrt(modelSquared / count),
		ComparisonMAE:  comparisonAbsolute / count,
		ComparisonRMSE: math.Sqrt(comparisonSquared / count),
	}
}

func aggregateSkaterValues(rows []SkaterSeason, season int) map[string]map[Stat]float64 {
	result := make(map[string]map[Stat]float64)
	for _, row := range rows {
		if row.Season != season {
			continue
		}
		values := result[playerKey(row.PlayerID)]
		if values == nil {
			values = make(map[Stat]float64)
			result[playerKey(row.PlayerID)] = values
		}
		values[StatGamesPlayed] += float64(row.GamesPlayed)
		values[StatTOISeconds] += float64(row.TOISeconds)
		values[StatGoals] += float64(row.Goals)
		values[StatAssists] += float64(row.Assists)
		values[StatPoints] += float64(row.Goals + row.Assists)
		values[StatPlusMinus] += float64(row.PlusMinus)
		values[StatPenaltyMinutes] += float64(row.PenaltyMinutes)
		values[StatPowerPlayPoints] += float64(row.PowerPlayPoints)
		values[StatShotsOnGoal] += float64(row.ShotsOnGoal)
		values[StatHits] += float64(row.Hits)
		values[StatBlockedShots] += float64(row.BlockedShots)
		values[StatFaceoffsWon] += float64(row.FaceoffsWon)
		values[StatFaceoffsLost] += float64(row.FaceoffsLost)
	}
	return result
}

func aggregateGoalieValues(rows []GoalieSeason, season int) map[string]map[Stat]float64 {
	components := make(map[string]GoalieSeason)
	for _, row := range rows {
		if row.Season != season {
			continue
		}
		key := playerKey(row.PlayerID)
		total := components[key]
		total.PlayerID = row.PlayerID
		total.GamesPlayed += row.GamesPlayed
		total.GamesStarted += row.GamesStarted
		total.TOISeconds += row.TOISeconds
		total.Wins += row.Wins
		total.Shutouts += row.Shutouts
		total.ShotsAgainst += row.ShotsAgainst
		total.Saves += row.Saves
		total.GoalsAgainst += row.GoalsAgainst
		components[key] = total
	}
	result := make(map[string]map[Stat]float64, len(components))
	for key, row := range components {
		values := map[Stat]float64{
			StatGamesPlayed: float64(row.GamesPlayed), StatGamesStarted: float64(row.GamesStarted),
			StatTOISeconds: float64(row.TOISeconds), StatWins: float64(row.Wins),
			StatShutouts: float64(row.Shutouts), StatShotsAgainst: float64(row.ShotsAgainst),
			StatSaves: float64(row.Saves), StatGoalsAgainst: float64(row.GoalsAgainst),
		}
		if row.ShotsAgainst > 0 {
			values[StatSavePercentage] = float64(row.Saves) / float64(row.ShotsAgainst)
		}
		if row.TOISeconds > 0 {
			values[StatGoalsAgainstAvg] = float64(row.GoalsAgainst) / float64(row.TOISeconds) * secondsPerHour
		}
		result[key] = values
	}
	return result
}

func validateEvaluation(evaluation Evaluation) error {
	if evaluation.ModelVersion == "" || evaluation.ConfigHash == "" || evaluation.SourceDataHash == "" ||
		evaluation.TargetSeason <= 0 || evaluation.AsOf.IsZero() || evaluation.ObservedAt.IsZero() {
		return fmt.Errorf("evaluation metadata is incomplete")
	}
	if evaluation.ObservedAt.Before(evaluation.AsOf) {
		return fmt.Errorf("evaluation observation time must not precede its as-of time")
	}
	if evaluation.PlayerKind != PlayerKindSkater && evaluation.PlayerKind != PlayerKindGoalie {
		return fmt.Errorf("invalid evaluation player kind %q", evaluation.PlayerKind)
	}
	if evaluation.ComparisonModel == "" {
		return fmt.Errorf("comparison model is required")
	}
	if len(evaluation.Metrics) == 0 {
		return fmt.Errorf("evaluation has no metrics")
	}
	seen := make(map[Stat]struct{}, len(evaluation.Metrics))
	for _, metric := range evaluation.Metrics {
		if metric.Stat == "" || !supportsStat(evaluation.PlayerKind, metric.Stat) {
			return fmt.Errorf("evaluation has unsupported stat %q", metric.Stat)
		}
		if _, exists := seen[metric.Stat]; exists {
			return fmt.Errorf("evaluation has duplicate stat %q", metric.Stat)
		}
		seen[metric.Stat] = struct{}{}
	}
	if slices.ContainsFunc(evaluation.Metrics, func(metric Metric) bool { return metric.SampleSize <= 0 }) {
		return fmt.Errorf("evaluation metrics require positive samples")
	}
	if slices.ContainsFunc(evaluation.Metrics, invalidMetricValues) {
		return fmt.Errorf("evaluation metric values must be finite and non-negative")
	}
	return nil
}

func invalidMetricValues(metric Metric) bool {
	values := [...]float64{metric.ModelMAE, metric.ModelRMSE, metric.ComparisonMAE, metric.ComparisonRMSE}
	return slices.ContainsFunc(values[:], func(value float64) bool { return !finite(value) || value < 0 })
}
