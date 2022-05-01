package cmd

import (
	"fmt"
	"net/http"
	"time"

	"github.com/ericsperano/yfh/core"
	"github.com/ericsperano/yfh/core/model"
	"github.com/gin-gonic/gin"
	"github.com/penglongli/gin-metrics/ginmetrics"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const FlagMetricsPort = "metrics_port"
const FlagMetricsTLSEnabled = "metrics_tls_enabled"

func setupMetricsRouter(yfh *core.YFH) *gin.Engine {
	r := gin.Default()
	//r.SetTrustedProxies(nil)
	//r.Use(cors.Default())

	// configure metrics middleware
	monitor := ginmetrics.GetMonitor()
	core.CreateMetricNHLConferences(monitor)
	core.CreateMetricNHLDivisions(monitor)
	core.CreateMetricNHLTeams(monitor)
	core.CreateMetricFantasyGames(monitor)
	core.CreateMetricLeagues(monitor)
	core.CreateMetricTeams(monitor)
	core.CreateMetricPlayers(monitor)
	core.CreateMetricGames(monitor)
	core.CreateMetricPlayerStats(monitor)
	core.CreateMetricRosterPlayers(monitor)
	core.CreateMetricStandings(monitor)
	monitor.SetMetricPath("/metrics")
	monitor.Use(r)

	r.GET("/ping", func(ctx *gin.Context) {
		ctx.String(http.StatusOK, "pong")
	})
	return r
}

func doMetrics(yfh *core.YFH, model interface{}, metrics string) {
	var count int64
	if err := yfh.GormDB.Model(model).Count(&count).Error; err != nil {
		log.Fatal(err)
	}
	log.Debugf("%T count=%d", model, count)
	if err := ginmetrics.GetMonitor().GetMetric(metrics).SetGaugeValue(nil, float64(count)); err != nil {
		log.Fatal(err)
	}
}

func init() {
	var cmd = &cobra.Command{
		Use:   "metrics",
		Short: "Run as an HTTP server to provide prometheus metrics",
		Long:  `TODO`,
		RunE: func(cmd *cobra.Command, args []string) error {
			core.SetLogLevel()
			core.LogIntro()

			yfh, err := core.NewYFH()
			if err != nil {
				return err
			}
			defer yfh.Close()

			listen := fmt.Sprintf(":%d", viper.GetInt(FlagMetricsPort))
			r := setupMetricsRouter(yfh)
			go core.DoEvery(5*time.Second, func(t time.Time) {
				doMetrics(yfh, &model.NHLConference{}, core.MetricNHLConferences)
				doMetrics(yfh, &model.NHLDivision{}, core.MetricNHLDivisions)
				doMetrics(yfh, &model.NHLTeam{}, core.MetricNHLTeams)
				doMetrics(yfh, &model.FantasyGame{}, core.MetricFantasyGames)
				doMetrics(yfh, &model.League{}, core.MetricLeagues)
				doMetrics(yfh, &model.Team{}, core.MetricTeams)
				doMetrics(yfh, &model.Player{}, core.MetricPlayers)
				doMetrics(yfh, &model.Game{}, core.MetricGames)
				doMetrics(yfh, &model.PlayerStats{}, core.MetricPlayerStats)
				doMetrics(yfh, &model.RosterPlayer{}, core.MetricRosterPlayers)
				doMetrics(yfh, &model.Standing{}, core.MetricStandings)
			})
			if viper.GetBool(FlagMetricsTLSEnabled) {
				return r.RunTLS(listen, viper.GetString(FlagTLSCertificate), viper.GetString(FlagTLSKey))
			}
			return r.Run(listen)
		},
	}
	flags := cmd.Flags()
	core.SetupViperConfig(flags)
	core.SetupViperLogLevel(flags)
	core.SetupViperRedis(flags)
	core.SetupViperPostgres(flags)
	core.FIntP(flags, FlagMetricsPort, "p", 8789, "Default port")
	core.FBool(flags, FlagMetricsTLSEnabled, false, "Enable TLS Mode")
	rootCmd.AddCommand(cmd)
}
