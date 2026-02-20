package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
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
	state := &syncState{client: client, current: workflowNone}
	out := cmd.OutOrStdout()

	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		state.cancel()
		cancel()
	}()
	defer signal.Stop(sigChan)

	// Step 1: Initialize reference data (franchises, seasons, league structure)
	if !viper.GetBool(config.FlagSkipInitializing) {
		if err := runInitialize(ctx, out, client, state); err != nil {
			return fmt.Errorf("initialization failed: %w", err)
		}
	} else {
		fmt.Println("- Skipping initialization.")
	}

	// Step 2: Fetch Yahoo players (unless skipped)
	if !viper.GetBool(config.FlagSkipYahooPlayers) {
		if err := runFetchYahooPlayers(ctx, out, client, state); err != nil {
			return fmt.Errorf("fetching Yahoo! players failed: %w", err)
		}
	} else {
		fmt.Println("- Skipping Yahoo players fetch.")
	}

	// Step 3: Fetch seasons data (unless skipped)
	if !viper.GetBool(config.FlagSkipSeasons) {
		if err := runFetchSeasons(ctx, out, client, state); err != nil {
			return fmt.Errorf("fetching seasons failed: %w", err)
		}
	} else {
		fmt.Println("- Skipping seasons fetch.")
	}

	// Step 4: Process players (download + import) unless skipped
	if !viper.GetBool(config.FlagSkipProcessPlayers) {
		if err := runProcessPlayers(ctx, out, client, state); err != nil {
			return fmt.Errorf("processing players failed: %w", err)
		}
		// Fetch and print result data
		_, err := client.GetProcessPlayersResultData(ctx)
		if err != nil {
			log.Warn().Err(err).Msg("Failed to fetch process players result data")
			//} else if resultData != nil {
			//	printProcessPlayersResult(out, resultData)
		}
	} else {
		fmt.Println("- Skipping players processing.")
	}

	// Step 5: Import seasons (unless skipped)
	if !viper.GetBool(config.FlagSkipImportSeasons) {
		if err := runImportSeasons(ctx, out, client, state); err != nil {
			return fmt.Errorf("importing seasons failed: %w", err)
		}
	} else {
		fmt.Println("- Skipping seasons import.")
	}

	fmt.Printf("✓ Sync completed in %s\n", formatElapsed(time.Since(start)))
	return nil
}

// workflowRunner encapsulates common workflow execution logic.
type workflowRunner struct {
	workflowType workflowType
	trigger      func() (bool, error)
	getStatus    statusFetcher
	timeout      time.Duration
}

func (r workflowRunner) run(ctx context.Context, out io.Writer, state *syncState) error {
	state.current = r.workflowType

	started, err := r.trigger()
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("workflow canceled by user")
		}
		return fmt.Errorf("failed to trigger workflow: %w", err)
	}

	if !started {
		log.Warn().Msg("Workflow was not started (may already be running)")
	}

	if err := monitorWorkflow(ctx, out, r.getStatus, r.timeout); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("workflow canceled by user")
		}
		return err
	}

	return nil
}

func runInitialize(ctx context.Context, out io.Writer, client *GraphQLClient, state *syncState) error {
	return workflowRunner{
		workflowType: workflowInitialize,
		trigger:      func() (bool, error) { return client.Initialize(ctx) },
		getStatus:    client.GetInitializeStatus,
		timeout:      config.DefaultWorkflowPollTimeout,
	}.run(ctx, out, state)
}

func runFetchYahooPlayers(ctx context.Context, out io.Writer, client *GraphQLClient, state *syncState) error {
	return workflowRunner{
		workflowType: workflowYahooPlayers,
		trigger:      func() (bool, error) { return client.FetchYahooPlayers(ctx) },
		getStatus:    client.GetFetchYahooPlayersStatus,
		timeout:      config.DefaultYahooPlayersTimeout,
	}.run(ctx, out, state)
}

func runFetchSeasons(ctx context.Context, out io.Writer, client *GraphQLClient, state *syncState) error {
	return workflowRunner{
		workflowType: workflowFetchSeasons,
		trigger:      func() (bool, error) { return client.FetchSeasons(ctx, buildSeasonsInput()) },
		getStatus:    client.GetFetchSeasonsStatus,
		timeout:      config.DefaultWorkflowPollTimeout,
	}.run(ctx, out, state)
}

func runProcessPlayers(ctx context.Context, out io.Writer, client *GraphQLClient, state *syncState) error {
	return workflowRunner{
		workflowType: workflowProcessPlayers,
		trigger:      func() (bool, error) { return client.ProcessPlayers(ctx, buildSeasonsInput()) },
		getStatus:    client.GetProcessPlayersStatus,
		timeout:      config.DefaultWorkflowPollTimeout,
	}.run(ctx, out, state)
}

func runImportSeasons(ctx context.Context, out io.Writer, client *GraphQLClient, state *syncState) error {
	return workflowRunner{
		workflowType: workflowImportSeasons,
		trigger:      func() (bool, error) { return client.ImportSeasons(ctx, buildSeasonsInput()) },
		getStatus:    client.GetImportSeasonsStatus,
		timeout:      config.DefaultWorkflowPollTimeout,
	}.run(ctx, out, state)
}

// syncState tracks the current workflow for signal handling
type syncState struct {
	client  *GraphQLClient
	current workflowType
}

func (s *syncState) cancel() {
	cancelCtx, cancel := context.WithTimeout(context.Background(), config.DefaultCancelTimeout)
	defer cancel()

	type cancelInfo struct {
		name string
		fn   func(context.Context) (bool, error)
	}
	cancelers := map[workflowType]cancelInfo{
		workflowYahooPlayers:   {"fetchYahooPlayers", s.client.CancelFetchYahooPlayers},
		workflowInitialize:     {"initialize", s.client.CancelInitialize},
		workflowFetchSeasons:   {"fetchSeasons", s.client.CancelFetchSeasons},
		workflowProcessPlayers: {"processPlayers", s.client.CancelProcessPlayers},
		workflowImportSeasons:  {"importSeasons", s.client.CancelImportSeasons},
	}
	info, ok := cancelers[s.current]
	if !ok {
		return
	}

	fmt.Printf("\nCanceling %s workflow...\n", info.name)
	if _, err := info.fn(cancelCtx); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Failed to cancel %s workflow\n", info.name)
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
