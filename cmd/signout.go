package cmd

import (
	"context"
	"fmt"

	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/config"
	"github.com/spf13/cobra"
)

var signoutFlagGroups = []*config.FlagGroup{
	&config.RedisFlags,
}

func cmdSignout() *cobra.Command {
	var cmd = &cobra.Command{
		Use:     "signout",
		Short:   "Remove OAuth token",
		Long:    `Remove Yahoo OAuth2 token from Redis.`,
		PreRunE: bindFlagsPreRunE(signoutFlagGroups...),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			redisClient := newRedisClient()
			defer closeOrWarn(redisClient, "redis client")

			err := cache.DeleteTokenForUser(ctx, redisClient, config.DefaultUser)
			if err != nil {
				return fmt.Errorf("remove token: %w", err)
			}

			fmt.Fprintln(cmd.OutOrStdout(), "Signed out successfully.")
			return nil
		},
	}
	config.InitFlags(cmd.Flags(), signoutFlagGroups...)
	return cmd
}
