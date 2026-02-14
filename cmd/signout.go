package cmd

import (
	"context"
	"fmt"

	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/spf13/cobra"
)

func cmdSignout() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "signout",
		Short: "Remove OAuth token",
		Long:  `Remove Yahoo OAuth2 token from Redis.`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			flags := cmd.Flags()
			return config.BindRedisFlags(flags)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			redisClient := cache.NewClient()
			defer redisClient.Close()

			err := cache.DeleteTokenForUser(ctx, redisClient, config.DefaultUser)
			if err != nil {
				return fmt.Errorf("failed to remove token: %w", err)
			}

			fmt.Fprintln(cmd.OutOrStdout(), "Signed out successfully.")
			return nil
		},
	}
	flags := cmd.Flags()
	config.InitRedisFlags(flags)
	return cmd
}
