package workflow

import (
	"fmt"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/graph/model"
	worknhl "github.com/sperano/puckdb/internal/worker/nhl"
	workplayer "github.com/sperano/puckdb/internal/worker/player"
	"github.com/sperano/puckdb/internal/worker/shared"
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

// NewExtractBoxscorePlayersProgressReport creates the initial progress structure.
// Bars are added dynamically once seasons are known.
func NewExtractBoxscorePlayersProgressReport() *shared.ProgressReport {
	return &shared.ProgressReport{
		Groups: []shared.ProgressGroup{
			{Header: "Extracting boxscore players...", Bars: []shared.ProgressBar{}},
			{Header: "Consolidating players...", Bars: []shared.ProgressBar{{Label: "Merging", Total: 1}}},
		},
	}
}

// ExtractBoxscorePlayersWorkflow extracts unique players from boxscores for each season
// and stores them in Redis. This enables downstream workflows to access player data
// without re-extracting from files.
func ExtractBoxscorePlayersWorkflow(ctx workflow.Context, input *model.SeasonsInput) error {
	logger := workflow.GetLogger(ctx)

	var seasonOverride *int
	if input != nil {
		seasonOverride = input.SeasonConcurrency
	}
	concurrency, err := shared.SnapshotConfigInt(ctx, logger, shared.SeasonConcurrencyParam, seasonOverride)
	if err != nil {
		return err
	}

	// Register query handler before any activity runs so progress queries work from workflow start
	tracker, err := shared.InitTracker(ctx, NewExtractBoxscorePlayersProgressReport())
	if err != nil {
		return err
	}

	ctx = workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())
	seasons, err := loadSeasonsManifest(ctx, logger, input)
	if err != nil {
		return err
	}
	logger.Info("ExtractBoxscorePlayersWorkflow started",
		"startSeason", input.StartSeason,
		"endSeason", input.EndSeason,
		"concurrency", concurrency)

	if len(seasons) == 0 {
		logger.Info("No seasons to process")
		return nil
	}

	// Phase 1: Extract boxscore players per season
	var ba *worknhl.BoxscoreActivities
	if _, err = processSeasonGroup(ctx, tracker, seasons, concurrency, SeasonGroupConfig{
		GroupIdx:      GroupExtractBoxscorePlayers,
		Counter:       shared.CountDaysInSeason,
		SourceKeyFunc: WorkflowIDExtractSeason,
		GroupLabel:    "Extracted players for",
		CountLabel:    "boxscore reads",
	}, func(_ workflow.Context, i int) workflow.Future {
		return workflow.ExecuteActivity(ctx, ba.ExtractAndSaveBoxscorePlayers,
			worknhl.ExtractAndSaveInput{
				Season:  seasons[i],
				EndDate: shared.EffectiveEndDate(ctx, seasons[i].StandingsEnd.Time),
			})
	}); err != nil {
		return err
	}

	// Phase 2: Consolidate all seasons into one unique set
	tracker.StartGroup(ctx, GroupConsolidatePlayers)

	seasonIDs := make([]nhl.Season, len(seasons))
	for i, s := range seasons {
		seasonIDs[i] = s.ID
	}

	var pa *workplayer.Activities
	var consolidateResult workplayer.ConsolidatePlayersResult
	consolidateInput := workplayer.ConsolidatePlayersInput{Seasons: seasonIDs}
	if err := workflow.ExecuteActivity(ctx, pa.ConsolidateBoxscorePlayersActivity, consolidateInput).Get(ctx, &consolidateResult); err != nil {
		return err
	}

	tracker.IncrementBar(ctx, GroupConsolidatePlayers, 0)
	tracker.CompleteGroup(ctx, GroupConsolidatePlayers,
		fmt.Sprintf("Consolidated %d unique players from %d total in %s.",
			consolidateResult.UniquePlayers, consolidateResult.TotalPlayers,
			tracker.GetElapsed(ctx, GroupConsolidatePlayers)))

	logger.Info("ExtractBoxscorePlayersWorkflow completed",
		"seasons", len(seasons),
		"uniquePlayers", consolidateResult.UniquePlayers)
	return nil
}
