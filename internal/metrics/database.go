package metrics

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	sqlcQueryPrefix = "-- name: "

	poolStateAcquired     = "acquired"
	poolStateConstructing = "constructing"
	poolStateIdle         = "idle"
	poolStateMax          = "max"
	poolStateTotal        = "total"

	destroyReasonIdle     = "idle"
	destroyReasonLifetime = "lifetime"
)

type queryTraceKey struct{}
type batchTraceKey struct{}

type queryTrace struct {
	name    string
	started time.Time
}

type batchTrace struct {
	started time.Time
}

// SQLCQueryTracer records timings for pgx queries that retain sqlc's leading
// "-- name:" comment. Raw SQL is ignored so labels stay bounded.
type SQLCQueryTracer struct {
	now func() time.Time
}

// NewSQLCQueryTracer returns a pgx tracer for sqlc query durations.
func NewSQLCQueryTracer() *SQLCQueryTracer {
	return &SQLCQueryTracer{now: time.Now}
}

func (t *SQLCQueryTracer) TraceQueryStart(
	ctx context.Context,
	_ *pgx.Conn,
	data pgx.TraceQueryStartData,
) context.Context {
	name, ok := sqlcQueryName(data.SQL)
	if !ok {
		return ctx
	}
	return context.WithValue(ctx, queryTraceKey{}, queryTrace{name: name, started: t.now()})
}

func (t *SQLCQueryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	trace, ok := ctx.Value(queryTraceKey{}).(queryTrace)
	if ok {
		ObserveDBQueryDuration(trace.name, t.now().Sub(trace.started))
	}
}

func (t *SQLCQueryTracer) TraceBatchStart(
	ctx context.Context,
	_ *pgx.Conn,
	_ pgx.TraceBatchStartData,
) context.Context {
	return context.WithValue(ctx, batchTraceKey{}, &batchTrace{started: t.now()})
}

func (t *SQLCQueryTracer) TraceBatchQuery(ctx context.Context, _ *pgx.Conn, data pgx.TraceBatchQueryData) {
	trace, ok := ctx.Value(batchTraceKey{}).(*batchTrace)
	if !ok {
		return
	}
	now := t.now()
	if name, named := sqlcQueryName(data.SQL); named {
		ObserveDBQueryDuration(name, now.Sub(trace.started))
	}
	trace.started = now
}

func (t *SQLCQueryTracer) TraceBatchEnd(context.Context, *pgx.Conn, pgx.TraceBatchEndData) {}

func sqlcQueryName(sql string) (string, bool) {
	firstLine, _, _ := strings.Cut(sql, "\n")
	if !strings.HasPrefix(firstLine, sqlcQueryPrefix) {
		return "", false
	}
	fields := strings.Fields(strings.TrimPrefix(firstLine, sqlcQueryPrefix))
	if len(fields) < 2 || !strings.HasPrefix(fields[1], ":") {
		return "", false
	}
	return fields[0], true
}

// PGXPoolCollector exports the current pgxpool.Stat snapshot at scrape time.
type PGXPoolCollector struct {
	pool             *pgxpool.Pool
	connections      *prometheus.Desc
	acquires         *prometheus.Desc
	acquireDuration  *prometheus.Desc
	emptyAcquires    *prometheus.Desc
	emptyAcquireWait *prometheus.Desc
	canceledAcquires *prometheus.Desc
	newConnections   *prometheus.Desc
	destroyedConns   *prometheus.Desc
}

// NewPGXPoolCollector creates a collector for one process-local pgx pool.
func NewPGXPoolCollector(pool *pgxpool.Pool) *PGXPoolCollector {
	return &PGXPoolCollector{
		pool:             pool,
		connections:      prometheus.NewDesc("puckdb_db_pool_connections", "Current PostgreSQL pool connections by state", []string{"state"}, nil),
		acquires:         prometheus.NewDesc("puckdb_db_pool_acquires_total", "Total successful PostgreSQL pool acquires", nil, nil),
		acquireDuration:  prometheus.NewDesc("puckdb_db_pool_acquire_duration_seconds_total", "Total time spent acquiring PostgreSQL pool connections", nil, nil),
		emptyAcquires:    prometheus.NewDesc("puckdb_db_pool_empty_acquires_total", "Total PostgreSQL pool acquires that waited for a connection", nil, nil),
		emptyAcquireWait: prometheus.NewDesc("puckdb_db_pool_empty_acquire_wait_seconds_total", "Total time spent waiting because the PostgreSQL pool was empty", nil, nil),
		canceledAcquires: prometheus.NewDesc("puckdb_db_pool_canceled_acquires_total", "Total canceled PostgreSQL pool acquires", nil, nil),
		newConnections:   prometheus.NewDesc("puckdb_db_pool_new_connections_total", "Total PostgreSQL connections created by the pool", nil, nil),
		destroyedConns:   prometheus.NewDesc("puckdb_db_pool_destroyed_connections_total", "Total PostgreSQL connections destroyed by reason", []string{"reason"}, nil),
	}
}

func (c *PGXPoolCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.connections
	ch <- c.acquires
	ch <- c.acquireDuration
	ch <- c.emptyAcquires
	ch <- c.emptyAcquireWait
	ch <- c.canceledAcquires
	ch <- c.newConnections
	ch <- c.destroyedConns
}

func (c *PGXPoolCollector) Collect(ch chan<- prometheus.Metric) {
	stat := c.pool.Stat()
	c.collectConnections(ch, stat)
	c.collectCounters(ch, stat)
}

func (c *PGXPoolCollector) collectConnections(ch chan<- prometheus.Metric, stat *pgxpool.Stat) {
	values := []struct {
		state string
		value int32
	}{
		{poolStateAcquired, stat.AcquiredConns()},
		{poolStateConstructing, stat.ConstructingConns()},
		{poolStateIdle, stat.IdleConns()},
		{poolStateMax, stat.MaxConns()},
		{poolStateTotal, stat.TotalConns()},
	}
	for _, value := range values {
		ch <- prometheus.MustNewConstMetric(c.connections, prometheus.GaugeValue, float64(value.value), value.state)
	}
}

func (c *PGXPoolCollector) collectCounters(ch chan<- prometheus.Metric, stat *pgxpool.Stat) {
	ch <- prometheus.MustNewConstMetric(c.acquires, prometheus.CounterValue, float64(stat.AcquireCount()))
	ch <- prometheus.MustNewConstMetric(c.acquireDuration, prometheus.CounterValue, stat.AcquireDuration().Seconds())
	ch <- prometheus.MustNewConstMetric(c.emptyAcquires, prometheus.CounterValue, float64(stat.EmptyAcquireCount()))
	ch <- prometheus.MustNewConstMetric(c.emptyAcquireWait, prometheus.CounterValue, stat.EmptyAcquireWaitTime().Seconds())
	ch <- prometheus.MustNewConstMetric(c.canceledAcquires, prometheus.CounterValue, float64(stat.CanceledAcquireCount()))
	ch <- prometheus.MustNewConstMetric(c.newConnections, prometheus.CounterValue, float64(stat.NewConnsCount()))
	ch <- prometheus.MustNewConstMetric(c.destroyedConns, prometheus.CounterValue, float64(stat.MaxIdleDestroyCount()), destroyReasonIdle)
	ch <- prometheus.MustNewConstMetric(c.destroyedConns, prometheus.CounterValue, float64(stat.MaxLifetimeDestroyCount()), destroyReasonLifetime)
}
