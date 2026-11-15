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
// extracted from boxscores. Existing files are only overwritten when the
// --refresh-current-player-logs flag is set, with the scope of
// refreshSelectedSeason.
func FetchPlayerLogsWorkflow(ctx workflow.Context, input *model.SeasonsInput) error {
	_, err := runSeasonFanOut(ctx, input, SeasonFanOut{
		Report:  NewFetchPlayerLogsProgressReport(),
		Prepare: planFetchPlayerLogs,
	})
	return err
}

// planFetchPlayerLogs sizes each season's bar by its cached boxscore
// players, counted in the Counter (after the stale report cleanup), and
// starts one FetchSeasonPlayerLogsWorkflow per season.
func planFetchPlayerLogs(_ workflow.Context, state SeasonFanOutState) (SeasonFanOutPlan, error) {
	input := state.Input
	return SeasonFanOutPlan{
		Group: SeasonGroupConfig{
			GroupIdx:      GroupFetchPlayerLogs,
			Counter:       countSeasonBoxscorePlayers,
			SourceKeyFunc: WorkflowIDFetchSeasonPlayerLogs,
			Completion:    fetchPlayerLogsCompletion,
		},
		Start: func(ctx workflow.Context, season nhl.SeasonInfo, _ int, last bool) workflow.Future {
			refresh := refreshSelectedSeason(input, input.RefreshCurrentPlayerLogs, last)
			return workflow.ExecuteChildWorkflow(
				shared.WithChildOptions(ctx, WorkflowIDFetchSeasonPlayerLogs(season.ID.StartYear())),
				FetchSeasonPlayerLogsWorkflow,
				FetchSeasonPlayerLogsInput{Season: season, RefreshCurrent: refresh})
		},
	}, nil
}

// countSeasonBoxscorePlayers is a season's bar total: the players its
// cached boxscores list.
func countSeasonBoxscorePlayers(ctx workflow.Context, season nhl.SeasonInfo) (int, error) {
	var playerAct *workplayer.Activities
	var players []store.BoxscorePlayer
	if err := workflow.ExecuteActivity(ctx, playerAct.LoadSeasonBoxscorePlayers, season.ID.StartYear()).Get(ctx, &players); err != nil {
		return 0, err
	}
	return len(players), nil
}

// fetchPlayerLogsCompletion is the player-log fetch group's completion message.
func fetchPlayerLogsCompletion(n int, elapsed string, counts core.OriginCounts) string {
	return counts.AppendSummary(fmt.Sprintf("Fetched player logs for %d seasons in %s.", n, elapsed), "player logs")
}
