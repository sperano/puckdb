package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/redis"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func cmdInfo() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "info",
		Short: "Show configuration",
		Long:  `Display current configuration values and Yahoo OAuth token status.`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			flags := cmd.Flags()
			if err := viper.BindPFlag(config.FlagYahooSeasons, flags.Lookup(config.FlagYahooSeasons)); err != nil {
				return err
			}
			if err := config.BindYahooOAuth2Flags(flags); err != nil {
				return err
			}
			if err := viper.BindPFlag(config.FlagDataPath, flags.Lookup(config.FlagDataPath)); err != nil {
				return err
			}
			if err := config.BindRedisFlags(flags); err != nil {
				return err
			}
			if err := config.BindPostgresFlags(flags); err != nil {
				return err
			}
			if err := config.BindTemporalFlags(flags); err != nil {
				return err
			}
			if err := config.BindTemporalRetryFlags(flags); err != nil {
				return err
			}
			if err := viper.BindPFlag(config.FlagAPIPort, flags.Lookup(config.FlagAPIPort)); err != nil {
				return err
			}
			if err := viper.BindPFlag(config.FlagAPITLSEnabled, flags.Lookup(config.FlagAPITLSEnabled)); err != nil {
				return err
			}
			if err := viper.BindPFlag(config.FlagMetricsPort, flags.Lookup(config.FlagMetricsPort)); err != nil {
				return err
			}
			if err := viper.BindPFlag(config.FlagMetricsTLSEnabled, flags.Lookup(config.FlagMetricsTLSEnabled)); err != nil {
				return err
			}
			if err := viper.BindPFlag(config.FlagMetricsRefreshInterval, flags.Lookup(config.FlagMetricsRefreshInterval)); err != nil {
				return err
			}
			if err := viper.BindPFlag(config.FlagTLSCertificate, flags.Lookup(config.FlagTLSCertificate)); err != nil {
				return err
			}
			if err := viper.BindPFlag(config.FlagTLSKey, flags.Lookup(config.FlagTLSKey)); err != nil {
				return err
			}
			if err := viper.BindPFlag(config.FlagWorkerPort, flags.Lookup(config.FlagWorkerPort)); err != nil {
				return err
			}
			return viper.BindPFlag(config.FlagWorkerTLSEnabled, flags.Lookup(config.FlagWorkerTLSEnabled))
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			redisClient := redis.NewClient()
			defer redisClient.Close()
			return cmdInfoImpl(cmd.OutOrStdout(), redisClient)
		},
	}
	flags := cmd.Flags()
	config.InitAPIPortFlag(flags)
	config.InitAPITLSEnabledFlag(flags)
	config.InitDataPathFlag(flags)
	config.InitMetricsPortFlag(flags)
	config.InitMetricsRefreshIntervalFlag(flags)
	config.InitMetricsTLSEnabledFlag(flags)
	config.InitPostgresFlags(flags)
	config.InitRedisFlags(flags)
	config.InitSeasonsFlag(cmd, flags, false)
	config.InitTemporalFlags(flags)
	config.InitTemporalRetryFlags(flags)
	config.InitWorkerPortFlag(flags)
	config.InitWorkerTLSEnabledFlag(flags)
	config.InitYahooOAuth2Flags(flags)
	config.InitTLSCertificate(flags)
	config.InitTLSKey(flags)
	return cmd
}

func cmdInfoImpl(w io.Writer, redisClient redis.Client) error {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: w})
	log.Info().Msgf("API Port:                      %d", viper.GetInt(config.FlagAPIPort))
	log.Info().Msgf("API TLS Enabled:               %v", viper.GetBool(config.FlagAPITLSEnabled))
	log.Info().Msgf("Data Path:                     %s", viper.GetString(config.FlagDataPath))
	log.Info().Msgf("Log Level:                     %s", viper.GetString(config.FlagLogLevel))
	log.Info().Msgf("Metrics Port:                  %d", viper.GetInt(config.FlagMetricsPort))
	log.Info().Msgf("Metrics Refresh Interval:      %d", viper.GetInt(config.FlagMetricsRefreshInterval))
	log.Info().Msgf("Metrics TLS Enabled:           %v", viper.GetBool(config.FlagMetricsTLSEnabled))
	log.Info().Msgf("Postgres Database:             %s", viper.GetString(config.FlagPostgresDatabase))
	x := viper.GetString(config.FlagPostgresPassword)
	log.Info().Msgf("Postgres Password:             %s", strings.Repeat("*", len(x)))
	log.Info().Msgf("Postgres Port:                 %d", viper.GetInt(config.FlagPostgresPort))
	log.Info().Msgf("Postgres User:                 %s", viper.GetString(config.FlagPostgresUser))
	log.Info().Msgf("Redis DB:                      %d", viper.GetInt(config.FlagRedisDB))
	x = viper.GetString(config.FlagRedisPassword)
	log.Info().Msgf("Redis Password:                %s", strings.Repeat("*", len(x)))
	log.Info().Msgf("Redis URL:                     %s", viper.GetString(config.FlagRedisURL))
	log.Info().Msgf("Seasons:                       %s", viper.GetString(config.FlagYahooSeasons))
	log.Info().Msgf("Temporal Host/Port:            %s", viper.GetString(config.FlagTemporalHostPort))
	log.Info().Msgf("Temporal Namespace:            %s", viper.GetString(config.FlagTemporalNamespace))
	log.Info().Msgf("Temporal Retry Initial:        %ds", viper.GetInt(config.FlagTemporalRetryInitialInterval))
	log.Info().Msgf("Temporal Retry Max Attempts:   %d", viper.GetInt(config.FlagTemporalRetryMaxAttempts))
	log.Info().Msgf("TLS Certificate:               %s", viper.GetString(config.FlagTLSCertificate))
	log.Info().Msgf("TLS Key:                       %s", viper.GetString(config.FlagTLSKey))
	log.Info().Msgf("Yahoo! Oauth2 Client ID:       %s", viper.GetString(config.FlagYahooOAuth2ClientID))
	log.Info().Msgf("Public URL:                    %s", viper.GetString(config.FlagPublicURL))
	x = viper.GetString(config.FlagYahooOAuth2ClientSecret)
	log.Info().Msgf("Yahoo! Oauth2 Client Secret:   %s", strings.Repeat("*", len(x)))
	log.Info().Msgf("Worker Port:                   %d", viper.GetInt(config.FlagWorkerPort))
	log.Info().Msgf("Worker TLS Enabled:            %v", viper.GetBool(config.FlagWorkerTLSEnabled))

	// Display token information
	ctx := context.Background()
	token, err := redis.LoadTokenForUser(ctx, redisClient, config.DefaultUser)
	if err != nil {
		log.Info().Msg("Yahoo! Token:                  not found")
	} else {
		log.Info().Msg("Yahoo! Token:                  found")
		fmt.Fprintf(w, "  Access Token:  %s\n", token.AccessToken)
		fmt.Fprintf(w, "  Token Type:    %s\n", token.TokenType)
		if token.RefreshToken != "" {
			fmt.Fprintf(w, "  Refresh Token: %s\n", token.RefreshToken)
		}
		fmt.Fprintf(w, "  Expiry:        %s\n", token.Expiry.Format(time.RFC3339))

		if !token.Expiry.IsZero() {
			now := time.Now()
			if token.Expiry.After(now) {
				remaining := token.Expiry.Sub(now)
				fmt.Fprintf(w, "  Expires in:    %s\n", formatDuration(remaining))
			} else {
				elapsed := now.Sub(token.Expiry)
				fmt.Fprintf(w, "  Expired:       %s ago\n", formatDuration(elapsed))
			}
		}
	}

	return nil
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
