package workflow

import (
	"fmt"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/store"
	workplayer "github.com/sperano/puckdb/worker/player"
	"github.com/sperano/puckdb/worker/shared"
	"go.temporal.io/sdk/workflow"
)

// FetchSeasonPlayerLogsInput contains parameters for the child workflow.
type FetchSeasonPlayerLogsInput struct {
	Season         nhl.SeasonInfo
	RefreshCurrent bool // If true, overwrite files for the current season
}

// GroupFetchSeasonPlayerLogs is the group index for per-season progress.
const GroupFetchSeasonPlayerLogs = 0

// NewFetchSeasonPlayerLogsReport creates the progress report for a single season.
func NewFetchSeasonPlayerLogsReport(playerCount int) *shared.ProgressReport {
	return &shared.ProgressReport{
		Total: playerCount,
		Groups: []shared.ProgressGroup{
			{Header: "Fetching player game logs...", Bars: []shared.ProgressBar{{Label: "Players", Total: playerCount}}},
		},
	}
}

// FetchSeasonPlayerLogsWorkflow fetches player game logs for all players in a season.
// It uses cached extraction from Redis (populated by ExtractBoxscorePlayersWorkflow).
func FetchSeasonPlayerLogsWorkflow(ctx workflow.Context, input FetchSeasonPlayerLogsInput) error {
	logger := workflow.GetLogger(ctx)
	season := input.Season

	logger.Info("FetchSeasonPlayerLogsWorkflow started",
		"startYear", season.ID.StartYear(),
		"startDate", season.StandingsStart.Format(config.DateFormat),
		"endDate", season.StandingsEnd.Format(config.DateFormat),
		"refreshCurrent", input.RefreshCurrent)

	ctx = workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())

	// Load players from Redis via PlayerActivities
	var playerAct *workplayer.Activities
	var players []store.BoxscorePlayer
	if err := workflow.ExecuteActivity(ctx, playerAct.LoadSeasonBoxscorePlayers, season.ID.StartYear()).Get(ctx, &players); err != nil {
		return err
	}

	playerCount := len(players)
	if playerCount == 0 {
		logger.Warn("No players found for season", "startYear", season.ID.StartYear())
		return nil
	}

	playerIDs := extractPlayerIDs(players)
	batchSize := shared.ResolveConfigInt(nil, shared.PlayerLogsBatchSizeParam, nil)
	numBatches := shared.BatchCount(len(playerIDs), batchSize)
	concurrency := shared.ResolveConfigInt(nil, shared.PlayerLogsConcurrencyParam, nil)

	// Set up ReportTracker
	tracker, err := shared.InitTracker(ctx, NewFetchSeasonPlayerLogsReport(playerCount))
	if err != nil {
		return err
	}
	tracker.StartGroup(ctx, GroupFetchSeasonPlayerLogs)

	logger.Info("Fetching player game logs",
		"startYear", season.ID.StartYear(),
		"playerCount", playerCount,
		"batches", numBatches,
		"batchSize", batchSize,
		"concurrency", concurrency)

	gameTypes := []int{nhl.GameTypeRegularSeason.Int(), nhl.GameTypePlayoffs.Int()}
	startYear := season.ID.StartYear()
	refreshCurrent := input.RefreshCurrent

	err = tracker.RunWorkerPoolWithIncrement(ctx, GroupFetchSeasonPlayerLogs, 0, numBatches, concurrency,
		func(i int) int { return len(shared.BatchSlice(playerIDs, i, batchSize)) },
		func(_ workflow.Context, batchIdx int) workflow.Future {
			batch := shared.BatchSlice(playerIDs, batchIdx, batchSize)
			activityInput := workplayer.DownloadPlayerGameLogsInput{
				PlayerIDs:      batch,
				StartSeason:    startYear,
				GameTypes:      gameTypes,
				RefreshCurrent: refreshCurrent,
				TotalPlayers:   playerCount,
			}
			return workflow.ExecuteActivity(ctx, playerAct.DownloadPlayerGameLogsBatch, activityInput)
		}, nil)
	if err != nil {
		return err
	}

	tracker.CompleteGroup(ctx, GroupFetchSeasonPlayerLogs,
		fmt.Sprintf("Fetched player logs for %d players in %s.",
			playerCount, tracker.GetElapsed(ctx, GroupFetchSeasonPlayerLogs)))

	logger.Info("FetchSeasonPlayerLogsWorkflow completed",
		"startYear", season.ID.StartYear(),
		"players", playerCount)

	return nil
}

// extractPlayerIDs converts BoxscorePlayer slice to player ID slice.
func extractPlayerIDs(players []store.BoxscorePlayer) []int64 {
	ids := make([]int64, len(players))
	for i, p := range players {
		ids[i] = p.ID
	}
	return ids
}

// WorkflowIDFetchSeasonPlayerLogs returns the workflow ID for a single season's player logs fetch.
func WorkflowIDFetchSeasonPlayerLogs(startYear int) string {
	return fmt.Sprintf("fetch-season-player-logs-%d", startYear)
}
