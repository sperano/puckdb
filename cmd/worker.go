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
			flags := cmd.Flags()
			if err := config.YahooOAuth2Flags.Bind(flags); err != nil {
				return err
			}
			if err := viper.BindPFlag(config.FlagYahooSeasons, flags.Lookup(config.FlagYahooSeasons)); err != nil {
				return err
			}
			if err := config.DataPathFlags.Bind(flags); err != nil {
				return err
			}
			if err := config.RedisFlags.Bind(flags); err != nil {
				return err
			}
			if err := config.PostgresFlags.Bind(flags); err != nil {
				return err
			}
			if err := config.TemporalFlags.Bind(flags); err != nil {
				return err
			}
			if err := config.TemporalRetryFlags.Bind(flags); err != nil {
				return err
			}
			if err := config.WorkerPortFlags.Bind(flags); err != nil {
				return err
			}
			if err := config.DownloadConcurrencyFlags.Bind(flags); err != nil {
				return err
			}
			if err := config.YahooPlayerFlags.Bind(flags); err != nil {
				return err
			}
			if err := config.GameIDCacheFlags.Bind(flags); err != nil {
				return err
			}
			if err := config.YahooDownloadSleepFlags.Bind(flags); err != nil {
				return err
			}
			if err := config.WorkerConcurrencyFlags.Bind(flags); err != nil {
				return err
			}
			return config.PlayerLandingFlags.Bind(flags)
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

			// Yahoo fetch activities
			w.RegisterActivity(workers.FetchLeagueActivity)
			w.RegisterActivity(workers.FetchTeamsActivity)
			w.RegisterActivity(workers.FetchYahooPlayerBatchActivity)
			w.RegisterActivity(workers.FetchSeasonsDataActivity)
			w.RegisterActivity(workers.FetchDayActivity)

			w.RegisterWorkflow(workers.FetchSeasonsWorkflow)
			w.RegisterWorkflow(workers.FetchSeasonWorkflow)
			w.RegisterWorkflow(workers.FetchYahooPlayersWorkflow)
			w.RegisterWorkflow(workers.ImportNHLTeamsAndPlayersWorkflow)
			w.RegisterWorkflow(workers.ProcessPlayersWorkflow)
			w.RegisterWorkflow(workers.ProcessPlayersWorkflowContinue)
			w.RegisterWorkflow(workers.ImportSeasonsWorkflow)
			w.RegisterWorkflow(workers.ImportSeasonWorkflow)
			w.RegisterWorkflow(workers.InitializeWorkflow)

			// Initialize activities
			w.RegisterActivity(workers.DownloadFranchisesActivity)
			w.RegisterActivity(workers.UpsertFranchisesActivity)
			w.RegisterActivity(workers.DownloadSeasonsManifestActivity)
			w.RegisterActivity(workers.UpsertSeasonsActivity)
			w.RegisterActivity(workers.InitializeSeasonTeamsActivity)

			// ImportSeasons activities
			w.RegisterActivity(workers.ImportBoxscoresForDateActivity)

			// Combined boxscore extraction (replaces separate player/team extraction)
			w.RegisterActivity(workers.ExtractBoxscoreDataForSeasonActivity)

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
	config.YahooOAuth2Flags.Init(flags)
	config.InitSeasonsFlag(cmd, flags, false)
	config.DataPathFlags.Init(flags)
	config.RedisFlags.Init(flags)
	config.PostgresFlags.Init(flags)
	config.TemporalFlags.Init(flags)
	config.TemporalRetryFlags.Init(flags)
	config.WorkerPortFlags.Init(flags)
	config.DownloadConcurrencyFlags.Init(flags)
	config.YahooPlayerFlags.Init(flags)
	config.GameIDCacheFlags.Init(flags)
	config.YahooDownloadSleepFlags.Init(flags)
	config.WorkerConcurrencyFlags.Init(flags)
	config.PlayerLandingFlags.Init(flags)
	return cmd
}
