package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/config"
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
	config.APIServerAddrFlags.Init(flags)

	rootCmd.AddCommand(cmdAPI(), cmdDB(), cmdMaurice(), cmdMCPServer(), cmdMetrics(), cmdRedis(), cmdSim(), cmdSync(), cmdWorker(), cmdYahoo())

	cobra.OnInitialize(func() {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout})
		config.SetupViper()
	})
	return rootCmd
}

// BindFlags applies viper config values onto any flag that was not explicitly
// set on the command line. It returns the first error encountered so callers
// (PreRunE/commonInit) can propagate it to cobra, which reports it and lets
// deferred cleanup run — rather than log.Fatal, which bypasses every defer.
func BindFlags(flags *pflag.FlagSet) error {
	var bindErr error
	flags.VisitAll(func(f *pflag.Flag) {
		if bindErr != nil {
			return
		}
		name := strings.ReplaceAll(f.Name, "-", "_")
		// Apply the viper config value to the flag when the flag is not set and viper has a value
		if !f.Changed && viper.IsSet(name) {
			val := viper.Get(name)
			if err := flags.Set(f.Name, fmt.Sprintf("%v", val)); err != nil {
				bindErr = fmt.Errorf("set value for %s to %v: %w", f.Name, val, err)
			}
		}
	})
	return bindErr
}

func commonInit(cmd *cobra.Command) error {
	flags := cmd.Flags()
	if err := config.BindLoggingFlags(flags); err != nil {
		return err
	}
	if err := config.APIServerAddrFlags.Bind(cmd.Root().PersistentFlags()); err != nil {
		return err
	}
	if err := BindFlags(cmd.PersistentFlags()); err != nil {
		return err
	}
	if err := BindFlags(cmd.Flags()); err != nil {
		return err
	}
	config.SetupLogger()
	config.SetLogLevel()
	config.LogIntro()
	return nil
}
