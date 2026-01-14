package cmd

import (
	"github.com/sperano/yfh/config"
	"github.com/sperano/yfh/temporal"
	workers "github.com/sperano/yfh/worker"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.temporal.io/sdk/worker"
)

//func setupRouter() *chi.Mux {
//	r := chi.NewRouter()
//	r.Use(handlers.ChiLogger)
//	//r.SetTrustedProxies(nil)
//	//r.Use(cors.Default())
//
//	// configure metrics middleware
//	//monitor := ginmetrics.GetMonitor()
//
//	//core.CreateRandomMetric(monitor)
//	//if err := core.CreateMetricDownload(monitor); err != nil {
//	//	log.Fatal().Msg(err.Error())
//	//}
//	//monitor.SetMetricPath("/metrics")
//	//monitor.Use(r)
//	r.Get("/ping", func(w http.ResponseWriter, r *http.Request) {
//		_, _ = w.Write([]byte("pong"))
//	})
//	return r
//}

func cmdWorker() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "worker",
		Short: "Run as a worker",
		Long:  `Run as a worker process`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			flags := cmd.Flags()
			if err := config.BindYahooOAuth2Flags(flags); err != nil {
				return err
			}
			if err := viper.BindPFlag(config.FlagSeasons, flags.Lookup(config.FlagSeasons)); err != nil {
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
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			tclient, err := temporal.NewClient()
			if err != nil {
				return err
			}
			defer tclient.Close()
			w := worker.New(tclient, workers.TaskQueueName, worker.Options{})

			// Import activities
			w.RegisterActivity(workers.ImportLeague)
			w.RegisterActivity(workers.ImportGameDay)
			w.RegisterActivity(workers.ImportTeam)
			w.RegisterActivity(workers.ImportRosterForTeamOnDay)
			w.RegisterActivity(workers.ImportTeamSummaryForTeamOnDay)

			// DownloadFromYahoo activities
			w.RegisterActivity(workers.DownloadLeague)
			w.RegisterActivity(workers.DownloadGameDay)
			w.RegisterActivity(workers.DownloadDailySchedule)
			w.RegisterActivity(workers.DownloadTeam)
			w.RegisterActivity(workers.DownloadRosterForTeamOnDay)
			w.RegisterActivity(workers.DownloadTeamSummaryForTeamOnDay)

			// Import workflows
			w.RegisterWorkflow(workers.ImportLeagueWorkflow)
			w.RegisterWorkflow(workers.ImportTeamWorkflow)
			w.RegisterWorkflow(workers.ImportGamesForSeasonWorkflow)
			w.RegisterWorkflow(workers.ImportGamesForDayWorkflow)
			w.RegisterWorkflow(workers.ImportRosterForTeamWorkflow)
			w.RegisterWorkflow(workers.ImportTeamSummariesForTeamWorkflow)
			w.RegisterWorkflow(workers.ImportEverythingWorkflow)
			w.RegisterWorkflow(workers.ImportEverythingForSeasonWorkflow)

			// DownloadFromYahoo workflows
			w.RegisterWorkflow(workers.DownloadGamesForSeasonWorkflow)
			w.RegisterWorkflow(workers.DownloadRosterForTeamWorkflow)
			w.RegisterWorkflow(workers.DownloadTeamSummariesForTeamWorkflow)
			w.RegisterWorkflow(workers.DownloadEverythingWorkflow)
			w.RegisterWorkflow(workers.DownloadEverythingForSeasonWorkflow)

			// Player extraction workflows
			w.RegisterWorkflow(workers.ExtractUniquePlayersWorkflow)
			w.RegisterWorkflow(workers.ExtractPlayersForSeasonWorkflow)
			w.RegisterWorkflow(workers.ExtractYahooPlayersForSeasonWorkflow)
			w.RegisterWorkflow(workers.ExtractBoxscorePlayersForSeasonWorkflow)
			w.RegisterWorkflow(workers.EnrichPlayersWorkflow)

			// Player extraction activities
			w.RegisterActivity(workers.ExtractYahooPlayersForDayActivity)
			w.RegisterActivity(workers.ExtractYahooPlayersForDayBatchActivity)
			w.RegisterActivity(workers.ExtractBoxscorePlayersForDayActivity)
			w.RegisterActivity(workers.ExtractBoxscorePlayersForDayBatchActivity)
			w.RegisterActivity(workers.MergeSeasonPlayersActivity)
			w.RegisterActivity(workers.MergeAllSeasonsActivity)
			w.RegisterActivity(workers.MergePlayerBatchesFromRedisActivity)
			w.RegisterActivity(workers.StoreSeasonResultActivity)
			w.RegisterActivity(workers.StoreEnrichmentPlayersActivity)
			w.RegisterActivity(workers.MergeAllSeasonsFromRedisActivity)
			w.RegisterActivity(workers.EnrichPlayerBatchActivity)

			//go func() {
			//	// dur := viper.GetInt(config.FlagRedisCacheDuration)
			//	// log.Info().Int("duration", dur).Msgf("Redis Cache Duration: %d Minutes", dur)
			//	listen := fmt.Sprintf(":%d", viper.GetInt(config.FlagWorkerPort))
			//	r := setupRouter()
			//	if viper.GetBool(config.FlagWorkerTLSEnabled) {
			//		if err := http.ListenAndServeTLS(listen, viper.GetString(config.FlagTLSCertificate), viper.GetString(config.FlagTLSKey), r); err != nil {
			//			panic(err)
			//		}
			//	}
			//	if err := http.ListenAndServe(listen, r); err != nil {
			//		panic(err)
			//	}
			//}()
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
	return cmd
}
