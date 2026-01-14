package cmd

//import (
//	"fmt"
//	"github.com/ericsperano/yfh/apiserver"
//	"github.com/ericsperano/yfh/cmd/common"
//	"github.com/ericsperano/yfh/config"
//	"github.com/go-chi/chi/v5"
//	"net/http"
//	"time"
//
//	"github.com/spf13/cobra"
//	"github.com/spf13/viper"
//)
//
//func setupMetricsRouter() *chi.Mux {
//	r := chi.NewRouter()
//	r.Use(apiserver.ChiLogger)
//	//r.SetTrustedProxies(nil)
//	//r.Use(cors.Default())
//
//	// configure metrics middleware
//	//monitor := ginmetrics.GetMonitor()
//	//if err := core.CreateMetricNHLConferences(monitor); err != nil {
//	//	log.Fatal().Msg(err.Error())
//	//}
//	//if err := core.CreateMetricNHLDivisions(monitor); err != nil {
//	//	log.Fatal().Msg(err.Error())
//	//}
//	//if err := core.CreateMetricNHLTeams(monitor); err != nil {
//	//	log.Fatal().Msg(err.Error())
//	//}
//	//if err := core.CreateMetricFantasyGames(monitor); err != nil {
//	//	log.Fatal().Msg(err.Error())
//	//}
//	//if err := core.CreateMetricLeagues(monitor); err != nil {
//	//	log.Fatal().Msg(err.Error())
//	//}
//	//if err := core.CreateMetricTeams(monitor); err != nil {
//	//	log.Fatal().Msg(err.Error())
//	//}
//	//if err := core.CreateMetricPlayers(monitor); err != nil {
//	//	log.Fatal().Msg(err.Error())
//	//}
//	//if err := core.CreateMetricGames(monitor); err != nil {
//	//	log.Fatal().Msg(err.Error())
//	//}
//	//if err := core.CreateMetricPlayerStats(monitor); err != nil {
//	//	log.Fatal().Msg(err.Error())
//	//}
//	//if err := core.CreateMetricRosterPlayers(monitor); err != nil {
//	//	log.Fatal().Msg(err.Error())
//	//}
//	//if err := core.CreateMetricStandings(monitor); err != nil {
//	//	log.Fatal().Msg(err.Error())
//	//}
//	//monitor.SetMetricPath("/metrics")
//	//monitor.Use(r)
//	r.Get("/ping", func(w http.ResponseWriter, r *http.Request) {
//		_, _ = w.Write([]byte("pong"))
//	})
//	return r
//}
//
////func doMetrics(yfh *core.YFH, model interface{}, metrics string) {
////	var count int64
////	if err := yfh.GormDB.Model(model).Count(&count).Error; err != nil {
////		log.Fatal().Err(err)
////	}
////	log.Debug().Int64("count", count).Str("model.Type", fmt.Sprintf("%T", model))
////	if err := ginmetrics.GetMonitor().GetMetric(metrics).SetGaugeValue(nil, float64(count)); err != nil {
////		log.Fatal().Err(err)
////	}
////}
//
//func init() {
//	var cmd = &cobra.Command{
//		Use:   "metrics",
//		Short: "Run as a global metrics server",
//		Long:  `Run as a global metrics server`,
//		PreRunE: func(cmd *cobra.Command, args []string) error {
//			flags := cmd.Flags()
//			if err := viper.BindPFlag(config.FlagLogLevel, flags.Lookup(config.FlagLogLevel)); err != nil {
//				return err
//			}
//			if err := viper.BindPFlag(config.FlagDataPath, flags.Lookup(config.FlagDataPath)); err != nil {
//				return err
//			}
//			if err := config.BindPostgresFlags(flags); err != nil {
//				return err
//			}
//			if err := viper.BindPFlag(config.FlagMetricsPort, flags.Lookup(config.FlagMetricsPort)); err != nil {
//				return err
//			}
//			if err := viper.BindPFlag(config.FlagMetricsTLSEnabled, flags.Lookup(config.FlagMetricsTLSEnabled)); err != nil {
//				return err
//			}
//			return viper.BindPFlag(config.FlagMetricsRefreshInterval, flags.Lookup(config.FlagMetricsRefreshInterval))
//		},
//		RunE: func(cmd *cobra.Command, args []string) error {
//			config.SetLogLevel()
//			common.LogIntro()
//			config.ViperEnvFile()
//
//			listen := fmt.Sprintf(":%d", viper.GetInt(config.FlagMetricsPort))
//			r := setupMetricsRouter()
//			go func() {
//				for _ = range time.Tick(5 * time.Second) {
//					//doMetrics(yfh, &model.NHLConference{}, config.MetricNHLConferences)
//					//doMetrics(yfh, &model.NHLDivision{}, config.MetricNHLDivisions)
//					//doMetrics(yfh, &model.NHLTeam{}, config.MetricNHLTeams)
//					//doMetrics(yfh, &model.FantasyGame{}, config.MetricFantasyGames)
//					//doMetrics(yfh, &model.League{}, config.MetricLeagues)
//					//doMetrics(yfh, &model.Team{}, config.MetricTeams)
//					//doMetrics(yfh, &model.Player{}, config.MetricPlayers)
//					//doMetrics(yfh, &model.Game{}, config.MetricGames)
//					//doMetrics(yfh, &model.PlayerStats{}, config.MetricPlayerStats)
//					//doMetrics(yfh, &model.RosterPlayer{}, config.MetricRosterPlayers)
//					//doMetrics(yfh, &model.Standing{}, config.MetricStandings)
//				}
//			}()
//			if viper.GetBool(config.FlagMetricsTLSEnabled) {
//				return http.ListenAndServeTLS(listen, viper.GetString(config.FlagTLSCertificate), viper.GetString(config.FlagTLSKey), r)
//			}
//			return http.ListenAndServe(listen, r)
//		},
//	}
//	flags := cmd.Flags()
//	config.InitLogLevelFlag(flags, config.DefaultLogLevel) // TODO should be in root command!
//	config.InitDataPathFlag(flags)
//	config.InitPostgresFlags(flags)
//	config.InitSeasonsFlag(cmd, flags, false)
//	config.InitMetricsPortFlag(flags)
//	config.InitMetricsTLSEnabledFlag(flags)
//	config.InitMetricsRefreshIntervalFlag(flags)
//	rootCmd.AddCommand(cmd)
//}
