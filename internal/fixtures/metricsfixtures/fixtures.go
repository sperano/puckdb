// Package metricsfixtures reads the worker metrics registry for tests that
// assert which labels a code path records. The registry is process-global,
// so callers compare counts before and after the operation under test and
// must not run in parallel with tests that record the same series. Only
// tests import this package.
package metricsfixtures

import (
	"testing"

	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/metrics"
)

// fsOpDurationMetric is the histogram that records every filesystem
// operation, whether or not it transferred bytes.
const fsOpDurationMetric = "puckdb_fs_operation_duration_seconds"

// FSOpCount returns how many filesystem operations have been recorded under
// the operation and file_type labels.
func FSOpCount(t testing.TB, operation string, ft core.FileType) uint64 {
	t.Helper()
	families, err := metrics.WorkerRegistry.Gather()
	if err != nil {
		t.Fatalf("gather worker metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != fsOpDurationMetric {
			continue
		}
		for _, m := range family.GetMetric() {
			labels := make(map[string]string, len(m.GetLabel()))
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			if labels["operation"] == operation && labels["file_type"] == ft.String() {
				return m.GetHistogram().GetSampleCount()
			}
		}
	}
	return 0
}
