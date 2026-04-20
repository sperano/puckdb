package workflow

import (
	"fmt"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/graph/model"
	worknhl "github.com/sperano/puckdb/worker/nhl"
	"github.com/sperano/puckdb/worker/shared"
	"go.temporal.io/sdk/workflow"
)

// MinEdgeStatsSeasonID is the first season with Edge tracking data.
const MinEdgeStatsSeasonID = 20212022

// countEdgeTeams returns 32 (fixed team count) for progress bar sizing.
func countEdgeTeams(_ nhl.SeasonInfo) (int, error) {
	return 32, nil
}

// FetchEdgeSeasonsWorkflow fetches Edge stats for all eligible seasons (>= 2021-2022).
func FetchEdgeSeasonsWorkflow(ctx workflow.Context, input *model.SeasonsInput) error {
	logger := workflow.GetLogger(ctx)

	tracker := shared.NewReportTracker(&shared.ProgressReport{
		Groups: []shared.ProgressGroup{
			{Header: "Fetching Edge stats...", Bars: []shared.ProgressBar{}},
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

	// Filter to Edge-eligible seasons
	seasons = filterEdgeSeasons(seasons)
	if len(seasons) == 0 {
		return nil
	}

	_, err = processSeasonGroup(ctx, tracker, seasons, concurrency, SeasonGroupConfig{
		GroupIdx:    0,
		Counter:     countEdgeTeams,
		ChildIDFunc: WorkflowIDFetchEdge,
		GroupLabel:  "Fetched Edge stats for",
		CountLabel:  "endpoints",
	}, func(ctx workflow.Context, i int) workflow.Future {
		season := seasons[i]
		return workflow.ExecuteChildWorkflow(
			shared.WithChildOptions(ctx, WorkflowIDFetchEdge(season.ID.StartYear())),
			FetchEdgeWorkflow, season)
	})
	return err
}

// FetchEdgeWorkflow fetches Edge stats for a single season (both game types).
func FetchEdgeWorkflow(ctx workflow.Context, season nhl.SeasonInfo) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("FetchEdgeWorkflow started", "season", season.ID.StartYear())

	ctx = workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())

	var sa *worknhl.SeasonsActivities
	startYear := season.ID.StartYear()

	for _, gameType := range edgeGameTypes {
		input := worknhl.FetchEdgeInput{Season: startYear, GameType: int(gameType)}

		if err := workflow.ExecuteActivity(ctx, sa.FetchEdgeLandings, input).Get(ctx, nil); err != nil {
			return fmt.Errorf("fetch edge landings (gt=%d): %w", gameType, err)
		}
		if err := workflow.ExecuteActivity(ctx, sa.FetchEdgeTeams, input).Get(ctx, nil); err != nil {
			return fmt.Errorf("fetch edge teams (gt=%d): %w", gameType, err)
		}
		if err := workflow.ExecuteActivity(ctx, sa.FetchEdgeSkaters, input).Get(ctx, nil); err != nil {
			return fmt.Errorf("fetch edge skaters (gt=%d): %w", gameType, err)
		}
		if err := workflow.ExecuteActivity(ctx, sa.FetchEdgeGoalies, input).Get(ctx, nil); err != nil {
			return fmt.Errorf("fetch edge goalies (gt=%d): %w", gameType, err)
		}
	}

	logger.Info("FetchEdgeWorkflow completed", "season", startYear)
	return nil
}

// WorkflowIDFetchEdge returns the workflow ID for a single season Edge fetch.
func WorkflowIDFetchEdge(startYear int) string {
	return fmt.Sprintf("fetch-edge-%d", startYear)
}

// edgeGameTypes lists the game types for Edge stat fetching.
var edgeGameTypes = []nhl.GameType{
	nhl.GameTypeRegularSeason,
	nhl.GameTypePlayoffs,
}

// filterEdgeSeasons returns only seasons eligible for Edge tracking data.
func filterEdgeSeasons(seasons []nhl.SeasonInfo) []nhl.SeasonInfo {
	var result []nhl.SeasonInfo
	for _, s := range seasons {
		if s.ID.ID() >= MinEdgeStatsSeasonID {
			result = append(result, s)
		}
	}
	return result
}
