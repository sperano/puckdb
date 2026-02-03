package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/graph/model"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func cmdImport() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "import",
		Short: "Import data into the database",
		Long:  `Trigger import workflows via GraphQL API and monitor until completion.`,
	}
	config.InitAPIServerAddrFlag(cmd.PersistentFlags())
	cmd.AddCommand(cmdImportPlayers())
	return cmd
}

func cmdImportPlayers() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "players",
		Short: "Import players into the database",
		Long: `Trigger the importPlayers workflow via GraphQL API and monitor until completion.
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
		RunE: runImportPlayers,
	}
	flags := cmd.Flags()
	config.InitAPIServerAddrFlag(flags)
	config.InitSeasonRangeFlags(flags)
	config.InitSeasonConcurrencyFlag(flags)
	config.InitMonitorFlag(flags)
	return cmd
}

// importState tracks the current workflow for signal handling
type importState struct {
	client  *GraphQLClient
	current workflowType
}

func (s *importState) cancel(_ context.Context) {
	// Use a fresh context to cancel since the original may be canceled.
	cancelCtx, cancel := context.WithTimeout(context.Background(), config.DefaultCancelTimeout)
	defer cancel()

	switch s.current {
	case workflowImportPlayers:
		log.Info().Msg("Canceling importPlayers workflow...")
		if _, err := s.client.CancelImportPlayers(cancelCtx); err != nil {
			log.Error().Err(err).Msg("Failed to cancel importPlayers workflow")
		} else {
			log.Info().Msg("importPlayers workflow canceled")
		}
	default:
		// No workflow to cancel
	}
}

func runImportPlayers(cmd *cobra.Command, _ []string) error {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: cmd.OutOrStdout()})

	apiAddr := viper.GetString(config.FlagAPIServerAddr)
	if apiAddr == "" {
		return fmt.Errorf("api-server-addr is required")
	}

	client := NewGraphQLClient(apiAddr)
	state := &importState{client: client, current: workflowNone}

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
		return monitorWorkflow(ctx, cmd, client.GetImportPlayersStatus, config.DefaultWorkflowPollTimeout)
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

	if err := monitorWorkflow(ctx, cmd, client.GetImportPlayersStatus, config.DefaultWorkflowPollTimeout); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("workflow canceled by user")
		}
		return err
	}

	totalDuration := time.Since(totalStart)
	log.Info().Str("duration", totalDuration.String()).Msg("Import completed")

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
