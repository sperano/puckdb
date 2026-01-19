package cmd

import (
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/temporal"
	workers "github.com/sperano/puckdb/worker"
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
		Short: "Start Temporal worker",
		Long:  `Start the Temporal worker to process import workflows`,
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
			if err := config.BindMaxSeasonConcurrencyFlag(flags); err != nil {
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
			w.RegisterActivity(workers.FetchSeasonsDataActivity)

			// Import workflows
			// w.RegisterWorkflow(workers.ImportLeagueWorkflow)      // commented out: depends on getGameKey
			// w.RegisterWorkflow(workers.ImportTeamWorkflow)        // commented out: depends on getGameKey
			// w.RegisterWorkflow(workers.ImportGamesForSeasonWorkflow)  // commented out: uses old Season struct
			w.RegisterWorkflow(workers.ImportGamesForDayWorkflow)
			// w.RegisterWorkflow(workers.ImportRosterForTeamWorkflow)       // commented out: uses old Season struct
			// w.RegisterWorkflow(workers.ImportTeamSummariesForTeamWorkflow) // commented out: uses old Season struct
			// w.RegisterWorkflow(workers.ImportEverythingWorkflow)          // commented out: uses old Season struct
			// w.RegisterWorkflow(workers.ImportEverythingForSeasonWorkflow) // commented out: uses old Season struct

			// DownloadFromYahoo workflows
			// w.RegisterWorkflow(workers.DownloadGamesForSeasonWorkflow)      // commented out: uses old Season struct
			w.RegisterWorkflow(workers.DownloadRosterForTeamWorkflow)
			w.RegisterWorkflow(workers.DownloadTeamSummariesForTeamWorkflow)
			// w.RegisterWorkflow(workers.DownloadEverythingWorkflow)          // commented out: uses old Season struct
			// w.RegisterWorkflow(workers.DownloadEverythingForSeasonWorkflow) // commented out: uses old Season struct
			w.RegisterWorkflow(workers.DownloadAllWorkflow)

			// Player extraction workflows
			// w.RegisterWorkflow(workers.ExtractUniquePlayersWorkflow)        // commented out: uses old Season struct
			// w.RegisterWorkflow(workers.ExtractPlayersForSeasonWorkflow)     // commented out: uses old Season struct
			// w.RegisterWorkflow(workers.ExtractYahooPlayersForSeasonWorkflow)  // commented out: uses old Season struct
			// w.RegisterWorkflow(workers.ExtractBoxscorePlayersForSeasonWorkflow) // commented out: uses old Season struct
			w.RegisterWorkflow(workers.EnrichPlayersWorkflow)

			// Player extraction activities
			w.RegisterActivity(workers.ExtractYahooPlayersForDayActivity)
			w.RegisterActivity(workers.ExtractYahooPlayersForDayBatchActivity)
			w.RegisterActivity(workers.ExtractBoxscorePlayersForDayActivity)
			w.RegisterActivity(workers.ExtractBoxscorePlayersForDayBatchActivity)
			w.RegisterActivity(workers.MergeSeasonPlayersActivity)
			w.RegisterActivity(workers.MergeAllSeasonsActivity)
			w.RegisterActivity(workers.MergePlayerBatchesFromRedisActivity)
			// w.RegisterActivity(workers.StoreSeasonResultActivity) // commented out
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
	config.InitMaxSeasonConcurrencyFlag(flags)
	return cmd
}
