package worker

import (
	"sort"

	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/graph/model"
	"github.com/spf13/viper"
	"go.temporal.io/sdk/workflow"
)

const WorkflowIDImportNHLTeamsAndPlayers = "import-nhl-teams-and-players"

// ImportTeamsAndPlayersResult contains the result of the player extraction.
type ImportTeamsAndPlayersResult struct {
	Players []BoxscorePlayer `json:"players"`
}

// ImportNHLTeamsAndPlayersWorkflow extracts teams and player IDs from boxscores,
// upserts teams to the database, and returns player IDs for downstream processing.
func ImportNHLTeamsAndPlayersWorkflow(ctx workflow.Context, input *model.SeasonsInput) (*ImportTeamsAndPlayersResult, error) {
	logger := workflow.GetLogger(ctx)

	maxConcurrency := viper.GetInt(config.FlagMaxSeasonConcurrency)
	if maxConcurrency <= 0 {
		maxConcurrency = config.DefaultMaxSeasonConcurrency
	}

	concurrency := config.DefaultSeasonConcurrency
	if input != nil && input.SeasonConcurrency != nil && *input.SeasonConcurrency > 0 {
		concurrency = *input.SeasonConcurrency
	}
	if concurrency > maxConcurrency {
		logger.Warn("Requested concurrency exceeds maximum, capping",
			"requested", concurrency,
			"max", maxConcurrency)
		concurrency = maxConcurrency
	}

	logger.Info("ImportNHLTeamsAndPlayersWorkflow started",
		"startSeason", input.StartSeason,
		"endSeason", input.EndSeason,
		"concurrency", concurrency)

	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())

	// Fetch seasons
	var seasons []SeasonInfo
	if err := workflow.ExecuteActivity(ctx, FetchSeasonsDataActivity, input).Get(ctx, &seasons); err != nil {
		return nil, err
	}

	tracker := NewProgressTracker(len(seasons))
	tracker.SetMessage("Extracting teams and players from boxscores")
	if err := tracker.RegisterQueryHandler(ctx); err != nil {
		return nil, err
	}

	// Extract boxscore data from all seasons with concurrency
	allResults, err := extractBoxscoreDataWithConcurrency(ctx, tracker, seasons, concurrency)
	if err != nil {
		return nil, err
	}

	// Merge results: dedupe players by ID
	playerMap := make(map[int64]BoxscorePlayer)

	for _, result := range allResults {
		for _, p := range result.Players {
			playerMap[p.ID] = p
		}
	}

	// Convert to sorted slice for determinism
	players := make([]BoxscorePlayer, 0, len(playerMap))
	for _, p := range playerMap {
		players = append(players, p)
	}
	sort.Slice(players, func(i, j int) bool { return players[i].ID < players[j].ID })

	logger.Info("ImportNHLTeamsAndPlayersWorkflow completed",
		"seasons_processed", len(seasons),
		"total_players", len(players))

	return &ImportTeamsAndPlayersResult{
		Players: players,
	}, nil
}

// extractBoxscoreDataWithConcurrency processes seasons with bounded concurrency,
// extracting both player IDs and teams from each season's boxscores.
func extractBoxscoreDataWithConcurrency(
	ctx workflow.Context,
	tracker *ProgressTracker,
	seasons []SeasonInfo,
	concurrency int,
) ([]BoxscoreExtractionResult, error) {
	if len(seasons) == 0 {
		return nil, nil
	}

	logger := workflow.GetLogger(ctx)
	results := make([]BoxscoreExtractionResult, len(seasons))

	// Track active futures
	type activeWork struct {
		season SeasonInfo
		future workflow.Future
		index  int
	}
	active := make(map[int]*activeWork)
	nextIdx := 0

	// Start initial batch
	for i := 0; i < concurrency && nextIdx < len(seasons); i++ {
		season := seasons[nextIdx]
		future := workflow.ExecuteActivity(ctx, ExtractBoxscoreDataForSeasonActivity, season)
		active[nextIdx] = &activeWork{season: season, future: future, index: nextIdx}
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
				var result BoxscoreExtractionResult
				if err := f.Get(ctx, &result); err != nil && firstErr == nil {
					firstErr = err
					return
				}

				// Store result at the correct index to maintain order
				results[capturedWork.index] = result

				logger.Info("Season extraction complete",
					"startYear", capturedWork.season.StartYear(),
					"players_found", len(result.Players))

				tracker.Increment()
				delete(active, capturedIdx)

				// Start next season if available
				if nextIdx < len(seasons) {
					season := seasons[nextIdx]
					future := workflow.ExecuteActivity(ctx, ExtractBoxscoreDataForSeasonActivity, season)
					active[nextIdx] = &activeWork{season: season, future: future, index: nextIdx}
					nextIdx++
				}
			})
		}

		selector.Select(ctx)

		if firstErr != nil {
			return nil, firstErr
		}
	}

	return results, nil
}
