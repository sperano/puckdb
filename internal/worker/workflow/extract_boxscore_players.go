package workflow

import (
	"fmt"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/graph/model"
	worknhl "github.com/sperano/puckdb/internal/worker/nhl"
	workplayer "github.com/sperano/puckdb/internal/worker/player"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/workflow"
)

const (
	// WorkflowIDExtractBoxscorePlayers is the ID for the extract boxscore players workflow.
	WorkflowIDExtractBoxscorePlayers = "extract-boxscore-players"

	// GroupExtractBoxscorePlayers is the group index for extraction progress tracking.
	GroupExtractBoxscorePlayers = 0

	// GroupConsolidatePlayers is the group index for consolidation progress tracking.
	GroupConsolidatePlayers = 1
)

// WorkflowIDExtractSeason returns the workflow ID for a single season extraction.
// Used as Redis key prefix for progress tracking.
func WorkflowIDExtractSeason(startYear int) string {
	return fmt.Sprintf("extract-season-%d", startYear)
}

// NewExtractBoxscorePlayersProgressReport creates the initial progress structure.
// Bars are added dynamically once seasons are known.
func NewExtractBoxscorePlayersProgressReport() *shared.ProgressReport {
	return &shared.ProgressReport{
		Groups: []shared.ProgressGroup{
			{Header: "Extracting boxscore players...", Bars: []shared.ProgressBar{}},
			{Header: "Consolidating players...", Bars: []shared.ProgressBar{{Label: "Merging", Total: 1}}},
		},
	}
}

// ExtractBoxscorePlayersWorkflow extracts unique players from boxscores for each season
// and stores them in Redis. This enables downstream workflows to access player data
// without re-extracting from files. Once every selected season is extracted it
// consolidates them into one set; an empty selection or a failed extraction
// consolidates nothing.
func ExtractBoxscorePlayersWorkflow(ctx workflow.Context, input *model.SeasonsInput) error {
	result, err := runSeasonFanOut(ctx, input, SeasonFanOut{
		Report:  NewExtractBoxscorePlayersProgressReport(),
		Prepare: planExtractBoxscorePlayers,
	})
	if err != nil || len(result.Seasons) == 0 {
		return err
	}
	ctx = workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())
	return consolidateBoxscorePlayers(ctx, result.Tracker, result.Seasons)
}

// planExtractBoxscorePlayers runs one ExtractAndSaveBoxscorePlayers activity
// per season, its bar sized by the season's days.
func planExtractBoxscorePlayers(_ workflow.Context, _ SeasonFanOutState) (SeasonFanOutPlan, error) {
	var ba *worknhl.BoxscoreActivities
	return SeasonFanOutPlan{
		Group: SeasonGroupConfig{
			GroupIdx:      GroupExtractBoxscorePlayers,
			Counter:       countExtractSeasonDays,
			SourceKeyFunc: WorkflowIDExtractSeason,
			GroupLabel:    "Extracted players for",
			CountLabel:    "boxscore reads",
		},
		Start: func(ctx workflow.Context, season nhl.SeasonInfo, _ int, _ bool) workflow.Future {
			return workflow.ExecuteActivity(ctx, ba.ExtractAndSaveBoxscorePlayers,
				worknhl.ExtractAndSaveInput{
					Season:  season,
					EndDate: shared.EffectiveEndDate(ctx, season.StandingsEnd.Time),
				})
		},
	}, nil
}

// countExtractSeasonDays sizes a season's extraction bar.
func countExtractSeasonDays(ctx workflow.Context, season nhl.SeasonInfo) (int, error) {
	return shared.CountDaysInSeason(ctx, season), nil
}

// consolidateBoxscorePlayers merges the extracted players of every season
// into one unique set in the consolidation group.
func consolidateBoxscorePlayers(ctx workflow.Context, tracker *shared.ReportTracker, seasons []nhl.SeasonInfo) error {
	tracker.StartGroup(ctx, GroupConsolidatePlayers)

	seasonIDs := make([]nhl.Season, len(seasons))
	for i, s := range seasons {
		seasonIDs[i] = s.ID
	}

	var pa *workplayer.Activities
	var consolidateResult workplayer.ConsolidatePlayersResult
	consolidateInput := workplayer.ConsolidatePlayersInput{Seasons: seasonIDs}
	if err := workflow.ExecuteActivity(ctx, pa.ConsolidateBoxscorePlayersActivity, consolidateInput).Get(ctx, &consolidateResult); err != nil {
		return err
	}

	tracker.IncrementBar(ctx, shared.BarRef{Group: GroupConsolidatePlayers})
	tracker.CompleteGroup(ctx, GroupConsolidatePlayers,
		fmt.Sprintf("Consolidated %d unique players from %d total in %s.",
			consolidateResult.UniquePlayers, consolidateResult.TotalPlayers,
			tracker.GetElapsed(ctx, GroupConsolidatePlayers)))

	workflow.GetLogger(ctx).Info("ExtractBoxscorePlayersWorkflow completed",
		"seasons", len(seasons),
		"unique_players", consolidateResult.UniquePlayers)
	return nil
}
