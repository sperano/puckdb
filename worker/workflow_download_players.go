package worker

import (
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/graph/model"
	"github.com/spf13/viper"
	"go.temporal.io/sdk/workflow"
)

const WorkflowIDDownloadPlayers = "download-players"

// DownloadPlayersWorkflow extracts all unique player IDs from boxscores across all seasons.
func DownloadPlayersWorkflow(ctx workflow.Context, input *model.DownloadSeasonsInput) error {
	logger := workflow.GetLogger(ctx)

	maxConcurrency := viper.GetInt(config.FlagMaxSeasonConcurrency)
	if maxConcurrency <= 0 {
		maxConcurrency = 10
	}

	concurrency := config.DefaultSeasonConcurrency
	if input.SeasonConcurrency != nil && *input.SeasonConcurrency > 0 {
		concurrency = *input.SeasonConcurrency
	}
	if concurrency > maxConcurrency {
		logger.Warn("Requested concurrency exceeds maximum, capping",
			"requested", concurrency,
			"max", maxConcurrency)
		concurrency = maxConcurrency
	}

	logger.Info("DownloadPlayersWorkflow started",
		"startSeason", input.StartSeason,
		"endSeason", input.EndSeason,
		"concurrency", concurrency)

	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())

	var seasons []SeasonInfo
	if err := workflow.ExecuteActivity(ctx, FetchSeasonsDataActivity, input).Get(ctx, &seasons); err != nil {
		return err
	}

	tracker := NewProgressTracker(len(seasons))
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return err
	}

	allPlayerIDs, err := extractPlayerIDsWithConcurrency(ctx, tracker, seasons, concurrency)
	if err != nil {
		return err
	}

	logger.Info("DownloadPlayersWorkflow complete",
		"seasons_processed", len(seasons),
		"total_unique_players", len(allPlayerIDs))

	return nil
}

// extractPlayerIDsWithConcurrency processes seasons with bounded concurrency,
// collecting player IDs from each and merging into a single set.
func extractPlayerIDsWithConcurrency(
	ctx workflow.Context,
	tracker *ProgressTracker,
	seasons []SeasonInfo,
	concurrency int,
) (map[int64]struct{}, error) {
	if len(seasons) == 0 {
		return make(map[int64]struct{}), nil
	}

	logger := workflow.GetLogger(ctx)
	allPlayerIDs := make(map[int64]struct{})

	// Track active futures
	type activeWork struct {
		season SeasonInfo
		future workflow.Future
	}
	active := make(map[int]*activeWork)
	nextIdx := 0

	// Start initial batch
	for i := 0; i < concurrency && nextIdx < len(seasons); i++ {
		season := seasons[nextIdx]
		future := workflow.ExecuteActivity(ctx, ExtractPlayerIDsForSeasonActivity, season)
		active[nextIdx] = &activeWork{season: season, future: future}
		nextIdx++
	}

	var firstErr error

	// Process until all work is done
	for len(active) > 0 {
		selector := workflow.NewSelector(ctx)

		for idx, work := range active {
			capturedIdx := idx
			capturedWork := work
			selector.AddFuture(capturedWork.future, func(f workflow.Future) {
				var playerIDs []int64
				if err := f.Get(ctx, &playerIDs); err != nil && firstErr == nil {
					firstErr = err
					return
				}

				// Merge player IDs into the combined set
				for _, id := range playerIDs {
					allPlayerIDs[id] = struct{}{}
				}

				logger.Info("Season extraction complete",
					"startYear", capturedWork.season.StartYear,
					"players_found", len(playerIDs),
					"total_unique", len(allPlayerIDs))

				tracker.Increment()
				delete(active, capturedIdx)

				// Start next season if available
				if nextIdx < len(seasons) {
					season := seasons[nextIdx]
					future := workflow.ExecuteActivity(ctx, ExtractPlayerIDsForSeasonActivity, season)
					active[nextIdx] = &activeWork{season: season, future: future}
					nextIdx++
				}
			})
		}

		selector.Select(ctx)

		if firstErr != nil {
			return nil, firstErr
		}
	}

	return allPlayerIDs, nil
}
