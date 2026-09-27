//go:build integration

package projection

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

const agingBacktestTargetSeason = 20252026

// TestAgingBacktest20252026 reruns the production-query backtest against a
// populated test database. PUCKDB_TEST_PG_URL must point at a database whose
// regular-season game aggregates include 2005-06 through 2025-26.
func TestAgingBacktest20252026(t *testing.T) {
	databaseURL := os.Getenv("PUCKDB_TEST_PG_URL")
	if databaseURL == "" {
		t.Skip("PUCKDB_TEST_PG_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	agedConfig := DefaultConfig()
	repository := NewRepository(pool)
	input, err := repository.LoadEvaluationInput(
		ctx, agedConfig, agingBacktestTargetSeason,
		time.Date(2025, time.September, 20, 0, 0, 0, 0, time.UTC),
		time.Date(2026, time.September, 26, 0, 0, 0, 0, time.UTC),
	)
	require.NoError(t, err)

	withoutConfig := agedConfig
	withoutConfig.ModelVersion = LinemateModelVersion
	without, err := Evaluate(withoutConfig, input)
	require.NoError(t, err)
	with, err := Evaluate(agedConfig, input)
	require.NoError(t, err)
	require.Len(t, without, 2, "backtest requires both skater and goalie outcomes")
	require.Len(t, with, 2, "backtest requires both skater and goalie outcomes")
	logAgingComparison(t, without, with)
}

func logAgingComparison(t *testing.T, without, with []Evaluation) {
	t.Helper()
	baseline := make(map[PlayerKind]map[Stat]Metric, len(without))
	for _, evaluation := range without {
		baseline[evaluation.PlayerKind] = metricsByStat(evaluation.Metrics)
	}
	for _, evaluation := range with {
		for _, metric := range evaluation.Metrics {
			previous, exists := baseline[evaluation.PlayerKind][metric.Stat]
			require.True(t, exists, "missing no-aging metric for %s/%s", evaluation.PlayerKind, metric.Stat)
			require.Equal(t, previous.SampleSize, metric.SampleSize)
			t.Logf("%s/%s n=%d no-aging MAE=%.6f aged MAE=%.6f delta=%+.6f",
				evaluation.PlayerKind, metric.Stat, metric.SampleSize,
				previous.ModelMAE, metric.ModelMAE, metric.ModelMAE-previous.ModelMAE)
		}
	}
}

func metricsByStat(metrics []Metric) map[Stat]Metric {
	values := make(map[Stat]Metric, len(metrics))
	for _, metric := range metrics {
		values[metric.Stat] = metric
	}
	return values
}
