package workflow

import (
	"fmt"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/workflow"
)

const (
	// yahooChildGroup and yahooChildBar locate the single bar a Yahoo season
	// child workflow advances, one unit per workflow-level step.
	yahooChildGroup = 0
	yahooChildBar   = 0

	// yahooLeagueAPIStepCount is the extra steps an API-backed league adds on
	// top of its metadata step: league data (transactions, draft results,
	// matchups) and the player pool. Stand-in leagues only have the metadata step.
	yahooLeagueAPIStepCount = 2

	// yahooTeamsBatchStepCount is the single batched teams step, present when
	// any API-backed league lists teams.
	yahooTeamsBatchStepCount = 1
)

// yahooStepFunc records one completed workflow-level step of a Yahoo season sync.
type yahooStepFunc func(ctx workflow.Context)

// yahooSeasonProgress advances the child workflow's own progress report.
type yahooSeasonProgress struct {
	tracker *shared.ReportTracker
}

func (p yahooSeasonProgress) advance(ctx workflow.Context) {
	p.tracker.IncrementBar(ctx, yahooChildGroup, yahooChildBar)
}

func (p yahooSeasonProgress) complete(ctx workflow.Context) {
	p.tracker.CompleteGroup(ctx, yahooChildGroup,
		fmt.Sprintf("Done in %s.", p.tracker.GetElapsed(ctx, yahooChildGroup)))
}

// startYahooSeasonProgress registers the child's progress query handler and
// starts its only group.
func startYahooSeasonProgress(ctx workflow.Context, input YahooSeasonWorkflowInput,
	mode seasonSyncMode) (yahooSeasonProgress, error) {
	tracker, err := shared.InitTracker(ctx, newYahooSeasonProgressReport(input, mode))
	if err != nil {
		return yahooSeasonProgress{}, err
	}
	tracker.StartGroup(ctx, yahooChildGroup)
	return yahooSeasonProgress{tracker: tracker}, nil
}

func newYahooSeasonProgressReport(input YahooSeasonWorkflowInput, mode seasonSyncMode) *shared.ProgressReport {
	total := yahooSeasonStepTotal(input.Season)
	return &shared.ProgressReport{
		Total: total,
		Groups: []shared.ProgressGroup{{
			Header: fmt.Sprintf("%s Yahoo %d metadata...", seasonSyncVerb(mode), input.StartYear),
			Bars:   []shared.ProgressBar{{Total: total}},
		}},
	}
}

// yahooSeasonStepTotal counts the workflow-level steps of a Yahoo season sync
// from its configuration alone: one metadata step per league, the batched
// teams step, and league data plus player pool per API-backed league. The
// parent sizes its bar with the same function, so the two always agree.
func yahooSeasonStepTotal(season config.Season) int {
	total := 0
	hasTeams := false
	for _, league := range season.Leagues {
		total++
		if league.UsesTemporaryMetadata() {
			continue
		}
		total += yahooLeagueAPIStepCount
		hasTeams = hasTeams || len(league.TeamIDs) > 0
	}
	if hasTeams {
		total += yahooTeamsBatchStepCount
	}
	return total
}

// yahooSeasonSourceKey maps a start year to the child workflow whose progress
// report feeds that season's bar in the parent.
func yahooSeasonSourceKey(mode seasonSyncMode) shared.ProgressSourceKeyFunc {
	if mode == seasonSyncImport {
		return WorkflowIDImportYahooSeason
	}
	return WorkflowIDFetchYahooSeason
}

// addYahooSeasonBars adds one bar per Yahoo season, sized by its step total and
// linked to the child workflow whose progress the resolver merges in.
func addYahooSeasonBars(tracker *shared.ReportTracker, seasons []YahooSeasonWorkflowInput, mode seasonSyncMode) {
	sourceKey := yahooSeasonSourceKey(mode)
	for _, season := range seasons {
		tracker.AddBar(groupYahooMetadata, shared.ProgressBar{
			Label:             nhl.SeasonInfo{ID: nhl.NewSeason(season.StartYear)}.Label(),
			Total:             yahooSeasonStepTotal(season.Season),
			ProgressSourceKey: sourceKey(season.StartYear),
		})
	}
}

// cleanupStaleYahooReports drops child reports left by a previous run so the
// resolver never merges them into this run's bars.
func cleanupStaleYahooReports(ctx workflow.Context, seasons []YahooSeasonWorkflowInput, mode seasonSyncMode) {
	sourceKey := yahooSeasonSourceKey(mode)
	staleIDs := make([]string, len(seasons))
	for i, season := range seasons {
		staleIDs[i] = sourceKey(season.StartYear)
	}
	deleteStaleProgressReports(ctx, staleIDs)
}
