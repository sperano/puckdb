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
			if err := config.BindPlayerLandingFlags(flags); err != nil {
				return err
			}
			return nil
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

			// DownloadFromYahoo activities
			w.RegisterActivity(workers.DownloadLeague)
			w.RegisterActivity(workers.DownloadDailySchedule)
			w.RegisterActivity(workers.DownloadTeam)
			w.RegisterActivity(workers.DownloadRosterForTeamOnDay)
			w.RegisterActivity(workers.DownloadTeamSummaryForTeamOnDay)
			w.RegisterActivity(workers.DownloadYahooPlayerBatch)
			w.RegisterActivity(workers.FetchSeasonsDataActivity)
			w.RegisterActivity(workers.DownloadDayActivity)

			w.RegisterWorkflow(workers.DownloadSeasonsWorkflow)
			w.RegisterWorkflow(workers.DownloadSeasonWorkflow)
			w.RegisterWorkflow(workers.DownloadDayWorkflow)
			w.RegisterWorkflow(workers.DownloadYahooPlayersWorkflow)
			w.RegisterWorkflow(workers.DownloadPlayersWorkflow)
			w.RegisterWorkflow(workers.DownloadPlayersWorkflowContinue)
			w.RegisterWorkflow(workers.ImportPlayersWorkflow)
			w.RegisterWorkflow(workers.ImportSeasonsWorkflow)

			// ImportSeasons activities
			w.RegisterActivity(workers.ImportBoxscoresForDateActivity)
			w.RegisterActivity(workers.ExtractTeamsForSeasonsActivity)
			w.RegisterActivity(workers.UpsertMissingTeamsActivity)

			w.RegisterActivity(workers.ExtractPlayerIDsForSeasonActivity)
			w.RegisterActivity(workers.DownloadPlayerLandingBatchActivity)
			w.RegisterActivity(workers.MergePlayerBatchesFromRedisActivity)

			// ImportPlayers activities
			w.RegisterActivity(workers.ListYahooPlayerFilesActivity)
			w.RegisterActivity(workers.ParseYahooPlayerBatchActivity)
			w.RegisterActivity(workers.SaveYahooPlayersToRedisActivity)
			w.RegisterActivity(workers.ListPlayerLandingIDsActivity)
			w.RegisterActivity(workers.ImportPlayerBatchActivity)
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
	config.InitPlayerLandingFlags(flags)
	return cmd
}
