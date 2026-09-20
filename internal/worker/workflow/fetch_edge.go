package workflow

import (
	"fmt"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/graph/model"
	worknhl "github.com/sperano/puckdb/internal/worker/nhl"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/workflow"
)

// MinEdgeStatsSeasonID is the first season with Edge tracking data.
const MinEdgeStatsSeasonID = 20212022

// edgeActivitiesPerTeam is the number of per-team activities (team detail + skaters + goalies).
const edgeActivitiesPerTeam = 3

// edgeActivityNames labels each per-team activity for error messages.
// Order must match the dispatch order below (FetchEdgeTeam, FetchEdgeTeamSkaters,
// FetchEdgeTeamGoalies) so the index used for completion matches the index used
// to look up the name.
var edgeActivityNames = [edgeActivitiesPerTeam]string{"team", "skaters", "goalies"}

// edgeTeamCount is the fixed NHL team count.
const edgeTeamCount = 32

// countEdgeActivities returns 192 (32 teams × 3 activities × 2 game types) for progress bar sizing.
func countEdgeActivities(_ workflow.Context, _ nhl.SeasonInfo) (int, error) {
	return edgeTeamCount * edgeActivitiesPerTeam * len(edgeGameTypes), nil
}

// FetchEdgeSeasonsWorkflow fetches Edge stats for all eligible seasons (>= 2021-2022).
func FetchEdgeSeasonsWorkflow(ctx workflow.Context, input *model.SeasonsInput) error {
	logger := workflow.GetLogger(ctx)

	concurrency, err := shared.SnapshotConfigInt(ctx, logger, shared.SeasonConcurrencyParam, input.SeasonConcurrency)
	if err != nil {
		return err
	}

	tracker, err := shared.InitTracker(ctx, &shared.ProgressReport{
		Groups: []shared.ProgressGroup{
			{Header: "Fetching Edge stats...", Bars: []shared.ProgressBar{}},
		},
	})
	if err != nil {
		return err
	}

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

	// With an explicit season range the operator scoped the refresh
	// deliberately, so the flag applies to every season in it. Without a
	// range it applies to the latest season only — the one still accruing
	// data. This intentionally avoids IsCurrentSeason: nhl.Current() rolls
	// over on July 1, which made the flag a silent no-op for a season whose
	// playoffs had just ended.
	refreshFlag := input.RefreshCurrentEdge != nil && *input.RefreshCurrentEdge
	rangeGiven := input.StartSeason != nil || input.EndSeason != nil

	_, err = processSeasonGroup(ctx, tracker, seasons, concurrency, SeasonGroupConfig{
		GroupIdx:      0,
		Counter:       countEdgeActivities,
		SourceKeyFunc: WorkflowIDFetchEdge,
		GroupLabel:    "Fetched Edge stats for",
		CountLabel:    "endpoints",
	}, func(ctx workflow.Context, i int) workflow.Future {
		season := seasons[i]
		refresh := refreshFlag && (rangeGiven || i == len(seasons)-1)
		return workflow.ExecuteChildWorkflow(
			shared.WithChildOptions(ctx, WorkflowIDFetchEdge(season.ID.StartYear())),
			FetchEdgeWorkflow, FetchEdgeWorkflowInput{Season: season, RefreshCurrent: refresh})
	})
	return err
}

// FetchEdgeWorkflowInput contains parameters for a single-season Edge fetch.
type FetchEdgeWorkflowInput struct {
	Season         nhl.SeasonInfo
	RefreshCurrent bool
}

// NewFetchEdgeProgressReport creates the progress structure for a single season Edge fetch.
func NewFetchEdgeProgressReport(season nhl.SeasonInfo) *shared.ProgressReport {
	total := edgeTeamCount * edgeActivitiesPerTeam * len(edgeGameTypes)
	return &shared.ProgressReport{
		Total: total,
		Groups: []shared.ProgressGroup{
			{Header: fmt.Sprintf("Fetching Edge %s...", season.Label()), Bars: []shared.ProgressBar{{Total: total}}},
		},
	}
}

// FetchEdgeWorkflow fetches Edge stats for a single season (both game types).
// Returns the count of completed activities for progress tracking.
func FetchEdgeWorkflow(ctx workflow.Context, input FetchEdgeWorkflowInput) (core.OriginCounts, error) {
	season := input.Season
	logger := workflow.GetLogger(ctx)
	logger.Info("FetchEdgeWorkflow started", "season", season.ID.StartYear(), "refreshCurrent", input.RefreshCurrent)

	// Set up progress tracking so parent can query our progress
	tracker, err := shared.InitTracker(ctx, NewFetchEdgeProgressReport(season))
	if err != nil {
		return nil, err
	}
	tracker.StartGroup(ctx, 0)

	ctx = workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())

	var sa *worknhl.SeasonsActivities
	startYear := season.ID.StartYear()
	seasonID := season.ID.ID()

	// Get team list for this season
	var teams []worknhl.EdgeTeamInfo
	if err := workflow.ExecuteActivity(ctx, sa.GetEdgeSeasonTeams, seasonID).Get(ctx, &teams); err != nil {
		return nil, fmt.Errorf("get season teams: %w", err)
	}

	var activityCount int
	for _, gameType := range edgeGameTypes {
		edgeInput := worknhl.FetchEdgeInput{Season: startYear, GameType: int(gameType), RefreshCurrent: input.RefreshCurrent}

		// Fetch league-wide landing pages first
		if err := workflow.ExecuteActivity(ctx, sa.FetchEdgeLandings, edgeInput).Get(ctx, nil); err != nil {
			return core.OriginCounts{core.OriginRemoteNHLAPI: activityCount}, fmt.Errorf("fetch edge landings (gt=%d): %w", gameType, err)
		}

		// Fan out per-team activities
		var futures []workflow.Future
		for _, team := range teams {
			teamInput := worknhl.FetchEdgeTeamInput{
				Season:         startYear,
				GameType:       int(gameType),
				TeamID:         team.TeamID,
				TeamAbbrev:     team.Abbrev,
				RefreshCurrent: input.RefreshCurrent,
			}
			futures = append(futures, workflow.ExecuteActivity(ctx, sa.FetchEdgeTeam, teamInput))
			futures = append(futures, workflow.ExecuteActivity(ctx, sa.FetchEdgeTeamSkaters, teamInput))
			futures = append(futures, workflow.ExecuteActivity(ctx, sa.FetchEdgeTeamGoalies, teamInput))
		}

		// Wait for all team activities to complete
		for i, f := range futures {
			if err := f.Get(ctx, nil); err != nil {
				teamIdx := i / edgeActivitiesPerTeam
				activityType := edgeActivityNames[i%edgeActivitiesPerTeam]
				return core.OriginCounts{core.OriginRemoteNHLAPI: activityCount}, fmt.Errorf("fetch edge %s for %s (gt=%d): %w", activityType, teams[teamIdx].Abbrev, gameType, err)
			}
			activityCount++
			tracker.IncrementBar(ctx, 0, 0)
		}
	}

	tracker.CompleteGroup(ctx, 0, fmt.Sprintf("Fetched Edge %s in %s.", season.Label(), tracker.GetElapsed(ctx, 0)))
	logger.Info("FetchEdgeWorkflow completed", "season", startYear, "activities", activityCount)
	return core.OriginCounts{core.OriginRemoteNHLAPI: activityCount}, nil
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
