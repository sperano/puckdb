package workflow

import (
	"fmt"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/core"
	worknhl "github.com/sperano/puckdb/internal/worker/nhl"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/workflow"
)

// Group indices for ImportNHLSeasonWorkflow progress.
const (
	GroupImportDays    = 0
	GroupImportPlayoff = 1
)

// NewImportSeasonProgressReport creates the progress structure for a single season import.
// The playoff bar starts with Total 0; it is sized to the season's team count
// (one unit per club-schedule import) once ListSeasonTeams has run.
func NewImportSeasonProgressReport(ctx workflow.Context, season nhl.SeasonInfo) *shared.ProgressReport {
	days := shared.CountDaysInSeason(ctx, season)
	return &shared.ProgressReport{
		Total: days,
		Groups: []shared.ProgressGroup{
			{Header: fmt.Sprintf("Importing games for %s...", season.Label()), Bars: []shared.ProgressBar{{Total: days}}},
			{Header: "Importing playoff games...", Bars: []shared.ProgressBar{{Total: 0}}},
		},
	}
}

// ImportNHLSeasonOptions are the per-run choices of ImportNHLSeasonWorkflow.
// Histories recorded before the options existed decode to the zero value.
type ImportNHLSeasonOptions struct {
	// ReimportDays imports every day, even days whose cached files and
	// importer version are unchanged since their last import.
	ReimportDays bool `json:"reimportDays"`
}

// ImportNHLSeasonWorkflow imports day-level data for a single season: boxscores,
// game stories, shifts, and per-day Yahoo team data for any configured Yahoo
// leagues. Days already imported from unchanged cached files are skipped
// unless opts.ReimportDays is set. Yahoo league/team metadata itself, and
// league-level data (transactions, draft results, matchups), are imported
// independently and beforehand by ImportYahooSeasonWorkflow in the parent
// season-sync workflow.
// Player game log imports are handled separately by ImportSeasonPlayerLogsWorkflow.
func ImportNHLSeasonWorkflow(ctx workflow.Context, season nhl.SeasonInfo, opts ImportNHLSeasonOptions) (core.OriginCounts, error) {
	logger := workflow.GetLogger(ctx)

	logger.Info("ImportNHLSeasonWorkflow started",
		"startYear", season.ID.StartYear(),
		"startDate", season.StandingsStart.Format(config.DateFormat),
		"endDate", season.StandingsEnd.Format(config.DateFormat),
		"reimportDays", opts.ReimportDays)

	cfg, err := snapshotSeasonConfig(ctx)
	if err != nil {
		return nil, err
	}

	tracker, err := shared.InitTracker(ctx, NewImportSeasonProgressReport(ctx, season))
	if err != nil {
		return nil, err
	}

	ctx = workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())

	// Import season-level data (rosters, club stats)
	var sa *worknhl.SeasonsActivities
	rosterInput := worknhl.FetchSeasonRostersInput{Season: season.ID.StartYear()}
	if err := workflow.ExecuteActivity(ctx, sa.ImportSeasonRosters, rosterInput).Get(ctx, nil); err != nil {
		return nil, fmt.Errorf("import season rosters: %w", err)
	}
	clubStatsInput := worknhl.FetchClubStatsInput{Season: season.ID.StartYear()}
	if err := workflow.ExecuteActivity(ctx, sa.ImportClubStats, clubStatsInput).Get(ctx, nil); err != nil {
		return nil, fmt.Errorf("import club stats: %w", err)
	}

	counts, err := importSeasonDays(ctx, tracker, season, cfg, opts)
	if err != nil {
		return nil, err
	}

	playoffs, err := importSeasonPlayoffs(ctx, tracker, season, cfg.DayConcurrency)
	if err != nil {
		return nil, err
	}
	counts.Add(playoffs.Origins)

	logger.Info("ImportNHLSeasonWorkflow completed",
		"startYear", season.ID.StartYear(),
		"playoffGames", playoffs.GamesImported)

	return counts, nil
}

// importSeasonPlayoffs imports playoff games after the regular-season day
// loop: one activity per team, each covering the team's home playoff games,
// so the bar ticks per team.
func importSeasonPlayoffs(ctx workflow.Context, tracker *shared.ReportTracker, season nhl.SeasonInfo,
	concurrency int) (worknhl.ImportTeamPlayoffGamesResult, error) {
	tracker.StartGroup(ctx, GroupImportPlayoff)

	startYear := season.ID.StartYear()
	playoffTotals := worknhl.ImportTeamPlayoffGamesResult{Origins: core.OriginCounts{}}
	var pa *worknhl.PlayoffActivities
	var teams []string
	if err := workflow.ExecuteActivity(ctx, pa.ListSeasonTeams, startYear).Get(ctx, &teams); err != nil {
		return playoffTotals, fmt.Errorf("list season teams: %w", err)
	}
	tracker.ResizeBar(ctx, shared.BarRef{Group: GroupImportPlayoff}, len(teams))

	err := tracker.RunWorkerPool(ctx, shared.PoolOptions{
		Total: len(teams), Concurrency: concurrency,
		Progress: shared.SingleBar{Bar: shared.BarRef{Group: GroupImportPlayoff}},
		Start: func(_ workflow.Context, i int) workflow.Future {
			input := worknhl.ImportTeamPlayoffGamesInput{Season: startYear, TeamAbbrev: teams[i]}
			return workflow.ExecuteActivity(ctx, pa.ImportTeamPlayoffGames, input)
		},
		Handle: func(ctx workflow.Context, i int, f workflow.Future) error {
			var res worknhl.ImportTeamPlayoffGamesResult
			if err := f.Get(ctx, &res); err != nil {
				return fmt.Errorf("import playoff games for %s: %w", teams[i], err)
			}
			playoffTotals.Origins.Add(res.Origins)
			playoffTotals.GamesImported += res.GamesImported
			playoffTotals.SkatersImported += res.SkatersImported
			playoffTotals.GoaliesImported += res.GoaliesImported
			return nil
		},
	})
	if err != nil {
		return playoffTotals, err
	}

	tracker.CompleteGroup(ctx, GroupImportPlayoff,
		fmt.Sprintf("Imported %d playoff games (%d skaters, %d goalies) for %s in %s.",
			playoffTotals.GamesImported, playoffTotals.SkatersImported, playoffTotals.GoaliesImported,
			season.Label(), tracker.GetElapsed(ctx, GroupImportPlayoff)))
	return playoffTotals, nil
}

// WorkflowIDImportNHLSeason identifies an NHL-only season child workflow.
func WorkflowIDImportNHLSeason(startYear int) string {
	return fmt.Sprintf("import-nhl-season-%d", startYear)
}
