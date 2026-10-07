package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-redis/redis/v8"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/httpx"
	"github.com/sperano/puckdb/internal/metrics"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/temporal"
	"github.com/sperano/puckdb/internal/worker/admin"
	"github.com/sperano/puckdb/internal/worker/asset"
	worknhl "github.com/sperano/puckdb/internal/worker/nhl"
	workplayer "github.com/sperano/puckdb/internal/worker/player"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/sperano/puckdb/internal/worker/simulation"
	"github.com/sperano/puckdb/internal/worker/workflow"
	"github.com/sperano/puckdb/internal/worker/yahoo"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.temporal.io/sdk/client"
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
	&config.YahooDownloadSleepFlags,
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
		Use:   "worker",
		Short: "Start Temporal worker",
		Long:  `Start the Temporal worker to process import workflows`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return config.BindFlags(cmd.Flags(), workerFlagGroups...)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			config.LogFlagValues()

			queueType := viper.GetString(config.FlagWorkerQueue)

			// Determine which queue to poll
			var queueName string
			switch queueType {
			case workerQueueTypeTasks:
				queueName = temporal.QueueTasks
			case workerQueueTypeAdmin:
				queueName = temporal.QueueAdmin
			default:
				return fmt.Errorf("invalid queue type %q: must be %q or %q", queueType, workerQueueTypeTasks, workerQueueTypeAdmin)
			}

			// errCh receives the first fatal error from any goroutine (metrics
			// server, main Temporal worker, asset worker if in tasks mode).
			// Whichever dies first terminates the command, so a port-bind failure
			// in the metrics server doesn't leave a worker running behind a dead
			// /metrics and /health. Capacity must match goroutine count up front;
			// reassignment after spawning the metrics goroutine would orphan its
			// error channel.
			const (
				errChCapAdmin = 2 // metrics + main worker
				errChCapTasks = 3 // metrics + main worker + asset worker
			)
			errChCap := errChCapAdmin
			if queueType == workerQueueTypeTasks {
				errChCap = errChCapTasks
			}
			errCh := make(chan error, errChCap)

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

			// Start metrics HTTP server (health check verifies JuiceFS mount).
			// ctx carries SIGINT/SIGTERM (wired above) so the server drains
			// in-flight requests on shutdown instead of being hard-killed.
			metricsAddr := fmt.Sprintf(":%d", viper.GetInt(config.FlagWorkerPort))
			dataPath := viper.GetString(config.FlagDataPath)
			go func() {
				if err := metrics.StartWorkerServer(ctx, metricsAddr, dataPath); err != nil {
					errCh <- fmt.Errorf("metrics server: %w", err)
				}
			}()

			// Open shared database pool for activities. ctx is used only for
			// pool construction (pgx does not retain it for the pool lifetime),
			// so signal cancellation here never aborts in-flight activity
			// queries, which carry their own Temporal activity contexts.
			pool, err := openPGXPool(ctx)
			if err != nil {
				return fmt.Errorf("open database pool: %w", err)
			}
			defer pool.Close()

			// Create shared Redis client for activities
			redisClient := newRedisClient()
			defer func() {
				if err := redisClient.Close(); err != nil {
					log.Warn().Err(err).Msg("failed to close redis client")
				}
			}()

			tclient, err := newTemporalClient()
			if err != nil {
				return err
			}
			defer tclient.Close()

			w := worker.New(tclient, queueName, worker.Options{
				MaxConcurrentWorkflowTaskPollers:       viper.GetInt(config.FlagWorkerMaxWorkflowPollers),
				MaxConcurrentActivityTaskPollers:       viper.GetInt(config.FlagWorkerMaxActivityPollers),
				MaxConcurrentWorkflowTaskExecutionSize: viper.GetInt(config.FlagWorkerMaxWorkflowExecution),
				MaxConcurrentActivityExecutionSize:     viper.GetInt(config.FlagWorkerMaxActivityExecution),
			})

			// Register workflows and activities based on queue type
			if queueType == workerQueueTypeAdmin {
				// Admin queue: lightweight admin operations only
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
			} else {
				// Tasks queue: main workloads
				registerTasksWorkflows(w)
				if err := ensureNewsSchedule(ctx, tclient.ScheduleClient(), viper.GetInt(config.FlagNewsScheduleMinutes)); err != nil {
					return err
				}
				if err := registerTasksActivities(w, pool, redisClient, tclient); err != nil {
					return err
				}

				// Asset worker: separate task queue so asset downloads cannot
				// starve the main puckdb-tasks queue (architectural delta 4).
				aw := worker.New(tclient, shared.TaskQueueAssets, worker.Options{
					MaxConcurrentWorkflowTaskPollers:       viper.GetInt(config.FlagWorkerMaxWorkflowPollers),
					MaxConcurrentActivityTaskPollers:       viper.GetInt(config.FlagWorkerMaxActivityPollers),
					MaxConcurrentWorkflowTaskExecutionSize: viper.GetInt(config.FlagWorkerMaxWorkflowExecution),
					MaxConcurrentActivityExecutionSize:     viper.GetInt(config.FlagWorkerMaxActivityExecution),
				})
				queries := sqlcdb.New(pool)
				registerAssetWorkflows(aw)
				registerAssetActivities(aw, queries)
				registerProgressActivities(aw, redisClient)

				go func() {
					errCh <- aw.Run(worker.InterruptCh())
				}()
			}

			go func() {
				errCh <- w.Run(worker.InterruptCh())
			}()
			return <-errCh
		},
	}
	flags := cmd.Flags()
	config.InitFlags(flags, workerFlagGroups...)
	return cmd
}

// registerTasksWorkflows registers all workflows for the tasks queue.
func registerTasksWorkflows(w worker.Worker) {
	// Fetch workflows
	w.RegisterWorkflow(workflow.FetchSeasonsWorkflow)
	w.RegisterWorkflow(workflow.FetchNHLSeasonWorkflow)
	w.RegisterWorkflow(workflow.FetchYahooSeasonWorkflow)
	w.RegisterWorkflow(workflow.FetchPlayerLogsWorkflow)
	w.RegisterWorkflow(workflow.FetchSeasonPlayerLogsWorkflow)
	w.RegisterWorkflow(workflow.FetchYahooPlayersWorkflow)
	w.RegisterWorkflow(workflow.FetchPlayerLandingsWorkflow)
	w.RegisterWorkflow(workflow.FetchEdgeSeasonsWorkflow)
	w.RegisterWorkflow(workflow.FetchEdgeWorkflow)

	// Import workflows
	w.RegisterWorkflow(workflow.ImportSeasonsWorkflow)
	w.RegisterWorkflow(workflow.ImportPlayerLogsWorkflow)
	w.RegisterWorkflow(workflow.ImportNHLSeasonWorkflow)
	w.RegisterWorkflow(workflow.ImportYahooSeasonWorkflow)
	w.RegisterWorkflow(workflow.ImportSeasonPlayerLogsWorkflow)
	w.RegisterWorkflow(workflow.ImportEdgeSeasonsWorkflow)
	w.RegisterWorkflow(workflow.ImportEdgeWorkflow)

	// Processing workflows
	w.RegisterWorkflow(workflow.ProcessPlayersWorkflow)
	w.RegisterWorkflow(workflow.ProcessPlayersWorkflowContinue)
	w.RegisterWorkflow(workflow.InitializeWorkflow)
	w.RegisterWorkflow(workflow.ExtractBoxscorePlayersWorkflow)

	// Simulation workflow
	w.RegisterWorkflow(simulation.SimPoolWorkflow)

	// Player news and rankings (draft helper)
	w.RegisterWorkflow(workflow.RefreshNewsWorkflow)
	w.RegisterWorkflow(workflow.RefreshDraftRankingsWorkflow)
}

// taskActivityDeps holds the dependencies shared by every per-domain
// activity registration helper below. Building it once in
// registerTasksActivities and threading it through avoids repeating the
// same five-argument signature on each helper.
type taskActivityDeps struct {
	storage         store.Storage
	pool            *pgxpool.Pool
	queries         *sqlcdb.Queries
	nhlClient       shared.NHLClient
	gobCache        *cache.GobCache
	redisClient     *redis.Client
	yahooDownloader shared.Downloader
}

// registerTasksActivities registers all activities for the tasks queue.
// tclient is needed by simulation.Activities to signal the parent
// SimPoolWorkflow when the LLM cost cap trips.
func registerTasksActivities(w worker.Worker, pool *pgxpool.Pool, redisClient *redis.Client, tclient client.Client) error {
	gobCache, err := newGobCache(redisClient)
	if err != nil {
		return err
	}

	d := taskActivityDeps{
		storage:         newDefaultStorage(),
		pool:            pool,
		queries:         sqlcdb.New(pool),
		nhlClient:       shared.NewNHLClient(),
		gobCache:        gobCache,
		redisClient:     redisClient,
		yahooDownloader: shared.NewYahooDownloader(redisClient),
	}

	registerSimulationActivities(w, pool, d.queries, tclient)
	registerProgressActivities(w, redisClient)
	registerYahooFetchActivities(w, d)

	importYahooActivities := registerYahooImportActivities(w, d)
	registerNHLActivities(w, d, importYahooActivities)
	registerPlayerActivities(w, d)
	registerNewsActivities(w, pool, d.queries)
	registerDraftRankingActivities(w, pool)

	return nil
}

// registerSimulationActivities registers the simulation pool activities.
// See worker/simulation. AgentFactory is nil so each activity falls back
// to defaultAgentFactory (NewAgent).
func registerSimulationActivities(w worker.Worker, pool *pgxpool.Pool, queries *sqlcdb.Queries, tclient client.Client) {
	simActs := &simulation.Activities{
		Queries:         queries,
		Tx:              simulation.NewPgxTransactor(pool),
		Signaler:        simulation.NewTemporalSignaler(tclient),
		ProviderConfigs: configuredLLMProviders(),
	}
	w.RegisterActivity(simActs.LoadPoolState)
	w.RegisterActivity(simActs.LoadDraftCandidates)
	w.RegisterActivity(simActs.DraftPick)
	w.RegisterActivity(simActs.ProcessWaivers)
	w.RegisterActivity(simActs.BuildFreeAgentPool)
	w.RegisterActivity(simActs.BuildManageRosterContext)
	w.RegisterActivity(simActs.ManageRoster)
	w.RegisterActivity(simActs.CollectDayStats)
	w.RegisterActivity(simActs.UpdateStandings)
	w.RegisterActivity(simActs.SetPoolStatus)
	w.RegisterActivity(simActs.RecordDraftOrder)
	w.RegisterActivity(simActs.PickTeamName)
	w.RegisterActivity(simActs.RecordDayDuration)
}

// registerYahooFetchActivities registers the Yahoo Fantasy API download activities.
func registerYahooFetchActivities(w worker.Worker, d taskActivityDeps) {
	fetchYahooActivities := &yahoo.FetchActivities{
		Storage:          d.storage,
		Download:         d.yahooDownloader,
		GobCache:         d.gobCache,
		PublicDownloader: yahoo.HTTPDownloaderFunc(httpx.DownloadPublic),
	}
	w.RegisterActivity(fetchYahooActivities.FetchLeague)
	w.RegisterActivity(fetchYahooActivities.FetchTeams)
	w.RegisterActivity(fetchYahooActivities.FetchYahooPlayerBatch)
	w.RegisterActivity(fetchYahooActivities.FetchYahooTransactions)
	w.RegisterActivity(fetchYahooActivities.FetchYahooDraftResults)
	w.RegisterActivity(fetchYahooActivities.FetchYahooMatchupWeek)
	w.RegisterActivity(fetchYahooActivities.PlanYahooLeaguePlayerPool)
	w.RegisterActivity(fetchYahooActivities.FetchYahooLeaguePlayersPage)
	w.RegisterActivity(fetchYahooActivities.CommitYahooLeaguePlayerPool)
}

// registerYahooImportActivities registers the Yahoo cached-file-to-Postgres
// import activities. Returns the activities struct since worknhl.ImportActivities
// embeds it as its Yahoo field.
func registerYahooImportActivities(w worker.Worker, d taskActivityDeps) *yahoo.ImportActivities {
	importYahooActivities := &yahoo.ImportActivities{
		Storage:  d.storage,
		GobCache: d.gobCache,
		Queries:  d.queries,
	}
	w.RegisterActivity(importYahooActivities.ImportYahooLeague)
	w.RegisterActivity(importYahooActivities.ImportYahooStandInLeague) // TEMPORARY: see config.LeagueMetadataSource
	w.RegisterActivity(importYahooActivities.ImportYahooTeams)
	w.RegisterActivity(importYahooActivities.ImportYahooDataForDate)
	w.RegisterActivity(importYahooActivities.ImportYahooLeagueData)
	w.RegisterActivity(importYahooActivities.ImportYahooLeaguePlayers)
	return importYahooActivities
}

// registerNHLActivities registers all NHL-domain activities: franchises,
// seasons/Edge tracking, cached-file import, daily schedule, playoffs, and
// boxscore extraction. importYahooActivities is threaded in because
// worknhl.ImportActivities delegates Yahoo-specific import steps to it.
func registerNHLActivities(w worker.Worker, d taskActivityDeps, importYahooActivities *yahoo.ImportActivities) {
	franchiseActivities := &worknhl.FranchiseActivities{
		Storage:   d.storage,
		GobCache:  d.gobCache,
		Upserter:  d.queries,
		NHLClient: d.nhlClient,
	}
	w.RegisterActivity(franchiseActivities.FetchFranchises)
	w.RegisterActivity(franchiseActivities.UpsertFranchises)

	seasonsActivities := &worknhl.SeasonsActivities{
		Storage:             d.storage,
		GobCache:            d.gobCache,
		NHLClient:           d.nhlClient,
		SeasonsUpserter:     d.queries,
		SeasonTeamsUpserter: d.queries,
		RosterQueries:       d.queries,
		UpcomingQueries:     d.queries,
		ClubStatsQueries:    d.queries,
		EdgeQueries:         d.queries,
		RedisClient:         d.redisClient,
	}
	w.RegisterActivity(seasonsActivities.FetchSeasonsManifest)
	w.RegisterActivity(seasonsActivities.UpsertSeasons)
	w.RegisterActivity(seasonsActivities.InitializeSeasonTeamsActivity)
	w.RegisterActivity(seasonsActivities.FetchSeasonRosters)
	w.RegisterActivity(seasonsActivities.FetchClubStats)
	w.RegisterActivity(seasonsActivities.ImportSeasonRosters)
	w.RegisterActivity(seasonsActivities.FetchUpcomingSeasonRosters)
	w.RegisterActivity(seasonsActivities.ImportUpcomingSeasonRosters)
	w.RegisterActivity(seasonsActivities.ImportClubStats)
	w.RegisterActivity(seasonsActivities.FetchEdgeLandings)
	w.RegisterActivity(seasonsActivities.GetEdgeSeasonTeams)
	w.RegisterActivity(seasonsActivities.FetchEdgeTeam)
	w.RegisterActivity(seasonsActivities.FetchEdgeTeamSkaters)
	w.RegisterActivity(seasonsActivities.FetchEdgeTeamGoalies)
	w.RegisterActivity(seasonsActivities.ImportEdgeSkaters)
	w.RegisterActivity(seasonsActivities.ImportEdgeGoalies)
	w.RegisterActivity(seasonsActivities.ImportEdgeTeams)
	w.RegisterActivity(seasonsActivities.ImportEdgeTeamZoneTimeDetails)
	w.RegisterActivity(seasonsActivities.ImportEdgeTeam)
	w.RegisterActivity(seasonsActivities.ImportEdgeTeamSkaters)
	w.RegisterActivity(seasonsActivities.ImportEdgeTeamGoalies)

	importActivities := &worknhl.ImportActivities{
		Storage:  d.storage,
		GobCache: d.gobCache,
		Queries:  d.queries,
		Tx:       worknhl.NewPgxTransactor(d.pool),
		Yahoo:    importYahooActivities,
	}
	w.RegisterActivity(importActivities.ImportDay)
	w.RegisterActivity(importActivities.ImportBoxscoresForDate)
	w.RegisterActivity(importActivities.ImportGameStoryForDate)
	w.RegisterActivity(importActivities.ImportPlayByPlayForDate)
	w.RegisterActivity(importActivities.ImportShiftChartForDate)
	w.RegisterActivity(importActivities.ImportStandingsForDate)
	w.RegisterActivity(importActivities.ImportPlayerGameLogsBatch)
	w.RegisterActivity(importActivities.CollectSeasonPlayerIDs)

	dailyScheduleActivities := &worknhl.DailyScheduleActivities{
		Storage:     d.storage,
		NHLClient:   d.nhlClient,
		GobCache:    d.gobCache,
		RedisClient: d.redisClient,
		Download:    d.yahooDownloader,
	}
	w.RegisterActivity(dailyScheduleActivities.FetchDailySchedule)
	w.RegisterActivity(dailyScheduleActivities.FetchDay)

	playoffActivities := &worknhl.PlayoffActivities{
		Storage:   d.storage,
		GobCache:  d.gobCache,
		NHLClient: d.nhlClient,
		Teams:     d.queries,
		Queries:   d.queries,
	}
	w.RegisterActivity(playoffActivities.ListSeasonTeams)
	w.RegisterActivity(playoffActivities.FetchTeamPlayoffGames)
	w.RegisterActivity(playoffActivities.ImportTeamPlayoffGames)

	boxscoreActivities := &worknhl.BoxscoreActivities{
		Storage:     d.storage,
		GobCache:    d.gobCache,
		RedisClient: d.redisClient,
	}
	w.RegisterActivity(boxscoreActivities.ExtractBoxscoreDataForSeason)
	w.RegisterActivity(boxscoreActivities.ExtractAndSaveBoxscorePlayers)
}

// registerPlayerActivities registers player-domain activities: landing page
// fetch, game log download/import, boxscore-player consolidation, and
// Yahoo/NHL player matching.
func registerPlayerActivities(w worker.Worker, d taskActivityDeps) {
	playerActivities := &workplayer.Activities{
		Storage:       d.storage,
		NHLClient:     d.nhlClient,
		RedisClient:   d.redisClient,
		GobCache:      d.gobCache,
		Queries:       d.queries,
		CareerQueries: d.queries,
	}
	w.RegisterActivity(playerActivities.FetchPlayerLandingsBatch)
	w.RegisterActivity(playerActivities.DownloadPlayerGameLogsBatch)
	w.RegisterActivity(playerActivities.LoadSeasonBoxscorePlayers)
	w.RegisterActivity(playerActivities.LoadAllBoxscorePlayers)
	w.RegisterActivity(playerActivities.ConsolidateBoxscorePlayersActivity)
	w.RegisterActivity(playerActivities.CountPlayersForAllSeasons)
	w.RegisterActivity(playerActivities.ProcessPlayerBatch)
	w.RegisterActivity(playerActivities.ListYahooPlayerFiles)
	w.RegisterActivity(playerActivities.ParseYahooPlayerBatch)
	w.RegisterActivity(playerActivities.SaveYahooPlayersToRedis)
	w.RegisterActivity(playerActivities.LoadUnmatchedYahooPlayers)
	w.RegisterActivity(playerActivities.VerifyUnmatchedBatch)
	w.RegisterActivity(playerActivities.CleanupYahooIDPoolData)
}

// registerAssetWorkflows registers all asset download workflows on the given
// worker (expected to be polling shared.TaskQueueAssets). The generic
// FetchAssetsClassWorkflow is intentionally not registered: it is invoked as a
// regular Go function from the nine entry-point wrappers, never via
// ExecuteChildWorkflow, so Temporal does not need to resolve it by name.
func registerAssetWorkflows(w worker.Worker) {
	// Parent: orchestrates the nine class children
	w.RegisterWorkflow(workflow.FetchAssetsWorkflow)

	// Per-class entry-point wrappers — each is a distinct workflow type in the Temporal UI
	w.RegisterWorkflow(workflow.FetchPlayerHeadshotsWorkflow)
	w.RegisterWorkflow(workflow.FetchPlayerHeroImagesWorkflow)
	w.RegisterWorkflow(workflow.FetchPlayerYahooImagesWorkflow)
	w.RegisterWorkflow(workflow.FetchTeamLogosWorkflow)
	w.RegisterWorkflow(workflow.FetchYahooTeamLogosWorkflow)
	w.RegisterWorkflow(workflow.FetchYahooLeagueLogosWorkflow)
	w.RegisterWorkflow(workflow.FetchYahooManagerImagesWorkflow)
}

// registerProgressActivities registers the ProgressReport Save/Load/DeleteBatch
// local activities on the given worker. Every worker that runs a workflow
// invoking shared.InitTracker (or any other progress-tracker call) needs these
// registered, otherwise local-activity dispatch falls through to the typed-nil
// receiver baked into the call site and panics on a.RedisClient.
func registerProgressActivities(w worker.Worker, redisClient *redis.Client) {
	progressActivities := &shared.ProgressActivities{RedisClient: redisClient}
	w.RegisterActivity(progressActivities.Save)
	w.RegisterActivity(progressActivities.Load)
	w.RegisterActivity(progressActivities.DeleteBatch)
}

// registerAssetActivities registers all asset download and query activities on
// the given worker.
func registerAssetActivities(w worker.Worker, queries *sqlcdb.Queries) {
	storage := newDefaultStorage()
	assetActivities := &asset.Activities{
		Storage:  storage,
		Download: asset.NewDownloader(),
		Queries:  queries,
	}

	// Download activity
	w.RegisterActivity(assetActivities.FetchAssetBatch)

	// Query (loader) activities — one per asset class
	w.RegisterActivity(assetActivities.LoadPlayerHeadshotAssets)
	w.RegisterActivity(assetActivities.LoadPlayerHeroImageAssets)
	w.RegisterActivity(assetActivities.LoadPlayerYahooImageAssets)
	w.RegisterActivity(assetActivities.LoadTeamLogoAssets)
	w.RegisterActivity(assetActivities.LoadYahooTeamLogoAssets)
	w.RegisterActivity(assetActivities.LoadYahooLeagueLogoAssets)
	w.RegisterActivity(assetActivities.LoadYahooManagerImageAssets)

	// Count activities — one per asset class. Used by the parent
	// FetchAssetsWorkflow to size its progress bars before dispatch.
	w.RegisterActivity(assetActivities.CountPlayerHeadshotAssets)
	w.RegisterActivity(assetActivities.CountPlayerHeroImageAssets)
	w.RegisterActivity(assetActivities.CountPlayerYahooImageAssets)
	w.RegisterActivity(assetActivities.CountTeamLogoAssets)
	w.RegisterActivity(assetActivities.CountYahooTeamLogoAssets)
	w.RegisterActivity(assetActivities.CountYahooLeagueLogoAssets)
	w.RegisterActivity(assetActivities.CountYahooManagerImageAssets)
}
