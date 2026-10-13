package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSQLCQueryName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		sql  string
		want string
		ok   bool
	}{
		{name: "one", sql: "-- name: GetPlayer :one\nSELECT 1", want: "GetPlayer", ok: true},
		{name: "batch", sql: "-- name: UpsertPlayers :batchexec\nINSERT INTO players", want: "UpsertPlayers", ok: true},
		{name: "raw SQL", sql: "SELECT 1"},
		{name: "missing command", sql: "-- name: Broken\nSELECT 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := sqlcQueryName(tt.sql)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSQLCQueryTracerObservesNamedQuery(t *testing.T) {
	t.Parallel()

	const queryName = "MetricsTracerTestQuery"
	start := time.Date(2026, time.October, 13, 0, 0, 0, 0, time.UTC)
	times := []time.Time{start, start.Add(250 * time.Millisecond)}
	tracer := &SQLCQueryTracer{now: func() time.Time {
		now := times[0]
		times = times[1:]
		return now
	}}
	before := histogramSampleCountByLabel(t, "puckdb_db_query_duration_seconds", "query", queryName)

	ctx := tracer.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{
		SQL: "-- name: " + queryName + " :one\nSELECT 1",
	})
	tracer.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})

	after := histogramSampleCountByLabel(t, "puckdb_db_query_duration_seconds", "query", queryName)
	assert.Equal(t, before+1, after)
}

func TestPGXPoolCollectorExposesCapacityAndState(t *testing.T) {
	t.Parallel()

	const maxConnections = int32(7)
	cfg, err := pgxpool.ParseConfig("postgres://metrics@localhost:1/metrics?sslmode=disable")
	require.NoError(t, err)
	cfg.MaxConns = maxConnections
	cfg.MinConns = 0
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	registry := prometheus.NewRegistry()
	registry.MustRegister(NewPGXPoolCollector(pool))
	families, err := registry.Gather()
	require.NoError(t, err)

	assert.Equal(t, float64(maxConnections), gaugeValue(t, families, "puckdb_db_pool_connections", "state", poolStateMax))
	assert.Equal(t, float64(0), gaugeValue(t, families, "puckdb_db_pool_connections", "state", poolStateAcquired))
	assertMetricFamilyExists(t, families, "puckdb_db_pool_empty_acquire_wait_seconds_total")
}

func histogramSampleCountByLabel(t *testing.T, name, labelName, labelValue string) uint64 {
	t.Helper()
	families, err := WorkerRegistry.Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			if metricLabel(metric.GetLabel(), labelName) == labelValue {
				return metric.GetHistogram().GetSampleCount()
			}
		}
	}
	return 0
}

func gaugeValue(t *testing.T, families []*dto.MetricFamily, name, labelName, labelValue string) float64 {
	t.Helper()
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			if metricLabel(metric.GetLabel(), labelName) == labelValue {
				return metric.GetGauge().GetValue()
			}
		}
	}
	t.Fatalf("metric %s{%s=%q} not found", name, labelName, labelValue)
	return 0
}

func assertMetricFamilyExists(t *testing.T, families []*dto.MetricFamily, name string) {
	t.Helper()
	for _, family := range families {
		if family.GetName() == name {
			return
		}
	}
	t.Fatalf("metric family %s not found", name)
}
