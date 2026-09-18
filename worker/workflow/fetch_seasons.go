package workflow

import (
	"fmt"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/graph/model"
	worknhl "github.com/sperano/puckdb/worker/nhl"
	"github.com/sperano/puckdb/worker/shared"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/workflow"
)

// Group index for FetchSeasonsWorkflow progress.
const GroupFetchSeasonsData = 0

// NewFetchSeasonsProgressReport creates the initial progress structure.
// Bars are added dynamically once seasons are known.
func NewFetchSeasonsProgressReport() *shared.ProgressReport {
	return &shared.ProgressReport{
		Groups: []shared.ProgressGroup{
			{Header: "Fetching seasons...", Bars: []shared.ProgressBar{}},
		},
	}
}

// daysWithPlayoffTeamsCounter returns a SeasonCounterFunc that sizes a season's
// bar as regular-season days plus one unit per team for the per-team playoff
// phase. The team count comes from the ListSeasonTeams activity so the parent
// bar total matches the child workflow's playoff bar (both read season_teams).
func daysWithPlayoffTeamsCounter() shared.SeasonCounterFunc {
	var pa *worknhl.PlayoffActivities
	return func(ctx workflow.Context, season nhl.SeasonInfo) (int, error) {
		days, err := shared.CountDaysInSeason(ctx, season)
		if err != nil {
			return 0, err
		}
		var teams []string
		if err := workflow.ExecuteActivity(ctx, pa.ListSeasonTeams, season.ID.StartYear()).Get(ctx, &teams); err != nil {
			return 0, fmt.Errorf("list season teams for %d: %w", season.ID.StartYear(), err)
		}
		return days + len(teams), nil
	}
}

// FetchSeasonsWorkflow downloads all data for the requested seasons by
// spawning a FetchSeasonWorkflow child for each one.
func FetchSeasonsWorkflow(ctx workflow.Context, input *model.SeasonsInput) error {
	return iterateSeasons(ctx, input, NewFetchSeasonsProgressReport(), GroupFetchSeasonsData,
		daysWithPlayoffTeamsCounter(), WorkflowIDFetchSeason,
		func(n int, elapsed string, counts core.OriginCounts) string {
			return counts.AppendSummary(fmt.Sprintf("Fetched %d seasons in %s.", n, elapsed), "schedules")
		},
		func(ctx workflow.Context, season nhl.SeasonInfo, _ bool) workflow.ChildWorkflowFuture {
			return workflow.ExecuteChildWorkflow(
				shared.WithChildOptions(ctx, WorkflowIDFetchSeason(season.ID.StartYear())),
				FetchSeasonWorkflow, season)
		})
}

// SeasonGroupConfig configures how processSeasonGroup processes a group of seasons.
type SeasonGroupConfig struct {
	GroupIdx      int
	Counter       shared.SeasonCounterFunc
	SourceKeyFunc shared.ProgressSourceKeyFunc
	GroupLabel    string // verb phrase for completion message (e.g., "Iterated", "Extracted players for")
	CountLabel    string // label for OriginCounts summary (e.g., "children count", "boxscore reads")
}

// processSeasonGroup runs concurrent work across seasons with progress tracking.
// It adds bars, starts the group, runs a worker pool, aggregates OriginCounts,
// formats a completion message, and saves progress to Redis at key points.
func processSeasonGroup(ctx workflow.Context, tracker *shared.ReportTracker, seasons []nhl.SeasonInfo, concurrency int,
	cfg SeasonGroupConfig, startWork shared.ActivityStarter) (core.OriginCounts, error) {

	// Clean up stale child progress reports from previous runs
	if cfg.SourceKeyFunc != nil {
		cleanupStaleChildReports(ctx, seasons, cfg.SourceKeyFunc)
	}

	if _, err := tracker.AddBarsForSeasons(ctx, cfg.GroupIdx, seasons, cfg.Counter, cfg.SourceKeyFunc); err != nil {
		return core.OriginCounts{}, err
	}
	tracker.StartGroup(ctx, cfg.GroupIdx)

	counts := core.OriginCounts{}
	err := tracker.RunWorkerPoolMultiBar(ctx, cfg.GroupIdx, 0, len(seasons), concurrency, startWork,
		func(ctx workflow.Context, i int, f workflow.Future) error {
			var seasonCounts core.OriginCounts
			if err := f.Get(ctx, &seasonCounts); err != nil {
				return err
			}
			counts.Add(seasonCounts)
			return nil
		})
	if err != nil {
		return counts, err
	}

	// Complete the group if a label is configured (direct callers like ExtractBoxscorePlayers).
	// iterateSeasons leaves GroupLabel empty and handles completion itself.
	if cfg.GroupLabel != "" {
		msg := fmt.Sprintf("%s %d seasons in %s.", cfg.GroupLabel, len(seasons), tracker.GetElapsed(ctx, cfg.GroupIdx))
		if summary := counts.Summary(cfg.CountLabel); summary != "" {
			msg += " " + summary
		}
		tracker.CompleteGroup(ctx, cfg.GroupIdx, msg)
	}
	return counts, nil
}

// CompletionMsgFunc builds the completion message for a season group.
// n is the number of seasons processed, elapsed is the formatted duration,
// and counts is the aggregated origin counts from all children.
type CompletionMsgFunc func(n int, elapsed string, counts core.OriginCounts) string

// iterateSeasons sets up a tracker, loads the seasons manifest, and delegates
// to processSeasonGroup for concurrent child workflow execution.
// If completionMsg is nil, a default message is used.
func iterateSeasons(ctx workflow.Context, input *model.SeasonsInput, progReport *shared.ProgressReport, groupIdx int,
	counter shared.SeasonCounterFunc, sourceKeyFunc shared.ProgressSourceKeyFunc,
	completionMsg CompletionMsgFunc, starter shared.ChildWorkflowStarter) error {
	logger := workflow.GetLogger(ctx)

	concurrency, err := shared.SnapshotConfigInt(ctx, logger, shared.SeasonConcurrencyParam, input.SeasonConcurrency)
	if err != nil {
		return err
	}

	tracker, err := shared.InitTracker(ctx, progReport)
	if err != nil {
		return err
	}

	logger.Info("iterateSeasons started",
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

	counts, err := processSeasonGroup(ctx, tracker, seasons, concurrency, SeasonGroupConfig{
		GroupIdx:      groupIdx,
		Counter:       counter,
		SourceKeyFunc: sourceKeyFunc,
	}, func(ctx workflow.Context, i int) workflow.Future {
		return starter(ctx, seasons[i], i == len(seasons)-1)
	})
	if err != nil {
		return err
	}

	// Build and save completion message
	elapsed := tracker.GetElapsed(ctx, groupIdx)
	var msg string
	if completionMsg != nil {
		msg = completionMsg(len(seasons), elapsed, counts)
	} else {
		msg = fmt.Sprintf("Processed %d seasons in %s.", len(seasons), elapsed)
		msg = counts.AppendSummary(msg, "origins")
	}
	tracker.CompleteGroup(ctx, groupIdx, msg)
	return nil
}

// loadSeasonsManifest fetches the seasons manifest from the NHL API via activity.
func loadSeasonsManifest(ctx workflow.Context, logger log.Logger, input *model.SeasonsInput) ([]nhl.SeasonInfo, error) {
	logger.Info("Fetching seasons manifest")
	var result worknhl.FetchSeasonsManifestResult
	if err := workflow.ExecuteActivity(ctx,
		((*worknhl.SeasonsActivities)(nil)).FetchSeasonsManifest,
		input).Get(ctx, &result); err != nil {
		return nil, err
	}
	logger.Info("Seasons manifest loaded", "count", len(result.Seasons))
	return result.Seasons, nil
}

// cleanupStaleChildReports deletes progress report keys from previous runs
// so stale data doesn't pollute the current display.
func cleanupStaleChildReports(ctx workflow.Context, seasons []nhl.SeasonInfo, sourceKeyFunc shared.ProgressSourceKeyFunc) {
	staleIDs := make([]string, len(seasons))
	for i, s := range seasons {
		staleIDs[i] = sourceKeyFunc(s.ID.StartYear())
	}
	localCtx := workflow.WithLocalActivityOptions(ctx, workflow.LocalActivityOptions{
		ScheduleToCloseTimeout: 5 * time.Second,
	})
	_ = workflow.ExecuteLocalActivity(localCtx, ((*shared.ProgressActivities)(nil)).DeleteBatch, staleIDs).Get(ctx, nil)
}
