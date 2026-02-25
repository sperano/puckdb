package cmd

import (
	"fmt"

	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/metrics"
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
				&config.GameIDCacheFlags,
				&config.YahooDownloadSleepFlags,
				&config.WorkerConcurrencyFlags,
				&config.PlayerLandingFlags,
				&config.ProcessPlayersFlags,
				&config.PlayerLogsFlags,
			)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			config.LogFlagValues()

			// Start metrics HTTP server
			metricsAddr := fmt.Sprintf(":%d", viper.GetInt(config.FlagWorkerPort))
			go metrics.StartWorkerServer(metricsAddr)

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
			w.RegisterActivity(workers.ClearProgressActivity)

			// Yahoo fetch activities
			w.RegisterActivity(workers.FetchLeagueActivity)
			w.RegisterActivity(workers.FetchTeamsActivity)
			w.RegisterActivity(workers.FetchYahooPlayerBatchActivity)
			w.RegisterActivity(workers.FetchSeasonsDataActivity)
			w.RegisterActivity(workers.FetchDayActivity)

			w.RegisterWorkflow(workers.FetchSeasonsWorkflow)
			w.RegisterWorkflow(workers.FetchSeasonWorkflow)
			w.RegisterWorkflow(workers.FetchPlayerLogsWorkflow)
			w.RegisterWorkflow(workers.FetchSeasonPlayerLogsWorkflow)
			w.RegisterWorkflow(workers.FetchYahooPlayersWorkflow)
			w.RegisterWorkflow(workers.ImportNHLTeamsAndPlayersWorkflow)
			w.RegisterWorkflow(workers.ProcessPlayersWorkflow)
			w.RegisterWorkflow(workers.ProcessPlayersWorkflowContinue)
			w.RegisterWorkflow(workers.ImportSeasonsWorkflow)
			w.RegisterWorkflow(workers.ImportSeasonWorkflow)
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

			// Initialize activities
			w.RegisterActivity(workers.DownloadFranchisesActivity)
			w.RegisterActivity(workers.UpsertFranchisesActivity)
			w.RegisterActivity(workers.DownloadSeasonsManifestActivity)
			w.RegisterActivity(workers.UpsertSeasonsActivity)
			w.RegisterActivity(workers.InitializeSeasonTeamsActivity)

			// ImportSeasons activities
			w.RegisterActivity(workers.ImportBoxscoresForDateActivity)
			w.RegisterActivity(workers.ImportPlayerGameLogsForDateActivity)
			w.RegisterActivity(workers.ImportGameStoryForDateActivity)
			w.RegisterActivity(workers.ImportYahooLeagueActivity)
			w.RegisterActivity(workers.ImportYahooTeamsActivity)
			w.RegisterActivity(workers.ImportYahooDataForDateActivity)

			// Combined boxscore extraction (replaces separate player/team extraction)
			w.RegisterActivity(workers.ExtractBoxscoreDataForSeasonActivity)
			w.RegisterActivity(workers.ExtractAndSaveBoxscorePlayersActivity)
			w.RegisterActivity(workers.ConsolidateBoxscorePlayersActivity)

			// Fetch player landings from NHL API
			w.RegisterActivity(workers.LoadAllBoxscorePlayersActivity)
			w.RegisterActivity(workers.FetchPlayerLandingsBatchActivity)

			// Player counting and cached extraction
			w.RegisterActivity(workers.CountPlayersForSeasonActivity)
			w.RegisterActivity(workers.GetCachedExtractionActivity)

			// Player game logs
			w.RegisterActivity(workers.DownloadPlayerGameLogsActivity)

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
		&config.GameIDCacheFlags,
		&config.YahooDownloadSleepFlags,
		&config.WorkerConcurrencyFlags,
		&config.PlayerLandingFlags,
		&config.ProcessPlayersFlags,
		&config.PlayerLogsFlags,
	)
	return cmd
}
