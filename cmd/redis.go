package cmd

import (
	"context"
	"fmt"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func cmdRedis() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "redis",
		Short: "Redis operations",
		Long:  `Redis management commands.`,
	}
	cmd.AddCommand(cmdRedisFlush())
	return cmd
}

func cmdRedisFlush() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "flush",
		Short:   "Flush all keys in Redis DB",
		Long:    `Flush all keys in the configured Redis database via GraphQL API.`,
		PreRunE: bindFlagsPreRunE(&config.APIServerAddrFlags, &config.AdminAuthFlags, &config.APIBasicAuthFlags),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getGraphQLClient()
			if err != nil {
				return err
			}
			ctx := context.Background()

			log.Info().Str("server", viper.GetString(config.FlagAPIServerAddr)).Msg("Flushing Redis DB")

			success, err := client.FlushRedisDB(ctx)
			if err != nil {
				return fmt.Errorf("flush Redis DB: %w", err)
			}

			if success {
				log.Info().Msg("Redis DB flushed successfully")
			} else {
				log.Warn().Msg("Redis DB flush returned false")
			}

			return nil
		},
	}
	config.InitFlags(cmd.Flags(), &config.APIServerAddrFlags, &config.AdminAuthFlags, &config.APIBasicAuthFlags)
	return cmd
}
