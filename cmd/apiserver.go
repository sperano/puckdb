package cmd

import (
	"fmt"

	"github.com/ericsperano/yfh/apiserver"
	"github.com/ericsperano/yfh/core"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const FlagTLSCertificate = "tls_certificate"
const FlagTLSKey = "tls_key"
const FlagAPIPort = "api_port"
const FlagAPITLSEnabled = "api_tls_enabled"

func init() {
	var cmd = &cobra.Command{
		Use:   "apiserver",
		Short: "Run as an HTTP server to handle the REST api",
		Long:  `TODO`,
		RunE: func(cmd *cobra.Command, args []string) error {
			core.SetLogLevel()
			core.LogIntro()

			yfh, err := core.NewYFH()
			if err != nil {
				return err
			}
			defer yfh.Close()

			listen := fmt.Sprintf(":%d", viper.GetInt(FlagAPIPort))
			r := apiserver.SetupRouter(yfh)
			if viper.GetBool(FlagAPITLSEnabled) {
				return r.RunTLS(listen, viper.GetString(FlagTLSCertificate), viper.GetString(FlagTLSKey))
			}
			return r.Run(listen)
		},
	}
	flags := cmd.Flags()
	core.SetupViperConfig(flags)
	core.SetupViperLogLevel(flags)
	core.SetupViperRedis(flags)
	core.FIntP(flags, FlagAPIPort, "p", 8787, "Default port")
	core.FBool(flags, FlagAPITLSEnabled, false, "Enable TLS Mode")
	core.FString(flags, apiserver.FlagUIURL, "", "Default UI URL")
	rootCmd.AddCommand(cmd)
}
