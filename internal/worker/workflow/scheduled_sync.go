package workflow

import (
	"errors"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/sperano/puckdb/internal/worker/shared"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// ScheduledSyncResult is what a scheduled sync did: the season it synced,
// the steps it started (by workflow ID, in order) and, when it stopped
// because one was already running, that step.
type ScheduledSyncResult struct {
	Season int
	Ran    []string
	// SkippedRunning is the workflow ID of the step found already running
	// (a manual sync); the run started no step after it.
	SkippedRunning string
}

// syncStep is one step of a scheduled sync: a top-level workflow started as
// a child under the workflow ID a manual sync uses.
type syncStep struct {
	id       string
	workflow any
	input    any
}

// ScheduledSeasonsSyncWorkflow is the daily `puckdb sync seasons --season
// <current>`: FetchSeasonsWorkflow, then ImportSeasonsWorkflow, for the
// season current at the run's start. The import also runs after a failed
// fetch, so what the fetch cached is imported (a day's NHL files are cached
// before its Yahoo files, and a day missing a Yahoo file is imported again
// once it appears); the run then fails with the fetch error. A step already
// running ends the run as skipped (see runSyncStep).
func ScheduledSeasonsSyncWorkflow(ctx workflow.Context) (ScheduledSyncResult, error) {
	season := currentSeasonStart(ctx)
	input := currentSeasonInput(season)
	result := ScheduledSyncResult{Season: season}
	fetchErr := runSyncStep(ctx, &result, syncStep{shared.WorkflowIDFetchSeasons, FetchSeasonsWorkflow, input})
	if result.SkippedRunning != "" || stopsScheduledSync(ctx, fetchErr) {
		return result, fetchErr
	}
	importErr := runSyncStep(ctx, &result, syncStep{shared.WorkflowIDImportSeasons, ImportSeasonsWorkflow, input})
	return result, errors.Join(fetchErr, importErr)
}

// ScheduledPlayersSyncWorkflow is the weekly `puckdb sync players --season
// <current>`: the Yahoo game player lists, the current season's boxscore
// players, their landings and game logs, player matching and the game log
// import, one after the other in the sync command's order. The first failed
// step fails the run, as in the sync command; a step already running ends it
// as skipped (see runSyncStep).
func ScheduledPlayersSyncWorkflow(ctx workflow.Context) (ScheduledSyncResult, error) {
	season := currentSeasonStart(ctx)
	result := ScheduledSyncResult{Season: season}
	for _, step := range playersSyncSteps(currentSeasonInput(season)) {
		if err := runSyncStep(ctx, &result, step); err != nil || result.SkippedRunning != "" {
			return result, err
		}
	}
	return result, nil
}

// playersSyncSteps are the players group's steps with the inputs the sync
// command sends for them.
func playersSyncSteps(input *model.SeasonsInput) []syncStep {
	return []syncStep{
		{WorkflowIDFetchYahooPlayers, FetchYahooPlayersWorkflow, (*FetchYahooPlayersInput)(nil)},
		{WorkflowIDExtractBoxscorePlayers, ExtractBoxscorePlayersWorkflow, input},
		{WorkflowIDFetchPlayerLandings, FetchPlayerLandingsWorkflow, &FetchPlayerLandingsInput{}},
		{shared.WorkflowIDFetchPlayerLogs, FetchPlayerLogsWorkflow, input},
		{WorkflowIDProcessPlayers, ProcessPlayersWorkflow, &ProcessPlayersInput{}},
		{shared.WorkflowIDImportPlayerLogs, ImportPlayerLogsWorkflow, input},
	}
}

// currentSeasonInput selects one season, as `--season <season>` does.
func currentSeasonInput(season int) *model.SeasonsInput {
	return &model.SeasonsInput{StartSeason: &season, EndSeason: &season}
}

// runSyncStep runs step as a child workflow under its fixed ID and waits for
// it. A child of that ID already running (a manual sync, or a step a manual
// sync started) is not an error: it is recorded in
// result.SkippedRunning, and the caller starts nothing after it, since that
// sync runs the following steps itself. The fixed IDs are also what makes
// a manual sync and the GraphQL progress queries see a scheduled step.
func runSyncStep(ctx workflow.Context, result *ScheduledSyncResult, step syncStep) error {
	childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID:          step.id,
		WorkflowTaskTimeout: config.DefaultWorkflowTaskTimeout,
		// Cancelling the scheduled run cancels the step and waits for it,
		// so its activities stop before the run reports cancelled.
		ParentClosePolicy:   enumspb.PARENT_CLOSE_POLICY_REQUEST_CANCEL,
		WaitForCancellation: true,
	})
	err := workflow.ExecuteChildWorkflow(childCtx, step.workflow, step.input).Get(ctx, nil)
	if temporal.IsWorkflowExecutionAlreadyStartedError(err) {
		workflow.GetLogger(ctx).Info("Sync step already running, skipping the rest of the scheduled sync",
			"workflow_id", step.id)
		result.SkippedRunning = step.id
		return nil
	}
	result.Ran = append(result.Ran, step.id)
	return err
}

// stopsScheduledSync reports whether err (a step's) or the run's own
// cancellation ends the run before its next step.
func stopsScheduledSync(ctx workflow.Context, err error) bool {
	return ctx.Err() != nil || temporal.IsCanceledError(err)
}
