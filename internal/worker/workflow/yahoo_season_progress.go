package workflow

import (
	"fmt"
	"strconv"

	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/workflow"
)

// yahooChildGroup is the only group of a Yahoo season child workflow's
// report. It holds one bar per configured league, in configuration order, so
// a league's position in the season config is its bar index.
const yahooChildGroup = 0

// yahooSeasonProgress advances the per-league bars of the child workflow's
// own progress report. Every change is saved so the parent's mirrored bars
// follow along.
type yahooSeasonProgress struct {
	tracker *shared.ReportTracker
}

// advance records one completed unit of a league's bar.
func (p yahooSeasonProgress) advance(ctx workflow.Context, bar int) {
	p.tracker.IncrementBar(ctx, yahooChildGroup, bar)
}

// advanceBars records one unit shared by several leagues (a batched import).
func (p yahooSeasonProgress) advanceBars(ctx workflow.Context, bars []int) {
	if len(bars) == 0 {
		return
	}
	p.tracker.IncrementBars(ctx, yahooChildGroup, bars)
}

// resize grows (delta > 0) or shrinks a league's Total once an activity
// result replaces an estimate, keeping the report Total in step.
func (p yahooSeasonProgress) resize(ctx workflow.Context, bar, delta int) {
	if delta == 0 {
		return
	}
	p.tracker.SetBarTotal(yahooChildGroup, bar, p.tracker.Bar(yahooChildGroup, bar).Total+delta)
	p.tracker.RecalcTotal()
	p.tracker.Save(ctx)
}

func (p yahooSeasonProgress) complete(ctx workflow.Context) {
	p.tracker.CompleteGroup(ctx, yahooChildGroup,
		fmt.Sprintf("Done in %s.", p.tracker.GetElapsed(ctx, yahooChildGroup)))
}

// leagueTotals returns each league bar's final Total, for the parent to size
// its mirrored bars before completing them.
func (p yahooSeasonProgress) leagueTotals() []int {
	return p.tracker.GroupBarTotals(yahooChildGroup)
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
	bars := make([]shared.ProgressBar, len(input.Season.Leagues))
	total := 0
	for i, league := range input.Season.Leagues {
		bars[i] = shared.ProgressBar{
			Label: strconv.Itoa(league.LeagueID),
			Total: yahooLeagueUnits(league, mode),
		}
		total += bars[i].Total
	}
	return &shared.ProgressReport{
		Total: total,
		Groups: []shared.ProgressGroup{{
			Header: fmt.Sprintf("%s Yahoo %d metadata...", seasonSyncVerb(mode), input.StartYear),
			Bars:   bars,
		}},
	}
}

// yahooLeagueBar is one league's bar in the child report.
type yahooLeagueBar struct {
	progress yahooSeasonProgress
	index    int
}

func (b yahooLeagueBar) advance(ctx workflow.Context) {
	b.progress.advance(ctx, b.index)
}

func (b yahooLeagueBar) resize(ctx workflow.Context, delta int) {
	b.progress.resize(ctx, b.index, delta)
}

// estimate starts tracking a run of calls already counted in the bar's Total
// as estimate units.
func (b yahooLeagueBar) estimate(estimate int) *yahooEstimate {
	return &yahooEstimate{bar: b, estimate: estimate}
}

// yahooEstimate tracks a run of downloads whose count is only estimated (a
// league's matchup weeks, a pool's pages) within one league bar. It grows the
// bar before a call that would overrun the estimate and shrinks it to the
// calls actually made when the run ends, so the bar always completes exactly.
type yahooEstimate struct {
	bar      yahooLeagueBar
	estimate int
	done     int
}

// beforeCall reserves room for one more call when the estimate is used up.
func (e *yahooEstimate) beforeCall(ctx workflow.Context) {
	if e.done < e.estimate {
		return
	}
	e.bar.resize(ctx, 1)
	e.estimate++
}

// afterCall records one completed call.
func (e *yahooEstimate) afterCall(ctx workflow.Context) {
	e.done++
	e.bar.advance(ctx)
}

// finish drops the estimated calls that were never made.
func (e *yahooEstimate) finish(ctx workflow.Context) {
	e.bar.resize(ctx, e.done-e.estimate)
	e.estimate = e.done
}
