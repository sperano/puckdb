package cmd

import (
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func cmdCacheCheck() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache-check",
		Short: "Verify cache completeness",
		Long: `Calculate expected vs actual cache files for NHL and Yahoo data.
Use --season to check a specific season, or --from-season/--to-season for a range.
If --seasons config file is provided, Yahoo files are also checked.`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			flags := cmd.Flags()
			if err := viper.BindPFlag(config.FlagDataPath, flags.Lookup(config.FlagDataPath)); err != nil {
				return err
			}
			if err := viper.BindPFlag(config.FlagYahooSeasons, flags.Lookup(config.FlagYahooSeasons)); err != nil {
				return err
			}
			if err := config.BindVerboseFlag(flags); err != nil {
				return err
			}
			if err := config.BindIncompleteFlag(flags); err != nil {
				return err
			}
			if err := config.BindRedisFlags(flags); err != nil {
				return err
			}
			return config.BindSeasonRangeFlags(flags)
		},
		RunE: runCacheCheck,
	}
	flags := cmd.Flags()
	config.InitDataPathFlag(flags)
	config.InitSeasonRangeFlags(flags)
	config.InitRedisFlags(flags)
	flags.StringP(config.FlagYahooSeasons, "S", config.DefaultYahooSeasonsFile, "Yahoo seasons config file (optional, enables Yahoo file checks)")
	config.InitVerboseFlag(flags)
	config.InitIncompleteFlag(flags)
	return cmd
}

func runCacheCheck(cmd *cobra.Command, _ []string) error {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: cmd.OutOrStdout()})

	redisClient := cache.NewClient()
	defer redisClient.Close()

	sp := newSpinner(cmd.OutOrStdout(), "Counting cache files...")
	sp.Start()

	allMetrics, err := getAllMetrics(cmd.Context(), redisClient)
	sp.Stop()

	if err != nil {
		return err
	}

	incompleteOnly := viper.GetBool(config.FlagIncomplete)
	if viper.GetBool(config.FlagVerbose) {
		printCacheMetricsVerbose(allMetrics, incompleteOnly)
	} else {
		printCacheMetricsCompact(allMetrics, incompleteOnly)
	}
	return nil
}
