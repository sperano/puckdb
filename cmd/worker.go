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

			// Yahoo fetch activities
			w.RegisterActivity(workers.FetchLeague)
			w.RegisterActivity(workers.FetchDailySchedule)
			w.RegisterActivity(workers.FetchTeam)
			w.RegisterActivity(workers.FetchRosterForTeamOnDay)
			w.RegisterActivity(workers.FetchTeamSummaryForTeamOnDay)
			w.RegisterActivity(workers.FetchYahooPlayerBatch)
			w.RegisterActivity(workers.FetchSeasonsDataActivity)
			w.RegisterActivity(workers.FetchDayActivity)

			w.RegisterWorkflow(workers.FetchSeasonsWorkflow)
			w.RegisterWorkflow(workers.FetchSeasonWorkflow)
			w.RegisterWorkflow(workers.FetchDayWorkflow)
			w.RegisterWorkflow(workers.FetchYahooPlayersWorkflow)
			w.RegisterWorkflow(workers.ImportNHLTeamsAndPlayersWorkflow)
			w.RegisterWorkflow(workers.ProcessPlayersWorkflow)
			w.RegisterWorkflow(workers.ProcessPlayersWorkflowContinue)
			w.RegisterWorkflow(workers.ImportSeasonsWorkflow)
			w.RegisterWorkflow(workers.InitializeWorkflow)

			// Initialize activities
			w.RegisterActivity(workers.DownloadFranchisesActivity)
			w.RegisterActivity(workers.UpsertFranchisesActivity)
			w.RegisterActivity(workers.DownloadSeasonsManifestActivity)
			w.RegisterActivity(workers.DownloadSeasonStandingsActivity)
			w.RegisterActivity(workers.UpsertSeasonsActivity)
			w.RegisterActivity(workers.UpsertSeasonTeamsActivity)
			w.RegisterActivity(workers.InitializeSeasonTeamsActivity)

			// ImportSeasons activities
			w.RegisterActivity(workers.ImportBoxscoresForDateActivity)

			// Combined boxscore extraction (replaces separate player/team extraction)
			w.RegisterActivity(workers.ExtractBoxscoreDataForSeasonActivity)

			w.RegisterActivity(workers.DownloadPlayerLandingBatchActivity)
			w.RegisterActivity(workers.MergePlayerBatchesFromRedisActivity)

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
