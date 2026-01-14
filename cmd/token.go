package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/sperano/yfh/config"
	"github.com/sperano/yfh/redis"
	"github.com/spf13/cobra"
)

func cmdToken() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "token",
		Short: "Print the OAuth token from Redis",
		Long:  `Print the Yahoo OAuth2 token stored in Redis, if present`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			flags := cmd.Flags()
			return config.BindRedisFlags(flags)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			redisClient := redis.NewClient()
			defer redisClient.Close()

			token, err := redis.LoadTokenForUser(ctx, redisClient, config.DefaultUser)
			if err != nil {
				fmt.Fprintln(cmd.OutOrStdout(), "no token found.")
				return nil
			}

			fmt.Fprintln(cmd.OutOrStdout(), "Token found:")
			fmt.Fprintf(cmd.OutOrStdout(), "  Access Token:  %s\n", token.AccessToken)
			fmt.Fprintf(cmd.OutOrStdout(), "  Token Type:    %s\n", token.TokenType)
			if token.RefreshToken != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "  Refresh Token: %s\n", token.RefreshToken)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  Expiry:        %s\n", token.Expiry.Format(time.RFC3339))

			if !token.Expiry.IsZero() {
				now := time.Now()
				if token.Expiry.After(now) {
					remaining := token.Expiry.Sub(now)
					fmt.Fprintf(cmd.OutOrStdout(), "  Expires in:    %s\n", formatDuration(remaining))
				} else {
					elapsed := now.Sub(token.Expiry)
					fmt.Fprintf(cmd.OutOrStdout(), "  Expired:       %s ago\n", formatDuration(elapsed))
				}
			}

			return nil
		},
	}
	flags := cmd.Flags()
	config.InitRedisFlags(flags)
	return cmd
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%d seconds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		hours := int(d.Hours())
		minutes := int(d.Minutes()) % 60
		return fmt.Sprintf("%d hours, %d minutes", hours, minutes)
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	return fmt.Sprintf("%d days, %d hours", days, hours)
}
