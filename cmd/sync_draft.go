package cmd

import (
	"fmt"
	"io"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/draftsession"
	"github.com/sperano/puckdb/internal/draftwatch"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var draftSyncFlagGroups = []*config.FlagGroup{
	&config.PostgresFlags,
	&config.RedisFlags,
	&config.YahooOAuth2Flags,
	&config.DataPathFlags,
	&config.GobCacheFlags,
	&config.YahooDownloadSleepFlags,
	&config.DraftSessionFlags,
	&config.DraftWatchFlags,
}

func cmdSyncDraft() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "draft",
		Short: "Force-refresh and reconcile one Yahoo draft session",
		Long: `Refresh only a league's mutable Yahoo status and draftresults resources,
replace their filesystem and Redis cache entries, and transactionally reconcile
the full-key draft session. With --watch, poll until canceled or postdraft; only
one draft synchronization per league key can run at a time. This never submits
a Yahoo pick.`,
		Args:    cobra.NoArgs,
		PreRunE: bindFlagsPreRunE(draftSyncFlagGroups...),
		RunE:    runSyncDraft,
	}
	config.InitFlags(cmd.Flags(), draftSyncFlagGroups...)
	return cmd
}

func runSyncDraft(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	pool, err := openPGXPool(ctx)
	if err != nil {
		return fmt.Errorf("open database pool: %w", err)
	}
	defer pool.Close()
	redisClient := newRedisClient()
	defer func() {
		if err := redisClient.Close(); err != nil {
			log.Warn().Err(err).Msg("close Redis after draft sync")
		}
	}()
	gobCache, err := newGobCache(redisClient)
	if err != nil {
		return fmt.Errorf("create draft cache: %w", err)
	}
	id, err := draftwatch.ResolveIdentity(ctx, pool, viper.GetString(config.FlagDraftSessionLeague), viper.GetInt(config.FlagDraftSeason))
	if err != nil {
		return err
	}
	repository := draftwatch.NewRepository(pool)
	runner := draftwatch.Runner{
		Pool: pool, Repository: repository,
		Source: draftwatch.NewYahooSource(newDefaultStorage(), gobCache, newYahooDownloader(redisClient)),
	}
	out := cmd.OutOrStdout()
	if !viper.GetBool(config.FlagDraftWatch) {
		session, report, err := runner.SyncOnce(ctx, id)
		if err != nil {
			return err
		}
		return writeDraftSyncOutcome(out, draftwatch.Outcome{Session: session, Report: report})
	}
	options, err := draftWatchOptions(out)
	if err != nil {
		return err
	}
	return runner.Watch(ctx, id, options)
}

func draftWatchOptions(out io.Writer) (draftwatch.WatchOptions, error) {
	intervalSeconds := viper.GetInt(config.FlagDraftPollInterval)
	maxBackoffSeconds := viper.GetInt(config.FlagDraftMaxBackoff)
	finalTimeoutSeconds := viper.GetInt(config.FlagDraftFinalTimeout)
	if intervalSeconds <= 0 || maxBackoffSeconds <= 0 || finalTimeoutSeconds <= 0 {
		return draftwatch.WatchOptions{}, fmt.Errorf("draft poll interval, maximum backoff and final timeout must be positive")
	}
	return draftwatch.WatchOptions{
		Interval:     time.Duration(intervalSeconds) * time.Second,
		MaxBackoff:   time.Duration(maxBackoffSeconds) * time.Second,
		FinalTimeout: time.Duration(finalTimeoutSeconds) * time.Second,
		OnOutcome: func(outcome draftwatch.Outcome) {
			if err := writeDraftSyncOutcome(out, outcome); err != nil {
				log.Error().Err(err).Msg("write draft sync status")
			}
		},
	}, nil
}

func writeDraftSyncOutcome(out io.Writer, outcome draftwatch.Outcome) error {
	if outcome.Err != nil {
		_, err := fmt.Fprintf(out, "draft poll failed (%s): %v\n", draftwatch.ClassifyError(outcome.Err), outcome.Err)
		return err
	}
	manual, conflicts := draftBoardCounts(outcome.Session.State)
	label := "poll"
	if outcome.Final {
		label = "final reconciliation"
	}
	if _, err := fmt.Fprintf(out,
		"%s: league=%s version=%d picks=%d manual=%d conflicts=%d authoritative=%t safe=%t status=%s changed=%t skipped=%d\n",
		label, outcome.Session.Identity.LeagueKey, outcome.Session.State.Version,
		len(draftsession.EffectiveBoard(outcome.Session.State)), manual, conflicts,
		outcome.Report.Complete, outcome.Session.SafeToRecommend(),
		outcome.Session.DraftStatus, outcome.Report.Changed, len(outcome.Report.Skipped)); err != nil {
		return err
	}
	for _, skipped := range outcome.Report.Skipped {
		if _, err := fmt.Fprintf(out, "skipped response_index=%d round=%d pick=%d reason=%s\n",
			skipped.Index+1, skipped.Key.Round, skipped.Key.Pick, skipped.Reason); err != nil {
			return err
		}
	}
	return nil
}

func draftBoardCounts(state draftsession.State) (manual, conflicts int) {
	manual = len(state.Manual)
	for _, change := range state.Manual {
		if change.Conflict {
			conflicts++
		}
	}
	return manual, conflicts
}
