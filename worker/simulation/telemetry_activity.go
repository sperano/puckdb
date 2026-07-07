package simulation

import (
	"context"
	"time"

	"github.com/sperano/puckdb/metrics"
	"go.temporal.io/sdk/activity"
)

// ============================================================================
// RecordDayDurationActivity — telemetry bridge.
//
// SimPoolWorkflow can't observe Prometheus metrics directly: the
// workflow goroutine must be deterministic for replay, and a metric
// observation is a non-deterministic side effect (the metrics
// registry's internal counters / histogram bins evolve over time).
//
// This activity is the side-effect bridge: the workflow captures
// day-start and day-end timestamps via workflow.Now (deterministic)
// and passes the duration to this activity, which then observes
// puckdb_sim_day_duration_seconds.
//
// Trivially small — no DB, no LLM. The Temporal round-trip cost is
// the only overhead, ~milliseconds, dwarfed by the day's actual
// activity work.
// ============================================================================

// RecordDayDurationInput is the payload. Duration is pre-computed
// in the workflow via workflow.Now(end).Sub(workflow.Now(start)) so
// the activity itself doesn't read wall-clock time. Seconds rather
// than time.Duration avoids nanosecond serialization quirks across
// SDK versions.
type RecordDayDurationInput struct {
	PoolID          int32   `json:"pool_id"`
	DurationSeconds float64 `json:"duration_seconds"`
}

// RecordDayDuration observes the day-duration histogram. Always
// returns nil error — a metrics observation failure shouldn't abort
// the workflow's day loop.
func (a *Activities) RecordDayDuration(ctx context.Context, in RecordDayDurationInput) error {
	activity.GetLogger(ctx).Debug("RecordDayDuration",
		"pool_id", in.PoolID,
		"duration_seconds", in.DurationSeconds,
	)
	metrics.ObserveSimDayDuration(time.Duration(in.DurationSeconds * float64(time.Second)))
	return nil
}
