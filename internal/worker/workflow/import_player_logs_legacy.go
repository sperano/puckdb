package workflow

import (
	"fmt"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/graph/model"
	worknhl "github.com/sperano/puckdb/internal/worker/nhl"
	workplayer "github.com/sperano/puckdb/internal/worker/player"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/workflow"
)

// This file replays ImportPlayerLogsWorkflow histories recorded before
// importPlayerLogsManifestChangeID. Those size each season's bar from the
// Redis boxscore-player count, while their children list the season's
// skaters again from the cached boxscores, so the two totals differ. Remove
// the file, ImportSeasonPlayerLogsWorkflow's registration and the
// CountPlayersForAllSeasons and CollectSeasonPlayerIDs activities once no
// such run is open.

// groupImportPlayerLogsLegacy is the single progress group of the legacy path.
const groupImportPlayerLogsLegacy = 0

// importPlayerLogsLegacy is ImportPlayerLogsWorkflow before the skater
// manifest, command for command.
func importPlayerLogsLegacy(ctx workflow.Context, input *model.SeasonsInput) error {
	logger := workflow.GetLogger(ctx)

	concurrency, err := shared.SnapshotConfigInt(ctx, logger, shared.SeasonConcurrencyParam, input.SeasonConcurrency)
	if err != nil {
		return err
	}
	tracker, err := shared.InitTracker(ctx, &shared.ProgressReport{
		Groups: []shared.ProgressGroup{{Header: "Importing player logs...", Bars: []shared.ProgressBar{}}},
	})
	if err != nil {
		return err
	}
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
		GroupIdx: groupImportPlayerLogsLegacy,
		Counter: func(_ workflow.Context, season nhl.SeasonInfo) (int, error) {
			return playerCounts[season.ID.StartYear()], nil
		},
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

// ImportSeasonPlayerLogsWorkflow is the legacy per-season child: it lists the
// season's skaters itself (CollectSeasonPlayerIDs) before importing their
// logs. Only importPlayerLogsLegacy starts it; new runs use
// ImportSeasonPlayerLogsV2Workflow.
func ImportSeasonPlayerLogsWorkflow(ctx workflow.Context, season nhl.SeasonInfo) (core.OriginCounts, error) {
	logger := workflow.GetLogger(ctx)
	logger.Info("ImportSeasonPlayerLogsWorkflow started",
		"startYear", season.ID.StartYear(),
		"startDate", season.StandingsStart.Format(config.DateFormat),
		"endDate", season.StandingsEnd.Format(config.DateFormat))

	dayConcurrency, err := shared.SnapshotConfigInt(ctx, nil, shared.DayConcurrencyParam, nil)
	if err != nil {
		return nil, err
	}
	ctx = workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())

	var ia *worknhl.ImportActivities
	var playerIDs []int64
	collectInput := worknhl.CollectSeasonPlayerIDsInput{
		Season:  season,
		EndDate: shared.EffectiveEndDate(ctx, season.StandingsEnd.Time),
	}
	if err := workflow.ExecuteActivity(ctx, ia.CollectSeasonPlayerIDs, collectInput).Get(ctx, &playerIDs); err != nil {
		return nil, fmt.Errorf("collect player IDs: %w", err)
	}
	return importSeasonPlayerLogBatches(ctx, season, playerIDs, dayConcurrency)
}
