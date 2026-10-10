package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/httpx"
	"github.com/sperano/puckdb/internal/notify"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/yahooaccess"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var yahooCheckFlagGroups = []*config.FlagGroup{
	&config.RedisFlags,
	&config.YahooOAuth2Flags,
	&config.YahooSeasonsFlags,
	&config.DataPathFlags,
	&config.YahooAccessCheckFlags,
	&config.NotifyEmailFlags,
}

func cmdYahooCheckAccess() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check-access",
		Short: "Check whether the Yahoo API serves a season's leagues",
		Long: `Check whether the Yahoo Fantasy API serves a season's leagues to this app:
resolve the season's game key, then fetch every configured league's settings
with the stored OAuth token. When the game key lookup fails, the leagues are
still probed with the game key cached in --data-path, as the importer does.
Also probes the season before the requested one as
a control, so the report shows whether the app still reaches a season Yahoo
already serves. Prints AUTHORIZED, NOT AUTHORIZED (Yahoo answers 403) or ERROR
(no usable token, network or unexpected response) per season, and emails the
report when --notify-email-to is set. Exits non-zero on ERROR or when the email
cannot be sent. See https://wiki.spe.quebec/en/puckdb/yahoo-access-check.`,
		PreRunE: bindFlagsPreRunE(yahooCheckFlagGroups...),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runYahooCheckAccess(cmd.Context(), cmd.OutOrStdout())
		},
	}
	config.InitFlags(cmd.Flags(), yahooCheckFlagGroups...)
	return cmd
}

func runYahooCheckAccess(ctx context.Context, out io.Writer) error {
	to := notify.ParseRecipients(viper.GetString(config.FlagNotifyEmailTo))
	smtpCfg := smtpConfigFromFlags()
	email := notify.Email{From: viper.GetString(config.FlagSMTPFrom), To: to}
	if len(to) > 0 {
		if err := errors.Join(smtpCfg.Validate(), email.Validate()); err != nil {
			return fmt.Errorf("notification email: %w", err)
		}
	}

	report := checkYahooAccess(context.WithValue(ctx, config.CtxUser, config.DefaultUser))
	fmt.Fprintln(out, report.Subject())
	fmt.Fprintln(out)
	fmt.Fprint(out, report.Text())

	if len(to) > 0 {
		email.Subject, email.Body, email.Date = report.Subject(), report.Text(), report.CheckedAt
		if err := notify.Send(ctx, smtpCfg, email); err != nil {
			return err
		}
		log.Info().Strs("to", to).Str("subject", email.Subject).Msg("Yahoo access report emailed")
	}
	if report.Outcome() == yahooaccess.Failed {
		return errors.New("yahoo access check could not decide; see the report above")
	}
	return nil
}

// checkYahooAccess runs the check for the configured season and the season
// before it; a setup failure (no seasons config, no token) becomes an ERROR
// report so it is emailed too.
func checkYahooAccess(ctx context.Context) yahooaccess.Report {
	ctx, cancel := context.WithTimeout(ctx, config.DefaultYahooCheckTimeout)
	defer cancel()
	checkedAt := time.Now()
	season := viper.GetInt(config.FlagYahooCheckSeason)
	seasons, err := config.GetYahooSeasonsConfig()
	if err != nil {
		return yahooaccess.FailedReport(checkedAt, err)
	}
	checks, err := yahooaccess.ResolveChecks(seasons, season)
	if err != nil {
		return yahooaccess.FailedReport(checkedAt, err)
	}
	storage, err := cachedDataStorage()
	if err != nil {
		return yahooaccess.FailedReport(checkedAt, err)
	}
	redisClient := newRedisClient()
	defer redisClient.Close()
	if err := cache.WaitReady(ctx, redisClient, config.DefaultRedisReadyRetries, config.DefaultRedisReadyRetryDelay); err != nil {
		return yahooaccess.FailedReport(checkedAt, err)
	}
	client, err := httpx.NewYahooHTTPClient(ctx, redisClient, yahooAuthFrom(viper.GetViper()))
	if tokenErr, ok := errors.AsType[*cache.OAuth2TokenMissingError](err); ok && tokenErr.PublicURL == "" {
		tokenErr.PublicURL = viper.GetString(config.FlagPublicURL)
	}
	if err != nil {
		return yahooaccess.FailedReport(checkedAt, err)
	}
	checker := yahooaccess.Checker{Client: client, Timeout: config.DefaultHTTPClientTimeout, Storage: storage}
	return checker.Check(ctx, checks, checkedAt)
}

// cachedDataStorage returns the storage of the data path holding the
// importer's cached game keys, or nil when --data-path is not set. A data path
// that is set but is not a directory (a volume that is not mounted) is an
// error, so the report does not blame Yahoo for a missing cache.
func cachedDataStorage() (store.Storage, error) {
	dataPath := viper.GetString(config.FlagDataPath)
	if dataPath == "" {
		return nil, nil
	}
	info, err := os.Stat(dataPath)
	if err != nil {
		return nil, fmt.Errorf("data path: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("data path %s is not a directory", dataPath)
	}
	return store.NewFSStorage(dataPath), nil
}

func smtpConfigFromFlags() notify.SMTPConfig {
	return notify.SMTPConfig{
		Host:     viper.GetString(config.FlagSMTPHost),
		Port:     viper.GetInt(config.FlagSMTPPort),
		Username: viper.GetString(config.FlagSMTPUsername),
		Password: viper.GetString(config.FlagSMTPPassword),
		Timeout:  config.DefaultSMTPTimeout,
	}
}
