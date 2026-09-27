package projection

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEvaluateUsesTargetOnlyAsHeldOutOutcome(t *testing.T) {
	t.Parallel()

	base := []SkaterSeason{
		skaterSeason(1, 20242025, "C", 70, 20, 30),
		skaterSeason(1, 20252026, "C", 80, 30, 40),
		skaterSeason(1, 20262027, "C", 82, 35, 45),
	}
	input := Input{
		TargetSeason: 20262027,
		AsOf:         time.Date(2026, time.September, 20, 0, 0, 0, 0, time.UTC),
		ObservedAt:   time.Date(2027, time.May, 1, 0, 0, 0, 0, time.UTC),
		Skaters:      base,
	}

	withoutFuture, err := Evaluate(DefaultConfig(), input)
	require.NoError(t, err)
	input.Skaters = append(input.Skaters, skaterSeason(1, 20272028, "C", 82, 100, 100))
	withFuture, err := Evaluate(DefaultConfig(), input)
	require.NoError(t, err)
	require.Equal(t, withoutFuture, withFuture)
	require.Len(t, withoutFuture, 1)
	goals := findMetric(t, withoutFuture[0], StatGoals)
	require.Equal(t, 1, goals.SampleSize)
	require.Greater(t, goals.ComparisonMAE, 0.0)
}

func TestEvaluateLinemateAdjustmentComparesSameModelWithoutCorrection(t *testing.T) {
	t.Parallel()

	strong := skaterSeason(10, 20242025, "C", 82, 20, 30)
	strong.LinematePointsPer60 = 4
	strong.LinemateTOISeconds = strong.TOISeconds * evenStrengthLinemates
	weak := skaterSeason(11, 20242025, "C", 82, 20, 30)
	weak.LinematePointsPer60 = 1
	weak.LinemateTOISeconds = weak.TOISeconds * evenStrengthLinemates
	input := Input{
		TargetSeason: 20252026,
		AsOf:         time.Date(2025, time.September, 20, 0, 0, 0, 0, time.UTC),
		ObservedAt:   time.Date(2026, time.May, 1, 0, 0, 0, 0, time.UTC),
		Skaters: []SkaterSeason{
			strong,
			weak,
			skaterSeason(10, 20252026, "C", 82, 19, 29),
			skaterSeason(11, 20252026, "C", 82, 22, 33),
		},
	}

	report, err := EvaluateLinemateAdjustment(DefaultConfig(), input)
	require.NoError(t, err)
	require.Equal(t, LinemateDisabledComparison, report.ComparisonModel)
	require.Equal(t, PlayerKindSkater, report.PlayerKind)
	goals := findMetric(t, report, StatGoals)
	require.Equal(t, 2, goals.SampleSize)
	require.NotEqual(t, goals.ComparisonMAE, goals.ModelMAE)
}

func TestEvaluateGoalieRatiosAgainstPreviousSeason(t *testing.T) {
	t.Parallel()

	input := Input{
		TargetSeason: 20262027,
		AsOf:         time.Date(2026, time.September, 20, 0, 0, 0, 0, time.UTC),
		ObservedAt:   time.Date(2027, time.May, 1, 0, 0, 0, 0, time.UTC),
		Goalies: []GoalieSeason{
			{PlayerID: 2, Season: 20252026, GamesPlayed: 50, GamesStarted: 45, TOISeconds: 160_000, Wins: 25, ShotsAgainst: 1_500, Saves: 1_380, GoalsAgainst: 120},
			{PlayerID: 2, Season: 20262027, GamesPlayed: 55, GamesStarted: 50, TOISeconds: 175_000, Wins: 30, ShotsAgainst: 1_650, Saves: 1_530, GoalsAgainst: 120},
		},
	}

	reports, err := Evaluate(DefaultConfig(), input)
	require.NoError(t, err)
	require.Len(t, reports, 1)
	require.Equal(t, PlayerKindGoalie, reports[0].PlayerKind)
	require.Equal(t, 1, findMetric(t, reports[0], StatSavePercentage).SampleSize)
	require.Equal(t, 1, findMetric(t, reports[0], StatGoalsAgainstAvg).SampleSize)
}

func TestEvaluateRejectsOverrides(t *testing.T) {
	t.Parallel()

	_, err := Evaluate(DefaultConfig(), Input{
		TargetSeason: 20262027,
		AsOf:         time.Date(2026, time.September, 20, 0, 0, 0, 0, time.UTC),
		ObservedAt:   time.Date(2027, time.May, 1, 0, 0, 0, 0, time.UTC),
		Overrides: []Override{{
			PlayerKey: "manual:rookie", Kind: PlayerKindSkater, Source: SourceManual,
			Values: map[Stat]Estimate{StatGoals: {Mean: 20}},
		}},
	})
	require.EqualError(t, err, "held-out evaluation does not accept overrides")
}

func TestValidateEvaluationRejectsEmptyMetrics(t *testing.T) {
	t.Parallel()

	err := validateEvaluation(Evaluation{
		ModelVersion: ModelVersion, ConfigHash: "config", SourceDataHash: "source",
		TargetSeason: 20262027, AsOf: time.Now(), ObservedAt: time.Now(),
		PlayerKind: PlayerKindSkater, ComparisonModel: PreviousSeasonComparison,
	})
	require.EqualError(t, err, "evaluation has no metrics")
}

func TestValidateEvaluationRejectsInvalidObservationAndMetric(t *testing.T) {
	t.Parallel()

	asOf := time.Now()
	evaluation := Evaluation{
		ModelVersion: ModelVersion, ConfigHash: "config", SourceDataHash: "source",
		TargetSeason: 20262027, AsOf: asOf, ObservedAt: asOf.Add(-time.Hour),
		PlayerKind: PlayerKindSkater, ComparisonModel: PreviousSeasonComparison,
		Metrics: []Metric{{Stat: StatGoals, SampleSize: 1}},
	}
	require.EqualError(t, validateEvaluation(evaluation), "evaluation observation time must not precede its as-of time")

	evaluation.ObservedAt = asOf
	evaluation.Metrics[0].ModelMAE = math.NaN()
	require.EqualError(t, validateEvaluation(evaluation), "evaluation metric values must be finite and non-negative")
}

func TestValidateEvaluationRejectsDuplicateStats(t *testing.T) {
	t.Parallel()

	now := time.Now()
	evaluation := Evaluation{
		ModelVersion: ModelVersion, ConfigHash: "config", SourceDataHash: "source",
		TargetSeason: 20262027, AsOf: now, ObservedAt: now,
		PlayerKind: PlayerKindSkater, ComparisonModel: PreviousSeasonComparison,
		Metrics: []Metric{
			{Stat: StatGoals, SampleSize: 1},
			{Stat: StatGoals, SampleSize: 1},
		},
	}
	require.EqualError(t, validateEvaluation(evaluation), `evaluation has duplicate stat "goals"`)
}

func TestEvaluateRequiresObservationTime(t *testing.T) {
	t.Parallel()

	_, err := Evaluate(DefaultConfig(), Input{TargetSeason: 20262027, AsOf: time.Now()})
	require.EqualError(t, err, "held-out evaluation requires an observation time at or after its as-of time")
}

func findMetric(t *testing.T, evaluation Evaluation, stat Stat) Metric {
	t.Helper()
	for _, metric := range evaluation.Metrics {
		if metric.Stat == stat {
			return metric
		}
	}
	t.Fatalf("metric %q not found", stat)
	return Metric{}
}
