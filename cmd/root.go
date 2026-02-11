package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/config"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

func Root() *cobra.Command {
	var rootCmd = &cobra.Command{
		Use:           "puckdb",
		Short:         "PuckDB CLI",
		Long:          `PuckDB imports NHL hockey data and Yahoo fantasy league data`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return commonInit(cmd)
		},
	}
	flags := rootCmd.PersistentFlags()
	config.InitLoggingFlags(flags, config.DefaultLogLevel, config.DefaultLogFile)

	rootCmd.AddCommand(cmdAPI(), cmdCacheCheck(), cmdDB(), cmdInfo(), cmdMetrics(), cmdRedis(), cmdSync(), cmdWorker(), cmdYahoo())

	cobra.OnInitialize(func() {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout})
		config.SetupViper()
	})
	return rootCmd
}

func BindFlags(flags *pflag.FlagSet) {
	flags.VisitAll(func(f *pflag.Flag) {
		name := strings.Replace(f.Name, "-", "_", -1)
		// Apply the viper config value to the flag when the flag is not set and viper has a value
		if !f.Changed && viper.IsSet(name) {
			val := viper.Get(name)
			if err := flags.Set(f.Name, fmt.Sprintf("%v", val)); err != nil {
				log.Fatal().Err(err).Msgf("failed to set value for %s: %v", f.Name, val)
			}
		}
	})
}

func commonInit(cmd *cobra.Command) error {
	flags := cmd.Flags()
	if err := config.BindLoggingFlags(flags); err != nil {
		return err
	}
	BindFlags(cmd.PersistentFlags())
	BindFlags(cmd.Flags())
	config.SetupLogger()
	config.SetLogLevel()
	config.LogIntro()
	return nil
}
