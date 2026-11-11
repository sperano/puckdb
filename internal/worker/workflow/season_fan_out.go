package workflow

import (
	"errors"
	"fmt"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/workflow"
)

// SeasonFanOut configures runSeasonFanOut: the parent's progress report, an
// optional selection of the manifest's seasons, and the preparation that
// turns the selected seasons into the plan of the fan-out group. Report and
// Prepare are required.
type SeasonFanOut struct {
	Report *shared.ProgressReport
	// Select keeps the manifest seasons the parent works on, in order; nil
	// keeps all of them.
	Select  func([]nhl.SeasonInfo) []nhl.SeasonInfo
	Prepare SeasonFanOutPrepare
}

// SeasonFanOutState is what Prepare sees once the manifest is known: the
// normalized input, the parent's tracker, the selected seasons (never empty)
// and the snapshotted season concurrency.
type SeasonFanOutState struct {
	Input       *model.SeasonsInput
	Tracker     *shared.ReportTracker
	Seasons     []nhl.SeasonInfo
	Concurrency int
}

// SeasonFanOutPrepare runs the parent's own work between the manifest and
// the fan-out group (a scan whose results size the bars, for example) and
// returns the group's plan. Its ctx carries the default activity options.
type SeasonFanOutPrepare func(workflow.Context, SeasonFanOutState) (SeasonFanOutPlan, error)

// SeasonFanOutPlan is the fan-out group: its progress configuration and the
// work started for each selected season.
type SeasonFanOutPlan struct {
	Group SeasonGroupConfig
	Start SeasonWorkStarter
}

// SeasonWorkStarter starts the work of one selected season (a child workflow
// or an activity returning core.OriginCounts) and returns its future. index
// is the season's position in the selection and last is true for the last
// selected season, the one still accruing data, so a starter can scope
// refresh-style flags to it without consulting the calendar.
type SeasonWorkStarter func(ctx workflow.Context, season nhl.SeasonInfo, index int, last bool) workflow.Future

// SeasonFanOutResult is what runSeasonFanOut leaves to the parent: the
// normalized input, the tracker, the selected seasons and the aggregated
// counts of every season's work, so follow-on phases stay explicit parent
// code.
type SeasonFanOutResult struct {
	Input   *model.SeasonsInput
	Tracker *shared.ReportTracker
	Seasons []nhl.SeasonInfo
	Counts  core.OriginCounts
}

var (
	errSeasonFanOutNoReport  = errors.New("season fan-out: no progress report")
	errSeasonFanOutNoPrepare = errors.New("season fan-out: no Prepare")
	errSeasonFanOutNoStart   = errors.New("season fan-out: plan has no Start")
)

// runSeasonFanOut runs the outer lifecycle shared by the season fan-out
// parents: it normalizes an omitted input (all configured seasons, configured
// concurrency), snapshots the season concurrency, registers the tracker,
// installs the default activity options, loads the seasons manifest and
// applies Select. An empty selection is a successful no-op: Prepare, stale
// report cleanup, bars and the group never run. Otherwise it calls Prepare
// once and runs the plan through processSeasonGroup. The options are
// validated before the first command, the plan before the group's first one.
func runSeasonFanOut(ctx workflow.Context, input *model.SeasonsInput, opts SeasonFanOut) (SeasonFanOutResult, error) {
	if err := opts.validate(); err != nil {
		return SeasonFanOutResult{}, err
	}
	ctx = workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())
	result, concurrency, err := startSeasonFanOut(ctx, input, opts)
	if err != nil || len(result.Seasons) == 0 {
		return result, err
	}
	plan, err := opts.Prepare(ctx, SeasonFanOutState{
		Input: result.Input, Tracker: result.Tracker, Seasons: result.Seasons, Concurrency: concurrency,
	})
	if err != nil {
		return result, err
	}
	if err := plan.validate(len(opts.Report.Groups)); err != nil {
		return result, err
	}
	seasons := result.Seasons
	result.Counts, err = processSeasonGroup(ctx, result.Tracker, seasons, concurrency, plan.Group,
		func(ctx workflow.Context, i int) workflow.Future {
			return plan.Start(ctx, seasons[i], i, i == len(seasons)-1)
		})
	return result, err
}

// startSeasonFanOut runs everything up to the selection and returns the
// result with the selected seasons (and empty counts) and the snapshotted
// concurrency.
func startSeasonFanOut(ctx workflow.Context, input *model.SeasonsInput, opts SeasonFanOut) (SeasonFanOutResult, int, error) {
	input = normalizeSeasonsInput(input)
	result := SeasonFanOutResult{Input: input, Counts: core.OriginCounts{}}
	logger := workflow.GetLogger(ctx)
	concurrency, err := shared.SnapshotConfigInt(ctx, logger, shared.SeasonConcurrencyParam, input.SeasonConcurrency)
	if err != nil {
		return result, 0, err
	}
	if result.Tracker, err = shared.InitTracker(ctx, opts.Report); err != nil {
		return result, 0, err
	}
	logger.Info("Season fan-out started",
		"start_season", input.StartSeason,
		"end_season", input.EndSeason,
		"concurrency", concurrency)

	seasons, err := loadSeasonsManifest(ctx, logger, input)
	if err != nil {
		return result, 0, err
	}
	if opts.Select != nil {
		seasons = opts.Select(seasons)
	}
	result.Seasons = seasons
	if len(seasons) == 0 {
		logger.Info("No seasons to process")
	}
	return result, concurrency, nil
}

// validate checks the options runSeasonFanOut needs before its first command.
func (o SeasonFanOut) validate() error {
	if o.Report == nil {
		return errSeasonFanOutNoReport
	}
	if o.Prepare == nil {
		return errSeasonFanOutNoPrepare
	}
	return nil
}

// validate checks what processSeasonGroup does not, before its first
// command (it validates the rest of its config itself); groups is the
// report's group count.
func (p SeasonFanOutPlan) validate(groups int) error {
	if p.Start == nil {
		return errSeasonFanOutNoStart
	}
	if p.Group.GroupIdx < 0 || p.Group.GroupIdx >= groups {
		return fmt.Errorf("season fan-out: group %d outside the report's %d groups", p.Group.GroupIdx, groups)
	}
	return nil
}
