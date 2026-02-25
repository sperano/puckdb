package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/graph/model"
	"github.com/spf13/viper"
	"go.temporal.io/sdk/workflow"
)

const (
	// WorkflowIDExtractBoxscorePlayers is the ID for the extract boxscore players workflow.
	WorkflowIDExtractBoxscorePlayers = "extract-boxscore-players"

	// GroupExtractBoxscorePlayers is the group index for extraction progress tracking.
	GroupExtractBoxscorePlayers = 0

	// GroupConsolidatePlayers is the group index for consolidation progress tracking.
	GroupConsolidatePlayers = 1
)

// WorkflowIDExtractSeason returns the workflow ID for a single season extraction.
// Used as Redis key prefix for progress tracking.
func WorkflowIDExtractSeason(startYear int) string {
	return fmt.Sprintf("extract-season-%d", startYear)
}

// ExtractBoxscorePlayersInput contains parameters for the extract boxscore players workflow.
type ExtractBoxscorePlayersInput struct {
	StartSeason       *int
	EndSeason         *int
	SeasonConcurrency *int
	TTLMinutes        *int // Redis TTL in minutes (default: 30)
}

// NewExtractBoxscorePlayersProgressReport creates the initial progress structure.
// Bars are added dynamically once seasons are known.
func NewExtractBoxscorePlayersProgressReport() *ProgressReport {
	return &ProgressReport{
		Groups: []ProgressGroup{
			{Header: "Extracting boxscore players...", Bars: []ProgressBar{}},
			{Header: "Consolidating players...", Bars: []ProgressBar{{Label: "Merging", Total: 1}}},
		},
	}
}

// ExtractBoxscorePlayersWorkflow extracts unique players from boxscores for each season
// and stores them in Redis. This enables downstream workflows to access player data
// without re-extracting from files.
func ExtractBoxscorePlayersWorkflow(ctx workflow.Context, input *ExtractBoxscorePlayersInput) error {
	logger := workflow.GetLogger(ctx)

	// Register query handler immediately so progress queries work from workflow start
	tracker := NewReportTracker(NewExtractBoxscorePlayersProgressReport())
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return err
	}

	maxConcurrency := viper.GetInt(config.FlagMaxSeasonConcurrency)
	if maxConcurrency <= 0 {
		maxConcurrency = config.DefaultMaxSeasonConcurrency
	}

	concurrency := config.DefaultSeasonConcurrency
	if input != nil && input.SeasonConcurrency != nil && *input.SeasonConcurrency > 0 {
		concurrency = *input.SeasonConcurrency
	}
	if concurrency > maxConcurrency {
		logger.Warn("Requested concurrency exceeds maximum, capping",
			"requested", concurrency,
			"max", maxConcurrency)
		concurrency = maxConcurrency
	}

	// Determine TTL for Redis storage
	ttl := cache.BoxscorePlayersTTL
	if input != nil && input.TTLMinutes != nil && *input.TTLMinutes > 0 {
		ttl = time.Duration(*input.TTLMinutes) * time.Minute
	}

	logger.Info("ExtractBoxscorePlayersWorkflow started",
		"startSeason", input.StartSeason,
		"endSeason", input.EndSeason,
		"concurrency", concurrency,
		"ttlMinutes", int(ttl.Minutes()))

	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())

	// Fetch seasons list
	seasonsInput := buildSeasonsInputFromExtract(input)
	var seasons []SeasonInfo
	if err := workflow.ExecuteActivity(ctx, FetchSeasonsDataActivity, seasonsInput).Get(ctx, &seasons); err != nil {
		return err
	}

	if len(seasons) == 0 {
		logger.Info("No seasons to process")
		return nil
	}

	// Clear stale progress from previous runs
	workflowIDs := make([]string, len(seasons))
	for i, s := range seasons {
		workflowIDs[i] = WorkflowIDExtractSeason(s.StartYear())
	}
	if err := workflow.ExecuteActivity(ctx, ClearProgressActivity, workflowIDs).Get(ctx, nil); err != nil {
		logger.Warn("Failed to clear progress", "error", err)
	}

	// Add one bar per season with day count as total
	// Set ChildWorkflowID to the Redis key pattern so resolver can read progress
	totalDays := 0
	for _, season := range seasons {
		days := countDaysInSeason(season)
		tracker.report.Groups[GroupExtractBoxscorePlayers].Bars = append(
			tracker.report.Groups[GroupExtractBoxscorePlayers].Bars,
			ProgressBar{
				Label:           season.Label(),
				Total:           days,
				ChildWorkflowID: WorkflowIDExtractSeason(season.StartYear()),
			},
		)
		totalDays += days
	}
	tracker.report.Total = totalDays
	tracker.StartGroup(ctx, GroupExtractBoxscorePlayers)

	// Process seasons concurrently using worker pool (one bar per season)
	err := tracker.RunWorkerPoolMultiBar(ctx, GroupExtractBoxscorePlayers, 0, len(seasons), concurrency,
		func(_ workflow.Context, i int) workflow.Future {
			return workflow.ExecuteActivity(ctx, ExtractAndSaveBoxscorePlayersActivity,
				ExtractAndSaveInput{Season: seasons[i], TTL: ttl})
		}, nil)
	if err != nil {
		return err
	}

	tracker.CompleteGroup(ctx, GroupExtractBoxscorePlayers,
		fmt.Sprintf("Extracted players for %d seasons in %s.", len(seasons), tracker.GetElapsed(ctx, GroupExtractBoxscorePlayers)))

	// Phase 2: Consolidate all seasons into one unique set
	tracker.StartGroup(ctx, GroupConsolidatePlayers)

	seasonIDs := make([]int, len(seasons))
	for i, s := range seasons {
		seasonIDs[i] = s.StartYear()
	}

	var consolidateResult ConsolidatePlayersResult
	consolidateInput := ConsolidatePlayersInput{Seasons: seasonIDs, TTL: ttl}
	if err := workflow.ExecuteActivity(ctx, ConsolidateBoxscorePlayersActivity, consolidateInput).Get(ctx, &consolidateResult); err != nil {
		return err
	}

	tracker.IncrementBar(GroupConsolidatePlayers, 0)
	tracker.CompleteGroup(ctx, GroupConsolidatePlayers,
		fmt.Sprintf("Consolidated %d unique players from %d total in %s.",
			consolidateResult.UniquePlayers, consolidateResult.TotalPlayers,
			tracker.GetElapsed(ctx, GroupConsolidatePlayers)))

	logger.Info("ExtractBoxscorePlayersWorkflow completed",
		"seasons", len(seasons),
		"uniquePlayers", consolidateResult.UniquePlayers)
	return nil
}

// buildSeasonsInputFromExtract converts ExtractBoxscorePlayersInput to SeasonsInput.
func buildSeasonsInputFromExtract(input *ExtractBoxscorePlayersInput) *model.SeasonsInput {
	if input == nil {
		return nil
	}
	return &model.SeasonsInput{
		StartSeason:       input.StartSeason,
		EndSeason:         input.EndSeason,
		SeasonConcurrency: input.SeasonConcurrency,
	}
}

// ExtractAndSaveInput contains parameters for the ExtractAndSaveBoxscorePlayersActivity.
type ExtractAndSaveInput struct {
	Season SeasonInfo
	TTL    time.Duration
}

// ExtractAndSaveBoxscorePlayersActivity extracts players from boxscores for a season
// and saves them to Redis.
func ExtractAndSaveBoxscorePlayersActivity(ctx context.Context, input ExtractAndSaveInput) error {
	// Extract players from boxscores
	result, err := ExtractBoxscoreDataForSeasonActivity(ctx, input.Season)
	if err != nil {
		return fmt.Errorf("extract boxscore data: %w", err)
	}

	// Save to Redis
	redisClient := cache.NewClient()
	defer func() { _ = redisClient.Close() }()

	if err := cache.SaveBoxscorePlayers(ctx, redisClient, input.Season.StartYear(), result.Players, input.TTL); err != nil {
		return fmt.Errorf("save boxscore players to redis: %w", err)
	}

	return nil
}
