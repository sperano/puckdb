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
	config.InitAPIServerAddrFlag(flags)
	config.InitSeasonRangeFlags(flags)
	config.InitSeasonConcurrencyFlag(flags)
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
	fmt.Printf("PuckDB Sync - %s\n\n", config.BuildNumber)
	apiAddr := viper.GetString(config.FlagAPIServerAddr)
	if apiAddr == "" {
		return fmt.Errorf("api-server-addr is required")
	}
	ctx := cmd.Context()
	client := NewGraphQLClient(apiAddr)

	// Step 1: Initialize reference data (franchises, seasons, league structure)
	if err := runInitialize(ctx, cmd.OutOrStdout(), client); err != nil {
		return fmt.Errorf("initialization failed: %w", err)
	}

	// Step 2: Sync players
	//return runSyncPlayers(cmd, args)
	return nil
}

func runInitialize(ctx context.Context, out io.Writer, client *GraphQLClient) error {
	state := &syncState{client: client, current: workflowNone}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		fmt.Println()
		log.Warn().Msg("Interrupt received, canceling workflow...")
		state.cancel(ctx)
		cancel()
	}()
	defer signal.Stop(sigChan)

	totalStart := time.Now()
	state.current = workflowInitialize
	log.Info().Str("server", client.endpoint).Msg("Triggering initialize workflow")
	started, err := client.Initialize(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("workflow canceled by user")
		}
		return fmt.Errorf("failed to trigger initialize: %w", err)
	}

	if !started {
		log.Warn().Msg("Workflow was not started (may already be running)")
	} else {
		log.Info().Msg("Workflow started successfully")
	}

	if err := monitorWorkflow(ctx, out, "Initializing NHL Franchises and NHL Seasons configurations...",
		client.GetInitializeStatus, config.DefaultWorkflowPollTimeout); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("workflow canceled by user")
		}
		return err
	}

	totalDuration := time.Since(totalStart)

	// Fetch and print result data
	resultData, err := client.GetInitializeResultData(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to fetch initialize result data")
	} else if resultData != nil {
		printInitializeResult(out, resultData, totalDuration)
	}

	return nil
}

func printInitializeResult(out io.Writer, result *InitializeResultData, duration time.Duration) {
	fmt.Fprintf(out, "= Initialization completed in %.1fs =\n", duration.Seconds())
	fmt.Fprintf(out, "  Franchises upserted:    %d\n", result.FranchisesUpserted)
	fmt.Fprintf(out, "  Seasons upserted:       %d\n", result.SeasonsUpserted)
	fmt.Fprintf(out, "  Season teams upserted:  %d\n", result.SeasonTeamsUpserted)
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

func (s *syncState) cancel(_ context.Context) {
	// Use a fresh context to cancel since the original may be canceled.
	cancelCtx, cancel := context.WithTimeout(context.Background(), config.DefaultCancelTimeout)
	defer cancel()

	switch s.current {
	case workflowInitialize:
		log.Info().Msg("Canceling initialize workflow...")
		if _, err := s.client.CancelInitialize(cancelCtx); err != nil {
			log.Error().Err(err).Msg("Failed to cancel initialize workflow")
		} else {
			log.Info().Msg("initialize workflow canceled")
		}
	case workflowImportPlayers:
		log.Info().Msg("Canceling importPlayers workflow...")
		if _, err := s.client.CancelImportPlayers(cancelCtx); err != nil {
			log.Error().Err(err).Msg("Failed to cancel importPlayers workflow")
		} else {
			log.Info().Msg("importPlayers workflow canceled")
		}
	case workflowImportSeasons:
		log.Info().Msg("Canceling importSeasons workflow...")
		if _, err := s.client.CancelImportSeasons(cancelCtx); err != nil {
			log.Error().Err(err).Msg("Failed to cancel importSeasons workflow")
		} else {
			log.Info().Msg("importSeasons workflow canceled")
		}
	default:
		// No workflow to cancel
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
		log.Warn().Msg("Interrupt received, canceling workflow...")
		state.cancel(ctx)
		cancel()
	}()
	defer signal.Stop(sigChan)

	if viper.GetBool(config.FlagMonitor) {
		log.Info().Str("server", apiAddr).Msg("Monitoring existing importPlayers workflow")
		state.current = workflowImportPlayers
		return monitorWorkflow_legacy(ctx, cmd, client.GetImportPlayersStatus, config.DefaultWorkflowPollTimeout)
	}

	totalStart := time.Now()

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
	} else {
		log.Info().Msg("Workflow started successfully")
	}

	if err := monitorWorkflow_legacy(ctx, cmd, client.GetImportPlayersStatus, config.DefaultWorkflowPollTimeout); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("workflow canceled by user")
		}
		return err
	}

	totalDuration := time.Since(totalStart)
	log.Info().Str("duration", totalDuration.String()).Msg("Import completed")

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
		log.Warn().Msg("Interrupt received, canceling workflow...")
		state.cancel(ctx)
		cancel()
	}()
	defer signal.Stop(sigChan)

	if viper.GetBool(config.FlagMonitor) {
		log.Info().Str("server", apiAddr).Msg("Monitoring existing importSeasons workflow")
		state.current = workflowImportSeasons
		return monitorWorkflow_legacy(ctx, cmd, client.GetImportSeasonsStatus, config.DefaultWorkflowPollTimeout)
	}

	totalStart := time.Now()

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
	} else {
		log.Info().Msg("Workflow started successfully")
	}

	if err := monitorWorkflow_legacy(ctx, cmd, client.GetImportSeasonsStatus, config.DefaultWorkflowPollTimeout); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("workflow canceled by user")
		}
		return err
	}

	totalDuration := time.Since(totalStart)
	log.Info().Str("duration", totalDuration.String()).Msg("Import completed")

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
