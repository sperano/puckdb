package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

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
	//log.Logger = log.Output(zerolog.ConsoleWriter{Out: cmd.OutOrStdout()})
	//
	//apiAddr := viper.GetString(config.FlagAPIServerAddr)
	//if apiAddr == "" {
	//	return fmt.Errorf("api-server-addr is required")
	//}
	//
	//client := NewGraphQLClient(apiAddr)
	//state := &downloadState{client: client, current: workflowNone}
	//
	//// Set up signal handling for Ctrl+C
	//ctx, cancel := context.WithCancel(cmd.Context())
	//defer cancel()
	//
	//sigChan := make(chan os.Signal, 1)
	//signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	//
	//go func() {
	//	<-sigChan
	//	fmt.Println() // newline after ^C
	//	state.cancel(ctx)
	//	cancel()
	//}()
	//defer signal.Stop(sigChan)
	//
	//if viper.GetBool(config.FlagMonitor) {
	//	log.Info().Str("server", apiAddr).Msg("Monitoring existing downloadSeasons workflow")
	//	state.current = workflowDownloadSeasons
	//	return monitorWorkflow_legacy(ctx, cmd, "Downloading seasons...", "Downloaded seasons.", client.GetDownloadSeasonsStatus, config.DefaultWorkflowPollTimeout)
	//}
	//
	//totalStart := time.Now()
	//var yahooPlayersDuration, seasonsDuration, playersDuration time.Duration
	//
	//// Step 1: Download Yahoo players (unless skipped)
	//if !viper.GetBool(config.FlagSkipYahooPlayers) {
	//	stepStart := time.Now()
	//	if err := runDownloadYahooPlayers(ctx, cmd, client, state); err != nil {
	//		if ctx.Err() != nil {
	//			return fmt.Errorf("workflow canceled by user")
	//		}
	//		return err
	//	}
	//	yahooPlayersDuration = time.Since(stepStart)
	//	log.Info().Str("duration", yahooPlayersDuration.String()).Msg("Yahoo players download completed")
	//} else {
	//	log.Info().Msg("Skipping Yahoo players download")
	//}
	//
	//// Check if context was canceled during Yahoo players download
	//if ctx.Err() != nil {
	//	return fmt.Errorf("workflow canceled by user")
	//}
	//
	//input := buildDownloadSeasonsInput()
	//
	//// Step 2: Download all season data (unless skipped)
	//if !viper.GetBool(config.FlagSkipSeasons) {
	//	stepStart := time.Now()
	//	state.current = workflowDownloadSeasons
	//
	//	logEvent := log.Info().Str("server", apiAddr)
	//	if input.StartSeason != nil {
	//		logEvent = logEvent.Int("startSeason", *input.StartSeason)
	//	}
	//	if input.EndSeason != nil {
	//		logEvent = logEvent.Int("endSeason", *input.EndSeason)
	//	}
	//	if input.SeasonConcurrency != nil {
	//		logEvent = logEvent.Int("seasonConcurrency", *input.SeasonConcurrency)
	//	}
	//	logEvent.Msg("Triggering downloadSeasons workflow")
	//
	//	started, err := client.DownloadSeasons(ctx, input)
	//	if err != nil {
	//		if ctx.Err() != nil {
	//			return fmt.Errorf("workflow canceled by user")
	//		}
	//		return fmt.Errorf("failed to trigger download: %w", err)
	//	}
	//
	//	if !started {
	//		log.Warn().Msg("Workflow was not started (may already be running)")
	//	} else {
	//		log.Info().Msg("Workflow started successfully")
	//	}
	//
	//	if err := monitorWorkflow_legacy(ctx, cmd, "Downloading seasons...", "Downloaded seasons.", client.GetDownloadSeasonsStatus, config.DefaultWorkflowPollTimeout); err != nil {
	//		if ctx.Err() != nil {
	//			return fmt.Errorf("workflow canceled by user")
	//		}
	//		return err
	//	}
	//	seasonsDuration = time.Since(stepStart)
	//	log.Info().Str("duration", seasonsDuration.String()).Msg("Seasons download completed")
	//} else {
	//	log.Info().Msg("Skipping seasons download")
	//}
	//
	//// Check if context was canceled
	//if ctx.Err() != nil {
	//	return fmt.Errorf("workflow canceled by user")
	//}
	//
	//// Step 3: Download players (extract player IDs from boxscores)
	//stepStart := time.Now()
	//if err := runDownloadPlayers(ctx, cmd, client, state, input); err != nil {
	//	if ctx.Err() != nil {
	//		return fmt.Errorf("workflow canceled by user")
	//	}
	//	return err
	//}
	//playersDuration = time.Since(stepStart)
	//log.Info().Str("duration", playersDuration.String()).Msg("Players download completed")
	//
	//// Log final summary
	//totalDuration := time.Since(totalStart)
	//log.Info().
	//	Str("yahooPlayers", yahooPlayersDuration.String()).
	//	Str("seasons", seasonsDuration.String()).
	//	Str("players", playersDuration.String()).
	//	Str("total", totalDuration.String()).
	//	Msg("Download completed")
	//
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

	if err := monitorWorkflow_legacy(ctx, cmd, "Downloading Yahoo! players...", client.GetDownloadYahooPlayersStatus, config.DefaultYahooPlayersTimeout); err != nil {
		return fmt.Errorf("downloadYahooPlayers failed: %w", err)
	}

	return nil
}

func runDownloadPlayers(ctx context.Context, out io.Writer, client *GraphQLClient, state *syncState, input *model.DownloadSeasonsInput) error {
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

	if err := monitorWorkflow(ctx, out, "Downloading players...", client.GetDownloadPlayersStatus, config.DefaultDownloadPlayersTimeout); err != nil {
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

func monitorWorkflow_legacy(ctx context.Context, cmd *cobra.Command, header string, getStatus statusFetcher, pollTimeout time.Duration) error {
	sp := newSpinner(cmd.OutOrStdout(), header)
	sp.Start()

	// Wait briefly for workflow to start and register query handlers
	time.Sleep(config.DefaultWorkflowStartupDelay)

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
			sp.message = formatStatusMessage(status, header)
			sp.mu.Unlock()

			switch status.Result.Status {
			case model.TemporalWorkflowStatusCompleted:
				sp.Stop()
				return nil
			case model.TemporalWorkflowStatusFailed:
				sp.Cancel()
				if status.Result.FailureReason != nil {
					return fmt.Errorf("workflow failed: %s", *status.Result.FailureReason)
				}
				return fmt.Errorf("workflow failed")
			case model.TemporalWorkflowStatusCanceled:
				sp.Cancel()
				return fmt.Errorf("workflow was canceled")
			case model.TemporalWorkflowStatusTerminated:
				sp.Cancel()
				return fmt.Errorf("workflow was terminated")
			case model.TemporalWorkflowStatusTimedOut:
				sp.Cancel()
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
			sp.Cancel()
			return ctx.Err()
		case <-timeout:
			sp.Cancel()
			return fmt.Errorf("workflow monitoring timed out after %v", pollTimeout)
		case <-ticker.C:
			// continue to next iteration
		}
	}
}

func monitorWorkflow(ctx context.Context, out io.Writer, header string, getStatus statusFetcher, pollTimeout time.Duration) error {
	sp := newSpinner(out, header)
	sp.Start()

	// Wait briefly for workflow to start and register query handlers
	time.Sleep(config.DefaultWorkflowStartupDelay)               // TODO: make this smarter by detecting when workflow is actually ready instead of fixed sleep
	ticker := time.NewTicker(config.DefaultWorkflowPollInterval) // TODO isnt this a flag instead of just a default?
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
			sp.message = formatStatusMessage(status, header)
			sp.mu.Unlock()

			switch status.Result.Status {
			case model.TemporalWorkflowStatusCompleted:
				sp.Stop()
				return nil
			case model.TemporalWorkflowStatusFailed:
				sp.Cancel()
				if status.Result.FailureReason != nil {
					return fmt.Errorf("workflow failed: %s", *status.Result.FailureReason)
				}
				return fmt.Errorf("workflow failed")
			case model.TemporalWorkflowStatusCanceled:
				sp.Cancel()
				return fmt.Errorf("workflow was canceled")
			case model.TemporalWorkflowStatusTerminated:
				sp.Cancel()
				return fmt.Errorf("workflow was terminated")
			case model.TemporalWorkflowStatusTimedOut:
				sp.Cancel()
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
			sp.Cancel()
			return ctx.Err()
		case <-timeout:
			sp.Cancel()
			return fmt.Errorf("workflow monitoring timed out after %v", pollTimeout)
		case <-ticker.C:
			// continue to next iteration
		}
	}
}

func formatStatusMessage(status *WorkflowStatus, header string) string {
	if status.Progress == nil {
		return fmt.Sprintf("Workflow status: %s", status.Result.Status)
	}

	var lines []string

	// Add workflow message if present
	if status.Progress.Message != nil && *status.Progress.Message != "" {
		lines = append(lines, *status.Progress.Message)
	}

	// No progress yet
	if status.Progress.Total == 0 {
		if len(lines) > 0 {
			return strings.Join(lines, "\n")
		}
		return fmt.Sprintf("Workflow status: %s", status.Result.Status)
	}

	// If no items, show simple header + progress bar
	if len(status.Progress.Items) == 0 {
		lines = append(lines, fmt.Sprintf("▶ %s", header))
		pct := float64(status.Progress.Completed) / float64(status.Progress.Total) * 100
		bar := renderProgressBar(pct, config.DefaultProgressBarWidth)
		lines = append(lines, fmt.Sprintf("%s %d/%d %s %d%%",
			SpinnerPlaceholder, status.Progress.Completed, status.Progress.Total, bar, int(pct)))
		return strings.Join(lines, "\n")
	}

	// Special handling for seasons: single header, only show in-progress items
	// TODO: wow this is ugly - ideally the workflow would return a more display-friendly status that we wouldn't have to do all this formatting logic for. Maybe something like:
	// {
	//   header: "Downloading seasons...",
	//   items: [
	//     {id: 2022, description: "2022-23 season", completed: 5, total: 82, started: true},
	//     {id: 2021, description: "2021-22 season", completed: 82, total: 82, started: true},
	//     ...
	//   ],
	//   progress: {completed: 87, total: 164}
	// }

	if header == "Downloading seasons..." {
		lines = append(lines, fmt.Sprintf("▶ %s", header))

		// First pass: find max widths for alignment (including total line)
		var maxCompleted, maxTotal, maxDescLen int
		const totalLabel = "Total"
		maxDescLen = len(totalLabel)

		for _, item := range status.Progress.Items {
			if !item.Started || (item.Completed == item.Total && item.Total > 0) {
				continue
			}
			if item.Completed > maxCompleted {
				maxCompleted = item.Completed
			}
			if item.Total > maxTotal {
				maxTotal = item.Total
			}
			desc := fmt.Sprintf("Item %d", item.ID)
			if item.Description != nil && *item.Description != "" {
				desc = *item.Description
			}
			if len(desc) > maxDescLen {
				maxDescLen = len(desc)
			}
		}

		// Include overall totals in width calculation
		if status.Progress.Completed > maxCompleted {
			maxCompleted = status.Progress.Completed
		}
		if status.Progress.Total > maxTotal {
			maxTotal = status.Progress.Total
		}

		completedWidth := len(fmt.Sprintf("%d", maxCompleted))
		totalWidth := len(fmt.Sprintf("%d", maxTotal))

		// Second pass: format with aligned columns
		for _, item := range status.Progress.Items {
			if !item.Started || (item.Completed == item.Total && item.Total > 0) {
				continue
			}

			description := fmt.Sprintf("Item %d", item.ID)
			if item.Description != nil && *item.Description != "" {
				description = *item.Description
			}

			pct := float64(item.Completed) / float64(item.Total) * 100
			bar := renderProgressBar(pct, config.DefaultProgressBarWidth)
			lines = append(lines, fmt.Sprintf("%s %-*s %*d/%*d %s %d%%",
				SpinnerPlaceholder, maxDescLen, description, completedWidth, item.Completed, totalWidth, item.Total, bar, int(pct)))
		}

		// Total progress line
		if status.Progress.Total > 0 {
			totalPct := float64(status.Progress.Completed) / float64(status.Progress.Total) * 100
			totalBar := renderProgressBar(totalPct, config.DefaultProgressBarWidth)
			lines = append(lines, fmt.Sprintf("  %-*s %*d/%*d %s %d%%",
				maxDescLen, totalLabel, completedWidth, status.Progress.Completed, totalWidth, status.Progress.Total, totalBar, int(totalPct)))
		}

		return strings.Join(lines, "\n")
	}

	// Default: display each item with ✓/▶/indent (for phases, etc.)
	for _, item := range status.Progress.Items {
		description := fmt.Sprintf("Item %d", item.ID)
		if item.Description != nil && *item.Description != "" {
			description = *item.Description
		}
		completedDescription := description
		if item.CompletedDescription != nil && *item.CompletedDescription != "" {
			completedDescription = *item.CompletedDescription
		}

		if item.Completed == item.Total && item.Total > 0 {
			// Completed: checkmark with completed description
			lines = append(lines, fmt.Sprintf("✓ %s", completedDescription))
		} else if item.Started {
			// In progress: arrow + name, then progress bar with spinner
			lines = append(lines, fmt.Sprintf("▶ %s", description))
			pct := float64(item.Completed) / float64(item.Total) * 100
			bar := renderProgressBar(pct, config.DefaultProgressBarWidth)
			lines = append(lines, fmt.Sprintf("%s %d/%d %s %d%%",
				SpinnerPlaceholder, item.Completed, item.Total, bar, int(pct)))
		} else {
			// Pending: indented
			lines = append(lines, fmt.Sprintf("  %s", description))
		}
	}

	return strings.Join(lines, "\n")
}

// formatElapsedTime calculates and formats the elapsed time between two RFC3339 timestamps.
func formatElapsedTime(startedAt, completedAt *string) string {
	if startedAt == nil || completedAt == nil {
		return ""
	}

	start, err := time.Parse(time.RFC3339, *startedAt)
	if err != nil {
		return ""
	}

	end, err := time.Parse(time.RFC3339, *completedAt)
	if err != nil {
		return ""
	}

	elapsed := end.Sub(start)

	// Format as human-readable duration
	if elapsed < time.Second {
		return fmt.Sprintf("%dms", elapsed.Milliseconds())
	}
	if elapsed < time.Minute {
		secs := elapsed.Seconds()
		if secs == float64(int(secs)) {
			return fmt.Sprintf("%ds", int(secs))
		}
		return fmt.Sprintf("%.1fs", secs)
	}
	if elapsed < time.Hour {
		mins := int(elapsed.Minutes())
		secs := int(elapsed.Seconds()) % 60
		if secs == 0 {
			return fmt.Sprintf("%dm", mins)
		}
		return fmt.Sprintf("%dm%ds", mins, secs)
	}

	hours := int(elapsed.Hours())
	mins := int(elapsed.Minutes()) % 60
	return fmt.Sprintf("%dh%dm", hours, mins)
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
