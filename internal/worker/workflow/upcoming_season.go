package workflow

import (
	"fmt"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/graph/model"
	worknhl "github.com/sperano/puckdb/internal/worker/nhl"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/workflow"
)

// processUpcomingSeason fetches or imports the rosters of the NHL season
// that has not started yet (see worknhl.FetchUpcomingSeasonRosters), so the
// draft helper sees offseason moves before puck drop.
func processUpcomingSeason(ctx workflow.Context, tracker *shared.ReportTracker, input *model.SeasonsInput,
	rangeLabel string, mode seasonSyncMode) (int, error) {
	tracker.SetBarTotal(groupUpcomingSeason, 0, 1)
	tracker.RecalcTotal()
	tracker.StartGroup(ctx, groupUpcomingSeason)
	var sa *worknhl.SeasonsActivities
	activityFn := sa.FetchUpcomingSeasonRosters
	if mode == seasonSyncImport {
		activityFn = sa.ImportUpcomingSeasonRosters
	}
	var result worknhl.UpcomingSeasonRostersResult
	if err := workflow.ExecuteActivity(ctx, activityFn, input).Get(ctx, &result); err != nil {
		tracker.SetMessage(ctx, fmt.Sprintf("Upcoming NHL season rosters failed for %s: %v", rangeLabel, err))
		return seasonYearUnset, err
	}
	tracker.IncrementBar(ctx, groupUpcomingSeason, 0)
	tracker.CompleteGroup(ctx, groupUpcomingSeason,
		upcomingSeasonMessage(result, mode, tracker.GetElapsed(ctx, groupUpcomingSeason), rangeLabel))
	return result.Season, nil
}

func upcomingSeasonMessage(result worknhl.UpcomingSeasonRostersResult, mode seasonSyncMode, elapsed, rangeLabel string) string {
	if result.Season == seasonYearUnset {
		return fmt.Sprintf("Skipped upcoming NHL season: none in %s has yet to start.", rangeLabel)
	}
	label := nhl.SeasonInfo{ID: nhl.NewSeason(result.Season)}.Label()
	prior := nhl.SeasonInfo{ID: nhl.NewSeason(result.Season - 1)}.Label()
	return fmt.Sprintf("%s %s rosters for %d of %d clubs carried forward from %s in %s.",
		seasonSyncPastVerb(mode), label, result.TeamsWithRosters, result.Teams, prior, elapsed)
}
