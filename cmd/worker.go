package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/database"
	puckhttp "github.com/sperano/puckdb/http"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/sperano/puckdb/store"
	"github.com/sperano/puckdb/temporal"
	workers "github.com/sperano/puckdb/worker"
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
			)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			config.LogFlagValues()

			// Start metrics HTTP server (health check verifies JuiceFS mount)
			metricsAddr := fmt.Sprintf(":%d", viper.GetInt(config.FlagWorkerPort))
			dataPath := viper.GetString(config.FlagDataPath)
			go metrics.StartWorkerServer(metricsAddr, dataPath)

			// Open shared database pool for activities
			ctx := context.Background()
			pool, err := database.OpenPGXPool(ctx)
			if err != nil {
				return fmt.Errorf("open database pool: %w", err)
			}
			defer pool.Close()

			// Create shared Redis client for activities
			redisClient := cache.NewClient()
			defer redisClient.Close()

			tclient, err := temporal.NewClient()
			if err != nil {
				return err
			}
			defer tclient.Close()
			w := worker.New(tclient, workers.TaskQueueName, worker.Options{
				MaxConcurrentWorkflowTaskPollers:       viper.GetInt(config.FlagWorkerMaxWorkflowPollers),
				MaxConcurrentActivityTaskPollers:       viper.GetInt(config.FlagWorkerMaxActivityPollers),
				MaxConcurrentWorkflowTaskExecutionSize: viper.GetInt(config.FlagWorkerMaxWorkflowExecution),
				MaxConcurrentActivityExecutionSize:     viper.GetInt(config.FlagWorkerMaxActivityExecution),
			})

			// Progress tracking activities
			w.RegisterActivity(workers.SaveProgressReportActivity)
			w.RegisterActivity(workers.LoadProgressReportActivity)
			w.RegisterActivity(workers.DeleteProgressReportBatchActivity)

			w.RegisterWorkflow(workers.FetchSeasonsWorkflow)
			w.RegisterWorkflow(workers.FetchSeasonWorkflow)
			w.RegisterWorkflow(workers.FetchPlayerLogsWorkflow)
			w.RegisterWorkflow(workers.FetchSeasonPlayerLogsWorkflow)
			w.RegisterWorkflow(workers.FetchYahooPlayersWorkflow)
			w.RegisterWorkflow(workers.ProcessPlayersWorkflow)
			w.RegisterWorkflow(workers.ProcessPlayersWorkflowContinue)
			w.RegisterWorkflow(workers.ImportSeasonsWorkflow)
			w.RegisterWorkflow(workers.ImportPlayerLogsWorkflow)
			w.RegisterWorkflow(workers.ImportSeasonWorkflow)
			w.RegisterWorkflow(workers.ImportSeasonPlayerLogsWorkflow)
			w.RegisterWorkflow(workers.InitializeWorkflow)
			w.RegisterWorkflow(workers.ExtractBoxscorePlayersWorkflow)
			w.RegisterWorkflow(workers.FetchPlayerLandingsWorkflow)

			// Database admin workflows
			w.RegisterWorkflow(workers.DropDatabaseWorkflow)
			w.RegisterWorkflow(workers.MigrateDatabaseWorkflow)
			w.RegisterWorkflow(workers.ResetDatabaseWorkflow)
			w.RegisterWorkflow(workers.FlushRedisWorkflow)

			// Database admin activities
			w.RegisterActivity(workers.DropDatabaseActivity)
			w.RegisterActivity(workers.MigrateDatabaseActivity)
			w.RegisterActivity(workers.FlushRedisActivity)

			// Struct-based activities with dependency injection
			storage := store.NewDefaultStorage()
			queries := sqlcdb.New(pool)
			nhlClient := workers.NewNHLClient()
			gobCacheTTL := time.Duration(viper.GetInt(config.FlagGobCacheTTL)) * time.Minute
			gobCache := cache.NewGobCacheWithTTL(redisClient, gobCacheTTL)

			leagueActivities := &workers.YahooActivities{
				Storage:          storage,
				Download:         workers.DownloadFromYahoo,
				GobCache:         gobCache,
				PublicDownloader: workers.HTTPDownloaderFunc(puckhttp.DownloadPublic),
			}
			w.RegisterActivity(leagueActivities.FetchLeague)
			w.RegisterActivity(leagueActivities.FetchTeams)
			w.RegisterActivity(leagueActivities.FetchYahooPlayerBatch)
			franchiseActivities := &workers.FranchiseActivities{
				Storage:   storage,
				GobCache:  gobCache,
				Upserter:  queries,
				NHLClient: nhlClient,
			}
			w.RegisterActivity(franchiseActivities.FetchFranchises)
			w.RegisterActivity(franchiseActivities.UpsertFranchises)

			seasonsActivities := &workers.SeasonsActivities{
				Storage:             storage,
				GobCache:            gobCache,
				NHLClient:           nhlClient,
				SeasonsUpserter:     queries,
				SeasonTeamsUpserter: queries,
				ImportQueries:       queries,
				RedisClient:         redisClient,
			}
			w.RegisterActivity(seasonsActivities.FetchSeasonsManifest)
			w.RegisterActivity(seasonsActivities.UpsertSeasons)
			w.RegisterActivity(seasonsActivities.InitializeSeasonTeamsActivity)
			w.RegisterActivity(seasonsActivities.ImportDay)
			w.RegisterActivity(seasonsActivities.ImportBoxscoresForDate)
			w.RegisterActivity(seasonsActivities.ImportGameStoryForDate)
			w.RegisterActivity(seasonsActivities.ImportPlayByPlayForDate)
			w.RegisterActivity(seasonsActivities.ImportShiftChartForDate)
			w.RegisterActivity(seasonsActivities.ImportPlayerGameLogsBatch)
			w.RegisterActivity(seasonsActivities.ImportYahooLeague)
			w.RegisterActivity(seasonsActivities.ImportYahooTeams)
			w.RegisterActivity(seasonsActivities.ImportYahooDataForDate)
			w.RegisterActivity(seasonsActivities.CollectSeasonPlayerIDs)

			dailyScheduleActivities := &workers.DailyScheduleActivities{
				Storage:       storage,
				NHLClient:     nhlClient,
				GobCache:      gobCache,
				GameDownloads: workers.DefaultGameDataDownloaders(),
				RedisClient:   redisClient,
				Download:      workers.DownloadFromYahoo,
			}
			w.RegisterActivity(dailyScheduleActivities.FetchDailySchedule)
			w.RegisterActivity(dailyScheduleActivities.FetchDay)

			// Boxscore extraction activities
			boxscoreActivities := &workers.BoxscoreActivities{
				Storage:     storage,
				GobCache:    gobCache,
				RedisClient: redisClient,
			}
			w.RegisterActivity(boxscoreActivities.ExtractBoxscoreDataForSeason)
			w.RegisterActivity(boxscoreActivities.ExtractAndSaveBoxscorePlayers)
			w.RegisterActivity(workers.ConsolidateBoxscorePlayersActivity)

			// Player activities (landings, game logs, boxscore player loading)
			playerActivities := &workers.PlayerActivities{
				Storage:     storage,
				NHLClient:   nhlClient,
				RedisClient: redisClient,
			}
			w.RegisterActivity(playerActivities.FetchPlayerLandingsBatch)
			w.RegisterActivity(playerActivities.DownloadPlayerGameLogsBatch)
			w.RegisterActivity(playerActivities.LoadSeasonBoxscorePlayers)
			w.RegisterActivity(playerActivities.LoadAllBoxscorePlayers)
			w.RegisterActivity(playerActivities.CountPlayersForAllSeasons)

			// ProcessPlayers activities (unified download + import)
			w.RegisterActivity(workers.ProcessPlayerBatchActivity)
			w.RegisterActivity(workers.ListYahooPlayerFilesActivity)
			w.RegisterActivity(workers.ParseYahooPlayerBatchActivity)
			w.RegisterActivity(workers.SaveYahooPlayersToRedisActivity)
			w.RegisterActivity(workers.LoadUnmatchedYahooPlayersActivity)
			w.RegisterActivity(workers.VerifyUnmatchedBatchActivity)
			w.RegisterActivity(workers.CleanupYahooIDPoolActivity)

			err = w.Run(worker.InterruptCh())
			if err != nil {
				return err
			}
			return nil
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
	)
	return cmd
}
