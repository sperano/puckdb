package workflow

import (
	"fmt"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/sperano/puckdb/internal/store"
	workplayer "github.com/sperano/puckdb/internal/worker/player"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/workflow"
)

// GroupFetchPlayerLogs is the group index for player logs progress.
const GroupFetchPlayerLogs = 0

// NewFetchPlayerLogsProgressReport creates the initial progress structure.
// Bars are added dynamically once seasons and player counts are known.
func NewFetchPlayerLogsProgressReport() *shared.ProgressReport {
	return &shared.ProgressReport{
		Groups: []shared.ProgressGroup{
			{Header: "Fetching player logs...", Bars: []shared.ProgressBar{}},
		},
	}
}

// FetchPlayerLogsWorkflow fetches player game logs for all seasons.
// It processes each season in a child workflow, downloading game logs for players
// extracted from boxscores. For the current season, files are only overwritten
// if the --refresh-current-player-logs flag is set.
func FetchPlayerLogsWorkflow(ctx workflow.Context, input *model.SeasonsInput) error {
	ctx = workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())
	var playerAct *workplayer.Activities
	// Same refresh scoping as FetchEdgeSeasonsWorkflow: an explicit season
	// range applies the flag to every season in it; without a range it applies
	// to the latest season only. Deliberately not IsCurrentSeason — see the
	// July-1 rollover note there.
	refreshFlag := input != nil && input.RefreshCurrentPlayerLogs != nil && *input.RefreshCurrentPlayerLogs
	rangeGiven := input != nil && (input.StartSeason != nil || input.EndSeason != nil)
	return iterateSeasons(ctx, input, NewFetchPlayerLogsProgressReport(), GroupFetchPlayerLogs,
		func(ctx workflow.Context, season nhl.SeasonInfo) (int, error) {
			var players []store.BoxscorePlayer
			if err := workflow.ExecuteActivity(ctx, playerAct.LoadSeasonBoxscorePlayers, season.ID.StartYear()).Get(ctx, &players); err != nil {
				return 0, err
			}
			return len(players), nil
		},
		WorkflowIDFetchSeasonPlayerLogs,
		func(n int, elapsed string, counts core.OriginCounts) string {
			return counts.AppendSummary(fmt.Sprintf("Fetched player logs for %d seasons in %s.", n, elapsed), "player logs")
		},
		func(ctx workflow.Context, season nhl.SeasonInfo, isLatest bool) workflow.ChildWorkflowFuture {
			refresh := refreshFlag && (rangeGiven || isLatest)
			return workflow.ExecuteChildWorkflow(
				shared.WithChildOptions(ctx, WorkflowIDFetchSeasonPlayerLogs(season.ID.StartYear())),
				FetchSeasonPlayerLogsWorkflow,
				FetchSeasonPlayerLogsInput{Season: season, RefreshCurrent: refresh})
		})
}
