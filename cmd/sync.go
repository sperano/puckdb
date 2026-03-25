package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/graph/model"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func cmdSync() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "sync",
		Short: "Sync data into the database",
		Long:  `Trigger sync workflows via GraphQL API and monitor until completion.`,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Check which logging flags were explicitly set before binding
			logLevelChanged := cmd.Flags().Changed(config.FlagLogLevel)
			logFileChanged := cmd.Flags().Changed(config.FlagLogFile)
			if err := syncInit(cmd, logLevelChanged, logFileChanged); err != nil {
				return err
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := runSync(cmd, nil); err != nil {
				_, _ = fmt.Fprintln(os.Stderr, err.Error())
				return err
			}
			return nil
		},
	}
	flags := cmd.PersistentFlags()
	config.InitFlags(flags,
		&config.SeasonRangeFlags,
		&config.SeasonConcurrencyFlags,
		&config.SyncSkipFlags,
		&config.SpinnerFlags,
	)
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
	if err := config.BindFlags(flags,
		&config.SeasonRangeFlags,
		&config.SeasonConcurrencyFlags,
		&config.SyncSkipFlags,
		&config.SpinnerFlags,
	); err != nil {
		return err
	}
	BindFlags(cmd.PersistentFlags())
	BindFlags(cmd.Flags())

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
	apiAddr := viper.GetString(config.FlagAPIServerAddr)
	if apiAddr == "" {
		return fmt.Errorf("api-server-addr is required")
	}

	client := NewGraphQLClient(apiAddr)
	state := &syncState{client: client}
	out := cmd.OutOrStdout()

	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		cancel()
		// Cancel active Temporal workflows after context is done.
		// Must happen here (not in a defer) because workflowRunner.run()
		// clears its workflow from state.current on return — by the time
		// runSync's defers fire, the active list is already empty.
		state.cancel()
	}()
	defer signal.Stop(sigChan)

	// Steps 1-3: Initialize + Yahoo players + Fetch seasons (in parallel)
	var parallelRunners []workflowRunner

	if viper.GetBool(config.FlagSkipInit) {
		fmt.Println("- Skipping initialization.")
	} else {
		parallelRunners = append(parallelRunners, workflowRunner{
			workflowType: workflowInitialize,
			trigger:      func() (bool, error) { return client.Initialize(ctx) },
			getStatus:    client.GetInitializeStatus,
		})
	}

	if viper.GetBool(config.FlagSkipYahooPlayers) {
		fmt.Println("- Skipping Yahoo players fetch.")
	} else {
		parallelRunners = append(parallelRunners, workflowRunner{
			workflowType: workflowYahooPlayers,
			trigger:      func() (bool, error) { return client.FetchYahooPlayers(ctx) },
			getStatus:    client.GetFetchYahooPlayersStatus,
		})
	}

	if viper.GetBool(config.FlagSkipFetchSeasons) {
		fmt.Println("- Skipping seasons fetch.")
	} else {
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

	// Step 4: Extract boxscore players to Redis
	if !viper.GetBool(config.FlagSkipExtractBoxscorePlayers) {
		if err := runExtractBoxscorePlayers(ctx, out, client, state); err != nil {
			return fmt.Errorf("extracting boxscore players failed: %w", err)
		}
	} else {
		fmt.Println("- Skipping boxscore players extraction.")
	}

	// Step 5: Fetch player landing pages from NHL API
	if !viper.GetBool(config.FlagSkipFetchPlayerLandings) {
		if err := runFetchPlayerLandings(ctx, out, client, state); err != nil {
			return fmt.Errorf("fetching player landings failed: %w", err)
		}
	} else {
		fmt.Println("- Skipping player landings fetch.")
	}

	// Step 6: Fetch player game logs for historical seasons
	if !viper.GetBool(config.FlagSkipFetchPlayerLogs) {
		if err := runFetchPlayerLogs(ctx, out, client, state); err != nil {
			return fmt.Errorf("fetching player logs failed: %w", err)
		}
	} else {
		fmt.Println("- Skipping player logs fetch.")
	}

	// Step 7: Process players (download + import) unless skipped
	if !viper.GetBool(config.FlagSkipProcessPlayers) {
		if err := runProcessPlayers(ctx, out, client, state); err != nil {
			return fmt.Errorf("processing players failed: %w", err)
		}
		//resultData, err := client.GetProcessPlayersResultData(ctx)
		//if err != nil {
		//	log.Warn().Err(err).Msg("Failed to fetch process players result data")
		//} else if resultData != nil {
		//	printProcessPlayersResult(out, resultData)
		//}
	} else {
		fmt.Println("- Skipping players processing.")
	}

	// Step 8: Import seasons (boxscores, game stories, Yahoo data)
	if !viper.GetBool(config.FlagSkipImportSeasons) {
		if err := runImportSeasons(ctx, out, client, state); err != nil {
			return fmt.Errorf("importing seasons failed: %w", err)
		}
	} else {
		fmt.Println("- Skipping seasons import.")
	}

	// Step 9: Import player game logs
	if !viper.GetBool(config.FlagSkipImportPlayerLogs) {
		if err := runImportPlayerLogs(ctx, out, client, state); err != nil {
			return fmt.Errorf("importing player logs failed: %w", err)
		}
	} else {
		fmt.Println("- Skipping player logs import.")
	}

	fmt.Printf("✓ Sync completed in %s\n", formatElapsed(time.Since(start)))
	return nil
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
	if theme := viper.GetString(config.FlagTheme); theme != "" {
		sp.SetColorTheme(theme)
	} else if viper.GetBool(config.FlagRandomThemes) {
		sp.SetRandomLineThemes()
	} else if viper.GetBool(config.FlagRandomTheme) {
		sp.SetRandomTheme()
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
		return fmt.Errorf("failed to trigger workflow: %w", err)
	}

	if !started {
		log.Warn().Msg("Workflow was not started (may already be running)")
	}

	if err := monitorWorkflow(ctx, sp, r.getStatus); err != nil {
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
			return fmt.Errorf("failed to trigger workflow: %w", err)
		}
		if !started {
			log.Warn().Msg("Workflow was not started (may already be running)")
		}
	}

	// Collect status fetchers
	fetchers := make([]statusFetcher, len(runners))
	for i, r := range runners {
		fetchers[i] = r.getStatus
	}

	// Monitor all workflows
	if err := monitorWorkflows(ctx, sp, fetchers); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("workflow canceled by user")
		}
		return err
	}

	return nil
}

// monitorWorkflows polls multiple status endpoints and combines their display.
func monitorWorkflows(ctx context.Context, sp *spinner, fetchers []statusFetcher) error {
	time.Sleep(config.DefaultWorkflowStartupDelay)

	consecutiveFailures := 0
	done := make([]bool, len(fetchers))
	statuses := make([]*WorkflowStatus, len(fetchers))

	for {
		allDone := true
		var messages []string

		for i, fetch := range fetchers {
			if done[i] {
				// Already completed, use cached final message
				if statuses[i] != nil {
					messages = append(messages, formatStatusMessage(statuses[i]))
				}
				continue
			}

			status, err := fetch(ctx)
			if err != nil {
				consecutiveFailures++
				if consecutiveFailures >= config.MaxConsecutiveQueryFailures {
					sp.Cancel()
					return fmt.Errorf("workflow query failed %d times consecutively: %w", consecutiveFailures, err)
				}
				allDone = false
				continue
			}

			consecutiveFailures = 0
			statuses[i] = status
			messages = append(messages, formatStatusMessage(status))

			switch status.Result.Status {
			case model.TemporalWorkflowStatusCompleted:
				done[i] = true
			case model.TemporalWorkflowStatusFailed:
				sp.Cancel()
				if status.Result.FailureReason != nil {
					return fmt.Errorf("workflow failed: %s", *status.Result.FailureReason)
				}
				return fmt.Errorf("workflow failed")
			case model.TemporalWorkflowStatusCanceled:
				sp.Cancel()
				return fmt.Errorf("workflow was canceled")
			default:
				allDone = false
			}
		}

		// Combine all messages
		combined := ""
		for i, msg := range messages {
			if i > 0 {
				combined += "\n"
			}
			combined += msg
		}
		sp.SetMessage(combined)

		if allDone {
			sp.Stop()
			return nil
		}

		select {
		case <-ctx.Done():
			sp.Cancel()
			return ctx.Err()
		case <-time.After(pollDelay(consecutiveFailures)):
		}
	}
}

func runExtractBoxscorePlayers(ctx context.Context, out io.Writer, client *GraphQLClient, state *syncState) error {
	return workflowRunner{
		workflowType: workflowExtractBoxscorePlayers,
		trigger:      func() (bool, error) { return client.ExtractBoxscorePlayers(ctx, buildSeasonsInput()) },
		getStatus:    client.GetExtractBoxscorePlayersStatus,
	}.run(ctx, out, state)
}

func runFetchPlayerLandings(ctx context.Context, out io.Writer, client *GraphQLClient, state *syncState) error {
	return workflowRunner{
		workflowType: workflowFetchPlayerLandings,
		trigger:      func() (bool, error) { return client.FetchPlayerLandings(ctx, nil) },
		getStatus:    client.GetFetchPlayerLandingsStatus,
	}.run(ctx, out, state)
}

func runFetchPlayerLogs(ctx context.Context, out io.Writer, client *GraphQLClient, state *syncState) error {
	return workflowRunner{
		workflowType: workflowFetchPlayerLogs,
		trigger:      func() (bool, error) { return client.FetchPlayerLogs(ctx, buildSeasonsInput()) },
		getStatus:    client.GetFetchPlayerLogsStatus,
	}.run(ctx, out, state)
}

func runProcessPlayers(ctx context.Context, out io.Writer, client *GraphQLClient, state *syncState) error {
	return workflowRunner{
		workflowType: workflowProcessPlayers,
		trigger:      func() (bool, error) { return client.ProcessPlayers(ctx, nil) },
		getStatus:    client.GetProcessPlayersStatus,
	}.run(ctx, out, state)
}

func runImportSeasons(ctx context.Context, out io.Writer, client *GraphQLClient, state *syncState) error {
	return workflowRunner{
		workflowType: workflowImportSeasons,
		trigger:      func() (bool, error) { return client.ImportSeasons(ctx, buildSeasonsInput()) },
		getStatus:    client.GetImportSeasonsStatus,
	}.run(ctx, out, state)
}

func runImportPlayerLogs(ctx context.Context, out io.Writer, client *GraphQLClient, state *syncState) error {
	return workflowRunner{
		workflowType: workflowImportPlayerLogs,
		trigger:      func() (bool, error) { return client.ImportPlayerLogs(ctx, buildSeasonsInput()) },
		getStatus:    client.GetImportPlayerLogsStatus,
	}.run(ctx, out, state)
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
	}

	for _, wt := range active {
		info, ok := cancelers[wt]
		if !ok {
			continue
		}
		fmt.Printf("\nCanceling %s workflow...\n", info.name)
		if _, err := info.fn(cancelCtx); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Failed to cancel %s workflow\n", info.name)
		}
	}
}

func printProcessPlayersResult(out io.Writer, result *model.ProcessPlayersResultData) {
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "=== Process Players Results ===")
	_, _ = fmt.Fprintf(out, "Total players:       %d\n", result.TotalPlayers)
	_, _ = fmt.Fprintf(out, "Imported:            %d\n", result.ImportedPlayers)
	_, _ = fmt.Fprintf(out, "Matched with Yahoo:  %d\n", result.MatchedWithYahoo)
	_, _ = fmt.Fprintf(out, "\nDownload stats:\n")
	_, _ = fmt.Fprintf(out, "  Downloaded:        %d\n", result.Downloaded)
	_, _ = fmt.Fprintf(out, "  Cache hits:        %d\n", result.CacheHits)
	_, _ = fmt.Fprintf(out, "  Missing (404):     %d\n", result.Missing)
	_, _ = fmt.Fprintf(out, "\nYahoo player stats:\n")
	_, _ = fmt.Fprintf(out, "  Total Yahoo:       %d\n", result.TotalYahooPlayers)
	_, _ = fmt.Fprintf(out, "  Skipped non-NHL:   %d\n", result.SkippedNonNHL)
	_, _ = fmt.Fprintf(out, "  Verified non-NHL:  %d\n", result.VerifiedNonNHLThisRun)

	if len(result.TrulyUnmatched) > 0 {
		_, _ = fmt.Fprintf(out, "\nTruly unmatched Yahoo players (%d):\n", len(result.TrulyUnmatched))
		for _, p := range result.TrulyUnmatched {
			_, _ = fmt.Fprintf(out, "  - %s %s [Yahoo: %d, NHL: %d %s, Games: %d]\n",
				p.FirstName, p.LastName, p.YahooID, p.NhlPlayerID, p.NhlName, p.NhlGames)
		}
	}

	if len(result.Errors) > 0 {
		_, _ = fmt.Fprintf(out, "\nErrors (%d):\n", len(result.Errors))
		for _, e := range result.Errors {
			_, _ = fmt.Fprintf(out, "  - %s\n", e)
		}
	}
}
