package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/graph/model"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func cmdDownload() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "download",
		Short: "Download NHL and Yahoo data",
		Long: `Trigger download workflow via GraphQL API and monitor until completion.
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
			if err := config.BindMonitorFlag(flags); err != nil {
				return err
			}
			if err := config.BindSkipYahooPlayersFlag(flags); err != nil {
				return err
			}
			if err := config.BindSkipSeasonsFlag(flags); err != nil {
				return err
			}
			return config.BindSeasonConcurrencyFlag(flags)
		},
		RunE: runDownload,
	}
	flags := cmd.Flags()
	config.InitAPIServerAddrFlag(flags)
	config.InitSeasonRangeFlags(flags)
	config.InitMonitorFlag(flags)
	config.InitSkipYahooPlayersFlag(flags)
	config.InitSkipSeasonsFlag(flags)
	config.InitSeasonConcurrencyFlag(flags)
	return cmd
}

// downloadState tracks the current workflow for signal handling
type downloadState struct {
	client  *GraphQLClient
	current workflowType
}

func (s *downloadState) cancel(_ context.Context) {
	// Use a fresh context to cancel since the original may be canceled.
	cancelCtx, cancel := context.WithTimeout(context.Background(), config.DefaultCancelTimeout)
	defer cancel()

	switch s.current {
	case workflowYahooPlayers:
		log.Info().Msg("Canceling downloadYahooPlayers workflow...")
		if _, err := s.client.CancelDownloadYahooPlayers(cancelCtx); err != nil {
			log.Error().Err(err).Msg("Failed to cancel downloadYahooPlayers workflow")
		} else {
			log.Info().Msg("downloadYahooPlayers workflow canceled")
		}
	case workflowDownloadSeasons:
		log.Info().Msg("Canceling downloadSeasons workflow...")
		if _, err := s.client.CancelDownloadSeasons(cancelCtx); err != nil {
			log.Error().Err(err).Msg("Failed to cancel downloadSeasons workflow")
		} else {
			log.Info().Msg("downloadSeasons workflow canceled")
		}
	case workflowDownloadPlayers:
		log.Info().Msg("Canceling downloadPlayers workflow...")
		if _, err := s.client.CancelDownloadPlayers(cancelCtx); err != nil {
			log.Error().Err(err).Msg("Failed to cancel downloadPlayers workflow")
		} else {
			log.Info().Msg("downloadPlayers workflow canceled")
		}
	case workflowNone:
	}
}

func runDownload(cmd *cobra.Command, _ []string) error {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: cmd.OutOrStdout()})

	apiAddr := viper.GetString(config.FlagAPIServerAddr)
	if apiAddr == "" {
		return fmt.Errorf("api-server-addr is required")
	}

	client := NewGraphQLClient(apiAddr)
	state := &downloadState{client: client, current: workflowNone}

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
		log.Info().Str("server", apiAddr).Msg("Monitoring existing downloadSeasons workflow")
		state.current = workflowDownloadSeasons
		return monitorWorkflow(ctx, cmd, client.GetDownloadSeasonsStatus, config.DefaultWorkflowPollTimeout)
	}

	totalStart := time.Now()
	var yahooPlayersDuration, seasonsDuration, playersDuration time.Duration

	// Step 1: Download Yahoo players (unless skipped)
	if !viper.GetBool(config.FlagSkipYahooPlayers) {
		stepStart := time.Now()
		if err := runDownloadYahooPlayers(ctx, cmd, client, state); err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("workflow canceled by user")
			}
			return err
		}
		yahooPlayersDuration = time.Since(stepStart)
		log.Info().Str("duration", yahooPlayersDuration.String()).Msg("Yahoo players download completed")
	} else {
		log.Info().Msg("Skipping Yahoo players download")
	}

	// Check if context was canceled during Yahoo players download
	if ctx.Err() != nil {
		return fmt.Errorf("workflow canceled by user")
	}

	input := buildDownloadSeasonsInput()

	// Step 2: Download all season data (unless skipped)
	if !viper.GetBool(config.FlagSkipSeasons) {
		stepStart := time.Now()
		state.current = workflowDownloadSeasons

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
		logEvent.Msg("Triggering downloadSeasons workflow")

		started, err := client.DownloadSeasons(ctx, input)
		if err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("workflow canceled by user")
			}
			return fmt.Errorf("failed to trigger download: %w", err)
		}

		if !started {
			log.Warn().Msg("Workflow was not started (may already be running)")
		} else {
			log.Info().Msg("Workflow started successfully")
		}

		if err := monitorWorkflow(ctx, cmd, client.GetDownloadSeasonsStatus, config.DefaultWorkflowPollTimeout); err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("workflow canceled by user")
			}
			return err
		}
		seasonsDuration = time.Since(stepStart)
		log.Info().Str("duration", seasonsDuration.String()).Msg("Seasons download completed")
	} else {
		log.Info().Msg("Skipping seasons download")
	}

	// Check if context was canceled
	if ctx.Err() != nil {
		return fmt.Errorf("workflow canceled by user")
	}

	// Step 3: Download players (extract player IDs from boxscores)
	stepStart := time.Now()
	if err := runDownloadPlayers(ctx, cmd, client, state, input); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("workflow canceled by user")
		}
		return err
	}
	playersDuration = time.Since(stepStart)
	log.Info().Str("duration", playersDuration.String()).Msg("Players download completed")

	// Log final summary
	totalDuration := time.Since(totalStart)
	log.Info().
		Str("yahooPlayers", yahooPlayersDuration.String()).
		Str("seasons", seasonsDuration.String()).
		Str("players", playersDuration.String()).
		Str("total", totalDuration.String()).
		Msg("Download completed")

	return nil
}

func runDownloadYahooPlayers(ctx context.Context, cmd *cobra.Command, client *GraphQLClient, state *downloadState) error {
	state.current = workflowYahooPlayers
	log.Info().Msg("Triggering downloadYahooPlayers workflow")

	started, err := client.DownloadYahooPlayers(ctx)
	if err != nil {
		return fmt.Errorf("failed to trigger downloadYahooPlayers: %w", err)
	}

	if !started {
		log.Warn().Msg("Yahoo players workflow was not started (may already be running)")
	} else {
		log.Info().Msg("Yahoo players workflow started successfully")
	}

	if err := monitorWorkflow(ctx, cmd, client.GetDownloadYahooPlayersStatus, config.DefaultYahooPlayersTimeout); err != nil {
		return fmt.Errorf("downloadYahooPlayers failed: %w", err)
	}

	return nil
}

func runDownloadPlayers(ctx context.Context, cmd *cobra.Command, client *GraphQLClient, state *downloadState, input *model.DownloadSeasonsInput) error {
	state.current = workflowDownloadPlayers
	log.Info().Msg("Triggering downloadPlayers workflow")

	started, err := client.DownloadPlayers(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to trigger downloadPlayers: %w", err)
	}

	if !started {
		log.Warn().Msg("Download players workflow was not started (may already be running)")
	} else {
		log.Info().Msg("Download players workflow started successfully")
	}

	if err := monitorWorkflow(ctx, cmd, client.GetDownloadPlayersStatus, config.DefaultDownloadPlayersTimeout); err != nil {
		return fmt.Errorf("downloadPlayers failed: %w", err)
	}

	return nil
}

func buildDownloadSeasonsInput() *model.DownloadSeasonsInput {
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

func monitorWorkflow(ctx context.Context, cmd *cobra.Command, getStatus statusFetcher, pollTimeout time.Duration) error {
	sp := newSpinner(cmd.OutOrStdout(), "Monitoring workflow...")
	sp.Start()
	defer sp.Stop()

	ticker := time.NewTicker(config.DefaultWorkflowPollInterval)
	defer ticker.Stop()

	timeout := time.After(pollTimeout)

	for {
		status, err := getStatus(ctx)
		if err != nil {
			sp.PrintAbove(func() {
				log.Warn().Err(err).Msg("Failed to get status, retrying...")
			})
		} else {
			sp.mu.Lock()
			sp.message = formatStatusMessage(status)
			sp.mu.Unlock()

			switch status.Result.Status {
			case model.TemporalWorkflowStatusCompleted:
				sp.Stop()
				log.Info().Msg("Workflow completed successfully")
				return nil
			case model.TemporalWorkflowStatusFailed:
				if status.Result.FailureReason != nil {
					return fmt.Errorf("workflow failed: %s", *status.Result.FailureReason)
				}
				return fmt.Errorf("workflow failed")
			case model.TemporalWorkflowStatusCanceled:
				return fmt.Errorf("workflow was canceled")
			case model.TemporalWorkflowStatusTerminated:
				return fmt.Errorf("workflow was terminated")
			case model.TemporalWorkflowStatusTimedOut:
				return fmt.Errorf("workflow timed out")
			case model.TemporalWorkflowStatusRunning:
				// Continue polling
			case model.TemporalWorkflowStatusUnspecified:
				// Workflow might not have started yet or doesn't exist
				log.Debug().Msg("Workflow status unspecified")
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout:
			return fmt.Errorf("workflow monitoring timed out after %v", pollTimeout)
		case <-ticker.C:
			// continue to next iteration
		}
	}
}

func formatStatusMessage(status *WorkflowStatus) string {
	if status.Progress == nil || status.Progress.Total == 0 {
		return fmt.Sprintf("Workflow status: %s", status.Result.Status)
	}

	// Sort seasons by startYear
	seasons := make([]*model.SeasonProgress, len(status.Progress.Seasons))
	copy(seasons, status.Progress.Seasons)
	sort.Slice(seasons, func(i, j int) bool {
		return seasons[i].StartYear < seasons[j].StartYear
	})

	// Collect active seasons (> 0% and < 100%)
	type progressLine struct {
		label     string
		completed int
		total     int
	}
	var activeSeasons []progressLine

	for _, season := range seasons {
		if season.Total > 0 {
			pct := float64(season.Completed) / float64(season.Total) * 100
			if pct > 0 && pct < 100 {
				activeSeasons = append(activeSeasons, progressLine{
					label:     fmt.Sprintf("%d", season.StartYear),
					completed: season.Completed,
					total:     season.Total,
				})
			}
		}
	}

	// Add total line
	allLines := append(activeSeasons, progressLine{
		label:     "Total",
		completed: status.Progress.Completed,
		total:     status.Progress.Total,
	})

	// Calculate max widths for alignment
	maxLabelWidth := 0
	maxCountWidth := 0
	for _, line := range allLines {
		if len(line.label) > maxLabelWidth {
			maxLabelWidth = len(line.label)
		}
		countStr := fmt.Sprintf("%d/%d", line.completed, line.total)
		if len(countStr) > maxCountWidth {
			maxCountWidth = len(countStr)
		}
	}

	// Format lines with aligned columns
	var lines []string
	for _, line := range allLines {
		pct := float64(line.completed) / float64(line.total) * 100
		pctTrunc := int(pct) // truncate, never round up to 100%
		countStr := fmt.Sprintf("%d/%d", line.completed, line.total)
		bar := renderProgressBar(pct, config.DefaultProgressBarWidth)
		lines = append(lines, fmt.Sprintf("%*s: %*s %s %3d%% ",
			maxLabelWidth, line.label,
			maxCountWidth, countStr,
			bar,
			pctTrunc))
	}

	return strings.Join(lines, "\n")
}

func renderProgressBar(pct float64, width int) string {
	filled := int(pct / 100.0 * float64(width))
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	empty := width - filled
	return "[" + strings.Repeat("█", filled) + strings.Repeat("░", empty) + "]"
}
