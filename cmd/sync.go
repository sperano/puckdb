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
			completions := make([]string, 0, len(config.AllSyncSteps)+len(config.SyncStepGroups))
			completions = append(completions, config.AllSyncSteps...)
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
	apiAddr := viper.GetString(config.FlagAPIServerAddr)
	if apiAddr == "" {
		return fmt.Errorf("api-server-addr is required")
	}

	steps, err := config.ParseSyncSteps(args)
	if err != nil {
		return err
	}

	client := NewGraphQLClient(apiAddr)
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

	// Parallel phase: init + yahoo-players + fetch-seasons
	var parallelRunners []workflowRunner

	if steps[config.StepInit] {
		parallelRunners = append(parallelRunners, workflowRunner{
			workflowType: workflowInitialize,
			trigger:      func() (bool, error) { return client.Initialize(ctx) },
			getStatus:    client.GetInitializeStatus,
		})
	}

	if steps[config.StepYahooPlayers] {
		parallelRunners = append(parallelRunners, workflowRunner{
			workflowType: workflowYahooPlayers,
			trigger:      func() (bool, error) { return client.FetchYahooPlayers(ctx) },
			getStatus:    client.GetFetchYahooPlayersStatus,
		})
	}

	if steps[config.StepFetchSeasons] {
		parallelRunners = append(parallelRunners, workflowRunner{
			workflowType: workflowFetchSeasons,
			trigger:      func() (bool, error) { return client.FetchSeasons(ctx, buildSeasonsInput()) },
			getStatus:    client.GetFetchSeasonsStatus,
		})
	}

	if len(parallelRunners) == 1 {
		if err := parallelRunners[0].run(ctx, out, state); err != nil {
			return err
		}
	} else if len(parallelRunners) > 1 {
		if err := runParallel(ctx, out, state, parallelRunners...); err != nil {
			return err
		}
	}

	// Sequential phase
	seqSteps := []syncStep{
		{config.StepExtractBoxscorePlayers, workflowExtractBoxscorePlayers, "boxscore players extraction",
			func() (bool, error) { return client.ExtractBoxscorePlayers(ctx, buildSeasonsInput()) },
			client.GetExtractBoxscorePlayersStatus},
		{config.StepFetchPlayerLandings, workflowFetchPlayerLandings, "player landings fetch",
			func() (bool, error) { return client.FetchPlayerLandings(ctx, nil) },
			client.GetFetchPlayerLandingsStatus},
		{config.StepFetchPlayerLogs, workflowFetchPlayerLogs, "player logs fetch",
			func() (bool, error) { return client.FetchPlayerLogs(ctx, buildSeasonsInput()) },
			client.GetFetchPlayerLogsStatus},
		{config.StepProcessPlayers, workflowProcessPlayers, "players processing",
			func() (bool, error) { return client.ProcessPlayers(ctx, nil) },
			client.GetProcessPlayersStatus},
		{config.StepImportSeasons, workflowImportSeasons, "seasons import",
			func() (bool, error) { return client.ImportSeasons(ctx, buildSeasonsInput()) },
			client.GetImportSeasonsStatus},
		{config.StepImportPlayerLogs, workflowImportPlayerLogs, "player logs import",
			func() (bool, error) { return client.ImportPlayerLogs(ctx, buildSeasonsInput()) },
			client.GetImportPlayerLogsStatus},
		{config.StepFetchEdgeStats, workflowFetchEdgeStats, "edge stats fetch",
			func() (bool, error) { return client.FetchEdgeStats(ctx, buildSeasonsInput()) },
			client.GetFetchEdgeStatsStatus},
		{config.StepImportEdgeStats, workflowImportEdgeStats, "edge stats import",
			func() (bool, error) { return client.ImportEdgeStats(ctx, buildSeasonsInput()) },
			client.GetImportEdgeStatsStatus},
		{config.StepRefreshNews, workflowRefreshNews, "news refresh",
			func() (bool, error) { return client.RefreshNews(ctx, buildRefreshNewsInput()) },
			client.GetRefreshNewsStatus},
		{config.StepRefreshDraftRankings, workflowRefreshDraftRankings, "draft rankings refresh",
			func() (bool, error) {
				input, err := buildRefreshDraftRankingsInput()
				if err != nil {
					return false, err
				}
				return client.RefreshDraftRankings(ctx, input)
			},
			client.GetRefreshDraftRankingsStatus},
		{config.StepFetchAssets, workflowFetchAssets, "assets fetch",
			func() (bool, error) { return client.FetchAssets(ctx, nil) },
			client.GetFetchAssetsStatus},
	}

	for _, step := range seqSteps {
		if !steps[step.name] {
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

	fmt.Printf("✓ Sync completed in %s\n", formatElapsed(time.Since(start)))
	return nil
}

func validateExplicitSeasonBounds(cmd *cobra.Command) error {
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

// syncStep defines a workflow-driven sync phase for the data-driven loop.
type syncStep struct {
	name      string
	wt        workflowType
	label     string
	trigger   func() (bool, error)
	getStatus func(context.Context) (*WorkflowStatus, error)
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
