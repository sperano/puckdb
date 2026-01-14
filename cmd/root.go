package cmd

import (
	"fmt"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/sperano/yfh/config"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"os"
	"strings"
)

func Root() *cobra.Command {
	var rootCmd = &cobra.Command{
		Use:           "yfh",
		Short:         "Yahoo Fantasy Hockey",
		Long:          `Yahoo Fantasy Hockey executable that can be run in many modes (apiserver, worker, etc.)`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return commonInit(cmd)
		},
	}
	flags := rootCmd.PersistentFlags()
	config.InitLogLevelFlag(flags, config.DefaultLogLevel)

	rootCmd.AddCommand(cmdAPI(), cmdCheck(), cmdDownload(), cmdDrop(), cmdImport(), cmdInit(), cmdInfo(), cmdWorker(), cmdYahoo())

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
	if err := viper.BindPFlag(config.FlagLogLevel, flags.Lookup(config.FlagLogLevel)); err != nil {
		return err
	}
	BindFlags(cmd.PersistentFlags())
	BindFlags(cmd.Flags())
	config.LogIntro()
	config.SetLogLevel()
	return nil
}
