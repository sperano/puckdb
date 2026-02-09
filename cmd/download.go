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

	if err := monitorWorkflow(ctx, out, client.GetDownloadPlayersStatus, config.DefaultDownloadPlayersTimeout); err != nil {
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
			sp.message = formatStatusMessage(status)
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

func monitorWorkflow(ctx context.Context, out io.Writer, getStatus statusFetcher, pollTimeout time.Duration) error {
	sp := newSpinner(out, "Starting...")
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
			sp.message = formatStatusMessage(status)
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

func formatStatusMessage(status *WorkflowStatus) string {
	if status.Progress == nil {
		return fmt.Sprintf("Workflow status: %s", status.Result.Status)
	}

	header := ""
	if status.Progress.Header != nil {
		header = *status.Progress.Header
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
		if header != "" {
			lines = append(lines, fmt.Sprintf("▶ %s", header))
		}
		pct := float64(status.Progress.Completed) / float64(status.Progress.Total) * 100
		bar := renderProgressBar(pct, config.DefaultProgressBarWidth)
		lines = append(lines, fmt.Sprintf("%s %d/%d %s %d%%",
			SpinnerPlaceholder, status.Progress.Completed, status.Progress.Total, bar, int(pct)))
		return strings.Join(lines, "\n")
	}

	// Grouped items display: single header with multiple concurrent progress bars
	if status.Progress.DisplayStyle != nil && *status.Progress.DisplayStyle == model.ProgressDisplayStyleGroupedItems {
		if header != "" {
			lines = append(lines, fmt.Sprintf("▶ %s", header))
		}

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
