package workflow

import (
	"fmt"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/core"
	worknhl "github.com/sperano/puckdb/internal/worker/nhl"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/workflow"
)

// Group indices for ImportNHLSeasonWorkflow progress.
const (
	GroupImportDays    = groupSeasonDays
	GroupImportPlayoff = groupSeasonPlayoff
)

// importDaySkipChangeID gates scheduling ImportDayIfChanged instead of
// ImportDay for each day. Histories recorded before it carry no marker and
// replay the unconditional ImportDay.
const (
	importDaySkipChangeID = "import-nhl-day-skip-unchanged"
	importDaySkipVersion  = 1
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
// season-sync workflow (day-level roster rows FK to yahoo_teams).
// The result counts one origin per cached file the day and playoff imports read.
// Player game log imports are handled separately by ImportSeasonPlayerLogsWorkflow.
func ImportNHLSeasonWorkflow(ctx workflow.Context, season nhl.SeasonInfo, opts ImportNHLSeasonOptions) (core.OriginCounts, error) {
	return runSeasonDays(ctx, season, importSeasonDaysMode(opts))
}

func importSeasonDaysMode(opts ImportNHLSeasonOptions) seasonDaysMode {
	var sa *worknhl.SeasonsActivities
	var pa *worknhl.PlayoffActivities
	return seasonDaysMode{
		sync:   seasonSyncImport,
		report: NewImportSeasonProgressReport,
		season: seasonActivities{rosters: sa.ImportSeasonRosters, clubStats: sa.ImportClubStats},
		planDays: func(ctx workflow.Context, plan seasonDaysPlan, result *seasonDaysResult) dayLoop {
			return planImportDays(ctx, plan, opts, result)
		},
		playoff: playoffMode{
			activity: pa.ImportTeamPlayoffGames,
			input: func(startYear int, team string) any {
				return worknhl.ImportTeamPlayoffGamesInput{Season: startYear, TeamAbbrev: team}
			},
			decode: decodeImportPlayoffs,
			message: func(totals playoffTotals, season nhl.SeasonInfo, elapsed string) string {
				return fmt.Sprintf("Imported %d playoff games (%d skaters, %d goalies) for %s in %s.",
					totals.games, totals.skaters, totals.goalies, season.Label(), elapsed)
			},
		},
	}
}

// planImportDays schedules ImportDayIfChanged per day, or the unconditional
// ImportDay for histories recorded before the skip gate.
func planImportDays(ctx workflow.Context, plan seasonDaysPlan, opts ImportNHLSeasonOptions,
	result *seasonDaysResult) dayLoop {
	skipUnchanged := workflow.GetVersion(ctx, importDaySkipChangeID, workflow.DefaultVersion,
		importDaySkipVersion) >= importDaySkipVersion
	workflow.GetLogger(ctx).Info("Importing NHL season days",
		"reimport_days", opts.ReimportDays,
		"skip_unchanged", skipUnchanged)

	dayInput := func(i int) worknhl.ImportDayInput {
		return worknhl.ImportDayInput{
			Date:      plan.day(i),
			Season:    plan.season.ID.StartYear(),
			SeasonID:  plan.season.ID.ID(),
			TeamIDs:   plan.teamIDs,
			TotalDays: plan.numDays,
		}
	}
	var ia *worknhl.ImportActivities
	if !skipUnchanged {
		return dayLoop{
			start: func(_ workflow.Context, i int) workflow.Future {
				return workflow.ExecuteActivity(ctx, ia.ImportDay, dayInput(i))
			},
			handle: shared.AggregateInto[core.OriginCounts](result.origins),
		}
	}
	return dayLoop{
		start: func(_ workflow.Context, i int) workflow.Future {
			input := worknhl.ImportDayIfChangedInput{ImportDayInput: dayInput(i), Force: opts.ReimportDays}
			return workflow.ExecuteActivity(ctx, ia.ImportDayIfChanged, input)
		},
		handle: result.collect,
	}
}

// collect is the day handler for ImportDayIfChanged results.
func (r *seasonDaysResult) collect(ctx workflow.Context, _ int, f workflow.Future) error {
	var day worknhl.ImportDayIfChangedResult
	if err := f.Get(ctx, &day); err != nil {
		return err
	}
	r.origins.Add(day.Origins)
	if day.Skipped {
		r.skipped++
	}
	if day.SkippedRows > 0 {
		r.partial++
	}
	return nil
}

func decodeImportPlayoffs(ctx workflow.Context, f workflow.Future) (playoffTotals, error) {
	var res worknhl.ImportTeamPlayoffGamesResult
	if err := f.Get(ctx, &res); err != nil {
		return playoffTotals{}, err
	}
	return playoffTotals{origins: res.Origins, games: res.GamesImported,
		skaters: res.SkatersImported, goalies: res.GoaliesImported}, nil
}

// WorkflowIDImportNHLSeason identifies an NHL-only season child workflow.
func WorkflowIDImportNHLSeason(startYear int) string {
	return fmt.Sprintf("import-nhl-season-%d", startYear)
}
