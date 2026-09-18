package workflow

import (
	"fmt"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/graph/model"
	workplayer "github.com/sperano/puckdb/worker/player"
	"github.com/sperano/puckdb/worker/shared"
	"go.temporal.io/sdk/workflow"
)

// GroupImportPlayerLogsData is the single progress group for ImportPlayerLogsWorkflow.
const GroupImportPlayerLogsData = 0

// NewImportPlayerLogsProgressReport creates the initial progress structure for import player logs.
func NewImportPlayerLogsProgressReport() *shared.ProgressReport {
	return &shared.ProgressReport{
		Groups: []shared.ProgressGroup{
			{Header: "Importing player logs...", Bars: []shared.ProgressBar{}},
		},
	}
}

// ImportPlayerLogsWorkflow imports player game logs from cached files into the database.
// Spawns ImportSeasonPlayerLogsWorkflow children for each season's player game log batches.
func ImportPlayerLogsWorkflow(ctx workflow.Context, input *model.SeasonsInput) error {
	logger := workflow.GetLogger(ctx)

	concurrency, err := shared.SnapshotConfigInt(ctx, logger, shared.SeasonConcurrencyParam, input.SeasonConcurrency)
	if err != nil {
		return err
	}

	tracker, err := shared.InitTracker(ctx, NewImportPlayerLogsProgressReport())
	if err != nil {
		return err
	}

	logger.Info("ImportPlayerLogsWorkflow started",
		"startSeason", input.StartSeason,
		"endSeason", input.EndSeason,
		"concurrency", concurrency)

	ctx = workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())

	seasons, err := loadSeasonsManifest(ctx, logger, input)
	if err != nil {
		return err
	}
	if len(seasons) == 0 {
		return nil
	}

	startYears := make([]int, len(seasons))
	for i, s := range seasons {
		startYears[i] = s.ID.StartYear()
	}

	var playerAct *workplayer.Activities
	var playerCounts map[int]int
	if err := workflow.ExecuteActivity(ctx, playerAct.CountPlayersForAllSeasons, startYears).Get(ctx, &playerCounts); err != nil {
		return fmt.Errorf("count players for all seasons: %w", err)
	}

	_, err = processSeasonGroup(ctx, tracker, seasons, concurrency, SeasonGroupConfig{
		GroupIdx:      GroupImportPlayerLogsData,
		Counter:       playerCounter(playerCounts),
		SourceKeyFunc: WorkflowIDImportSeasonPlayerLogs,
		GroupLabel:    "Imported player logs for",
		CountLabel:    "cache reads",
	}, func(ctx workflow.Context, i int) workflow.Future {
		season := seasons[i]
		return workflow.ExecuteChildWorkflow(
			shared.WithChildOptions(ctx, WorkflowIDImportSeasonPlayerLogs(season.ID.StartYear())),
			ImportSeasonPlayerLogsWorkflow, season)
	})
	return err
}

// playerCounter returns a shared.SeasonCounterFunc that returns the player count
// for each season from pre-fetched counts.
func playerCounter(playerCounts map[int]int) shared.SeasonCounterFunc {
	return func(_ workflow.Context, season nhl.SeasonInfo) (int, error) {
		return playerCounts[season.ID.StartYear()], nil
	}
}
