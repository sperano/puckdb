package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/core"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// syncFlagGroups lists every flag group the sync command exposes. Defined
// once and shared by InitFlags and BindFlags (in syncInit) so the two can
// never drift out of sync.
var syncFlagGroups = []*config.FlagGroup{
	&config.SeasonRangeFlags,
	&config.SeasonConcurrencyFlags,
	&config.SyncBehaviorFlags,
	&config.SpinnerFlags,
	&config.APIBasicAuthFlags,
}

var seasonBoundFlagNames = []string{
	config.FlagSeasonYear,
	config.FlagFromSeasonYear,
	config.FlagToSeasonYear,
}

func cmdSync() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "sync [steps...]",
		Short: "Sync data into the database",
		Long: `Trigger sync workflows via GraphQL API and monitor until completion.

With no arguments, all steps run. Specify step names to run only those steps.

Steps (in execution order):
  init                       Initialize franchises, seasons, league structure
  yahoo-players              Fetch Yahoo! players
  fetch-seasons              Download NHL schedules, boxscores, Yahoo! fantasy
  extract-boxscore-players   Extract boxscore players to Redis
  fetch-player-landings      Fetch player landing pages from NHL API
  fetch-player-logs          Download player game logs
  process-players            Process players (download + import)
  import-seasons             Import seasons into the database
  import-player-logs         Import player game logs into the database
  fetch-edge-stats           Download Edge tracking data from NHL API
  import-edge-stats          Import Edge tracking data into the database
  refresh-news               Fetch player news and status sources, group reports into incidents
                             (--news-force ignores refresh schedules, --news-only picks sources)
  fetch-assets               Cache image assets (player photos, team logos, etc.) to disk

Groups (expand to multiple steps):
  seasons                    fetch-seasons + import-seasons
  players                    yahoo-players + all player steps + import-player-logs
  edge                       fetch-edge-stats + import-edge-stats`,
		ValidArgsFunction: func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
			// AllSyncSteps returns a copy, so appending cannot reach its list.
			completions := config.AllSyncSteps()
			for group := range config.SyncStepGroups {
				completions = append(completions, group)
			}
			return completions, cobra.ShellCompDirectiveNoFileComp
		},
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			logLevelChanged := cmd.Flags().Changed(config.FlagLogLevel)
			logFileChanged := cmd.Flags().Changed(config.FlagLogFile)
			return syncInit(cmd, logLevelChanged, logFileChanged)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := runSync(cmd, args); err != nil {
				_, _ = fmt.Fprintln(os.Stderr, err.Error())
				return err
			}
			return nil
		},
	}
	flags := cmd.PersistentFlags()
	config.InitFlags(flags, syncFlagGroups...)
	cmd.AddCommand(cmdSyncDraft())
	return cmd
}

func syncInit(cmd *cobra.Command, logLevelChanged, logFileChanged bool) error {
	flags := cmd.Flags()
	if err := config.BindLoggingFlags(flags); err != nil {
		return err
	}
	if err := config.APIServerAddrFlags.Bind(cmd.Root().PersistentFlags()); err != nil {
		return err
	}
	if err := config.BindFlags(flags, syncFlagGroups...); err != nil {
		return err
	}
	if err := BindFlags(cmd.PersistentFlags()); err != nil {
		return err
	}
	if err := BindFlags(cmd.Flags()); err != nil {
		return err
	}

	// Apply import-specific defaults before setting up logger
	if !logLevelChanged {
		viper.Set(config.FlagLogLevel, config.DefaultImportLogLevel)
	}
	if !logFileChanged {
		viper.Set(config.FlagLogFile, config.DefaultImportLogFile)
	}

	config.SetupLogger()
	config.SetLogLevel()
	config.LogIntro()
	return nil
}

func runSync(cmd *cobra.Command, args []string) error {
	start := time.Now()
	fmt.Printf("PuckDB Sync - %s\n", config.BuildNumber)
	if err := validateExplicitSeasonBounds(cmd); err != nil {
		return err
	}
	client, err := getGraphQLClient()
	if err != nil {
		return err
	}

	steps, err := config.ParseSyncSteps(args)
	if err != nil {
		return err
	}

	state := &syncState{client: client}
	out := cmd.OutOrStdout()

	// Polling uses a context detached from cmd.Context so that on the first
	// SIGINT/SIGTERM — delivered as cancellation of the root signal context,
	// see main.go and SignalContext — the workflow-cancel RPCs issued by
	// state.cancel() complete before polling is released and runSync
	// returns. A second signal force-quits: SignalContext restores the
	// default signal disposition after the first. Unlike worker, sync has
	// no local signal handler — this behavior depends on main wiring
	// SignalContext through ExecuteContext; executed without that wired
	// root, a signal falls back to Go's default disposition and kills the
	// process without canceling remote workflows.
	ctx, cancel := context.WithCancel(context.WithoutCancel(cmd.Context()))
	defer cancel()

	go watchSyncCancel(ctx, cmd.Context(), state.cancel, cancel)

	if err := runParallelPhase(ctx, out, state, parallelSyncRunners(ctx, client, steps)); err != nil {
		return err
	}
	if err := runSequentialPhase(ctx, out, state, sequentialSyncSteps(ctx, client), steps); err != nil {
		return err
	}

	fmt.Printf("✓ Sync completed in %s\n", core.FormatDuration(time.Since(start)))
	return nil
}

// runParallelPhase runs the parallel phase's runners: a single one with its
// own display, several with a combined one.
func runParallelPhase(ctx context.Context, out io.Writer, state *syncState, runners []workflowRunner) error {
	switch len(runners) {
	case 0:
		return nil
	case 1:
		return runners[0].run(ctx, out, state)
	default:
		return runParallel(ctx, out, state, runners...)
	}
}

// runSequentialPhase runs the enabled steps one after another, stopping at
// the first failure.
func runSequentialPhase(ctx context.Context, out io.Writer, state *syncState, seqSteps []syncStep, enabled map[string]bool) error {
	for _, step := range seqSteps {
		if !enabled[step.name] {
			continue
		}
		runner := workflowRunner{
			workflowType: step.wt,
			trigger:      step.trigger,
			getStatus:    step.getStatus,
		}
		if err := runner.run(ctx, out, state); err != nil {
			return fmt.Errorf("%s failed: %w", step.label, err)
		}
	}
	return nil
}

func validateExplicitSeasonBounds(cmd *cobra.Command) error {
	var fromSeason, toSeason int
	for _, name := range seasonBoundFlagNames {
		if !cmd.Flags().Changed(name) {
			continue
		}
		value, err := cmd.Flags().GetInt(name)
		if err != nil {
			return fmt.Errorf("read --%s: %w", name, err)
		}
		if value <= 0 {
			return fmt.Errorf("--%s must be greater than zero", name)
		}
		switch name {
		case config.FlagFromSeasonYear:
			fromSeason = value
		case config.FlagToSeasonYear:
			toSeason = value
		}
	}
	if fromSeason > 0 && toSeason > 0 && fromSeason > toSeason {
		return fmt.Errorf("invalid season range: --%s %d is after --%s %d",
			config.FlagFromSeasonYear, fromSeason, config.FlagToSeasonYear, toSeason)
	}
	return nil
}

// watchSyncCancel bridges root-context cancellation (first SIGINT/SIGTERM)
// to sync shutdown: it runs cancelWorkflows to completion, then release to
// unblock polling. If pollCtx ends first (normal completion), it returns
// without doing either, so the goroutine never leaks.
func watchSyncCancel(pollCtx, rootCtx context.Context, cancelWorkflows func(), release context.CancelFunc) {
	select {
	case <-pollCtx.Done():
		return
	case <-rootCtx.Done():
		cancelWorkflows()
		release()
	}
}

// workflowRunner encapsulates common workflow execution logic.
type workflowRunner struct {
	workflowType workflowType
	trigger      func() (bool, error)
	getStatus    statusFetcher
}

// newThemedSpinner creates a spinner with the user's configured color theme and starts it.
func newThemedSpinner(out io.Writer) *spinner {
	sp := newSpinner(out, "Starting...")
	switch theme := viper.GetString(config.FlagTheme); theme {
	case "":
		// no theme
	case ThemeRandom:
		sp.SetRandomTheme()
	case ThemeRandomEach:
		sp.SetRandomLineThemes()
	default:
		sp.SetColorTheme(theme)
	}
	sp.Start()
	return sp
}

func (r workflowRunner) run(ctx context.Context, out io.Writer, state *syncState) error {
	state.setActive(r.workflowType)
	defer state.clearActive(r.workflowType)

	sp := newThemedSpinner(out)

	started, err := r.trigger()
	if err != nil {
		sp.Cancel()
		if ctx.Err() != nil {
			return fmt.Errorf("workflow canceled by user")
		}
		return fmt.Errorf("trigger workflow: %w", err)
	}

	if !started {
		log.Warn().Msg("Workflow was not started (may already be running)")
	}

	if err := monitorWorkflow(ctx, sp, r.getStatus, r.workflowType); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("workflow canceled by user")
		}
		return err
	}

	return nil
}

// runParallel runs multiple workflows concurrently with a combined progress display.
func runParallel(ctx context.Context, out io.Writer, state *syncState, runners ...workflowRunner) error {
	// Mark all workflows as active
	for _, r := range runners {
		state.setActive(r.workflowType)
	}
	defer func() {
		for _, r := range runners {
			state.clearActive(r.workflowType)
		}
	}()

	sp := newThemedSpinner(out)

	// Trigger all workflows
	for _, r := range runners {
		started, err := r.trigger()
		if err != nil {
			sp.Cancel()
			if ctx.Err() != nil {
				return fmt.Errorf("workflow canceled by user")
			}
			return fmt.Errorf("trigger workflow: %w", err)
		}
		if !started {
			log.Warn().Msg("Workflow was not started (may already be running)")
		}
	}

	// Monitor all workflows
	if err := monitorWorkflows(ctx, sp, runners); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("workflow canceled by user")
		}
		return err
	}

	return nil
}

// syncState tracks the current workflow(s) for signal handling
type syncState struct {
	client  *GraphQLClient
	current []workflowType
	mu      sync.Mutex
}

func (s *syncState) setActive(wt workflowType) {
	s.mu.Lock()
	s.current = append(s.current, wt)
	s.mu.Unlock()
}

func (s *syncState) clearActive(wt workflowType) {
	s.mu.Lock()
	for i, w := range s.current {
		if w == wt {
			s.current = append(s.current[:i], s.current[i+1:]...)
			break
		}
	}
	s.mu.Unlock()
}

func (s *syncState) cancel() {
	s.mu.Lock()
	active := make([]workflowType, len(s.current))
	copy(active, s.current)
	s.mu.Unlock()

	if len(active) == 0 {
		return
	}

	cancelCtx, cancel := context.WithTimeout(context.Background(), config.DefaultCancelTimeout)
	defer cancel()

	type cancelInfo struct {
		name string
		fn   func(context.Context) (bool, error)
	}
	cancelers := map[workflowType]cancelInfo{
		workflowYahooPlayers:           {"fetchYahooPlayers", s.client.CancelFetchYahooPlayers},
		workflowInitialize:             {"initialize", s.client.CancelInitialize},
		workflowFetchSeasons:           {"fetchSeasons", s.client.CancelFetchSeasons},
		workflowExtractBoxscorePlayers: {"extractBoxscorePlayers", s.client.CancelExtractBoxscorePlayers},
		workflowFetchPlayerLandings:    {"fetchPlayerLandings", s.client.CancelFetchPlayerLandings},
		workflowFetchPlayerLogs:        {"fetchPlayerLogs", s.client.CancelFetchPlayerLogs},
		workflowProcessPlayers:         {"processPlayers", s.client.CancelProcessPlayers},
		workflowImportSeasons:          {"importSeasons", s.client.CancelImportSeasons},
		workflowImportPlayerLogs:       {"importPlayerLogs", s.client.CancelImportPlayerLogs},
		workflowFetchEdgeStats:         {"fetchEdgeStats", s.client.CancelFetchEdgeStats},
		workflowImportEdgeStats:        {"importEdgeStats", s.client.CancelImportEdgeStats},
		workflowFetchAssets:            {"fetchAssets", s.client.CancelFetchAssets},
		workflowRefreshNews:            {"refreshNews", s.client.CancelRefreshNews},
		workflowRefreshDraftRankings:   {"refreshDraftRankings", s.client.CancelRefreshDraftRankings},
	}

	for _, wt := range active {
		info, ok := cancelers[wt]
		if !ok {
			continue
		}
		fmt.Printf("\nCanceling %s workflow...\n", info.name)
		if _, err := info.fn(cancelCtx); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Failed to cancel %s workflow: %v\n", info.name, err)
		}
	}
}
