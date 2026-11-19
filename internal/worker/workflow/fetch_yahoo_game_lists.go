package workflow

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/sperano/puckdb/internal/worker/yahoo"
	"go.temporal.io/sdk/workflow"
)

// Units of a season's bar besides its pages: the plan, then the commit when
// the list is downloaded.
const (
	gameListPlanUnits   = 1
	gameListCommitUnits = 1
)

// gameListOutcome is what happened to one season's list.
type gameListOutcome int

const (
	// gameListNotRun: the run was cancelled before the season started.
	gameListNotRun gameListOutcome = iota
	// gameListFailed: the plan, a page or the commit failed.
	gameListFailed
	// gameListKept: the committed list is current or final.
	gameListKept
	// gameListUnavailable: Yahoo lists no NHL game for the season.
	gameListUnavailable
	// gameListDownloaded: a new list was downloaded and committed.
	gameListDownloaded
)

// newFetchYahooPlayersProgressReport is the report before the seasons are
// known: one group, whose bars (one per season) are added once the games
// list has been read.
func newFetchYahooPlayersProgressReport() *shared.ProgressReport {
	return &shared.ProgressReport{
		Groups: []shared.ProgressGroup{{Header: "Fetching Yahoo! game player lists..."}},
	}
}

// fetchYahooGamePlayerLists downloads Yahoo's NHL games list, then each
// season's game player list that PlanYahooGamePlayerList says is due (a
// finished season's committed list is final, the current season's is
// downloaded again once older than --yahoo-game-player-list-max-age), with
// up to --yahoo-game-player-list-concurrency seasons at once. Each season has
// a bar, sized by its pages as they arrive. A season's failure does not stop
// the others; the run then fails with every failed season's error.
func fetchYahooGamePlayerLists(ctx workflow.Context) error {
	logger := workflow.GetLogger(ctx)
	concurrency, err := shared.SnapshotConfigInt(ctx, logger, shared.YahooGamePlayerListConcurrencyParam, nil)
	if err != nil {
		return err
	}
	startedAt := workflow.Now(ctx)
	tracker, err := shared.InitTracker(ctx, newFetchYahooPlayersProgressReport())
	if err != nil {
		return err
	}
	tracker.SerializeSaves(ctx)
	tracker.StartGroup(ctx, GroupFetchYahooPlayers)

	var act *yahoo.FetchActivities
	var seasons []int
	yahooCtx := workflow.WithActivityOptions(ctx, shared.YahooDownloadActivityOptions())
	if err := workflow.ExecuteActivity(yahooCtx, act.ListYahooNHLGameSeasons).Get(ctx, &seasons); err != nil {
		return fmt.Errorf("list Yahoo NHL games: %w", err)
	}
	for _, season := range seasons {
		tracker.AddBar(GroupFetchYahooPlayers, shared.ProgressBar{Label: strconv.Itoa(season), Total: gameListPlanUnits})
	}
	tracker.Save(ctx)

	outcomes, err := fetchYahooGamePlayerListSeasons(ctx, tracker, seasons, concurrency)
	counts := countGameListOutcomes(outcomes)
	logger.Info("FetchYahooPlayersWorkflow finished", "seasons", len(seasons),
		"downloaded", counts[gameListDownloaded], "kept", counts[gameListKept],
		"unavailable", counts[gameListUnavailable], "failed", counts[gameListFailed],
		"not_run", counts[gameListNotRun])
	if err != nil {
		return err
	}
	elapsed := shared.FormatDuration(workflow.Now(ctx).Sub(startedAt))
	tracker.CompleteGroup(ctx, GroupFetchYahooPlayers, fmt.Sprintf(
		"Downloaded %d of %d Yahoo! game player lists in %s.", counts[gameListDownloaded], len(seasons), elapsed))
	return nil
}

// countGameListOutcomes counts the seasons per outcome.
func countGameListOutcomes(outcomes []gameListOutcome) map[gameListOutcome]int {
	counts := make(map[gameListOutcome]int, len(outcomes))
	for _, outcome := range outcomes {
		counts[outcome]++
	}
	return counts
}

// fetchYahooGamePlayerListSeasons runs fetchYahooGamePlayerList for every
// season, up to concurrency at a time, and returns each season's outcome
// (gameListFailed on an error, gameListNotRun when cancelled first). Every
// season runs whatever another one's outcome; the returned error joins each
// failed season's error, in season order.
func fetchYahooGamePlayerListSeasons(ctx workflow.Context, tracker *shared.ReportTracker, seasons []int,
	concurrency int) ([]gameListOutcome, error) {
	outcomes := make([]gameListOutcome, len(seasons))
	errs := make([]error, len(seasons))
	slots := workflow.NewSemaphore(ctx, int64(concurrency))
	pending := workflow.NewWaitGroup(ctx)
	var canceled error
	for i, season := range seasons {
		if canceled = slots.Acquire(ctx, 1); canceled != nil {
			break
		}
		pending.Add(1)
		workflow.Go(ctx, func(ctx workflow.Context) {
			defer pending.Done()
			defer slots.Release(1)
			bar := gameListBar{tracker: tracker, ref: shared.BarRef{Group: GroupFetchYahooPlayers, Bar: i}}
			outcome, err := fetchYahooGamePlayerList(ctx, season, bar)
			outcomes[i] = outcome
			if err != nil {
				outcomes[i] = gameListFailed
				errs[i] = fmt.Errorf("fetch Yahoo game player list %d: %w", season, err)
			}
		})
	}
	pending.Wait(ctx)
	return outcomes, errors.Join(canceled, errors.Join(errs...))
}

// fetchYahooGamePlayerList plans the season's list and, when it is due,
// downloads its pages and commits the manifest that makes them the season's
// list. The download ID, taken from the workflow clock, keeps each
// download's pages apart.
func fetchYahooGamePlayerList(ctx workflow.Context, season int, bar gameListBar) (gameListOutcome, error) {
	var act *yahoo.FetchActivities
	logger := workflow.GetLogger(ctx)
	yahooCtx := workflow.WithActivityOptions(ctx, shared.YahooDownloadActivityOptions())
	bar.start(ctx)
	var plan yahoo.YahooGamePlayerListPlan
	planInput := yahoo.PlanYahooGamePlayerListInput{Season: season}
	if err := workflow.ExecuteActivity(yahooCtx, act.PlanYahooGamePlayerList, planInput).Get(ctx, &plan); err != nil {
		return gameListFailed, err
	}
	bar.advance(ctx)
	if plan.Unavailable {
		logger.Info("No Yahoo game player list", "season", season, "reason", plan.Reason)
		return gameListUnavailable, nil
	}
	if !plan.Refresh {
		logger.Info("Skipping Yahoo game player list download", "season", season, "reason", plan.Reason)
		return gameListKept, nil
	}
	fetchedAt := workflow.Now(ctx)
	commit := yahoo.CommitYahooGamePlayerListInput{
		Season: season, GameKey: plan.GameKey, DownloadID: fetchedAt.UnixMilli(), FetchedAt: fetchedAt,
	}
	pages := gameListPages(plan.PreviousPlayerCount)
	bar.resize(ctx, pages+gameListCommitUnits)
	requested, err := fetchGamePlayerListPages(yahooCtx, &commit, bar, pages)
	if err != nil {
		discardGamePlayerList(ctx, commit, requested)
		return gameListFailed, err
	}
	commitCtx := workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())
	if err := workflow.ExecuteActivity(commitCtx, act.CommitYahooGamePlayerList, commit).Get(ctx, nil); err != nil {
		discardGamePlayerList(ctx, commit, requested)
		return gameListFailed, err
	}
	bar.advance(ctx)
	return gameListDownloaded, nil
}

// discardGamePlayerList deletes the pages a failed download requested, on a
// context the workflow's cancellation does not reach, so a failed or
// cancelled download leaves no page behind. A failed discard is logged: the
// download's own error is the one the season reports.
func discardGamePlayerList(ctx workflow.Context, commit yahoo.CommitYahooGamePlayerListInput, requested []int) {
	if len(requested) == 0 {
		return
	}
	var act *yahoo.FetchActivities
	discardCtx, _ := workflow.NewDisconnectedContext(ctx)
	discardCtx = workflow.WithActivityOptions(discardCtx, shared.DefaultActivityOptions())
	input := yahoo.DiscardYahooGamePlayerListInput{Season: commit.Season, DownloadID: commit.DownloadID, Starts: requested}
	if err := workflow.ExecuteActivity(discardCtx, act.DiscardYahooGamePlayerList, input).Get(discardCtx, nil); err != nil {
		workflow.GetLogger(ctx).Warn("Could not discard failed Yahoo game player list download",
			"season", commit.Season, "download_id", commit.DownloadID, "error", err)
	}
}

// gameListPages estimates a list's pages from the previous list's player
// count: its full pages plus the short page that ends it (one page when
// there is no previous list).
func gameListPages(previousPlayers int) int {
	return previousPlayers/resource.GamePlayersPageSize + 1
}

// fetchGamePlayerListPages downloads the list's pages one at a time until a
// short page, recording each in commit, so no page past the list's end is
// requested. estimate is the number of page units already in the bar: a
// page beyond it grows the bar first, and the short page shrinks the bar to
// the pages downloaded. Reaching yahoo.MaxGamePlayerListPages is an error.
// It returns the offset of every page requested, a failed one included
// (its activity may have stored it before failing).
func fetchGamePlayerListPages(ctx workflow.Context, commit *yahoo.CommitYahooGamePlayerListInput,
	bar gameListBar, estimate int) ([]int, error) {
	var act *yahoo.FetchActivities
	var requested []int
	for page := 0; page < yahoo.MaxGamePlayerListPages; page++ {
		if page >= estimate {
			bar.resize(ctx, 1)
			estimate++
		}
		input := yahoo.FetchYahooGamePlayersPageInput{
			Season: commit.Season, GameKey: commit.GameKey, DownloadID: commit.DownloadID,
			Start: page * resource.GamePlayersPageSize,
		}
		requested = append(requested, input.Start)
		var result yahoo.FetchYahooGamePlayersPageResult
		if err := workflow.ExecuteActivity(ctx, act.FetchYahooGamePlayersPage, input).Get(ctx, &result); err != nil {
			return requested, err
		}
		bar.advance(ctx)
		commit.Starts = append(commit.Starts, input.Start)
		commit.Players += result.Players
		if result.Players < resource.GamePlayersPageSize {
			bar.resize(ctx, page+1-estimate)
			return requested, nil
		}
	}
	return requested, fmt.Errorf("list has more than %d pages", yahoo.MaxGamePlayerListPages)
}

// gameListBar is one season's bar.
type gameListBar struct {
	tracker *shared.ReportTracker
	ref     shared.BarRef
}

func (b gameListBar) start(ctx workflow.Context) {
	b.tracker.StartBar(ctx, b.ref)
}

func (b gameListBar) advance(ctx workflow.Context) {
	b.tracker.IncrementBar(ctx, b.ref)
}

// resize changes the bar's Total by delta, keeping the report Total in step.
func (b gameListBar) resize(ctx workflow.Context, delta int) {
	if delta == 0 {
		return
	}
	b.tracker.ResizeBar(ctx, b.ref, b.tracker.Bar(b.ref).Total+delta)
}
