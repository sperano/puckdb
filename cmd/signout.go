package cmd

import (
	"context"
	"fmt"

	"github.com/sperano/yfh/config"
	"github.com/sperano/yfh/redis"
	"github.com/spf13/cobra"
)

func cmdSignout() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "signout",
		Short: "Remove the OAuth token from Redis",
		Long:  `Sign out by removing the Yahoo OAuth2 token from Redis`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			flags := cmd.Flags()
			return config.BindRedisFlags(flags)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			redisClient := redis.NewClient()
			defer redisClient.Close()

			err := redis.DeleteTokenForUser(ctx, redisClient, config.DefaultUser)
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
