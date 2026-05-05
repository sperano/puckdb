package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/graph/model"
	"github.com/spf13/viper"
	"golang.org/x/term"
)

func buildSeasonsInput() *model.SeasonsInput {
	input := &model.SeasonsInput{}

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

	if viper.GetBool(config.FlagRefreshCurrentPlayerLogs) {
		refreshPlayerLogs := true
		input.RefreshCurrentPlayerLogs = &refreshPlayerLogs
	}
	if viper.GetBool(config.FlagRefreshCurrentEdge) {
		refreshEdge := true
		input.RefreshCurrentEdge = &refreshEdge
	}

	return input
}

func monitorWorkflow(ctx context.Context, sp *spinner, getStatus statusFetcher) error {
	// Wait briefly for workflow to start and register query handlers
	time.Sleep(config.DefaultWorkflowStartupDelay)

	consecutiveFailures := 0

	for {
		status, err := getStatus(ctx)
		if err != nil {
			consecutiveFailures++
			if consecutiveFailures >= config.MaxConsecutiveQueryFailures {
				sp.Cancel()
				return fmt.Errorf("workflow query failed %d times consecutively: %w", consecutiveFailures, err)
			}
			sp.PrintAbove(func() {
				log.Warn().Err(err).Int("attempt", consecutiveFailures).Msg("Failed to get status, retrying...")
			})
		} else {
			consecutiveFailures = 0 // Reset on success
			msg := formatStatusMessage(status)
			if w := yahooWarning(status); w != "" {
				msg += "\n" + w
			}
			sp.mu.Lock()
			sp.message = msg
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
		case <-time.After(pollDelay(consecutiveFailures)):
		}
	}
}

// pollDelay returns the poll interval, backing off exponentially on consecutive failures.
func pollDelay(consecutiveFailures int) time.Duration {
	if consecutiveFailures == 0 {
		return config.DefaultWorkflowPollInterval
	}
	delay := config.DefaultWorkflowPollInterval << consecutiveFailures
	if delay > config.MaxWorkflowPollBackoff {
		return config.MaxWorkflowPollBackoff
	}
	return delay
}

func formatStatusMessage(status *WorkflowStatus) string {
	if status.Progress != nil {
		return formatProgressReport(status.Progress)
	}
	return fmt.Sprintf("Workflow status: %s", status.Result.Status)
}

// yahooWarning returns the Yahoo token warning line if the token is missing,
// or empty string if the token is present.
func yahooWarning(status *WorkflowStatus) string {
	if status.YahooTokenMissing {
		return formatYahooTokenWarning(status.YahooLoginURL)
	}
	return ""
}

// formatYahooTokenWarning returns a red ANSI-colored warning line for missing Yahoo token.
func formatYahooTokenWarning(loginURL string) string {
	return fmt.Sprintf("\033[38;5;196m⚠ No Yahoo token — visit %s\033[0m", loginURL)
}

// progressBarWidth returns the inner bar width sized to fill the terminal,
// given the label-area width on the bar's line. ProgressLineOverhead assumes
// the default label area; callers that widen it (multi-bar groups with long
// labels) must pass their actual width so the bar shrinks accordingly,
// otherwise the rendered line overflows and wraps — which breaks the
// spinner's in-place redraw.
// Falls back to DefaultProgressBarWidth if the terminal size is unavailable.
// The result is snapped to a multiple of 8 for clean gradient segments.
func progressBarWidth(labelAreaWidth int) int {
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 {
		return config.DefaultProgressBarWidth
	}
	overhead := config.ProgressLineOverhead + (labelAreaWidth - config.ProgressLabelAreaWidth)
	barW := w - overhead
	barW = (barW / colorThemePaletteSize) * colorThemePaletteSize // snap to multiple of 8
	if barW < config.MinProgressBarWidth {
		barW = config.MinProgressBarWidth
	}
	return barW
}

func renderProgressBar(pct float64, width int) string {
	filled := int(pct / 100.0 * float64(width))
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}

	// Split into 8 equal gradient segments (dark to light).
	// Each segment gets shade sentinels so the render loop can colorize the filled portion.
	segmentWidth := width / colorThemePaletteSize
	var bar strings.Builder
	bar.WriteByte('[')
	remaining := filled
	for i := range colorThemePaletteSize {
		segFilled := min(remaining, segmentWidth)
		remaining -= segFilled
		bar.WriteString(progressShades[i])
		bar.WriteString(strings.Repeat("█", segFilled))
		bar.WriteString(ProgressShadeEnd)
		bar.WriteString(strings.Repeat("░", segmentWidth-segFilled))
	}
	bar.WriteByte(']')
	return bar.String()
}

// formatLabelArea formats the label + x/y portion at the default
// config.ProgressLabelAreaWidth. Use formatLabelAreaW when the caller has
// pre-computed a wider area to keep multi-row groups aligned.
func formatLabelArea(label string, current, total int) string {
	return formatLabelAreaW(label, current, total, config.ProgressLabelAreaWidth)
}

// formatLabelAreaW renders label + x/y inside a fixed-width column. If the
// content is longer than width, padding clamps to one space so the bracket
// after this column never collides with the progress text — but the column
// will then exceed width, which misaligns multi-row groups. Callers that need
// alignment must compute width = max(label + 1 + progress) across all rows.
func formatLabelAreaW(label string, current, total, width int) string {
	progress := fmt.Sprintf("%d/%d", current, total)
	if label == "" {
		return fmt.Sprintf("%*s", width, progress)
	}
	padding := width - len(label) - len(progress)
	if padding < 1 {
		padding = 1
	}
	return label + strings.Repeat(" ", padding) + progress
}

// formatProgressReport renders the new ProgressReport format.
func formatProgressReport(report *model.ProgressReport) string {
	var lines []string
	for _, g := range report.Groups {
		lines = append(lines, renderProgressGroup(g))
	}
	return strings.Join(lines, "\n")
}

// renderProgressGroup renders a single group (completed or in-progress).
func renderProgressGroup(g *model.ProgressGroup) string {
	// CompletedAt is set by CompleteGroup — it's the authoritative signal.
	// Bar-based checks break when Total is 0 (e.g., no unmatched players).
	if g.CompletedAt > 0 {
		return "✓ " + g.CompletedMsg
	}

	// Single bar: show label if present
	if len(g.Bars) == 1 {
		b := g.Bars[0]
		// Skip progress bar when total is 1 - just show header with spinner
		if b.Total <= 1 {
			return fmt.Sprintf("%s %s", SpinnerPlaceholder, g.Header)
		}
		label := ""
		if b.Label != nil {
			label = *b.Label
		}
		pct := float64(b.Current) / float64(b.Total) * 100
		bar := renderProgressBar(pct, progressBarWidth(config.ProgressLabelAreaWidth))
		return fmt.Sprintf("▶ %s\n%s %s %s %d%%",
			g.Header, SpinnerPlaceholder, formatLabelArea(label, b.Current, b.Total), bar, int(pct))
	}

	// Multi-bar: with labels and total line
	return renderMultiBarGroup(g)
}

func renderMultiBarGroup(g *model.ProgressGroup) string {
	var activeBars []*model.ProgressBar
	var totalCurrent, totalTotal int

	for _, b := range g.Bars {
		totalCurrent += b.Current
		totalTotal += b.Total
		if b.Started && b.Current < b.Total {
			activeBars = append(activeBars, b)
		}
	}

	lines := []string{"▶ " + g.Header}

	// Compute label-area width: max(label + 1 + x/y) across all active bars
	// and the Total line, clamped at the default minimum so short groups still
	// look like the rest of the UI.
	width := config.ProgressLabelAreaWidth
	rowWidth := func(label string, current, total int) int {
		return len(label) + 1 + len(fmt.Sprintf("%d/%d", current, total))
	}
	for _, b := range activeBars {
		label := ""
		if b.Label != nil {
			label = *b.Label
		}
		if w := rowWidth(label, b.Current, b.Total); w > width {
			width = w
		}
	}
	if totalTotal > 0 {
		if w := rowWidth("Total", totalCurrent, totalTotal); w > width {
			width = w
		}
	}

	for _, b := range activeBars {
		label := ""
		if b.Label != nil {
			label = *b.Label
		}
		pct := 0.0
		if b.Total > 0 {
			pct = float64(b.Current) / float64(b.Total) * 100
		}
		bar := renderProgressBar(pct, progressBarWidth(width))
		lines = append(lines, fmt.Sprintf("%s %s %s %d%%",
			SpinnerPlaceholder, formatLabelAreaW(label, b.Current, b.Total, width), bar, int(pct)))
	}

	if totalTotal > 0 {
		pct := float64(totalCurrent) / float64(totalTotal) * 100
		bar := renderProgressBar(pct, progressBarWidth(width))
		lines = append(lines, fmt.Sprintf("  %s %s %d%%",
			formatLabelAreaW("Total", totalCurrent, totalTotal, width), bar, int(pct)))
	}

	return strings.Join(lines, "\n")
}
