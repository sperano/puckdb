package workflow

import (
	"fmt"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/graph/model"
	worknhl "github.com/sperano/puckdb/worker/nhl"
	"github.com/sperano/puckdb/worker/shared"
	"go.temporal.io/sdk/workflow"
)

// edgeImportActivitiesPerTeam is the number of per-team import activities (team + skaters + goalies).
const edgeImportActivitiesPerTeam = 3

// countEdgeImportActivities returns 192 (32 teams × 3 activities × 2 game types) for progress bar sizing.
func countEdgeImportActivities(_ workflow.Context, _ nhl.SeasonInfo) (int, error) {
	return edgeTeamCount * edgeImportActivitiesPerTeam * len(edgeGameTypes), nil
}

// ImportEdgeSeasonsWorkflow imports cached Edge stats into the database for all eligible seasons.
func ImportEdgeSeasonsWorkflow(ctx workflow.Context, input *model.SeasonsInput) error {
	logger := workflow.GetLogger(ctx)

	tracker, err := shared.InitTracker(ctx, &shared.ProgressReport{
		Groups: []shared.ProgressGroup{
			{Header: "Importing Edge stats...", Bars: []shared.ProgressBar{}},
		},
	})
	if err != nil {
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
		Counter:     countEdgeImportActivities,
		SourceKeyFunc: WorkflowIDImportEdge,
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

// NewImportEdgeProgressReport creates the progress structure for a single season Edge import.
func NewImportEdgeProgressReport(season nhl.SeasonInfo) *shared.ProgressReport {
	total := edgeTeamCount * edgeImportActivitiesPerTeam * len(edgeGameTypes)
	return &shared.ProgressReport{
		Total: total,
		Groups: []shared.ProgressGroup{
			{Header: fmt.Sprintf("Importing Edge %s...", season.Label()), Bars: []shared.ProgressBar{{Total: total}}},
		},
	}
}

// ImportEdgeWorkflow imports cached Edge stats for a single season (both game types).
// Returns the count of completed activities for progress tracking.
func ImportEdgeWorkflow(ctx workflow.Context, season nhl.SeasonInfo) (core.OriginCounts, error) {
	logger := workflow.GetLogger(ctx)
	logger.Info("ImportEdgeWorkflow started", "season", season.ID.StartYear())

	// Set up progress tracking so parent can query our progress
	tracker, err := shared.InitTracker(ctx, NewImportEdgeProgressReport(season))
	if err != nil {
		return nil, err
	}
	tracker.StartGroup(ctx, 0)

	ctx = workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())

	var sa *worknhl.SeasonsActivities
	startYear := season.ID.StartYear()
	seasonID := season.ID.ID()

	// Get team list for this season (same as FetchEdgeWorkflow)
	var teams []worknhl.EdgeTeamInfo
	if err := workflow.ExecuteActivity(ctx, sa.GetEdgeSeasonTeams, seasonID).Get(ctx, &teams); err != nil {
		return nil, fmt.Errorf("get season teams: %w", err)
	}

	var activityCount int
	for _, gameType := range edgeGameTypes {
		// Fan out per-team activities
		var futures []workflow.Future
		for _, team := range teams {
			teamInput := worknhl.ImportEdgeTeamInput{
				Season:     startYear,
				GameType:   int(gameType),
				TeamID:     team.TeamID,
				TeamAbbrev: team.Abbrev,
			}
			futures = append(futures, workflow.ExecuteActivity(ctx, sa.ImportEdgeTeam, teamInput))
			futures = append(futures, workflow.ExecuteActivity(ctx, sa.ImportEdgeTeamSkaters, teamInput))
			futures = append(futures, workflow.ExecuteActivity(ctx, sa.ImportEdgeTeamGoalies, teamInput))
		}

		// Wait for all team activities to complete
		for i, f := range futures {
			if err := f.Get(ctx, nil); err != nil {
				teamIdx := i / edgeImportActivitiesPerTeam
				activityType := []string{"team", "skaters", "goalies"}[i%edgeImportActivitiesPerTeam]
				return core.OriginCounts{core.OriginFileSystem: activityCount}, fmt.Errorf("import edge %s for %s (gt=%d): %w", activityType, teams[teamIdx].Abbrev, gameType, err)
			}
			activityCount++
			tracker.IncrementBar(ctx, 0, 0)
		}
	}

	tracker.CompleteGroup(ctx, 0, fmt.Sprintf("Imported Edge %s in %s.", season.Label(), tracker.GetElapsed(ctx, 0)))
	logger.Info("ImportEdgeWorkflow completed", "season", startYear, "activities", activityCount)
	return core.OriginCounts{core.OriginFileSystem: activityCount}, nil
}

// WorkflowIDImportEdge returns the workflow ID for a single season Edge import.
func WorkflowIDImportEdge(startYear int) string {
	return fmt.Sprintf("import-edge-%d", startYear)
}
