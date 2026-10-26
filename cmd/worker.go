package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-redis/redis/v8"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/metrics"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/temporal"
	"github.com/sperano/puckdb/internal/worker/admin"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/worker"
)

// Worker queue type values accepted by the --worker-queue flag (config.FlagWorkerQueue).
// These select which Temporal task queue (temporal.QueueTasks / temporal.QueueAdmin)
// the worker polls, and are distinct from those queue name constants.
const (
	workerQueueTypeTasks = "tasks"
	workerQueueTypeAdmin = "admin"
)

// workerFlagGroups lists every flag group the worker command exposes.
// Defined once and shared by InitFlags (registration) and BindFlags
// (viper binding in PreRunE) so the two can never drift out of sync.
var workerFlagGroups = []*config.FlagGroup{
	&config.YahooOAuth2Flags,
	&config.YahooSeasonsFlags,
	&config.DataPathFlags,
	&config.RedisFlags,
	&config.PostgresFlags,
	&config.TemporalFlags,
	&config.TemporalRetryFlags,
	&config.WorkerPortFlags,
	&config.DownloadConcurrencyFlags,
	&config.YahooPlayerFlags,
	&config.GobCacheFlags,
	&config.YahooRateLimitFlags,
	&config.WorkerConcurrencyFlags,
	&config.PlayerLandingFlags,
	&config.ProcessPlayersFlags,
	&config.PlayerLogsFlags,
	&config.AssetFlags,
	&config.MauriceFlags,
	&config.NewsSourcesFlags,
	&config.NewsWorkerFlags,
	&config.NewsExtractModelFlags,
	&config.NewsExtractWorkerFlags,
	&config.DraftWorkerFlags,
}

func cmdWorker() *cobra.Command {
	var cmd = &cobra.Command{
		Use:     "worker",
		Short:   "Start Temporal worker",
		Long:    `Start the Temporal worker to process import workflows`,
		PreRunE: bindFlagsPreRunE(workerFlagGroups...),
		RunE:    runWorker,
	}
	flags := cmd.Flags()
	config.InitFlags(flags, workerFlagGroups...)
	return cmd
}

func runWorker(cmd *cobra.Command, _ []string) error {
	config.LogFlagValues()

	queueType := viper.GetString(config.FlagWorkerQueue)
	queueName, err := workerQueueName(queueType)
	if err != nil {
		return err
	}
	errCh := make(chan error, workerErrChCap(queueType))

	// Wire SIGINT/SIGTERM into a context so the non-Temporal resources
	// (metrics HTTP server, database pool construction) observe
	// shutdown. main() now delivers SIGINT/SIGTERM as cancellation of
	// cmd.Context() via ExecuteContext (see SignalContext); the local
	// NotifyContext here is kept so this command stays signal-correct
	// when executed without the wired root (tests, embedding), and it
	// composes harmlessly with the root's signal context otherwise.
	// The Temporal workers still drain via their own
	// worker.InterruptCh() below, which fires on the same signals, so
	// both shutdown paths agree.
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	startWorkerMetricsServer(ctx, errCh)

	pool, closePool, err := openWorkerPool(ctx)
	if err != nil {
		return err
	}
	defer closePool()

	// Create shared Redis client for activities
	redisClient := newRedisClient()
	defer closeOrWarn(redisClient, "redis client")

	tclient, closeTemporal, err := newWorkerTemporalClient()
	if err != nil {
		return err
	}
	defer closeTemporal()

	options := workerOptions()
	// Keeps an activity's progress in the heartbeats it sends
	// while waiting for the Yahoo rate limit.
	options.Interceptors = []interceptor.WorkerInterceptor{&shared.HeartbeatDetailsInterceptor{}}
	w := worker.New(tclient, queueName, options)

	if queueType == workerQueueTypeAdmin {
		registerAdminQueue(w, pool, redisClient)
	} else if err := startTasksQueue(ctx, tclient, w, pool, redisClient, errCh); err != nil {
		return err
	}

	go func() {
		errCh <- w.Run(worker.InterruptCh())
	}()
	return <-errCh
}

// workerQueueName maps a --worker-queue value to the Temporal task queue the
// worker polls.
func workerQueueName(queueType string) (string, error) {
	switch queueType {
	case workerQueueTypeTasks:
		return temporal.QueueTasks, nil
	case workerQueueTypeAdmin:
		return temporal.QueueAdmin, nil
	default:
		return "", fmt.Errorf("invalid queue type %q: must be %q or %q", queueType, workerQueueTypeTasks, workerQueueTypeAdmin)
	}
}

// workerErrChCap sizes the channel that receives the first fatal error from
// any goroutine (metrics server, main Temporal worker, asset worker if in
// tasks mode). Whichever dies first terminates the command, so a port-bind
// failure in the metrics server doesn't leave a worker running behind a dead
// /metrics and /health. Capacity must match goroutine count up front;
// reassignment after spawning the metrics goroutine would orphan its error
// channel.
func workerErrChCap(queueType string) int {
	const (
		errChCapAdmin = 2 // metrics + main worker
		errChCapTasks = 3 // metrics + main worker + asset worker
	)
	if queueType == workerQueueTypeTasks {
		return errChCapTasks
	}
	return errChCapAdmin
}

// startWorkerMetricsServer starts the metrics HTTP server (health check
// verifies JuiceFS mount). ctx carries SIGINT/SIGTERM so the server drains
// in-flight requests on shutdown instead of being hard-killed.
func startWorkerMetricsServer(ctx context.Context, errCh chan<- error) {
	metricsAddr := fmt.Sprintf(":%d", viper.GetInt(config.FlagWorkerPort))
	dataPath := viper.GetString(config.FlagDataPath)
	go func() {
		if err := metrics.StartWorkerServer(ctx, metricsAddr, dataPath); err != nil {
			errCh <- fmt.Errorf("metrics server: %w", err)
		}
	}()
}

// openWorkerPool opens the database pool shared by activities and registers
// its metrics collector; the returned func unregisters the collector and
// closes the pool. ctx is used only for pool construction (pgx does not
// retain it for the pool lifetime), so signal cancellation never aborts
// in-flight activity queries, which carry their own Temporal activity
// contexts.
func openWorkerPool(ctx context.Context) (*pgxpool.Pool, func(), error) {
	pool, err := openPGXPool(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("open database pool: %w", err)
	}
	poolCollector := metrics.NewPGXPoolCollector(pool)
	if err := metrics.WorkerRegistry.Register(poolCollector); err != nil {
		pool.Close()
		return nil, nil, fmt.Errorf("register database pool metrics: %w", err)
	}
	return pool, func() {
		metrics.WorkerRegistry.Unregister(poolCollector)
		pool.Close()
	}, nil
}

// newWorkerTemporalClient connects to Temporal reporting the SDK's own
// metrics (schedule-to-start latency, task slot gauges, poll counts) on the
// same registry/endpoint as the puckdb_* worker metrics, so queueing on this
// worker's task queues is visible in Prometheus. The returned func closes the
// client, then the metrics reporter.
func newWorkerTemporalClient() (client.Client, func(), error) {
	metricsHandler, closeMetrics := temporal.NewMetricsHandler(metrics.WorkerRegistry)
	tclient, err := newTemporalClient(metricsHandler)
	if err != nil {
		closeOrWarn(closeMetrics, "temporal metrics handler")
		return nil, nil, err
	}
	return tclient, func() {
		tclient.Close()
		closeOrWarn(closeMetrics, "temporal metrics handler")
	}, nil
}

// workerOptions returns the poller and execution limits shared by the main
// and asset workers.
func workerOptions() worker.Options {
	return worker.Options{
		MaxConcurrentWorkflowTaskPollers:       viper.GetInt(config.FlagWorkerMaxWorkflowPollers),
		MaxConcurrentActivityTaskPollers:       viper.GetInt(config.FlagWorkerMaxActivityPollers),
		MaxConcurrentWorkflowTaskExecutionSize: viper.GetInt(config.FlagWorkerMaxWorkflowExecution),
		MaxConcurrentActivityExecutionSize:     viper.GetInt(config.FlagWorkerMaxActivityExecution),
	}
}

// registerAdminQueue registers the lightweight admin operations, the only
// work of the admin queue.
func registerAdminQueue(w worker.Worker, pool *pgxpool.Pool, redisClient *redis.Client) {
	w.RegisterWorkflow(admin.DropDatabaseWorkflow)
	w.RegisterWorkflow(admin.MigrateDatabaseWorkflow)
	w.RegisterWorkflow(admin.ResetDatabaseWorkflow)
	w.RegisterWorkflow(admin.FlushRedisWorkflow)
	adminActivities := &admin.Activities{
		Pool: pool, Redis: redisClient, Conn: postgresConnFrom(viper.GetViper()),
	}
	w.RegisterActivity(adminActivities.DropDatabaseActivity)
	w.RegisterActivity(adminActivities.MigrateDatabaseActivity)
	w.RegisterActivity(adminActivities.FlushRedisActivity)
}

// startTasksQueue registers the main workloads on w and starts the asset
// worker, whose error reaches errCh.
func startTasksQueue(ctx context.Context, tclient client.Client, w worker.Worker, pool *pgxpool.Pool, redisClient *redis.Client, errCh chan<- error) error {
	registerTasksWorkflows(w)
	if err := ensureNewsSchedule(ctx, tclient.ScheduleClient(), viper.GetInt(config.FlagNewsScheduleMinutes)); err != nil {
		return err
	}
	if err := registerTasksActivities(w, pool, redisClient); err != nil {
		return err
	}

	// Asset worker: separate task queue so asset downloads cannot
	// starve the main puckdb-tasks queue (architectural delta 4).
	aw := worker.New(tclient, shared.TaskQueueAssets, workerOptions())
	registerAssetWorkflows(aw)
	registerAssetQueueActivities(aw, sqlcdb.New(pool), redisClient)

	go func() {
		errCh <- aw.Run(worker.InterruptCh())
	}()
	return nil
}
