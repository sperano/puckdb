package cmd

import (
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/ericsperano/yfh/core"
	"github.com/ericsperano/yfh/worker"
	"github.com/gin-gonic/gin"
	"github.com/penglongli/gin-metrics/ginmetrics"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const FlagWorkerPort = "worker_port"
const FlagWorkerTLSEnabled = "worker_tls_enabled"

func setupRouter(yfh *core.YFH) *gin.Engine {
	r := gin.Default()
	//r.SetTrustedProxies(nil)
	//r.Use(cors.Default())

	// configure metrics middleware
	monitor := ginmetrics.GetMonitor()
	//core.CreateRandomMetric(monitor)
	core.CreateMetricTaskConsumed(monitor)
	core.CreateMetricTaskPublished(monitor)
	core.CreateMetricDownload(monitor)
	core.CreateMetricElapseTime(monitor)

	monitor.SetMetricPath("/metrics")
	monitor.Use(r)

	r.GET("/ping", func(ctx *gin.Context) {
		ctx.String(http.StatusOK, "pong")
	})
	return r
}

func init() {
	var cmd = &cobra.Command{
		Use:   "worker",
		Short: "TODO",
		Long:  `TODO`,
		RunE: func(cmd *cobra.Command, args []string) error {
			core.SetLogLevel()
			core.LogIntro()

			yfh, err := core.NewYFH()
			if err != nil {
				return err
			}
			defer yfh.Close()
			go worker.StarWorkerQueue(yfh)
			go func() {
				listen := fmt.Sprintf(":%d", viper.GetInt(FlagWorkerPort))
				r := setupRouter(yfh)
				if viper.GetBool(FlagWorkerTLSEnabled) {
					if err := r.RunTLS(listen, viper.GetString(FlagTLSCertificate), viper.GetString(FlagTLSKey)); err != nil {
						panic(err)
					}
				}
				if err := r.Run(listen); err != nil {
					panic(err)
				}
			}()
			c := make(chan os.Signal, 2)
			signal.Notify(c, os.Interrupt, syscall.SIGTERM)
			<-c // block until signal received
			log.Info("Shutting down gin server")
			return nil
		},
	}
	flags := cmd.Flags()
	core.SetupViperConfig(flags)
	core.SetupViperGithubAccessToken(flags)
	core.SetupViperLogLevel(flags)
	core.SetupViperRedis(flags)
	core.SetupViperPostgres(flags)
	flags.IntP(FlagWorkerPort, "p", 8788, "Default port")
	viper.BindPFlag(FlagWorkerPort, flags.Lookup(FlagWorkerPort))
	flags.Bool(FlagWorkerTLSEnabled, false, "Enable TLS Mode")
	viper.BindPFlag(FlagWorkerTLSEnabled, flags.Lookup(FlagWorkerTLSEnabled))
	flags.Int(worker.FlagMaxGamesImporter, runtime.NumCPU()*2, "Max number of games importer goroutines")
	viper.BindPFlag(worker.FlagMaxGamesImporter, flags.Lookup(worker.FlagMaxGamesImporter))
	flags.Int(worker.FlagMaxRostersImporter, runtime.NumCPU()*2, "Max number of rosters importer goroutines")
	viper.BindPFlag(worker.FlagMaxRostersImporter, flags.Lookup(worker.FlagMaxRostersImporter))
	rootCmd.AddCommand(cmd)
}
