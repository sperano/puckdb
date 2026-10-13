package temporal

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewMetricsHandler_RegistersTemporalMetricsOnTheGivenRegistry(t *testing.T) {
	t.Parallel()
	registry := prometheus.NewRegistry()
	handler, closer := NewMetricsHandler(registry)

	handler.Counter("test_counter").Inc(1)
	handler.Gauge("test_gauge").Update(42)
	handler.Timer("test_timer").Record(250 * time.Millisecond)

	// Closing flushes the tally scope's last report before stopping it, so
	// the values above are guaranteed visible to the registry afterwards.
	require.NoError(t, closer.Close())

	families, err := registry.Gather()
	require.NoError(t, err)

	byName := make(map[string]*dto.MetricFamily, len(families))
	for _, f := range families {
		byName[f.GetName()] = f
	}
	assert.NotNil(t, byName["test_counter_total"], "expected counter metric, got %v", byName)
	assert.NotNil(t, byName["test_gauge"], "expected gauge metric, got %v", byName)

	// Must be a histogram, not tally/Prometheus's default summary: a
	// summary's quantiles are computed client-side and cannot be aggregated
	// across pods or task queues, which would defeat the point of exporting
	// schedule-to-start latency as a fleet-wide queueing signal.
	timer := byName["test_timer_seconds"]
	if assert.NotNil(t, timer, "expected timer metric, got %v", byName) {
		assert.Equal(t, dto.MetricType_HISTOGRAM, timer.GetType())
	}
}

func TestNewMetricsHandler_WithTagsScopesSubsequentMetrics(t *testing.T) {
	t.Parallel()
	registry := prometheus.NewRegistry()
	handler, closer := NewMetricsHandler(registry)

	tagged := handler.WithTags(map[string]string{"task_queue": "puckdb-tasks"})
	tagged.Counter("tagged_counter").Inc(1)

	require.NoError(t, closer.Close())

	families, err := registry.Gather()
	require.NoError(t, err)

	var found bool
	for _, f := range families {
		if f.GetName() != "tagged_counter_total" {
			continue
		}
		for _, m := range f.GetMetric() {
			for _, l := range m.GetLabel() {
				// The sanitizer configured in NewMetricsHandler replaces "-"
				// with "_" in tag values to satisfy Prometheus naming rules.
				if l.GetName() == "task_queue" && l.GetValue() == "puckdb_tasks" {
					found = true
				}
			}
		}
	}
	assert.True(t, found, "expected tagged_counter_total with task_queue label, got %v", families)
}
