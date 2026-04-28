package cmd

import (
	"context"
	"fmt"

	"github.com/go-redis/redis/v8"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/database"
	"github.com/sperano/puckdb/httpx"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/sperano/puckdb/store"
	"github.com/sperano/puckdb/temporal"
	"github.com/sperano/puckdb/worker/admin"
	"github.com/sperano/puckdb/worker/asset"
	worknhl "github.com/sperano/puckdb/worker/nhl"
	workplayer "github.com/sperano/puckdb/worker/player"
	"github.com/sperano/puckdb/worker/shared"
	"github.com/sperano/puckdb/worker/workflow"
	"github.com/sperano/puckdb/worker/yahoo"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.temporal.io/sdk/worker"
)

func cmdWorker() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "worker",
		Short: "Start Temporal worker",
		Long:  `Start the Temporal worker to process import workflows`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return config.BindFlags(cmd.Flags(),
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
			)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			config.LogFlagValues()

			queueType := viper.GetString(config.FlagWorkerQueue)

			// Determine which queue to poll
			var queueName string
			switch queueType {
			case "tasks":
				queueName = temporal.QueueTasks
			case "admin":
				queueName = temporal.QueueAdmin
			default:
				return fmt.Errorf("invalid queue type %q: must be 'tasks' or 'admin'", queueType)
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
			if queueType == "tasks" {
				errChCap = errChCapTasks
			}
			errCh := make(chan error, errChCap)

			// Start metrics HTTP server (health check verifies JuiceFS mount).
			// cmd.Context() carries the cobra-wired SIGINT/SIGTERM signal so the
			// server drains in-flight requests on shutdown instead of being
			// hard-killed.
			metricsAddr := fmt.Sprintf(":%d", viper.GetInt(config.FlagWorkerPort))
			dataPath := viper.GetString(config.FlagDataPath)
			go func() {
				if err := metrics.StartWorkerServer(cmd.Context(), metricsAddr, dataPath); err != nil {
					errCh <- fmt.Errorf("metrics server: %w", err)
				}
			}()

			// Open shared database pool for activities
			ctx := context.Background()
			pool, err := database.OpenPGXPool(ctx)
			if err != nil {
				return fmt.Errorf("open database pool: %w", err)
			}
			defer pool.Close()

			// Create shared Redis client for activities
			redisClient := cache.NewClient()
			defer func() {
				if err := redisClient.Close(); err != nil {
					log.Warn().Err(err).Msg("failed to close redis client")
				}
			}()

			tclient, err := temporal.NewClient()
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
			if queueType == "admin" {
				// Admin queue: lightweight admin operations only
				w.RegisterWorkflow(admin.DropDatabaseWorkflow)
				w.RegisterWorkflow(admin.MigrateDatabaseWorkflow)
				w.RegisterWorkflow(admin.ResetDatabaseWorkflow)
				w.RegisterWorkflow(admin.FlushRedisWorkflow)
				w.RegisterActivity(admin.DropDatabaseActivity)
				w.RegisterActivity(admin.MigrateDatabaseActivity)
				w.RegisterActivity(admin.FlushRedisActivity)
			} else {
				// Tasks queue: main workloads
				registerTasksWorkflows(w)
				if err := registerTasksActivities(w, pool, redisClient); err != nil {
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

				fmt.Printf("Starting asset worker on queue %q\n", shared.TaskQueueAssets)
				go func() {
					errCh <- aw.Run(worker.InterruptCh())
				}()
			}

			fmt.Printf("Starting worker on queue %q\n", queueName)
			go func() {
				errCh <- w.Run(worker.InterruptCh())
			}()
			return <-errCh
		},
	}
	flags := cmd.Flags()
	config.InitFlags(flags,
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
	)
	return cmd
}

// registerTasksWorkflows registers all workflows for the tasks queue.
func registerTasksWorkflows(w worker.Worker) {
	// Fetch workflows
	w.RegisterWorkflow(workflow.FetchSeasonsWorkflow)
	w.RegisterWorkflow(workflow.FetchSeasonWorkflow)
	w.RegisterWorkflow(workflow.FetchPlayerLogsWorkflow)
	w.RegisterWorkflow(workflow.FetchSeasonPlayerLogsWorkflow)
	w.RegisterWorkflow(workflow.FetchYahooPlayersWorkflow)
	w.RegisterWorkflow(workflow.FetchPlayerLandingsWorkflow)
	w.RegisterWorkflow(workflow.FetchEdgeSeasonsWorkflow)
	w.RegisterWorkflow(workflow.FetchEdgeWorkflow)

	// Import workflows
	w.RegisterWorkflow(workflow.ImportSeasonsWorkflow)
	w.RegisterWorkflow(workflow.ImportPlayerLogsWorkflow)
	w.RegisterWorkflow(workflow.ImportSeasonWorkflow)
	w.RegisterWorkflow(workflow.ImportSeasonPlayerLogsWorkflow)
	w.RegisterWorkflow(workflow.ImportEdgeSeasonsWorkflow)
	w.RegisterWorkflow(workflow.ImportEdgeWorkflow)

	// Processing workflows
	w.RegisterWorkflow(workflow.ProcessPlayersWorkflow)
	w.RegisterWorkflow(workflow.ProcessPlayersWorkflowContinue)
	w.RegisterWorkflow(workflow.InitializeWorkflow)
	w.RegisterWorkflow(workflow.ExtractBoxscorePlayersWorkflow)
}

// registerTasksActivities registers all activities for the tasks queue.
func registerTasksActivities(w worker.Worker, pool *pgxpool.Pool, redisClient *redis.Client) error {
	storage := store.NewDefaultStorage()
	queries := sqlcdb.New(pool)
	nhlClient := shared.NewNHLClient()
	gobCache, err := newGobCache(redisClient)
	if err != nil {
		return err
	}

	yahooDownloader := shared.NewYahooDownloader(redisClient)

	registerProgressActivities(w, redisClient)

	// Yahoo fetch activities
	fetchYahooActivities := &yahoo.FetchActivities{
		Storage:          storage,
		Download:         yahooDownloader,
		GobCache:         gobCache,
		PublicDownloader: yahoo.HTTPDownloaderFunc(httpx.DownloadPublic),
	}
	w.RegisterActivity(fetchYahooActivities.FetchLeague)
	w.RegisterActivity(fetchYahooActivities.FetchTeams)
	w.RegisterActivity(fetchYahooActivities.FetchYahooPlayerBatch)
	w.RegisterActivity(fetchYahooActivities.FetchYahooLeagueData)

	// Franchise activities
	franchiseActivities := &worknhl.FranchiseActivities{
		Storage:   storage,
		GobCache:  gobCache,
		Upserter:  queries,
		NHLClient: nhlClient,
	}
	w.RegisterActivity(franchiseActivities.FetchFranchises)
	w.RegisterActivity(franchiseActivities.UpsertFranchises)

	// Seasons activities
	seasonsActivities := &worknhl.SeasonsActivities{
		Storage:             storage,
		GobCache:            gobCache,
		NHLClient:           nhlClient,
		SeasonsUpserter:     queries,
		SeasonTeamsUpserter: queries,
		RosterQueries:       queries,
		ClubStatsQueries:    queries,
		EdgeQueries:         queries,
		RedisClient:         redisClient,
	}
	w.RegisterActivity(seasonsActivities.FetchSeasonsManifest)
	w.RegisterActivity(seasonsActivities.UpsertSeasons)
	w.RegisterActivity(seasonsActivities.InitializeSeasonTeamsActivity)
	w.RegisterActivity(seasonsActivities.FetchSeasonRosters)
	w.RegisterActivity(seasonsActivities.FetchClubStats)
	w.RegisterActivity(seasonsActivities.ImportSeasonRosters)
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

	// Yahoo import activities
	importYahooActivities := &yahoo.ImportActivities{
		Storage:  storage,
		GobCache: gobCache,
		Queries:  queries,
	}
	w.RegisterActivity(importYahooActivities.ImportYahooLeague)
	w.RegisterActivity(importYahooActivities.ImportYahooTeams)
	w.RegisterActivity(importYahooActivities.ImportYahooDataForDate)
	w.RegisterActivity(importYahooActivities.ImportYahooLeagueData)

	// Import activities
	importActivities := &worknhl.ImportActivities{
		Storage:  storage,
		GobCache: gobCache,
		Queries:  queries,
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

	// Daily schedule activities
	dailyScheduleActivities := &worknhl.DailyScheduleActivities{
		Storage:     storage,
		NHLClient:   nhlClient,
		GobCache:    gobCache,
		RedisClient: redisClient,
		Download:    yahooDownloader,
	}
	w.RegisterActivity(dailyScheduleActivities.FetchDailySchedule)
	w.RegisterActivity(dailyScheduleActivities.FetchDay)

	// Playoff activities
	playoffActivities := &worknhl.PlayoffActivities{
		Storage:   storage,
		GobCache:  gobCache,
		NHLClient: nhlClient,
		Teams:     queries,
		Queries:   queries,
	}
	w.RegisterActivity(playoffActivities.FetchPlayoffGames)
	w.RegisterActivity(playoffActivities.ImportPlayoffGames)

	// Boxscore extraction activities
	boxscoreActivities := &worknhl.BoxscoreActivities{
		Storage:     storage,
		GobCache:    gobCache,
		RedisClient: redisClient,
	}
	w.RegisterActivity(boxscoreActivities.ExtractBoxscoreDataForSeason)
	w.RegisterActivity(boxscoreActivities.ExtractAndSaveBoxscorePlayers)
	w.RegisterActivity(workplayer.ConsolidateBoxscorePlayersActivity)

	// Player activities
	playerActivities := &workplayer.Activities{
		Storage:       storage,
		NHLClient:     nhlClient,
		RedisClient:   redisClient,
		GobCache:      gobCache,
		Queries:       queries,
		CareerQueries: queries,
	}
	w.RegisterActivity(playerActivities.FetchPlayerLandingsBatch)
	w.RegisterActivity(playerActivities.DownloadPlayerGameLogsBatch)
	w.RegisterActivity(playerActivities.LoadSeasonBoxscorePlayers)
	w.RegisterActivity(playerActivities.LoadAllBoxscorePlayers)
	w.RegisterActivity(playerActivities.CountPlayersForAllSeasons)
	w.RegisterActivity(playerActivities.ProcessPlayerBatch)
	w.RegisterActivity(playerActivities.ListYahooPlayerFiles)
	w.RegisterActivity(playerActivities.ParseYahooPlayerBatch)
	w.RegisterActivity(playerActivities.SaveYahooPlayersToRedis)
	w.RegisterActivity(playerActivities.LoadUnmatchedYahooPlayers)
	w.RegisterActivity(playerActivities.VerifyUnmatchedBatch)
	w.RegisterActivity(playerActivities.CleanupYahooIDPoolData)

	return nil
}

// registerAssetWorkflows registers all asset download workflows on the given
// worker (expected to be polling shared.TaskQueueAssets). The generic
// FetchAssetsClassWorkflow is intentionally not registered: it is invoked as a
// regular Go function from the nine entry-point wrappers, never via
// ExecuteChildWorkflow, so Temporal does not need to resolve it by name.
func registerAssetWorkflows(w worker.Worker) {
	// Parent: orchestrates the nine class children
	w.RegisterWorkflow(workflow.FetchAssetsWorkflow)

	// Nine entry-point wrappers — each is a distinct workflow type in the Temporal UI
	w.RegisterWorkflow(workflow.FetchPlayerHeadshotsWorkflow)
	w.RegisterWorkflow(workflow.FetchPlayerHeroImagesWorkflow)
	w.RegisterWorkflow(workflow.FetchPlayerYahooImagesSmallWorkflow)
	w.RegisterWorkflow(workflow.FetchPlayerYahooImagesMediumWorkflow)
	w.RegisterWorkflow(workflow.FetchPlayerYahooImagesLargeWorkflow)
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
	storage := store.NewDefaultStorage()
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
	w.RegisterActivity(assetActivities.LoadPlayerYahooImageSmallAssets)
	w.RegisterActivity(assetActivities.LoadPlayerYahooImageMediumAssets)
	w.RegisterActivity(assetActivities.LoadPlayerYahooImageLargeAssets)
	w.RegisterActivity(assetActivities.LoadTeamLogoAssets)
	w.RegisterActivity(assetActivities.LoadYahooTeamLogoAssets)
	w.RegisterActivity(assetActivities.LoadYahooLeagueLogoAssets)
	w.RegisterActivity(assetActivities.LoadYahooManagerImageAssets)

	// Count activities — one per asset class. Used by the parent
	// FetchAssetsWorkflow to size its progress bars before dispatch.
	w.RegisterActivity(assetActivities.CountPlayerHeadshotAssets)
	w.RegisterActivity(assetActivities.CountPlayerHeroImageAssets)
	w.RegisterActivity(assetActivities.CountPlayerYahooImageSmallAssets)
	w.RegisterActivity(assetActivities.CountPlayerYahooImageMediumAssets)
	w.RegisterActivity(assetActivities.CountPlayerYahooImageLargeAssets)
	w.RegisterActivity(assetActivities.CountTeamLogoAssets)
	w.RegisterActivity(assetActivities.CountYahooTeamLogoAssets)
	w.RegisterActivity(assetActivities.CountYahooLeagueLogoAssets)
	w.RegisterActivity(assetActivities.CountYahooManagerImageAssets)
}
