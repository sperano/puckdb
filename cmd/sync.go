package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

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
	config.InitAPIServerAddrFlag(flags)
	config.InitSeasonRangeFlags(flags)
	config.InitSeasonConcurrencyFlag(flags)
	config.InitSkipYahooPlayersFlag(flags)
	cmd.AddCommand(cmdSyncSeasons())
	return cmd
}

func syncInit(cmd *cobra.Command, logLevelChanged, logFileChanged bool) error {
	flags := cmd.Flags()
	if err := config.BindLoggingFlags(flags); err != nil {
		return err
	}
	if err := config.BindAPIServerAddrFlag(flags); err != nil {
		return err
	}
	if err := config.BindSeasonRangeFlags(flags); err != nil {
		return err
	}
	if err := config.BindSeasonConcurrencyFlag(flags); err != nil {
		return err
	}
	if err := config.BindSkipYahooPlayersFlag(flags); err != nil {
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
	if err := runInitialize(ctx, out, client, state); err != nil {
		return fmt.Errorf("initialization failed: %w", err)
	}
	if ctx.Err() != nil {
		return fmt.Errorf("workflow canceled by user")
	}

	// Step 2: Download Yahoo player pages (unless skipped)
	if !viper.GetBool(config.FlagSkipYahooPlayers) {
		if err := runDownloadYahooPlayer(ctx, out, client, state); err != nil {
			return err
		}
	} else {
		fmt.Println("Skipping Yahoo players download")
	}

	return nil
}

func runInitialize(ctx context.Context, out io.Writer, client *GraphQLClient, state *syncState) error {
	state.current = workflowInitialize
	started, err := client.Initialize(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("workflow canceled by user")
		}
		return fmt.Errorf("failed to trigger initialize: %w", err)
	}

	if !started {
		log.Warn().Msg("Workflow was not started (may already be running)")
	}
	if err := monitorWorkflow(ctx, out, "", "",
		client.GetInitializeStatus, config.DefaultWorkflowPollTimeout); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("workflow canceled by user")
		}
		return err
	}

	return nil
}

func runDownloadYahooPlayer(ctx context.Context, out io.Writer, client *GraphQLClient, state *syncState) error {
	state.current = workflowYahooPlayers
	started, err := client.DownloadYahooPlayers(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("workflow canceled by user")
		}
		return fmt.Errorf("failed to trigger downloadYahooPlayers: %w", err)
	}

	if !started {
		log.Warn().Msg("Yahoo players workflow was not started (may already be running)")
	}

	if err := monitorWorkflow(ctx, out, "Downloading Yahoo! players...", "Downloaded Yahoo! players.",
		client.GetDownloadYahooPlayersStatus, config.DefaultYahooPlayersTimeout); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("workflow canceled by user")
		}
		return fmt.Errorf("downloadYahooPlayers failed: %w", err)
	}

	return nil
}

//func cmdImportPlayers() *cobra.Command {
//	var cmd = &cobra.Command{
//		Use:   "players",
//		Short: "Import players into the database",
//		Long: `Trigger the importPlayers workflow via GraphQL API and monitor until completion.
//Use --season for a specific season, or --from-season/--to-season for a range.
//Use --monitor to watch an existing workflow without triggering a new one.`,
//		PreRunE: func(cmd *cobra.Command, args []string) error {
//			flags := cmd.Flags()
//			if err := config.BindAPIServerAddrFlag(flags); err != nil {
//				return err
//			}
//			if err := config.BindSeasonRangeFlags(flags); err != nil {
//				return err
//			}
//			if err := config.BindSeasonConcurrencyFlag(flags); err != nil {
//				return err
//			}
//			return config.BindMonitorFlag(flags)
//		},
//		RunE: runImportPlayers,
//	}
//	flags := cmd.Flags()
//	config.InitAPIServerAddrFlag(flags)
//	config.InitSeasonRangeFlags(flags)
//	config.InitSeasonConcurrencyFlag(flags)
//	config.InitMonitorFlag(flags)
//	return cmd
//}

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
		workflowYahooPlayers:  {"downloadYahooPlayers", s.client.CancelDownloadYahooPlayers},
		workflowInitialize:    {"initialize", s.client.CancelInitialize},
		workflowImportPlayers: {"importPlayers", s.client.CancelImportPlayers},
		workflowImportSeasons: {"importSeasons", s.client.CancelImportSeasons},
	}
	info, ok := cancelers[s.current]
	if !ok {
		return
	}

	fmt.Printf("\nCanceling %s workflow...\n", info.name)
	if _, err := info.fn(cancelCtx); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Failed to cancel %s workflow\n", info.name)
	} else {
		fmt.Printf("%s workflow canceled\n", info.name)
	}
}

func runSyncPlayers(cmd *cobra.Command, _ []string) error {
	apiAddr := viper.GetString(config.FlagAPIServerAddr)
	if apiAddr == "" {
		return fmt.Errorf("api-server-addr is required")
	}

	client := NewGraphQLClient(apiAddr)
	state := &syncState{client: client, current: workflowNone}

	// Set up signal handling for Ctrl+C
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		fmt.Println() // newline after ^C
		state.cancel()
		cancel()
	}()
	defer signal.Stop(sigChan)

	if viper.GetBool(config.FlagMonitor) {
		state.current = workflowImportPlayers
		return monitorWorkflow_legacy(ctx, cmd, "Importing players...", "Imported players.", client.GetImportPlayersStatus, config.DefaultWorkflowPollTimeout)
	}

	input := buildImportPlayersInput()

	state.current = workflowImportPlayers
	logEvent := log.Info().Str("server", apiAddr)
	if input.StartSeason != nil {
		logEvent = logEvent.Int("startSeason", *input.StartSeason)
	}
	if input.EndSeason != nil {
		logEvent = logEvent.Int("endSeason", *input.EndSeason)
	}
	if input.SeasonConcurrency != nil {
		logEvent = logEvent.Int("seasonConcurrency", *input.SeasonConcurrency)
	}
	logEvent.Msg("Triggering importPlayers workflow")

	started, err := client.ImportPlayers(ctx, input)
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("workflow canceled by user")
		}
		return fmt.Errorf("failed to trigger importPlayers: %w", err)
	}

	if !started {
		log.Warn().Msg("Workflow was not started (may already be running)")
	}

	if err := monitorWorkflow_legacy(ctx, cmd, "Importing players...", "Imported players.", client.GetImportPlayersStatus, config.DefaultWorkflowPollTimeout); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("workflow canceled by user")
		}
		return err
	}

	// Fetch and print result data
	resultData, err := client.GetImportPlayersResultData(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to fetch import result data")
	} else if resultData != nil {
		printImportPlayersResult(cmd, resultData)
	}

	return nil
}

func buildImportPlayersInput() *model.DownloadSeasonsInput {
	input := &model.DownloadSeasonsInput{}

	start, end := config.GetSeasonRange()
	if start > 0 {
		input.StartSeason = &start
	}
	if end > 0 {
		input.EndSeason = &end
	}

	if concurrency := viper.GetInt(config.FlagSeasonConcurrency); concurrency > 0 {
		input.SeasonConcurrency = &concurrency
	}

	return input
}

func printImportPlayersResult(cmd *cobra.Command, result *ImportPlayersResultData) {
	fmt.Fprintln(cmd.OutOrStdout())
	fmt.Fprintln(cmd.OutOrStdout(), "=== Import Results ===")
	fmt.Fprintf(cmd.OutOrStdout(), "Total players:       %d\n", result.TotalPlayers)
	fmt.Fprintf(cmd.OutOrStdout(), "Imported:            %d\n", result.ImportedPlayers)
	fmt.Fprintf(cmd.OutOrStdout(), "Matched with Yahoo:  %d\n", result.MatchedWithYahoo)

	if len(result.UnmatchedYahoo) > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "\nUnmatched Yahoo players (%d):\n", len(result.UnmatchedYahoo))
		for _, p := range result.UnmatchedYahoo {
			fmt.Fprintf(cmd.OutOrStdout(), "  - %s %s (#%d, %s) [Yahoo ID: %d]\n",
				p.FirstName, p.LastName, p.JerseyNumber, p.Team, p.YahooID)
		}
	}

	if len(result.Errors) > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "\nErrors (%d):\n", len(result.Errors))
		for _, e := range result.Errors {
			fmt.Fprintf(cmd.OutOrStdout(), "  - %s\n", e)
		}
	}
}

func cmdSyncSeasons() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "seasons",
		Short: "Sync seasons into the database",
		Long: `Trigger the syncSeasons workflow via GraphQL API and monitor until completion.
Use --season for a specific season, or --from-season/--to-season for a range.
Use --monitor to watch an existing workflow without triggering a new one.`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			flags := cmd.Flags()
			if err := config.BindAPIServerAddrFlag(flags); err != nil {
				return err
			}
			if err := config.BindSeasonRangeFlags(flags); err != nil {
				return err
			}
			if err := config.BindSeasonConcurrencyFlag(flags); err != nil {
				return err
			}
			return config.BindMonitorFlag(flags)
		},
		RunE: runSyncSeasons,
	}
	flags := cmd.Flags()
	config.InitAPIServerAddrFlag(flags)
	config.InitSeasonRangeFlags(flags)
	config.InitSeasonConcurrencyFlag(flags)
	config.InitMonitorFlag(flags)
	return cmd
}

func runSyncSeasons(cmd *cobra.Command, _ []string) error {
	apiAddr := viper.GetString(config.FlagAPIServerAddr)
	if apiAddr == "" {
		return fmt.Errorf("api-server-addr is required")
	}

	client := NewGraphQLClient(apiAddr)
	state := &syncState{client: client, current: workflowNone}

	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		fmt.Println()
		state.cancel()
		cancel()
	}()
	defer signal.Stop(sigChan)

	if viper.GetBool(config.FlagMonitor) {
		//log.Info().Str("server", apiAddr).Msg("Monitoring existing importSeasons workflow")
		state.current = workflowImportSeasons
		return monitorWorkflow_legacy(ctx, cmd, "Importing seasons...", "Imported seasons.", client.GetImportSeasonsStatus, config.DefaultWorkflowPollTimeout)
	}

	input := buildImportSeasonsInput()

	state.current = workflowImportSeasons
	logEvent := log.Info().Str("server", apiAddr)
	if input.StartSeason != nil {
		logEvent = logEvent.Int("startSeason", *input.StartSeason)
	}
	if input.EndSeason != nil {
		logEvent = logEvent.Int("endSeason", *input.EndSeason)
	}
	if input.SeasonConcurrency != nil {
		logEvent = logEvent.Int("seasonConcurrency", *input.SeasonConcurrency)
	}
	logEvent.Msg("Triggering importSeasons workflow")

	started, err := client.ImportSeasons(ctx, input)
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("workflow canceled by user")
		}
		return fmt.Errorf("failed to trigger importSeasons: %w", err)
	}

	if !started {
		log.Warn().Msg("Workflow was not started (may already be running)")
	}

	if err := monitorWorkflow_legacy(ctx, cmd, "Importing seasons...", "Imported seasons.", client.GetImportSeasonsStatus, config.DefaultWorkflowPollTimeout); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("workflow canceled by user")
		}
		return err
	}

	return nil
}

func buildImportSeasonsInput() *model.DownloadSeasonsInput {
	input := &model.DownloadSeasonsInput{}

	start, end := config.GetSeasonRange()
	if start > 0 {
		input.StartSeason = &start
	}
	if end > 0 {
		input.EndSeason = &end
	}

	if concurrency := viper.GetInt(config.FlagSeasonConcurrency); concurrency > 0 {
		input.SeasonConcurrency = &concurrency
	}

	return input
}
