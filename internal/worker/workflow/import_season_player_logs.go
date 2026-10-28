package workflow

import (
	"fmt"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/core"
	worknhl "github.com/sperano/puckdb/internal/worker/nhl"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/workflow"
)

// playerGameLogBatchSize is the number of player IDs processed per ImportPlayerGameLogsBatch activity.
const playerGameLogBatchSize = 50

// GroupImportPlayerLogs is the single progress group for ImportSeasonPlayerLogsV2Workflow.
const GroupImportPlayerLogs = 0

// NewImportSeasonPlayerLogsReport creates the progress report for a single season's player log import.
func NewImportSeasonPlayerLogsReport(season nhl.SeasonInfo, playerCount int) *shared.ProgressReport {
	return &shared.ProgressReport{
		Total: playerCount,
		Groups: []shared.ProgressGroup{
			{Header: fmt.Sprintf("Importing player logs for %s...", season.Label()), Bars: []shared.ProgressBar{{Total: playerCount}}},
		},
	}
}

// ImportSeasonPlayerLogsInput is one season of ImportPlayerLogsWorkflow: the
// season and its skater manifest (worknhl.SeasonSkaterManifest.PlayerIDs),
// the same IDs the parent sized the season's bar from.
type ImportSeasonPlayerLogsInput struct {
	Season    nhl.SeasonInfo `json:"season"`
	PlayerIDs []int64        `json:"playerIDs"`
}

// ImportSeasonPlayerLogsV2Workflow imports the game logs of every player in
// input.PlayerIDs, each counted once on the season's bar, so the bar reaches
// exactly the total the parent gave it.
func ImportSeasonPlayerLogsV2Workflow(ctx workflow.Context, input ImportSeasonPlayerLogsInput) (core.OriginCounts, error) {
	season := input.Season
	workflow.GetLogger(ctx).Info("ImportSeasonPlayerLogsV2Workflow started",
		"startYear", season.ID.StartYear(),
		"players", len(input.PlayerIDs))

	dayConcurrency, err := shared.SnapshotConfigInt(ctx, nil, shared.DayConcurrencyParam, nil)
	if err != nil {
		return nil, err
	}
	ctx = workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())
	return importSeasonPlayerLogBatches(ctx, season, input.PlayerIDs, dayConcurrency)
}

// importSeasonPlayerLogBatches imports playerIDs' game logs in batches of
// playerGameLogBatchSize under a one-bar tracker sized len(playerIDs).
func importSeasonPlayerLogBatches(ctx workflow.Context, season nhl.SeasonInfo, playerIDs []int64, concurrency int) (core.OriginCounts, error) {
	logger := workflow.GetLogger(ctx)
	if len(playerIDs) == 0 {
		logger.Info("No players found for season", "startYear", season.ID.StartYear())
		return nil, nil
	}

	batches := shared.NewBatches(playerIDs, playerGameLogBatchSize)
	tracker, err := shared.InitTracker(ctx, NewImportSeasonPlayerLogsReport(season, len(playerIDs)))
	if err != nil {
		return nil, err
	}
	tracker.StartGroup(ctx, GroupImportPlayerLogs)

	logger.Info("Importing player game logs",
		"startYear", season.ID.StartYear(),
		"playerCount", len(playerIDs),
		"batches", batches.Count(),
		"concurrency", concurrency)

	var ia *worknhl.ImportActivities
	totals := seasonPlayerLogTotals{origins: core.OriginCounts{}}
	err = tracker.RunWorkerPool(ctx, shared.PoolOptions{
		Total: batches.Count(), Concurrency: concurrency,
		Progress: batches.ItemProgress(shared.BarRef{Group: GroupImportPlayerLogs}),
		Start: func(activityCtx workflow.Context, i int) workflow.Future {
			input := worknhl.ImportPlayerGameLogsBatchInput{
				Season:    season.ID,
				PlayerIDs: batches.Batch(i),
			}
			return workflow.ExecuteActivity(activityCtx, ia.ImportPlayerGameLogsBatch, input)
		},
		Handle: func(resultCtx workflow.Context, _ int, f workflow.Future) error {
			var batchResult worknhl.ImportPlayerGameLogsBatchResult
			if err := f.Get(resultCtx, &batchResult); err != nil {
				return err
			}
			totals.add(batchResult)
			return nil
		},
	})
	if err != nil {
		return nil, err
	}

	tracker.CompleteGroup(ctx, GroupImportPlayerLogs,
		totals.summary(len(playerIDs), tracker.GetElapsed(ctx, GroupImportPlayerLogs)))
	logger.Info("Imported season player logs",
		"startYear", season.ID.StartYear(),
		"players", len(playerIDs),
		"imported", totals.imported,
		"withoutLog", totals.withoutLog,
		"unreadable", totals.unreadable)
	return totals.origins, nil
}

// seasonPlayerLogTotals sums one season's ImportPlayerGameLogsBatch results.
type seasonPlayerLogTotals struct {
	origins    core.OriginCounts
	imported   int
	withoutLog int
	unreadable int
}

func (t *seasonPlayerLogTotals) add(r worknhl.ImportPlayerGameLogsBatchResult) {
	t.origins.Add(r.Origins)
	t.imported += r.PlayersProcessed
	t.withoutLog += r.PlayersWithoutLog
	t.unreadable += r.PlayersUnreadable
}

// summary is the season group's completion message: how many of the
// players had a log imported, and why the others did not.
func (t *seasonPlayerLogTotals) summary(players int, elapsed string) string {
	msg := fmt.Sprintf("Imported game logs for %d of %d players in %s.", t.imported, players, elapsed)
	if t.withoutLog > 0 {
		msg += fmt.Sprintf(" %d had no cached log.", t.withoutLog)
	}
	if t.unreadable > 0 {
		msg += fmt.Sprintf(" %d logs were unreadable.", t.unreadable)
	}
	return msg
}

// WorkflowIDImportSeasonPlayerLogs returns the workflow ID for a single season's player log import.
func WorkflowIDImportSeasonPlayerLogs(startYear int) string {
	return fmt.Sprintf("import-season-player-logs-%d", startYear)
}
