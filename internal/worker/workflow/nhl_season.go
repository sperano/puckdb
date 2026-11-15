package workflow

import (
	"fmt"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/core"
	worknhl "github.com/sperano/puckdb/internal/worker/nhl"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/sperano/puckdb/internal/worker/yahoo"
	"go.temporal.io/sdk/workflow"
)

// Progress groups of both NHL season workflows (FetchNHLSeasonWorkflow and
// ImportNHLSeasonWorkflow export them under their own names).
const (
	groupSeasonDays    = 0
	groupSeasonPlayoff = 1
)

// seasonDaysMode is everything FetchNHLSeasonWorkflow and
// ImportNHLSeasonWorkflow differ in; runSeasonDays runs the sequence they
// share. Every field keeps the command sequence its histories recorded.
type seasonDaysMode struct {
	sync seasonSyncMode
	// startDaysFirst starts the day group before the season activities, as
	// fetch histories recorded it; import starts it after them. The Save
	// local activity it runs is part of the history, so moving it fails
	// replay.
	startDaysFirst bool
	report         func(workflow.Context, nhl.SeasonInfo) *shared.ProgressReport
	season         seasonActivities
	// planDays is called once, after the day group started and the day
	// count was computed (from workflow.Now, so after the season
	// activities); it may call GetVersion and fills result from its handler.
	planDays func(ctx workflow.Context, plan seasonDaysPlan, result *seasonDaysResult) dayLoop
	playoff  playoffMode
}

// seasonActivities are the season-level activities, run in order before
// the day loop; both take the season's start year.
type seasonActivities struct {
	rosters   any // takes a worknhl.FetchSeasonRostersInput
	clubStats any // takes a worknhl.FetchClubStatsInput
}

// seasonDaysPlan is what both day loops derive from the season and config.
type seasonDaysPlan struct {
	season    nhl.SeasonInfo
	startDate time.Time
	numDays   int
	teamIDs   []yahoo.TeamInfo
}

// day returns the date of day i of the plan.
func (p seasonDaysPlan) day(i int) time.Time { return p.startDate.AddDate(0, 0, i) }

// dayLoop is one mode's day activity: start schedules day i, handle
// collects its result.
type dayLoop struct {
	start  func(workflow.Context, int) workflow.Future
	handle func(workflow.Context, int, workflow.Future) error
}

// playoffMode is one mode's per-team playoff activity.
type playoffMode struct {
	activity any
	input    func(startYear int, team string) any
	decode   func(workflow.Context, workflow.Future) (playoffTotals, error)
	message  func(totals playoffTotals, season nhl.SeasonInfo, elapsed string) string
}

// playoffTotals sums the playoff results of a season; fetch fills only
// games and origins.
type playoffTotals struct {
	origins                 core.OriginCounts
	games, skaters, goalies int
}

func (t *playoffTotals) add(other playoffTotals) {
	t.origins.Add(other.origins)
	t.games += other.games
	t.skaters += other.skaters
	t.goalies += other.goalies
}

// seasonDaysResult totals the day loop of an NHL season workflow.
type seasonDaysResult struct {
	origins core.OriginCounts
	// skipped counts days imported before from unchanged files (import only).
	skipped int
	// partial counts imported days that skipped rows after errors; they
	// stay unrecorded and are imported again by the next run (import only).
	partial int
}

// runSeasonDays runs one NHL season in the given mode: the season
// activities, every day from the season's start to yesterday (or the
// season's end) DayConcurrency days at a time, then one playoff activity per
// team. The result counts the day origins and the playoff origins.
func runSeasonDays(ctx workflow.Context, season nhl.SeasonInfo, mode seasonDaysMode) (core.OriginCounts, error) {
	logger := workflow.GetLogger(ctx)
	logger.Info("NHL season started",
		"start_year", season.ID.StartYear(),
		"start_date", season.StandingsStart.Format(config.DateFormat),
		"end_date", season.StandingsEnd.Format(config.DateFormat))

	cfg, err := snapshotSeasonConfig(ctx)
	if err != nil {
		return nil, err
	}
	tracker, err := shared.InitTracker(ctx, mode.report(ctx, season))
	if err != nil {
		return nil, err
	}
	if mode.startDaysFirst {
		tracker.StartGroup(ctx, groupSeasonDays)
	}

	ctx = workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())
	if err := runSeasonActivities(ctx, season, mode); err != nil {
		return nil, err
	}
	days, err := runDayLoop(ctx, tracker, season, cfg, mode)
	if err != nil {
		return nil, err
	}
	playoffs, err := runPlayoffs(ctx, tracker, season, cfg.DayConcurrency, mode)
	if err != nil {
		return nil, err
	}
	days.origins.Add(playoffs.origins)

	logger.Info("NHL season completed",
		"start_year", season.ID.StartYear(),
		"playoff_games", playoffs.games)
	return days.origins, nil
}

func runSeasonActivities(ctx workflow.Context, season nhl.SeasonInfo, mode seasonDaysMode) error {
	action := seasonSyncAction(mode.sync)
	startYear := season.ID.StartYear()
	rosterInput := worknhl.FetchSeasonRostersInput{Season: startYear}
	if err := workflow.ExecuteActivity(ctx, mode.season.rosters, rosterInput).Get(ctx, nil); err != nil {
		return fmt.Errorf("%s season rosters: %w", action, err)
	}
	clubStatsInput := worknhl.FetchClubStatsInput{Season: startYear}
	if err := workflow.ExecuteActivity(ctx, mode.season.clubStats, clubStatsInput).Get(ctx, nil); err != nil {
		return fmt.Errorf("%s club stats: %w", action, err)
	}
	return nil
}

// runDayLoop runs the mode's day activity for every day of the season. Per-day
// Yahoo team IDs come from the season's snapshotted Yahoo config; no Yahoo
// activities run here (FetchYahooSeasonWorkflow / ImportYahooSeasonWorkflow
// handle league and team metadata).
func runDayLoop(ctx workflow.Context, tracker *shared.ReportTracker, season nhl.SeasonInfo,
	cfg seasonConfig, mode seasonDaysMode) (seasonDaysResult, error) {
	if !mode.startDaysFirst {
		tracker.StartGroup(ctx, groupSeasonDays)
	}
	yahooCfg, _ := cfg.Yahoo.Season(season.ID.StartYear())
	plan := seasonDaysPlan{
		season:    season,
		startDate: season.StandingsStart.Time,
		numDays:   core.CountDays(season.StandingsStart.Time, shared.EffectiveEndDate(ctx, season.StandingsEnd.Time)),
		teamIDs:   yahooTeamIDsForSeason(yahooCfg),
	}
	workflow.GetLogger(ctx).Info("Processing days in parallel",
		"num_days", plan.numDays,
		"concurrency", cfg.DayConcurrency)

	result := seasonDaysResult{origins: core.OriginCounts{}}
	loop := mode.planDays(ctx, plan, &result)
	err := tracker.RunWorkerPool(ctx, shared.PoolOptions{
		Total: plan.numDays, Concurrency: cfg.DayConcurrency, SaveEvery: dayLoopProgressSaveEvery(ctx),
		Progress: shared.SingleBar{Bar: shared.BarRef{Group: groupSeasonDays}},
		Start:    loop.start,
		Handle:   loop.handle,
	})
	if err != nil {
		return result, err
	}

	tracker.CompleteGroup(ctx, groupSeasonDays, daysCompletionMessage(seasonSyncPastVerb(mode.sync),
		plan.numDays, result, season, tracker.GetElapsed(ctx, groupSeasonDays)))
	return result, nil
}

// runPlayoffs runs the mode's playoff activity after the day loop: one
// activity per team, each covering the team's home playoff games, so the bar
// ticks per team.
func runPlayoffs(ctx workflow.Context, tracker *shared.ReportTracker, season nhl.SeasonInfo,
	concurrency int, mode seasonDaysMode) (playoffTotals, error) {
	tracker.StartGroup(ctx, groupSeasonPlayoff)

	startYear := season.ID.StartYear()
	totals := playoffTotals{origins: core.OriginCounts{}}
	var pa *worknhl.PlayoffActivities
	var teams []string
	if err := workflow.ExecuteActivity(ctx, pa.ListSeasonTeams, startYear).Get(ctx, &teams); err != nil {
		return totals, fmt.Errorf("list season teams: %w", err)
	}
	tracker.ResizeBar(ctx, shared.BarRef{Group: groupSeasonPlayoff}, len(teams))

	action := seasonSyncAction(mode.sync)
	err := tracker.RunWorkerPool(ctx, shared.PoolOptions{
		Total: len(teams), Concurrency: concurrency,
		Progress: shared.SingleBar{Bar: shared.BarRef{Group: groupSeasonPlayoff}},
		Start: func(_ workflow.Context, i int) workflow.Future {
			return workflow.ExecuteActivity(ctx, mode.playoff.activity, mode.playoff.input(startYear, teams[i]))
		},
		Handle: func(ctx workflow.Context, i int, f workflow.Future) error {
			team, err := mode.playoff.decode(ctx, f)
			if err != nil {
				return fmt.Errorf("%s playoff games for %s: %w", action, teams[i], err)
			}
			totals.add(team)
			return nil
		},
	})
	if err != nil {
		return totals, err
	}

	tracker.CompleteGroup(ctx, groupSeasonPlayoff,
		mode.playoff.message(totals, season, tracker.GetElapsed(ctx, groupSeasonPlayoff)))
	return totals, nil
}

// seasonSyncAction is the lower-case verb of the mode's error messages.
func seasonSyncAction(mode seasonSyncMode) string {
	if mode == seasonSyncImport {
		return "import"
	}
	return "fetch"
}

func daysCompletionMessage(pastVerb string, numDays int, result seasonDaysResult, season nhl.SeasonInfo,
	elapsed string) string {
	var msg string
	if result.skipped == 0 {
		msg = fmt.Sprintf("%s %d days for %s in %s", pastVerb, numDays, season.Label(), elapsed)
	} else {
		msg = fmt.Sprintf("%s %d of %d days for %s in %s; %d unchanged since their last import",
			pastVerb, numDays-result.skipped, numDays, season.Label(), elapsed, result.skipped)
	}
	if result.partial > 0 {
		msg += fmt.Sprintf("; %d with skipped rows, retried next run", result.partial)
	}
	return msg + "."
}
