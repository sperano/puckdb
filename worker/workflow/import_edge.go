package workflow

import (
	"fmt"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/graph/model"
	worknhl "github.com/sperano/puckdb/worker/nhl"
	"github.com/sperano/puckdb/worker/shared"
	"go.temporal.io/sdk/workflow"
)

// ImportEdgeSeasonsWorkflow imports cached Edge stats into the database for all eligible seasons.
func ImportEdgeSeasonsWorkflow(ctx workflow.Context, input *model.SeasonsInput) error {
	logger := workflow.GetLogger(ctx)

	tracker := shared.NewReportTracker(&shared.ProgressReport{
		Groups: []shared.ProgressGroup{
			{Header: "Importing Edge stats...", Bars: []shared.ProgressBar{}},
		},
	})
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return err
	}

	concurrency := shared.ResolveConfigInt(logger, shared.SeasonConcurrencyParam, input.SeasonConcurrency)
	ctx = workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())

	seasons, err := loadSeasonsManifest(ctx, logger, input)
	if err != nil {
		return err
	}

	seasons = filterEdgeSeasons(seasons)
	if len(seasons) == 0 {
		return nil
	}

	_, err = processSeasonGroup(ctx, tracker, seasons, concurrency, SeasonGroupConfig{
		GroupIdx:    0,
		Counter:     countEdgeTeams,
		ChildIDFunc: WorkflowIDImportEdge,
		GroupLabel:  "Imported Edge stats for",
		CountLabel:  "imports",
	}, func(ctx workflow.Context, i int) workflow.Future {
		season := seasons[i]
		return workflow.ExecuteChildWorkflow(
			shared.WithChildOptions(ctx, WorkflowIDImportEdge(season.ID.StartYear())),
			ImportEdgeWorkflow, season)
	})
	return err
}

// ImportEdgeWorkflow imports cached Edge stats for a single season (both game types).
func ImportEdgeWorkflow(ctx workflow.Context, season nhl.SeasonInfo) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("ImportEdgeWorkflow started", "season", season.ID.StartYear())

	ctx = workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())

	var sa *worknhl.SeasonsActivities
	startYear := season.ID.StartYear()

	for _, gameType := range edgeGameTypes {
		input := worknhl.FetchEdgeInput{Season: startYear, GameType: int(gameType)}

		if err := workflow.ExecuteActivity(ctx, sa.ImportEdgeTeams, input).Get(ctx, nil); err != nil {
			return fmt.Errorf("import edge teams (gt=%d): %w", gameType, err)
		}
		if err := workflow.ExecuteActivity(ctx, sa.ImportEdgeTeamZoneTimeDetails, input).Get(ctx, nil); err != nil {
			return fmt.Errorf("import edge team zone time (gt=%d): %w", gameType, err)
		}
		if err := workflow.ExecuteActivity(ctx, sa.ImportEdgeSkaters, input).Get(ctx, nil); err != nil {
			return fmt.Errorf("import edge skaters (gt=%d): %w", gameType, err)
		}
		if err := workflow.ExecuteActivity(ctx, sa.ImportEdgeGoalies, input).Get(ctx, nil); err != nil {
			return fmt.Errorf("import edge goalies (gt=%d): %w", gameType, err)
		}
	}

	logger.Info("ImportEdgeWorkflow completed", "season", startYear)
	return nil
}

// WorkflowIDImportEdge returns the workflow ID for a single season Edge import.
func WorkflowIDImportEdge(startYear int) string {
	return fmt.Sprintf("import-edge-%d", startYear)
}
