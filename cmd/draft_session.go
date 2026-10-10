package cmd

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/draftsession"
	"github.com/sperano/puckdb/internal/draftwatch"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const capabilityObservationLimit = 500

var draftSessionCLIFlagGroups = []*config.FlagGroup{
	&config.PostgresFlags,
	&config.DraftSessionFlags,
}

func cmdDraftSession() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Inspect and manually maintain a live draft session",
		Long:  "Local operations only; no command in this group submits a pick to Yahoo.",
	}
	config.InitFlags(cmd.PersistentFlags(), draftSessionCLIFlagGroups...)
	cmd.AddCommand(
		newDraftSessionCommand("status", "Show the reconciled board and freshness", runDraftSessionStatus),
		newDraftSessionCommand("capability", "Report observed Yahoo draftresults behavior", runDraftCapability),
		newDraftSessionCommand("add", "Add a local manual pick", runDraftManualAdd,
			&config.DraftManualSlotFlags, &config.DraftManualPickFlags),
		newDraftSessionCommand("correct", "Correct a local board slot", runDraftManualCorrect,
			&config.DraftManualSlotFlags, &config.DraftManualPickFlags),
		newDraftSessionCommand("undo", "Undo a local board slot", runDraftManualUndo,
			&config.DraftManualSlotFlags),
		newDraftSessionCommand("resolve", "Resolve a Yahoo/manual conflict", runDraftManualResolve,
			&config.DraftManualSlotFlags, &config.DraftResolutionFlags),
	)
	return cmd
}

func newDraftSessionCommand(use, short string, run func(*cobra.Command, draftSessionContext) error,
	localGroups ...*config.FlagGroup) *cobra.Command {
	cmd := &cobra.Command{
		Use: use, Short: short, Args: cobra.NoArgs,
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			groups := append([]*config.FlagGroup{}, draftSessionCLIFlagGroups...)
			groups = append(groups, localGroups...)
			return config.BindFlags(cmd.Flags(), groups...)
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			loaded, err := loadDraftSessionContext(cmd.Context())
			if err != nil {
				return err
			}
			defer loaded.pool.Close()
			return run(cmd, loaded)
		},
	}
	config.InitFlags(cmd.Flags(), localGroups...)
	return cmd
}

type draftSessionContext struct {
	pool       *pgxpool.Pool
	identity   draftwatch.Identity
	repository *draftwatch.Repository
}

func loadDraftSessionContext(ctx context.Context) (draftSessionContext, error) {
	pool, err := openPGXPool(ctx)
	if err != nil {
		return draftSessionContext{}, fmt.Errorf("open database pool: %w", err)
	}
	id, err := draftwatch.ResolveIdentity(ctx, pool, viper.GetString(config.FlagDraftSessionLeague), viper.GetInt(config.FlagDraftSeason))
	if err != nil {
		pool.Close()
		return draftSessionContext{}, err
	}
	return draftSessionContext{pool: pool, identity: id, repository: draftwatch.NewRepository(pool)}, nil
}

func runDraftSessionStatus(cmd *cobra.Command, loaded draftSessionContext) error {
	session, err := loaded.repository.Get(cmd.Context(), loaded.identity.LeagueKey)
	if err != nil {
		return err
	}
	return writeDraftSession(cmd.OutOrStdout(), session)
}

func runDraftManualAdd(cmd *cobra.Command, loaded draftSessionContext) error {
	return runDraftManualPick(cmd, loaded, draftsession.ManualAdd)
}

func runDraftManualCorrect(cmd *cobra.Command, loaded draftSessionContext) error {
	return runDraftManualPick(cmd, loaded, draftsession.ManualCorrect)
}

func runDraftManualPick(cmd *cobra.Command, loaded draftSessionContext, kind draftsession.ManualKind) error {
	key, err := draftPickKeyFromFlags()
	if err != nil {
		return err
	}
	teamKey := viper.GetString(config.FlagDraftTeamKey)
	playerKey := viper.GetString(config.FlagDraftPlayerKey)
	teamID, err := draftwatch.ParseTeamKey(teamKey, loaded.identity)
	if err != nil {
		return err
	}
	playerID, err := draftwatch.ParsePlayerKey(playerKey, loaded.identity)
	if err != nil {
		return err
	}
	pick := draftsession.Pick{Key: key, TeamID: teamID, PlayerID: playerID}
	if cost := viper.GetInt(config.FlagDraftCost); cost > 0 {
		pick.Cost = &cost
	}
	session, _, err := loaded.repository.ApplyManual(cmd.Context(), loaded.identity,
		draftsession.ManualOperation{Kind: kind, Key: key, Pick: &pick}, time.Now().UTC())
	if err != nil {
		return err
	}
	return writeDraftSession(cmd.OutOrStdout(), session)
}

func runDraftManualUndo(cmd *cobra.Command, loaded draftSessionContext) error {
	key, err := draftPickKeyFromFlags()
	if err != nil {
		return err
	}
	session, _, err := loaded.repository.ApplyManual(cmd.Context(), loaded.identity,
		draftsession.ManualOperation{Kind: draftsession.ManualUndo, Key: key}, time.Now().UTC())
	if err != nil {
		return err
	}
	return writeDraftSession(cmd.OutOrStdout(), session)
}

func runDraftManualResolve(cmd *cobra.Command, loaded draftSessionContext) error {
	key, err := draftPickKeyFromFlags()
	if err != nil {
		return err
	}
	var choice draftsession.ConflictChoice
	switch strings.TrimSpace(viper.GetString(config.FlagDraftResolution)) {
	case "keep-manual":
		choice = draftsession.KeepManual
	case "accept-upstream":
		choice = draftsession.AcceptUpstream
	default:
		return fmt.Errorf("--%s must be keep-manual or accept-upstream", config.FlagDraftResolution)
	}
	session, _, err := loaded.repository.ResolveConflict(cmd.Context(), loaded.identity, key, choice, time.Now().UTC())
	if err != nil {
		return err
	}
	return writeDraftSession(cmd.OutOrStdout(), session)
}

func draftPickKeyFromFlags() (draftsession.PickKey, error) {
	key := draftsession.PickKey{
		Round: viper.GetInt(config.FlagDraftRound),
		Pick:  viper.GetInt(config.FlagDraftPick),
	}
	if key.Round <= 0 || key.Pick <= 0 {
		return draftsession.PickKey{}, fmt.Errorf("--%s and --%s must be positive", config.FlagDraftRound, config.FlagDraftPick)
	}
	return key, nil
}

func writeDraftSession(out io.Writer, session draftwatch.Session) error {
	manual, conflicts := draftBoardCounts(session.State)
	if _, err := fmt.Fprintf(out,
		"league=%s version=%d status=%s complete=%t safe=%t manual=%d conflicts=%d last_poll=%s last_success=%s error=%q\n",
		session.Identity.LeagueKey, session.State.Version, session.DraftStatus, session.Complete,
		session.SafeToRecommend(), manual, conflicts, formatOptionalTime(session.LastPollAt),
		formatOptionalTime(session.LastSuccessAt), session.LastError); err != nil {
		return err
	}
	for _, entry := range draftsession.EffectiveBoard(session.State) {
		conflict := ""
		if change, ok := session.State.Manual[entry.Pick.Key]; ok && change.Conflict {
			conflict = " CONFLICT"
		}
		if _, err := fmt.Fprintf(out, "round=%d pick=%d team=%d player=%d source=%s%s\n",
			entry.Pick.Key.Round, entry.Pick.Key.Pick, entry.Pick.TeamID, entry.Pick.PlayerID,
			entry.Source, conflict); err != nil {
			return err
		}
	}
	return nil
}

func formatOptionalTime(value *time.Time) string {
	if value == nil {
		return "never"
	}
	return value.UTC().Format(time.RFC3339)
}

func runDraftCapability(cmd *cobra.Command, loaded draftSessionContext) error {
	session, err := loaded.repository.Get(cmd.Context(), loaded.identity.LeagueKey)
	if err != nil {
		return err
	}
	observations, err := loaded.repository.ListObservations(cmd.Context(), loaded.identity.LeagueKey, capabilityObservationLimit)
	if err != nil {
		return err
	}
	rules, err := draft.LoadSnapshot(cmd.Context(), sqlcdb.New(loaded.pool), loaded.identity.Season, loaded.identity.LeagueID)
	if err != nil {
		return err
	}
	return writeDraftCapability(cmd.OutOrStdout(), session, observations, rules.Rules)
}

func writeDraftCapability(out io.Writer, session draftwatch.Session, observations []draftwatch.Observation, rules draft.Rules) error {
	stats := summarizeObservations(observations)
	_, err := fmt.Fprintf(out, `# Yahoo draft watch capability: %s

- League: %s (%d teams, %s scoring)
- Draft format: %s; auction=%t; pick time=%d seconds; imported status=%s
- Observed poll window: %s to %s (%d polls, %d successful)
- Poll request duration: min=%s median=%s max=%s
- Changed snapshots: %d; largest poll-to-change detection gap=%s
- Partial/incomplete snapshots: %d; empty snapshots: %d
- Authentication failures: %d; rate-limit responses: %d; other failures: %d
- Current session: version=%d, status=%s, upstream complete=%t, recommendation safe=%t
- Yahoo publication latency: unsupported by draftresults (no per-pick upstream timestamp); the detection gap above is only a polling upper bound.
- Manual fallback: available through draft session add/correct/undo; unresolved conflicts require draft session resolve.
`, session.Identity.LeagueKey, rules.Name, rules.NumTeams, rules.ScoringType,
		rules.Draft.Type, rules.Draft.Auction, rules.Draft.PickTimeSeconds, rules.Draft.Status,
		stats.first, stats.last, len(observations), stats.successes,
		stats.minimum, stats.median, stats.maximum, stats.changes, stats.maxChangeGap,
		stats.partial, stats.empty, stats.authentication, stats.rateLimited, stats.otherFailures,
		session.State.Version, session.DraftStatus, session.State.UpstreamComplete, session.SafeToRecommend())
	return err
}

type observationSummary struct {
	first, last                                string
	minimum, median, maximum                   time.Duration
	maxChangeGap                               time.Duration
	successes, changes, partial, empty         int
	authentication, rateLimited, otherFailures int
}

func summarizeObservations(observations []draftwatch.Observation) observationSummary {
	if len(observations) == 0 {
		return observationSummary{first: "unmeasured", last: "unmeasured"}
	}
	chronological := append([]draftwatch.Observation(nil), observations...)
	sort.Slice(chronological, func(i, j int) bool { return chronological[i].PolledAt.Before(chronological[j].PolledAt) })
	summary := observationSummary{
		first: chronological[0].PolledAt.UTC().Format(time.RFC3339),
		last:  chronological[len(chronological)-1].PolledAt.UTC().Format(time.RFC3339),
	}
	var durations []time.Duration
	for index, observation := range chronological {
		if observation.Success {
			summary.successes++
			durations = append(durations, observation.Duration)
			if observation.ParsedCount == 0 {
				summary.empty++
			}
			if !observation.Authoritative {
				summary.partial++
			}
		}
		if observation.Changed {
			summary.changes++
			if index > 0 {
				gap := observation.PolledAt.Sub(chronological[index-1].PolledAt)
				if gap > summary.maxChangeGap {
					summary.maxChangeGap = gap
				}
			}
		}
		switch observation.ErrorClass {
		case "authentication":
			summary.authentication++
		case "rate_limited":
			summary.rateLimited++
		case "":
		default:
			summary.otherFailures++
		}
	}
	if len(durations) > 0 {
		sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
		summary.minimum = durations[0]
		summary.median = durations[len(durations)/2]
		summary.maximum = durations[len(durations)-1]
	}
	return summary
}
