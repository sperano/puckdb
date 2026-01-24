package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/graph/model"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	FlagRefreshInterval    = "refresh"
	DefaultRefreshInterval = 2
)

// DashboardData holds all workflow statuses for display
type DashboardData struct {
	BuildNumber     int
	DownloadSeasons *WorkflowStatus
	YahooPlayers    *WorkflowStatus
	ExtractPlayers  *WorkflowStatus
	FetchedAt       time.Time
	Error           error
}

func cmdDashboard() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "dashboard",
		Short: "Display workflow status dashboard",
		Long: `Connect to the API server and display a live dashboard of workflow statuses.
The dashboard refreshes at a configurable interval and shows progress for all running workflows.`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			flags := cmd.Flags()
			if err := viper.BindPFlag(FlagAPIServerAddr, flags.Lookup(FlagAPIServerAddr)); err != nil {
				return err
			}
			return viper.BindPFlag(FlagRefreshInterval, flags.Lookup(FlagRefreshInterval))
		},
		RunE: runDashboard,
	}
	flags := cmd.Flags()
	flags.String(FlagAPIServerAddr, DefaultAPIServerAddr, "API server address (e.g., http://localhost:8080)")
	flags.Int(FlagRefreshInterval, DefaultRefreshInterval, "Refresh interval in seconds")
	return cmd
}

func runDashboard(cmd *cobra.Command, _ []string) error {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: cmd.OutOrStdout()})

	apiAddr := viper.GetString(FlagAPIServerAddr)
	if apiAddr == "" {
		return fmt.Errorf("api-server-addr is required")
	}

	refreshInterval := viper.GetInt(FlagRefreshInterval)
	if refreshInterval < 1 {
		refreshInterval = DefaultRefreshInterval
	}

	client := NewGraphQLClient(apiAddr)

	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		cancel()
	}()
	defer signal.Stop(sigChan)

	ticker := time.NewTicker(time.Duration(refreshInterval) * time.Second)
	defer ticker.Stop()

	// Initial fetch and display
	data := fetchDashboardData(ctx, client)
	clearScreen()
	printDashboard(apiAddr, refreshInterval, data)

	for {
		select {
		case <-ctx.Done():
			fmt.Println("\nDashboard stopped.")
			return nil
		case <-ticker.C:
			data = fetchDashboardData(ctx, client)
			clearScreen()
			printDashboard(apiAddr, refreshInterval, data)
		}
	}
}

func fetchDashboardData(ctx context.Context, client *GraphQLClient) *DashboardData {
	data := &DashboardData{
		FetchedAt: time.Now(),
	}

	// Fetch build number
	buildNumber, err := client.GetBuildNumber(ctx)
	if err != nil {
		data.Error = err
	} else {
		data.BuildNumber = buildNumber
	}

	// Fetch workflow statuses (continue even if some fail)
	data.DownloadSeasons, _ = client.GetDownloadSeasonsStatus(ctx)
	data.YahooPlayers, _ = client.GetDownloadYahooPlayersStatus(ctx)
	data.ExtractPlayers, _ = client.GetExtractUniquePlayersStatus(ctx)

	return data
}

// GetBuildNumber fetches the build number from the API
func (c *GraphQLClient) GetBuildNumber(ctx context.Context) (int, error) {
	const query = `query { buildNumber }`

	resp, err := c.execute(ctx, query, nil)
	if err != nil {
		return 0, err
	}

	var result struct {
		BuildNumber int `json:"buildNumber"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return 0, fmt.Errorf("failed to parse response: %w", err)
	}

	return result.BuildNumber, nil
}

func clearScreen() {
	fmt.Print("\033[H\033[2J")
}

func printDashboard(apiAddr string, refreshInterval int, data *DashboardData) {
	width := 70

	// Header
	fmt.Println(strings.Repeat("═", width))
	fmt.Printf("  PuckDB Dashboard\n")
	fmt.Println(strings.Repeat("─", width))
	fmt.Printf("  Server: %s\n", apiAddr)
	fmt.Printf("  Build:  %d\n", data.BuildNumber)
	fmt.Printf("  Updated: %s (refresh: %ds)\n", data.FetchedAt.Format("15:04:05"), refreshInterval)
	fmt.Println(strings.Repeat("═", width))

	if data.Error != nil {
		fmt.Printf("\n  Error: %v\n", data.Error)
	}

	// Download Seasons workflow
	fmt.Println()
	printWorkflowSection("Download Seasons", data.DownloadSeasons, width)

	// Yahoo Players workflow
	fmt.Println()
	printWorkflowSection("Yahoo Players", data.YahooPlayers, width)

	// Extract Players workflow
	fmt.Println()
	printWorkflowSection("Extract Players", data.ExtractPlayers, width)

	fmt.Println()
	fmt.Println(strings.Repeat("─", width))
	fmt.Println("  Press Ctrl+C to exit")
}

func printWorkflowSection(name string, status *WorkflowStatus, width int) {
	fmt.Printf("  ┌─ %s ", name)
	fmt.Print(strings.Repeat("─", width-len(name)-6))
	fmt.Println("┐")

	if status == nil || status.Result == nil {
		fmt.Printf("  │  Status: %-*s│\n", width-13, "Not available")
		fmt.Printf("  └%s┘\n", strings.Repeat("─", width-4))
		return
	}

	// Status line with color
	statusStr := formatWorkflowStatus(status.Result.Status)
	fmt.Printf("  │  Status: %-*s│\n", width-13, statusStr)

	// Failure reason if present
	if status.Result.FailureReason != nil && *status.Result.FailureReason != "" {
		reason := *status.Result.FailureReason
		if len(reason) > width-15 {
			reason = reason[:width-18] + "..."
		}
		fmt.Printf("  │  Error: %-*s│\n", width-12, reason)
	}

	// Progress if available
	if status.Progress != nil && status.Progress.Total > 0 {
		pct := float64(status.Progress.Completed) / float64(status.Progress.Total) * 100
		progressStr := fmt.Sprintf("%d/%d (%.1f%%)", status.Progress.Completed, status.Progress.Total, pct)
		fmt.Printf("  │  Progress: %-*s│\n", width-15, progressStr)

		// Progress bar
		barWidth := width - 8
		bar := renderProgressBar(pct, barWidth)
		fmt.Printf("  │  %s  │\n", bar)

		// Season details if available
		if len(status.Progress.Seasons) > 0 {
			fmt.Printf("  │  %-*s│\n", width-5, "Seasons:")
			printSeasonProgress(status.Progress.Seasons, width)
		}
	}

	fmt.Printf("  └%s┘\n", strings.Repeat("─", width-4))
}

func printSeasonProgress(seasons []*model.SeasonProgress, width int) {
	// Show active seasons (in progress) and recently completed
	for _, s := range seasons {
		if s.Total == 0 {
			continue
		}
		pct := float64(s.Completed) / float64(s.Total) * 100

		// Show seasons that are in progress (between 0 and 100%)
		if pct > 0 && pct < 100 {
			seasonStr := fmt.Sprintf("    %d: %d/%d", s.StartYear, s.Completed, s.Total)
			barWidth := width - len(seasonStr) - 12
			if barWidth < 10 {
				barWidth = 10
			}
			bar := renderProgressBar(pct, barWidth)
			line := fmt.Sprintf("%s %s %3.0f%%", seasonStr, bar, pct)
			fmt.Printf("  │  %-*s│\n", width-5, line)
		}
	}
}

func formatWorkflowStatus(status model.TemporalWorkflowStatus) string {
	switch status {
	case model.TemporalWorkflowStatusRunning:
		return "RUNNING"
	case model.TemporalWorkflowStatusCompleted:
		return "COMPLETED"
	case model.TemporalWorkflowStatusFailed:
		return "FAILED"
	case model.TemporalWorkflowStatusCanceled:
		return "CANCELED"
	case model.TemporalWorkflowStatusTerminated:
		return "TERMINATED"
	case model.TemporalWorkflowStatusTimedOut:
		return "TIMED OUT"
	case model.TemporalWorkflowStatusContinuedAsNew:
		return "CONTINUED"
	default:
		return "UNKNOWN"
	}
}
