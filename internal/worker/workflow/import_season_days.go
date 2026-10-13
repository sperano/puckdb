package workflow

import (
	"fmt"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/core"
	worknhl "github.com/sperano/puckdb/internal/worker/nhl"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/workflow"
)

// importDaySkipChangeID gates scheduling ImportDayIfChanged instead of
// ImportDay for each day. Histories recorded before it carry no marker and
// replay the unconditional ImportDay.
const (
	importDaySkipChangeID = "import-nhl-day-skip-unchanged"
	importDaySkipVersion  = 1
)

// seasonDaysResult totals the day loop of ImportNHLSeasonWorkflow.
type seasonDaysResult struct {
	origins core.OriginCounts
	skipped int
}

// importSeasonDays imports every day from the season's start to yesterday
// (or the season's end), DayConcurrency days at a time. Per-day Yahoo team
// IDs come from the season's snapshotted Yahoo config. No Yahoo activities
// run here: league/team metadata import is ImportYahooSeasonWorkflow's job,
// and must complete before this season's day loop starts (day-level roster
// rows FK to yahoo_teams).
func importSeasonDays(ctx workflow.Context, tracker *shared.ReportTracker, season nhl.SeasonInfo,
	cfg seasonConfig, opts ImportNHLSeasonOptions) (core.OriginCounts, error) {
	tracker.StartGroup(ctx, GroupImportDays)

	yahooCfg, _ := cfg.Yahoo.Season(season.ID.StartYear())
	teamIDs := yahooTeamIDsForSeason(yahooCfg)
	startDate := season.StandingsStart.Time
	numDays := core.CountDays(startDate, shared.EffectiveEndDate(ctx, season.StandingsEnd.Time))
	dayInput := func(i int) worknhl.ImportDayInput {
		return worknhl.ImportDayInput{
			Date:      startDate.AddDate(0, 0, i),
			Season:    season.ID.StartYear(),
			SeasonID:  season.ID.ID(),
			TeamIDs:   teamIDs,
			TotalDays: numDays,
		}
	}

	var ia *worknhl.ImportActivities
	result := seasonDaysResult{origins: core.OriginCounts{}}
	skipUnchanged := workflow.GetVersion(ctx, importDaySkipChangeID, workflow.DefaultVersion, importDaySkipVersion) >= importDaySkipVersion
	var err error
	if skipUnchanged {
		err = tracker.RunWorkerPool(ctx, GroupImportDays, 0, numDays, cfg.DayConcurrency,
			func(_ workflow.Context, i int) workflow.Future {
				input := worknhl.ImportDayIfChangedInput{ImportDayInput: dayInput(i), Force: opts.ReimportDays}
				return workflow.ExecuteActivity(ctx, ia.ImportDayIfChanged, input)
			}, result.collect)
	} else {
		err = tracker.RunWorkerPool(ctx, GroupImportDays, 0, numDays, cfg.DayConcurrency,
			func(_ workflow.Context, i int) workflow.Future {
				return workflow.ExecuteActivity(ctx, ia.ImportDay, dayInput(i))
			}, shared.AggregateInto[core.OriginCounts](result.origins))
	}
	if err != nil {
		return nil, err
	}

	tracker.CompleteGroup(ctx, GroupImportDays,
		daysCompletionMessage(numDays, result.skipped, season, tracker.GetElapsed(ctx, GroupImportDays)))
	return result.origins, nil
}

// collect is the RunWorkerPool handler for ImportDayIfChanged results.
func (r *seasonDaysResult) collect(ctx workflow.Context, _ int, f workflow.Future) error {
	var day worknhl.ImportDayIfChangedResult
	if err := f.Get(ctx, &day); err != nil {
		return err
	}
	r.origins.Add(day.Origins)
	if day.Skipped {
		r.skipped++
	}
	return nil
}

func daysCompletionMessage(numDays, skipped int, season nhl.SeasonInfo, elapsed string) string {
	if skipped == 0 {
		return fmt.Sprintf("Imported %d days for %s in %s.", numDays, season.Label(), elapsed)
	}
	return fmt.Sprintf("Imported %d of %d days for %s in %s; %d unchanged since their last import.",
		numDays-skipped, numDays, season.Label(), elapsed, skipped)
}
