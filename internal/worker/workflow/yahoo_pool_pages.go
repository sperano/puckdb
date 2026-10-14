package workflow

import (
	"fmt"

	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/sperano/puckdb/internal/worker/yahoo"
	"go.temporal.io/sdk/workflow"
)

const (
	// yahooPoolPageConcurrency bounds one pool's page downloads in flight.
	yahooPoolPageConcurrency = 8
	// yahooPoolParallelPagesChange versions the concurrent page fetch: a run
	// that recorded the one-page-at-a-time walk replays it.
	yahooPoolParallelPagesChange  = "yahoo-pool-parallel-pages"
	yahooPoolParallelPagesVersion = 1
	// yahooPoolCancelPastEndChange versions cancelling the pages in flight
	// past the pool's last page: a run that recorded letting them finish
	// replays it.
	yahooPoolCancelPastEndChange  = "yahoo-pool-cancel-past-end"
	yahooPoolCancelPastEndVersion = 1
	// yahooPoolPastCap ends a one-at-a-time walk's page range just past the
	// page cap, so a pool that never ends reaches the cap error.
	yahooPoolPastCap = yahoo.MaxLeaguePlayerPoolPages + 1
)

// yahooPoolStep is where a recorded page leaves a pool download.
type yahooPoolStep int

const (
	// yahooPoolContinue follows a full page: more pages may follow.
	yahooPoolContinue yahooPoolStep = iota
	// yahooPoolComplete follows a short page, the pool's last.
	yahooPoolComplete
	// yahooPoolUnavailable means Yahoo does not serve the pool yet.
	yahooPoolUnavailable
)

// fetchYahooPoolPages downloads the pool's pages until a short one, recording
// each in commit, and reports whether the pool is unavailable. The first
// page goes alone: only it can report the pool unavailable, and later pages
// of an unavailable pool would fail instead. The other pages the previous
// snapshot's size predicts are then fetched concurrently, and any beyond
// them one at a time until a short page. Pages are recorded in page order,
// so commit names exactly the pages a one-at-a-time walk would. On failure
// the page estimate is left as is, so the bar stays incomplete.
func fetchYahooPoolPages(ctx workflow.Context, commit *yahoo.CommitYahooLeaguePlayerPoolInput,
	pages *yahooEstimate, previousPlayers int) (bool, error) {
	walk := &yahooPoolWalk{commit: commit, pages: pages}
	known := 0
	version := workflow.GetVersion(ctx, yahooPoolParallelPagesChange, workflow.DefaultVersion, yahooPoolParallelPagesVersion)
	if version >= yahooPoolParallelPagesVersion && previousPlayers > 0 {
		known = yahooPoolPages(previousPlayers)
	}
	if known == 0 {
		step, err := walk.sequential(ctx, 0, yahooPoolPastCap)
		return step == yahooPoolUnavailable, err
	}
	step, err := walk.sequential(ctx, 0, 1)
	if err == nil && step == yahooPoolContinue {
		walk.cancelPastEnd = workflow.GetVersion(ctx, yahooPoolCancelPastEndChange, workflow.DefaultVersion,
			yahooPoolCancelPastEndVersion) >= yahooPoolCancelPastEndVersion
		step, err = walk.concurrent(ctx, 1, known)
	}
	if err == nil && step == yahooPoolContinue {
		step, err = walk.sequential(ctx, known, yahooPoolPastCap)
	}
	return step == yahooPoolUnavailable, err
}

// yahooPoolWalk downloads one pool's pages into commit, advancing pages.
// With cancelPastEnd, a concurrent walk cancels the pages in flight past the
// pool's last page.
type yahooPoolWalk struct {
	commit        *yahoo.CommitYahooLeaguePlayerPoolInput
	pages         *yahooEstimate
	cancelPastEnd bool
}

// sequential downloads pages [from, to) one at a time until one ends the
// pool; the page past the cap is an error, not a download.
func (w *yahooPoolWalk) sequential(ctx workflow.Context, from, to int) (yahooPoolStep, error) {
	for page := from; page < to; page++ {
		if page == yahoo.MaxLeaguePlayerPoolPages {
			return yahooPoolContinue, fmt.Errorf("pool has more than %d pages", yahoo.MaxLeaguePlayerPoolPages)
		}
		var result yahoo.FetchYahooLeaguePlayersPageResult
		if err := w.start(ctx, page).Get(ctx, &result); err != nil {
			return yahooPoolContinue, err
		}
		w.pages.afterCall(ctx)
		if step := w.record(ctx, page, result); step != yahooPoolContinue {
			return step, nil
		}
	}
	return yahooPoolContinue, nil
}

// yahooPoolPageOutcome is one concurrently downloaded page; cancel cancels
// its download while it is in flight (nil when the walk never cancels).
type yahooPoolPageOutcome struct {
	done   bool
	result yahoo.FetchYahooLeaguePlayersPageResult
	err    error
	cancel workflow.CancelFunc
}

// endsPool reports whether a one-at-a-time walk would stop at this page.
func (o yahooPoolPageOutcome) endsPool() bool {
	return o.err != nil || o.result.Unavailable || o.result.Players < resource.LeaguePlayersPageSize
}

// concurrent downloads pages [from, to) with up to yahooPoolPageConcurrency
// in flight. Once a page that ends the pool (short, unavailable or failed)
// is known, no later page is started, and the ones in flight past it are
// cancelled (with cancelPastEnd; before it, they finish) and discarded, so a
// download still queued on the Yahoo rate limiter gives its token back.
// Every page before the earliest such page was started before it and has
// finished, so recording in page order sees the same pages, and stops at the
// same one, as a one-at-a-time walk.
func (w *yahooPoolWalk) concurrent(ctx workflow.Context, from, to int) (yahooPoolStep, error) {
	if to <= from {
		return yahooPoolContinue, nil
	}
	outcomes := make([]yahooPoolPageOutcome, to-from)
	firstEnd := -1
	start := func(ctx workflow.Context, i int) workflow.Future {
		if w.cancelPastEnd {
			ctx, outcomes[i].cancel = cancellablePage(ctx)
		}
		return w.start(ctx, from+i)
	}
	handle := func(ctx workflow.Context, i int, f workflow.Future) error {
		outcome := &outcomes[i]
		outcome.done, outcome.err = true, f.Get(ctx, &outcome.result)
		if outcome.err == nil {
			w.pages.afterCall(ctx)
		}
		if outcome.endsPool() && (firstEnd < 0 || i < firstEnd) {
			firstEnd = i
			cancelInFlight(outcomes[i+1:])
		}
		return nil
	}
	stop := func() bool { return firstEnd >= 0 }
	if err := shared.RunActivitiesUntil(ctx, len(outcomes), yahooPoolPageConcurrency, start, handle, stop); err != nil {
		return yahooPoolContinue, err
	}
	for i, outcome := range outcomes {
		if outcome.err != nil {
			return yahooPoolContinue, outcome.err
		}
		if step := w.record(ctx, from+i, outcome.result); step != yahooPoolContinue {
			w.discard(from+i+1, outcomes[i+1:])
			return step, nil
		}
	}
	return yahooPoolContinue, nil
}

// cancellablePage returns a context whose page download can be cancelled.
// The page's future resolves only once its activity has ended, so a page
// that finished downloading before the cancellation reached it still counts
// as downloaded, and is discarded.
func cancellablePage(ctx workflow.Context) (workflow.Context, workflow.CancelFunc) {
	opts := workflow.GetActivityOptions(ctx)
	opts.WaitForCancellation = true
	return workflow.WithCancel(workflow.WithActivityOptions(ctx, opts))
}

// cancelInFlight cancels the pages of outcomes still in flight.
func cancelInFlight(outcomes []yahooPoolPageOutcome) {
	for _, outcome := range outcomes {
		if !outcome.done && outcome.cancel != nil {
			outcome.cancel()
		}
	}
}

// discard names in commit the pages downloaded past the pool's last page
// (outcomes, starting at page from), so the commit can delete their files.
func (w *yahooPoolWalk) discard(from int, outcomes []yahooPoolPageOutcome) {
	for i, outcome := range outcomes {
		if outcome.done && outcome.err == nil && !outcome.result.Unavailable {
			w.commit.Discarded = append(w.commit.Discarded, (from+i)*resource.LeaguePlayersPageSize)
		}
	}
}

// start schedules the download of one page, reserving room for it in the
// page estimate.
func (w *yahooPoolWalk) start(ctx workflow.Context, page int) workflow.Future {
	var act *yahoo.FetchActivities
	w.pages.beforeCall(ctx)
	input := yahoo.FetchYahooLeaguePlayersPageInput{
		Season: w.commit.Season, LeagueID: w.commit.LeagueID, DownloadID: w.commit.DownloadID,
		Start: page * resource.LeaguePlayersPageSize,
	}
	return workflow.ExecuteActivity(ctx, act.FetchYahooLeaguePlayersPage, input)
}

// record adds a downloaded page to commit and says whether the pool goes on.
// An unavailable pool commits nothing, so it gives back the commit unit.
func (w *yahooPoolWalk) record(ctx workflow.Context, page int, result yahoo.FetchYahooLeaguePlayersPageResult) yahooPoolStep {
	if result.Unavailable {
		w.pages.finish(ctx)
		w.pages.bar.resize(ctx, -yahooPoolCommitUnits)
		return yahooPoolUnavailable
	}
	w.commit.LeagueKey, w.commit.GameKey = result.LeagueKey, result.GameKey
	w.commit.Starts = append(w.commit.Starts, page*resource.LeaguePlayersPageSize)
	w.commit.Players += result.Players
	if result.Players < resource.LeaguePlayersPageSize {
		w.pages.finish(ctx)
		return yahooPoolComplete
	}
	return yahooPoolContinue
}
