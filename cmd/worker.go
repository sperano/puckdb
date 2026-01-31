package cmd

import (
	"fmt"

	"github.com/go-redis/redis/v8"
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
			flags := cmd.Flags()
			if err := config.BindYahooOAuth2Flags(flags); err != nil {
				return err
			}
			if err := viper.BindPFlag(config.FlagYahooSeasons, flags.Lookup(config.FlagYahooSeasons)); err != nil {
				return err
			}
			if err := viper.BindPFlag(config.FlagDataPath, flags.Lookup(config.FlagDataPath)); err != nil {
				return err
			}
			if err := config.BindRedisFlags(flags); err != nil {
				return err
			}
			if err := config.BindPostgresFlags(flags); err != nil {
				return err
			}
			if err := config.BindTemporalFlags(flags); err != nil {
				return err
			}
			if err := config.BindTemporalRetryFlags(flags); err != nil {
				return err
			}
			if err := viper.BindPFlag(config.FlagWorkerPort, flags.Lookup(config.FlagWorkerPort)); err != nil {
				return err
			}
			if err := viper.BindPFlag(config.FlagWorkerTLSEnabled, flags.Lookup(config.FlagWorkerTLSEnabled)); err != nil {
				return err
			}
			if err := config.BindSkipPreseasonFlag(flags); err != nil {
				return err
			}
			if err := config.BindMaxSeasonConcurrencyFlag(flags); err != nil {
				return err
			}
			if err := config.BindDayConcurrencyFlag(flags); err != nil {
				return err
			}
			if err := config.BindMaxYahooPlayerIDFlag(flags); err != nil {
				return err
			}
			if err := config.BindYahooPlayerBatchSizeFlag(flags); err != nil {
				return err
			}
			if err := config.BindYahooPlayerActivityBatchSizeFlag(flags); err != nil {
				return err
			}
			if err := config.BindYahooPlayersPerExecutionFlag(flags); err != nil {
				return err
			}
			if err := config.BindGameIDCacheTTLFlag(flags); err != nil {
				return err
			}
			if err := config.BindYahooDownloadSleepFlags(flags); err != nil {
				return err
			}
			if err := config.BindWorkerConcurrencyFlags(flags); err != nil {
				return err
			}
			if err := config.BindDataPathScanIntervalFlag(flags); err != nil {
				return err
			}
			if err := config.BindDataPathStatsTTLFlag(flags); err != nil {
				return err
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			config.LogFlagValues()

			// Start metrics HTTP server
			metricsAddr := fmt.Sprintf(":%d", viper.GetInt(config.FlagWorkerPort))
			go metrics.StartWorkerServer(metricsAddr)

			// Start data path scanner if data path is configured
			var dataPathScanner *metrics.DataPathScanner
			if dataPath := viper.GetString(config.FlagDataPath); dataPath != "" {
				redisClient := redis.NewClient(&redis.Options{
					Addr:     viper.GetString(config.FlagRedisURL),
					Password: viper.GetString(config.FlagRedisPassword),
					DB:       viper.GetInt(config.FlagRedisDB),
				})
				dataPathScanner = metrics.NewDataPathScanner(redisClient, dataPath)
				dataPathScanner.Start()
				defer func() {
					dataPathScanner.Stop()
					redisClient.Close()
				}()
			}

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

			// Import activities
			w.RegisterActivity(workers.ImportLeague)
			w.RegisterActivity(workers.ImportTeam)
			w.RegisterActivity(workers.ImportRosterForTeamOnDay)
			w.RegisterActivity(workers.ImportTeamSummaryForTeamOnDay)

			// DownloadFromYahoo activities
			w.RegisterActivity(workers.DownloadLeague)
			w.RegisterActivity(workers.DownloadDailySchedule)
			w.RegisterActivity(workers.DownloadTeam)
			w.RegisterActivity(workers.DownloadRosterForTeamOnDay)
			w.RegisterActivity(workers.DownloadTeamSummaryForTeamOnDay)
			w.RegisterActivity(workers.DownloadYahooPlayer)
			w.RegisterActivity(workers.DownloadYahooPlayerBatch)
			w.RegisterActivity(workers.FetchSeasonsDataActivity)
			w.RegisterActivity(workers.DownloadDayActivity)

			// Import workflows
			// w.RegisterWorkflow(workers.ImportLeagueWorkflow)      // commented out: depends on getGameKey
			// w.RegisterWorkflow(workers.ImportTeamWorkflow)        // commented out: depends on getGameKey
			// w.RegisterWorkflow(workers.ImportGamesForSeasonWorkflow)  // commented out: uses old Season struct
			// w.RegisterWorkflow(workers.ImportRosterForTeamWorkflow)       // commented out: uses old Season struct
			// w.RegisterWorkflow(workers.ImportTeamSummariesForTeamWorkflow) // commented out: uses old Season struct
			// w.RegisterWorkflow(workers.ImportEverythingWorkflow)          // commented out: uses old Season struct
			// w.RegisterWorkflow(workers.ImportEverythingForSeasonWorkflow) // commented out: uses old Season struct

			w.RegisterWorkflow(workers.DownloadSeasonsWorkflow)
			w.RegisterWorkflow(workers.DownloadSeasonWorkflow)
			w.RegisterWorkflow(workers.DownloadDayWorkflow)
			w.RegisterWorkflow(workers.DownloadYahooPlayersWorkflow)
			w.RegisterWorkflow(workers.DownloadPlayersWorkflow)

			// Player extraction workflows
			// w.RegisterWorkflow(workers.ExtractUniquePlayersWorkflow)        // commented out: uses old Season struct
			// w.RegisterWorkflow(workers.ExtractPlayersForSeasonWorkflow)     // commented out: uses old Season struct
			// w.RegisterWorkflow(workers.ExtractYahooPlayersForSeasonWorkflow)  // commented out: uses old Season struct
			// w.RegisterWorkflow(workers.ExtractBoxscorePlayersForSeasonWorkflow) // commented out: uses old Season struct
			w.RegisterWorkflow(workers.EnrichPlayersWorkflow)

			// Player extraction activities
			//w.RegisterActivity(workers.ExtractBoxscorePlayersForDayActivity)
			//w.RegisterActivity(workers.ExtractBoxscorePlayersForDayBatchActivity)
			w.RegisterActivity(workers.ExtractPlayerIDsForSeasonActivity)
			w.RegisterActivity(workers.MergeSeasonPlayersActivity)
			w.RegisterActivity(workers.MergeAllSeasonsActivity)
			w.RegisterActivity(workers.MergePlayerBatchesFromRedisActivity)
			// w.RegisterActivity(workers.StoreSeasonResultActivity) // commented out
			w.RegisterActivity(workers.StoreEnrichmentPlayersActivity)
			w.RegisterActivity(workers.MergeAllSeasonsFromRedisActivity)
			w.RegisterActivity(workers.EnrichPlayerBatchActivity)

			err = w.Run(worker.InterruptCh())
			if err != nil {
				return err
			}
			return nil
		},
	}
	flags := cmd.Flags()
	config.InitYahooOAuth2Flags(flags)
	config.InitSeasonsFlag(cmd, flags, false)
	config.InitDataPathFlag(flags)
	config.InitRedisFlags(flags)
	config.InitPostgresFlags(flags)
	config.InitTemporalFlags(flags)
	config.InitTemporalRetryFlags(flags)
	config.InitWorkerPortFlag(flags)
	config.InitWorkerTLSEnabledFlag(flags)
	config.InitSkipPreseasonFlag(flags)
	config.InitMaxSeasonConcurrencyFlag(flags)
	config.InitDayConcurrencyFlag(flags)
	config.InitMaxYahooPlayerIDFlag(flags)
	config.InitYahooPlayerBatchSizeFlag(flags)
	config.InitYahooPlayerActivityBatchSizeFlag(flags)
	config.InitYahooPlayersPerExecutionFlag(flags)
	config.InitGameIDCacheTTLFlag(flags)
	config.InitYahooDownloadSleepFlags(flags)
	config.InitWorkerConcurrencyFlags(flags)
	config.InitDataPathScanIntervalFlag(flags)
	config.InitDataPathStatsTTLFlag(flags)
	return cmd
}
